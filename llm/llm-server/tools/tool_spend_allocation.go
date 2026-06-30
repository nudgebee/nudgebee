package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
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
		"Optional account_id (defaults to current account) and window (any '{N}d' from 1 to 365 days, e.g. '7d', '15d', '30d', '90d'; defaults to '30d'). " +
		"Returns each dimension value with its spend (USD), resource count, and share of attributed spend."
}

func (t SpendAllocationTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"group_by": {
				Type:        core.ToolSchemaTypeString,
				Description: "Dimension to attribute spend to. 'namespace' (k8s), 'workload' (k8s controller/Deployment), 'service', 'region', 'resource_type', or 'tag'.",
				Enum:        []any{"namespace", "workload", "service", "region", "resource_type", "tag"},
				Default:     "namespace",
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional: restrict to a single Kubernetes namespace (exact match) BEFORE grouping. Combine with group_by='workload' to break a namespace's spend down by workload (e.g. namespace='nudgebee', group_by='workload'). Independent of group_by and of the substring 'filter'.",
			},
			"tag_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "The tag or Kubernetes label key to group by. Required when group_by='tag' (e.g. 'team', 'env', 'app.kubernetes.io/name').",
			},
			"account_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "UUID of a specific cloud account to scope results to. Defaults to the current account.",
			},
			"window": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time window as '{N}d' (e.g. '7d', '15d', '30d', '90d'). Range: 1–365 days.",
				Default:     "30d",
			},
			"filter": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional case-insensitive substring to scope results to a specific dimension value (e.g. group_by='namespace' with filter='prod' returns only prod-matching namespaces). Use when the user asks about ONE namespace/service/region/etc. so only the relevant rows are returned instead of the top-25.",
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
	filter := ""
	namespaceScope := ""

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
	if v, ok := input.Arguments["filter"].(string); ok && v != "" {
		filter = strings.TrimSpace(v)
	}
	if v, ok := input.Arguments["namespace"].(string); ok && v != "" {
		namespaceScope = strings.TrimSpace(v)
	}
	if accountId == "" {
		accountId = nbCtx.AccountId
	}

	if _, ok := allocationDimensions[groupBy]; !ok {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error: unsupported group_by %q. Use one of: namespace, workload, service, region, resource_type, tag.", groupBy),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	if groupBy == "tag" && tagKey == "" {
		return core.NBToolResponse{
			Data:   "Error: group_by='tag' requires a tag_key (the tag or k8s label key to group by, e.g. 'team').",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	windowDays, err := parseWindowDays(window)
	if err != nil {
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	window = fmt.Sprintf("%dd", windowDays)

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
	windowStart := windowEnd.AddDate(0, 0, -windowDays)

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Database error: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	rows, err := querySpendAllocation(dbManager, tenantId, accountId, groupBy, tagKey, filter, namespaceScope, windowStart, windowEnd)
	if err != nil {
		slog.Error("spend_allocation: query failed", "error", err, "group_by", groupBy, "window", window, "account_id", accountId)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error querying spend data: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// grand_total / dimension_count are identical on every row (window aggregates),
	// so read them once from the first row. They describe the FULL set before the
	// top-25 LIMIT, so the agent can say "top N of M, total $X" accurately.
	grandTotal := 0.0
	dimensionCount := 0
	if len(rows) > 0 {
		grandTotal = rows[0].GrandTotal
		dimensionCount = rows[0].DimensionCount
	}
	computeShares(rows, grandTotal)

	responseMap := map[string]any{
		"group_by":         groupBy,
		"window":           window,
		"window_start":     windowStart.Format("2006-01-02"),
		"window_end":       windowEnd.Format("2006-01-02"),
		"total_attributed": roundCents(grandTotal),
		"dimension_count":  dimensionCount,
		"shown_count":      len(rows),
		"data":             rows,
	}

	// Coverage (scope_total / unattributed) is only meaningful for an UNFILTERED
	// breakdown of the full dimension. When `filter` is set, grandTotal already
	// covers just the matching rows, so an unfiltered scope_total would make
	// `unattributed` balloon to "everything that didn't match" and the coverage
	// note would falsely blame missing-dimension resources. So compute coverage
	// only when there is no substring filter. (namespaceScope is fine — it scopes
	// both grandTotal and scopeTotal identically.)
	var note string
	switch {
	case filter != "":
		note = fmt.Sprintf("Filtered to %s values matching %q; shares are of the matched total ($%.2f), not the whole account.", groupBy, filter, grandTotal)
	case dimensionCount > len(rows):
		note = fmt.Sprintf("Showing the top %d of %d %s values by spend; shares are of the total attributed spend ($%.2f) across all of them.",
			len(rows), dimensionCount, groupBy, grandTotal)
	default:
		note = "Shares are of the total attributed spend across ALL dimension values. Spend on resources lacking this dimension is excluded."
	}

	if filter == "" {
		// scope_total is the total spend of all in-scope resources (account +
		// optional namespace), INCLUDING those with no value for this dimension.
		// Comparing it to grand_total lets the agent report coverage honestly —
		// important where a dimension is sparse (e.g. non-k8s spend has no workload).
		scopeTotal, scopeErr := querySpendScopeTotal(dbManager, tenantId, accountId, namespaceScope, windowStart, windowEnd)
		if scopeErr != nil {
			slog.Warn("spend_allocation: scope total query failed", "error", scopeErr, "account_id", accountId)
		} else {
			unattributed := scopeTotal - grandTotal
			if unattributed < 0 {
				unattributed = 0
			}
			responseMap["scope_total"] = roundCents(scopeTotal)
			responseMap["unattributed"] = roundCents(unattributed)
			if scopeTotal > 0 && unattributed > 0.005 {
				note += fmt.Sprintf(" Coverage: $%.2f of $%.2f in-scope spend is attributed to a %s (%.1f%%); the remaining $%.2f has no %s value (e.g. non-Kubernetes or unlabeled resources).",
					grandTotal, scopeTotal, groupBy, grandTotal/scopeTotal*100, unattributed, groupBy)
			}
		}
	}
	responseMap["note"] = note
	if groupBy == "tag" {
		responseMap["tag_key"] = tagKey
	}
	if accountId != "" {
		responseMap["account_id"] = accountId
	}
	if filter != "" {
		responseMap["filter"] = filter
	}
	if namespaceScope != "" {
		responseMap["namespace"] = namespaceScope
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
	// GrandTotal and DimensionCount are window aggregates over the FULL result set
	// (all dimension values, before the LIMIT), so every returned row carries the
	// same value. They are read once (from row 0) into the response envelope and
	// excluded from per-row JSON to avoid repetition.
	GrandTotal     float64 `json:"-" db:"grand_total"`
	DimensionCount int     `json:"-" db:"dimension_count"`
}

// allocationDimensions whitelists group_by values to the SQL expression that
// extracts the dimension from cloud_resourses. Whitelisting (not interpolating
// user input) keeps the dynamic dimension safe from injection. The "tag" entry
// is a sentinel; its expression is built with a bound parameter for tag_key.
// All dimension expressions read Nudgebee's collector-normalized fields on
// cloud_resourses (the same schema for every tenant — see k8s-collector
// discovery_handler.py), so they are generic across customers, not specific to
// any one cluster's data shape. Resources lacking a dimension (e.g. non-k8s AWS
// resources have no 'controller') yield NULL and are reported as unattributed.
var allocationDimensions = map[string]string{
	"namespace":     "cr.meta->>'namespace'",
	"service":       "cr.service_name",
	"region":        "cr.region",
	"resource_type": "cr.type",
	"workload":      "COALESCE(NULLIF(cr.meta->>'controller', ''), cr.name)", // owning controller (Deployment/StatefulSet/…), falling back to the resource name
	"tag":           "",                                                      // built from a bound tag_key parameter
}

// computeShares fills in each row's PctOfTotal as a share of grandTotal — the
// spend across ALL dimension values, not just the returned (top-N) rows — so the
// percentages are truthful shares of the whole and (correctly) need not sum to
// 100 when a long tail is truncated. Pure (no DB) so it can be unit-tested.
func computeShares(rows []allocationRow, grandTotal float64) {
	if grandTotal <= 0 {
		return
	}
	for i := range rows {
		rows[i].PctOfTotal = roundCents(rows[i].Amount / grandTotal * 100)
	}
}

func querySpendAllocation(dbManager *common.DatabaseManager, tenantId, accountId, groupBy, tagKey, filter, namespaceScope string, windowStart, windowEnd time.Time) ([]allocationRow, error) {
	args := []any{tenantId, windowStart, windowEnd}

	// Resolve the dimension expression. For tag/label, bind the key as a parameter.
	// Cloud tags live on cr.tags; Kubernetes labels are normalized by the collector
	// under cr.meta->'config'->'labels' (NOT cr.meta->'labels').
	dimExpr := allocationDimensions[groupBy]
	if groupBy == "tag" {
		args = append(args, tagKey)
		idx := len(args)
		dimExpr = fmt.Sprintf("COALESCE(cr.tags->>$%d, cr.meta->'config'->'labels'->>$%d)", idx, idx)
	}

	// Optional single-account scoping (same param reused for resources and spends).
	resourceFilter, spendFilter := "", ""
	if accountId != "" {
		args = append(args, accountId)
		idx := len(args)
		resourceFilter = fmt.Sprintf(" AND cr.account = $%d", idx)
		spendFilter = fmt.Sprintf(" AND spends.cloud_account = $%d", idx)
	}

	// Optional namespace scope (exact match), independent of group_by — lets the
	// agent break ONE namespace down by another dimension (e.g. by workload).
	if namespaceScope != "" {
		args = append(args, namespaceScope)
		resourceFilter += fmt.Sprintf(" AND cr.meta->>'namespace' = $%d", len(args))
	}

	// Optional case-insensitive scope on the dimension value so a question about
	// one dimension value returns just its row(s), not the top-25.
	dimFilter := ""
	if filter != "" {
		args = append(args, "%"+filter+"%")
		dimFilter = fmt.Sprintf(" AND sub.dim ILIKE $%d", len(args))
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
			ROUND(SUM(s.amount)::numeric, 2)::float AS amount,
			ROUND(SUM(SUM(s.amount)) OVER ()::numeric, 2)::float AS grand_total,
			COUNT(*) OVER ()::int AS dimension_count
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
		WHERE s.amount > 0 AND sub.dim IS NOT NULL AND sub.dim <> ''%s
		GROUP BY sub.dim
		ORDER BY SUM(s.amount) DESC
		LIMIT 25`, spendFilter, dimExpr, resourceFilter, dimFilter)

	rows := []allocationRow{}
	err := dbManager.Db.Select(&rows, query, args...)
	return rows, err
}

// querySpendScopeTotal sums spend for all in-scope resources (tenant + optional
// account + optional namespace), regardless of any dimension value — the
// denominator for the attributed-vs-unattributed coverage figure. Mirrors the
// dedup-free 1:1 spend↔cloud_resourses join used by querySpendAllocation.
func querySpendScopeTotal(dbManager *common.DatabaseManager, tenantId, accountId, namespaceScope string, windowStart, windowEnd time.Time) (float64, error) {
	args := []any{tenantId, windowStart, windowEnd}
	spendFilter, resourceFilter := "", ""
	if accountId != "" {
		args = append(args, accountId)
		idx := len(args)
		spendFilter = fmt.Sprintf(" AND spends.cloud_account = $%d", idx)
		resourceFilter = fmt.Sprintf(" AND cr.account = $%d", idx)
	}
	if namespaceScope != "" {
		args = append(args, namespaceScope)
		resourceFilter += fmt.Sprintf(" AND cr.meta->>'namespace' = $%d", len(args))
	}

	query := fmt.Sprintf(`
		SELECT COALESCE(ROUND(SUM(s.amount)::numeric, 2)::float, 0)
		FROM (
			SELECT spends.cloud_resource_id, SUM(spends.amount) AS amount
			FROM spends
			WHERE spends.date >= $2 AND spends.date < $3 AND tenant = $1%s
			GROUP BY spends.cloud_resource_id
		) s
		INNER JOIN (
			SELECT cr.id FROM cloud_resourses cr WHERE cr.tenant = $1%s
		) sub ON s.cloud_resource_id = sub.id
		WHERE s.amount > 0`, spendFilter, resourceFilter)

	var total float64
	err := dbManager.Db.Get(&total, query, args...)
	return total, err
}
