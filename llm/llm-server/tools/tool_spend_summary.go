package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
		"Optional window parameter: '7d', '30d' (default), or '90d'."
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
				Description: "Time window for spend data. '7d' for last 7 days, '30d' for last 30 days, '90d' for last 90 days.",
				Enum:        []any{"7d", "30d", "90d"},
				Default:     "30d",
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

	if v, ok := input.Arguments["group_by"].(string); ok && v != "" {
		groupBy = v
	}
	if v, ok := input.Arguments["window"].(string); ok && v != "" {
		window = v
	}
	if v, ok := input.Arguments["account_id"].(string); ok && v != "" {
		accountId = v
	}

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
	var windowStart time.Time
	switch window {
	case "7d":
		windowStart = windowEnd.AddDate(0, 0, -7)
	case "90d":
		windowStart = windowEnd.AddDate(0, 0, -90)
	default:
		windowStart = windowEnd.AddDate(0, 0, -30)
		window = "30d"
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Database error: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	var result any

	switch groupBy {
	case "service":
		result, err = querySpendByService(dbManager, tenantId, accountId, windowStart, windowEnd)
	default:
		result, err = querySpendByCloudAccount(dbManager, tenantId, accountId, windowStart, windowEnd)
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
		"data":         result,
	}
	if accountId != "" {
		responseMap["account_id"] = accountId
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
}

type spendByServiceRow struct {
	ServiceName      string  `json:"service_name" db:"service_name"`
	ResourceCount    int     `json:"resource_count" db:"resource_count"`
	Amount           float64 `json:"amount" db:"spend_amount"`
	AmountLast       float64 `json:"amount_previous_period" db:"spend_amount_last"`
	PercentageChange float64 `json:"percentage_change" db:"percentage_change"`
	EstimatedSaving  float64 `json:"estimated_savings" db:"resource_estimated_saving"`
}

func querySpendByCloudAccount(dbManager *common.DatabaseManager, tenantId string, accountId string, windowStart, windowEnd time.Time) ([]spendByAccountRow, error) {
	// When accountId is provided, filter to that single account.
	// Otherwise show all accounts for the tenant.
	accountFilter := ""
	args := []any{tenantId, windowStart, windowEnd}
	if accountId != "" {
		accountFilter = " AND spends.cloud_account = $4"
		args = append(args, accountId)
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
			END AS percentage_change
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
		) r ON ca.id = r.cloud_account_id
		ORDER BY s.amount DESC
		LIMIT 10`, accountFilter, accountFilter)

	rows := []spendByAccountRow{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}

func querySpendByService(dbManager *common.DatabaseManager, tenantId string, accountId string, windowStart, windowEnd time.Time) ([]spendByServiceRow, error) {
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
			ROUND(COALESCE(SUM(r.estimated_savings), 0)::numeric, 2)::float AS resource_estimated_saving
		FROM (
			SELECT DISTINCT ON (cr.resourse_id, cr.service_name) cr.id, cr.resourse_id, cr.service_name
			FROM cloud_resourses cr
			WHERE cr.tenant = $1 AND cr.service_name IS NOT NULL%s
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
		LIMIT 20`, resourceAccountFilter, accountFilter, accountFilter)

	rows := []spendByServiceRow{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}
