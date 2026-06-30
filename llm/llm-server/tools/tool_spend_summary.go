package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolSpendSummary, func(accountId string) (core.NBTool, error) {
		return SpendSummaryTool{}, nil
	})
}

const ToolSpendSummary = "spend_summary"

// SpendSummaryTool provides pre-aggregated cloud spend data grouped by account or service.
// Unlike SQL-executor tools, it runs fixed parameterized queries — no LLM-generated SQL.
type SpendSummaryTool struct{}

func (t SpendSummaryTool) Name() string             { return ToolSpendSummary }
func (t SpendSummaryTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t SpendSummaryTool) Description() string {
	return "Retrieves pre-aggregated cloud spend summary. Returns spend amounts, period-over-period changes, and estimated savings. " +
		"Optional group_by parameter: 'cloud_account' (default) for per-account breakdown or 'service' for per-service breakdown. " +
		"Optional account_id parameter: UUID of a specific cloud account to scope results to (defaults to the current account). " +
		"Optional window parameter: any '{N}d' value from 1 to 365 days (e.g. '7d', '15d', '30d', '90d'). Defaults to '30d'."
}

func (t SpendSummaryTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"group_by": {
				Type:        core.ToolSchemaTypeString,
				Description: "How to group spend data. 'cloud_account' for per-account breakdown, 'service' for per-service breakdown.",
				Enum:        []any{"cloud_account", "service"},
				Default:     "cloud_account",
			},
			"account_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of a specific cloud account to scope results to. Defaults to the current account.",
			},
			"window": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time window for spend data as '{N}d' (e.g. '7d', '15d', '30d', '90d'). Range: 1–365 days.",
				Default:     "30d",
			},
			"filter": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional case-insensitive substring to scope results to a specific entity. With group_by='service' it matches the service name (e.g. 'elasticsearch'); with group_by='cloud_account' it matches the account name. Use this when the user asks about ONE service/account so only the relevant rows are returned instead of the top-N.",
			},
		},
		Required: []string{},
	}
}

// InferToolRequestType classifies this tool as read-only so it can be parallelized safely.
func (t SpendSummaryTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t SpendSummaryTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	groupBy := "cloud_account"
	window := "30d"
	accountId := ""
	filter := ""

	if v, ok := input.Arguments["group_by"].(string); ok && v != "" {
		groupBy = v
	}
	if v, ok := input.Arguments["window"].(string); ok && v != "" {
		window = v
	}
	if v, ok := input.Arguments["account_id"].(string); ok && v != "" {
		accountId = v
	}
	if v, ok := input.Arguments["filter"].(string); ok && v != "" {
		filter = strings.TrimSpace(v)
	}

	windowDays, err := parseWindowDays(window)
	if err != nil {
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	window = fmt.Sprintf("%dd", windowDays)

	// Default to the requesting user's account to avoid cross-account duplication.
	// Multiple cloud_accounts often share the same underlying billing source (e.g.,
	// several GCP integrations pointing to the same billing account), each ingesting
	// identical spend data. Scoping to a single account prevents double-counting.
	if accountId == "" {
		accountId = nbCtx.AccountId
	}

	// Resolve tenant from account
	tenantId, err := security.GetTenantIdFromAccountId(nbCtx.AccountId)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error resolving tenant: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	if tenantId == "" {
		return core.NBToolResponse{
			Data:   "No tenant found for this account.",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// Calculate time window. Use start-of-today as the upper bound so the query
	// includes all of windowEnd's data (date >= windowStart AND date < windowEnd).
	now := time.Now().UTC()
	windowEnd := now.Truncate(24 * time.Hour) // start of today
	windowStart := windowEnd.AddDate(0, 0, -windowDays)

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Database error: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	var result any
	var grandTotal float64
	var rowCount, shownCount int

	switch groupBy {
	case "service":
		rows, qErr := querySpendByService(dbManager, tenantId, accountId, filter, windowStart, windowEnd)
		err = qErr
		result = rows
		shownCount = len(rows)
		if len(rows) > 0 {
			grandTotal = rows[0].GrandTotal
			rowCount = rows[0].RowCount
		}
	default:
		rows, qErr := querySpendByCloudAccount(dbManager, tenantId, accountId, filter, windowStart, windowEnd)
		err = qErr
		result = rows
		shownCount = len(rows)
		if len(rows) > 0 {
			grandTotal = rows[0].GrandTotal
			rowCount = rows[0].RowCount
		}
	}

	if err != nil {
		slog.Error("spend_summary: query failed", "error", err, "group_by", groupBy, "window", window, "account_id", accountId)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error querying spend data: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	responseMap := map[string]any{
		"group_by":     groupBy,
		"window":       window,
		"window_start": windowStart.Format("2006-01-02"),
		"window_end":   windowEnd.Format("2006-01-02"),
		"total_spend":  roundCents(grandTotal),
		"data":         result,
	}
	// When the result is truncated (more groups exist than were returned), tell the
	// agent so it reports "top N of M" with the true total instead of implying the
	// shown rows are everything.
	if rowCount > shownCount {
		responseMap["group_count"] = rowCount
		responseMap["shown_count"] = shownCount
		responseMap["note"] = fmt.Sprintf("Showing the top %d of %d %s groups by spend; total_spend ($%.2f) is the sum across ALL of them.",
			shownCount, rowCount, groupBy, grandTotal)
	}
	if accountId != "" {
		responseMap["account_id"] = accountId
	}
	if filter != "" {
		responseMap["filter"] = filter
	}

	jsonBytes, err := json.Marshal(responseMap)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error formatting response: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	return core.NBToolResponse{
		Data:   string(jsonBytes),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

type spendByAccountRow struct {
	AccountID        string  `json:"account_id" db:"id"`
	AccountName      string  `json:"account_name" db:"account_name"`
	Amount           float64 `json:"amount" db:"amount"`
	Saving           float64 `json:"estimated_savings" db:"saving"`
	AmountLast       float64 `json:"amount_previous_period" db:"amount_last"`
	PercentageChange float64 `json:"percentage_change" db:"percentage_change"`
	// Window aggregates over the full result set (before LIMIT); identical on every
	// row, read once into the response envelope, excluded from per-row JSON.
	GrandTotal float64 `json:"-" db:"grand_total"`
	RowCount   int     `json:"-" db:"row_count"`
}

type spendByServiceRow struct {
	ServiceName      string  `json:"service_name" db:"service_name"`
	ResourceCount    int     `json:"resource_count" db:"resource_count"`
	Amount           float64 `json:"amount" db:"spend_amount"`
	AmountLast       float64 `json:"amount_previous_period" db:"spend_amount_last"`
	PercentageChange float64 `json:"percentage_change" db:"percentage_change"`
	EstimatedSaving  float64 `json:"estimated_savings" db:"resource_estimated_saving"`
	// Window aggregates over the full result set (before LIMIT); identical on every
	// row, read once into the response envelope, excluded from per-row JSON.
	GrandTotal float64 `json:"-" db:"grand_total"`
	RowCount   int     `json:"-" db:"row_count"`
}

func querySpendByCloudAccount(dbManager *common.DatabaseManager, tenantId string, accountId string, filter string, windowStart, windowEnd time.Time) ([]spendByAccountRow, error) {
	// When accountId is provided, filter to that single account.
	// Otherwise show all accounts for the tenant.
	accountFilter := ""
	args := []any{tenantId, windowStart, windowEnd}
	if accountId != "" {
		accountFilter = " AND spends.cloud_account = $4"
		args = append(args, accountId)
	}

	// Optional case-insensitive name scope so a question about one account
	// returns just that account's row instead of the top-10.
	nameFilter := ""
	if filter != "" {
		args = append(args, "%"+filter+"%")
		nameFilter = fmt.Sprintf(" WHERE ca.account_name ILIKE $%d", len(args))
	}

	query := fmt.Sprintf(`
		SELECT
			ca.id,
			ca.account_name,
			ROUND(COALESCE(s.amount, 0)::numeric, 2)::float AS amount,
			ROUND(COALESCE(r.estimated_savings, 0)::numeric, 2)::float AS saving,
			ROUND(COALESCE(s1.amount, 0)::numeric, 2)::float AS amount_last,
			CASE WHEN COALESCE(s1.amount, 0) > 0
				THEN ROUND(((s.amount - s1.amount) / s1.amount * 100)::numeric, 2)::float
				ELSE 0
			END AS percentage_change,
			ROUND(SUM(COALESCE(s.amount, 0)) OVER ()::numeric, 2)::float AS grand_total,
			COUNT(*) OVER ()::int AS row_count
		FROM cloud_accounts ca
		INNER JOIN (
			SELECT SUM(spends.amount) AS amount, spends.cloud_account
			FROM spends
			WHERE spends.date >= $2 AND spends.date < $3 AND tenant = $1%s
			GROUP BY spends.cloud_account
		) s ON ca.id = s.cloud_account
		LEFT JOIN (
			SELECT SUM(spends.amount) AS amount, spends.cloud_account
			FROM spends
			WHERE spends.date >= $2 - ($3 - $2) AND spends.date < $2 AND tenant = $1%s
			GROUP BY spends.cloud_account
		) s1 ON ca.id = s1.cloud_account
		LEFT JOIN (
			SELECT recommendation.cloud_account_id, SUM(recommendation.estimated_savings) AS estimated_savings
			FROM recommendation
			GROUP BY recommendation.cloud_account_id
		) r ON ca.id = r.cloud_account_id%s
		ORDER BY s.amount DESC
		LIMIT 10`, accountFilter, accountFilter, nameFilter)

	rows := []spendByAccountRow{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}

func querySpendByService(dbManager *common.DatabaseManager, tenantId string, accountId string, filter string, windowStart, windowEnd time.Time) ([]spendByServiceRow, error) {
	// When accountId is provided, scope spends to that account and deduplicate
	// resources by resourse_id to avoid counting the same cloud resource multiple
	// times (GCP sub-projects that share the same billing account create duplicate
	// cloud_resourses rows with the same resourse_id).
	accountFilter := ""
	resourceAccountFilter := ""
	args := []any{tenantId, windowStart, windowEnd}
	if accountId != "" {
		accountFilter = " AND spends.cloud_account = $4"
		resourceAccountFilter = " AND cr.account = $4"
		args = append(args, accountId)
	}

	// Optional case-insensitive service-name scope, applied early in the dedup
	// subquery so a question about one service narrows the resource set instead of
	// returning the top-20.
	nameFilter := ""
	if filter != "" {
		args = append(args, "%"+filter+"%")
		nameFilter = fmt.Sprintf(" AND cr.service_name ILIKE $%d", len(args))
	}

	query := fmt.Sprintf(`
		SELECT
			dedup.service_name,
			COUNT(DISTINCT dedup.resourse_id)::int AS resource_count,
			ROUND(SUM(s.amount)::numeric, 2)::float AS spend_amount,
			CASE WHEN SUM(s1.amount) IS NOT NULL
				THEN ROUND(SUM(s1.amount)::numeric, 2)::float
				ELSE 0
			END AS spend_amount_last,
			CASE WHEN SUM(s1.amount) > 0
				THEN ROUND(((SUM(s.amount) - SUM(s1.amount)) / SUM(s1.amount) * 100)::numeric, 2)::float
				ELSE 0
			END AS percentage_change,
			ROUND(COALESCE(SUM(r.estimated_savings), 0)::numeric, 2)::float AS resource_estimated_saving,
			ROUND(SUM(SUM(s.amount)) OVER ()::numeric, 2)::float AS grand_total,
			COUNT(*) OVER ()::int AS row_count
		FROM (
			SELECT DISTINCT ON (cr.resourse_id, cr.service_name) cr.id, cr.resourse_id, cr.service_name
			FROM cloud_resourses cr
			WHERE cr.tenant = $1 AND cr.service_name IS NOT NULL%s%s
			ORDER BY cr.resourse_id, cr.service_name, cr.created_at ASC
		) dedup
		LEFT JOIN (
			SELECT recommendation.resource_id, SUM(recommendation.estimated_savings) AS estimated_savings
			FROM recommendation
			GROUP BY recommendation.resource_id
		) r ON dedup.id = r.resource_id
		INNER JOIN (
			SELECT spends.cloud_resource_id, SUM(spends.amount) AS amount
			FROM spends
			WHERE spends.date >= $2 AND spends.date < $3 AND tenant = $1%s
			GROUP BY spends.cloud_resource_id
		) s ON s.cloud_resource_id = dedup.id
		LEFT JOIN (
			SELECT spends.cloud_resource_id, SUM(spends.amount) AS amount
			FROM spends
			WHERE spends.date >= $2 - ($3 - $2) AND spends.date < $2 AND tenant = $1%s
			GROUP BY spends.cloud_resource_id
		) s1 ON s1.cloud_resource_id = dedup.id
		WHERE s.amount > 0
		GROUP BY dedup.service_name
		ORDER BY SUM(s.amount) DESC
		LIMIT 20`, resourceAccountFilter, nameFilter, accountFilter, accountFilter)

	rows := []spendByServiceRow{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}
