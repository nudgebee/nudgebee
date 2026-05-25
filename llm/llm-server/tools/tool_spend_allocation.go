package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// roundCents rounds a monetary/percentage value to two decimals.
func roundCents(v float64) float64 { return math.Round(v*100) / 100 }

func init() {
	core.RegisterNBToolFactory(ToolSpendAllocation, func(accountId string) (core.NBTool, error) {
		return SpendAllocationTool{}, nil
	})
}

const ToolSpendAllocation = "spend_allocation"

// SpendAllocationTool attributes cloud spend to a chosen dimension (k8s
// namespace, service, region, resource type, or a tag/label key) for
// cost showback. Like SpendSummaryTool it runs fixed parameterized queries
// over the daily `spends` table joined to `cloud_resourses` — no
// LLM-generated SQL. Read-only.
type SpendAllocationTool struct{}

func (t SpendAllocationTool) Name() string             { return ToolSpendAllocation }
func (t SpendAllocationTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t SpendAllocationTool) Description() string {
	return "Attributes cloud spend to a dimension for cost showback / chargeback: who or what is spending. " +
		"group_by: 'namespace' (default, Kubernetes namespace), 'service', 'region', 'resource_type', or 'tag'. " +
		"When group_by='tag', provide tag_key (the tag or k8s label key to group by, e.g. 'team', 'env', 'cost-center'). " +
		"Optional account_id (defaults to current account) and window ('7d', '30d' default, '90d'). " +
		"Returns each dimension value with its spend (USD), resource count, and share of attributed spend."
}

func (t SpendAllocationTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"group_by": {
				Type:        core.ToolSchemaTypeString,
				Description: "Dimension to attribute spend to. 'namespace' (k8s), 'service', 'region', 'resource_type', or 'tag'.",
				Enum:        []any{"namespace", "service", "region", "resource_type", "tag"},
				Default:     "namespace",
			},
			"tag_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "The tag or Kubernetes label key to group by. Required when group_by='tag' (e.g. 'team', 'env', 'cost-center').",
			},
			"account_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of a specific cloud account to scope results to. Defaults to the current account.",
			},
			"window": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time window. '7d', '30d' (default), or '90d'.",
				Enum:        []any{"7d", "30d", "90d"},
				Default:     "30d",
			},
		},
		Required: []string{},
	}
}

// InferToolRequestType classifies this tool as read-only so it can be parallelized safely.
func (t SpendAllocationTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (core.ToolRequestType, error) {
	return core.ToolRequestTypeRead, nil
}

func (t SpendAllocationTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	groupBy := "namespace"
	tagKey := ""
	window := "30d"
	accountId := ""

	if v, ok := input.Arguments["group_by"].(string); ok && v != "" {
		groupBy = v
	}
	if v, ok := input.Arguments["tag_key"].(string); ok && v != "" {
		tagKey = v
	}
	if v, ok := input.Arguments["window"].(string); ok && v != "" {
		window = v
	}
	if v, ok := input.Arguments["account_id"].(string); ok && v != "" {
		accountId = v
	}
	if accountId == "" {
		accountId = nbCtx.AccountId
	}

	if _, ok := allocationDimensions[groupBy]; !ok {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error: unsupported group_by %q. Use one of: namespace, service, region, resource_type, tag.", groupBy),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	if groupBy == "tag" && tagKey == "" {
		return core.NBToolResponse{
			Data:   "Error: group_by='tag' requires a tag_key (the tag or k8s label key to group by, e.g. 'team').",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// Fail fast on missing account context to avoid an unscoped, cross-tenant query.
	if nbCtx.AccountId == "" {
		return core.NBToolResponse{
			Data:   "Error: account context is missing.",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

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

	now := time.Now().UTC()
	windowEnd := now.Truncate(24 * time.Hour)
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

	rows, err := querySpendAllocation(dbManager, tenantId, accountId, groupBy, tagKey, windowStart, windowEnd)
	if err != nil {
		slog.Error("spend_allocation: query failed", "error", err, "group_by", groupBy, "window", window, "account_id", accountId)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error querying spend data: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	total := computeShares(rows)

	responseMap := map[string]any{
		"group_by":         groupBy,
		"window":           window,
		"window_start":     windowStart.Format("2006-01-02"),
		"window_end":       windowEnd.Format("2006-01-02"),
		"total_attributed": roundCents(total),
		"data":             rows,
		"note":             "Shares are of attributed spend (top dimension values shown). Spend on resources lacking this dimension is excluded.",
	}
	if groupBy == "tag" {
		responseMap["tag_key"] = tagKey
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

type allocationRow struct {
	DimensionValue string  `json:"dimension_value" db:"dimension_value"`
	ResourceCount  int     `json:"resource_count" db:"resource_count"`
	Amount         float64 `json:"amount" db:"amount"`
	PctOfTotal     float64 `json:"pct_of_total"`
}

// allocationDimensions whitelists group_by values to the SQL expression that
// extracts the dimension from cloud_resourses. Whitelisting (not interpolating
// user input) keeps the dynamic dimension safe from injection. The "tag" entry
// is a sentinel; its expression is built with a bound parameter for tag_key.
var allocationDimensions = map[string]string{
	"namespace":     "cr.meta->>'namespace'",
	"service":       "cr.service_name",
	"region":        "cr.region",
	"resource_type": "cr.type",
	"tag":           "", // built from a bound tag_key parameter
}

// computeShares fills in each row's PctOfTotal as a share of the summed spend
// and returns that total. Pure (no DB) so it can be unit-tested directly.
func computeShares(rows []allocationRow) float64 {
	total := 0.0
	for _, r := range rows {
		total += r.Amount
	}
	if total > 0 {
		for i := range rows {
			rows[i].PctOfTotal = roundCents(rows[i].Amount / total * 100)
		}
	}
	return total
}

func querySpendAllocation(dbManager *common.DatabaseManager, tenantId, accountId, groupBy, tagKey string, windowStart, windowEnd time.Time) ([]allocationRow, error) {
	args := []any{tenantId, windowStart, windowEnd}

	// Resolve the dimension expression. For tag/label, bind the key as a
	// parameter (used for both the cloud tag and the k8s label lookup).
	dimExpr := allocationDimensions[groupBy]
	if groupBy == "tag" {
		args = append(args, tagKey)
		idx := len(args)
		dimExpr = fmt.Sprintf("COALESCE(cr.tags->>$%d, cr.meta->'labels'->>$%d)", idx, idx)
	}

	// Optional single-account scoping (same param reused for resources and spends).
	resourceFilter, spendFilter := "", ""
	if accountId != "" {
		args = append(args, accountId)
		idx := len(args)
		resourceFilter = fmt.Sprintf(" AND cr.account = $%d", idx)
		spendFilter = fmt.Sprintf(" AND spends.cloud_account = $%d", idx)
	}

	// Join pre-aggregated spend directly to cloud_resourses on the internal id
	// (1:1 — cr.id is the PK that spends.cloud_resource_id references), so no
	// spend is dropped even when a resourse_id has multiple cloud_resourses rows
	// (e.g. GCP sub-projects sharing a billing account). Resource count is
	// COUNT(DISTINCT resourse_id) so those duplicates count as one resource.
	query := fmt.Sprintf(`
		SELECT
			sub.dim AS dimension_value,
			COUNT(DISTINCT sub.resourse_id)::int AS resource_count,
			ROUND(SUM(s.amount)::numeric, 2)::float AS amount
		FROM (
			SELECT spends.cloud_resource_id, SUM(spends.amount) AS amount
			FROM spends
			WHERE spends.date >= $2 AND spends.date < $3 AND tenant = $1%s
			GROUP BY spends.cloud_resource_id
		) s
		INNER JOIN (
			SELECT cr.id, cr.resourse_id, %s AS dim
			FROM cloud_resourses cr
			WHERE cr.tenant = $1%s
		) sub ON s.cloud_resource_id = sub.id
		WHERE s.amount > 0 AND sub.dim IS NOT NULL AND sub.dim <> ''
		GROUP BY sub.dim
		ORDER BY SUM(s.amount) DESC
		LIMIT 25`, spendFilter, dimExpr, resourceFilter)

	rows := []allocationRow{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}
