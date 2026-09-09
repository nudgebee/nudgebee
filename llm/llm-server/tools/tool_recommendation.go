package tools

import (
	"fmt"
	"nudgebee/llm/tools/core"
)

func init() {
	core.RegisterNBToolFactory(ToolRecommendationExecuteSql, func(accountId string) (core.NBTool, error) {
		return RecommendationExecuteTool{}, nil
	})
}

// PrimaryRecommendationRank is the window expression that ranks the alternative
// recommendations describing ONE opportunity, so callers can keep just the
// best of them. alias is the table alias of `recommendation` in the caller's
// query.
//
// Rows sharing a dedupe_group are alternative ways to act on the same thing —
// AWS Cost Explorer returns a commitment purchase as 1yr/3yr × All/No-Upfront
// per plan type, and a second producer writes its own row for the same
// opportunity. Only one is purchasable, so summing them overstates savings by
// roughly the number of variants.
//
// This MUST stay identical to recommendation_groupings_v2's window in
// api-server (services/query/metadata.go) — same partition key, same
// highest-savings-wins ordering. That is what keeps the AI's savings total and
// the Optimise page's Total Savings card equal by construction rather than by
// coincidence. TestPrimaryRecommendationRankShape pins the shape here; changing
// one side without the other makes the two surfaces disagree again.
//
// Multi-cloud note: the providers are not treated symmetrically, and that is a
// wart, not a design. AWS producers set dedupe_group; Azure does not, so it
// needs the provider-specific branch below (now duplicated in both services);
// GCP currently ships no commitment recommendations, so nothing groups there
// yet. When GCP CUDs arrive they must NOT get a third special case — the
// durable fix is producer-side dedupe_group for every provider, after which
// this CASE collapses to the dedupe_group branch alone.
// accountAlias is the alias of the joined `cloud_accounts` row, needed for the
// Azure fallback below.
func PrimaryRecommendationRank(alias, accountAlias string) string {
	return fmt.Sprintf(`ROW_NUMBER() OVER (
			PARTITION BY
				CASE
					WHEN %[1]s.dedupe_group IS NOT NULL AND %[1]s.dedupe_group <> '' THEN %[1]s.dedupe_group
					WHEN %[1]s.resource_id IS NOT NULL THEN %[1]s.resource_id::text
					-- Azure ingestion sets neither a dedupe_group nor a resource_id on
					-- much of its output, so without this branch each such row becomes
					-- its own partition here while the Optimise page groups them.
					-- Gated on the provider first so non-Azure rows never detoast the
					-- recommendation jsonb.
					WHEN LOWER(%[2]s.cloud_provider) = 'azure' AND %[1]s.recommendation->>'recommendation_type_id' IS NOT NULL
						THEN %[1]s.cloud_account_id::text || ':'
							|| COALESCE(%[1]s.recommendation->>'recommendation_type_id', '') || ':'
							|| COALESCE(%[1]s.recommendation->>'ext_subid', %[1]s.recommendation->>'subscription_id', '') || ':'
							|| COALESCE(%[1]s.recommendation->>'ext_sku', '')
					ELSE %[1]s.id::text
				END,
				%[1]s.category
			ORDER BY
				-- Terminal rows sort last, so a retired variant can only win a group
				-- with nothing live in it. Ranked on savings alone it would win
				-- outright, marking the live row non-primary and removing it from
				-- every savings total.
				CASE WHEN %[1]s.status IN ('Archive', 'Closed') THEN 1 ELSE 0 END,
				%[1]s.estimated_savings DESC, %[1]s.updated_at DESC, %[1]s.id
		)`, alias, accountAlias)
}

// PrimarySavingsSubquery builds a subquery over `recommendation` that keeps one
// row per opportunity (the highest-saving alternative), projecting groupCol and
// estimated_savings. scopeFilter is AND-ed into the inner WHERE and must be
// written against alias `r2` (e.g. " AND r2.tenant_id = $1").
//
// Every savings roll-up in this service goes through here so the AI cannot
// report one total from spend_summary and a different one from the
// recommendations agent.
// Callers should scope as narrowly as they can: the window function is an
// optimisation fence, so a filter left on the outer join (e.g. one account)
// cannot be pushed in, and the rank would be computed across the whole tenant —
// 247k open recommendations on the largest dev tenant.
func PrimarySavingsSubquery(groupCol, scopeFilter string) string {
	return fmt.Sprintf(`(
			SELECT %[1]s, SUM(estimated_savings) AS estimated_savings
			FROM (
				SELECT r2.%[1]s, r2.estimated_savings, %[2]s AS dedupe_rank
				FROM recommendation r2
				JOIN cloud_accounts ca2 ON ca2.id = r2.cloud_account_id
				WHERE r2.status = 'Open'%[3]s
			) primary_recs
			WHERE dedupe_rank = 1
			GROUP BY %[1]s
		)`, groupCol, PrimaryRecommendationRank("r2", "ca2"), scopeFilter)
}

// recommendationView is the read-only projection the recommendations agent
// queries. Composed at init (not a const) so the dedupe window comes from the
// single shared definition rather than a second copy.
var recommendationView = `
		SELECT r.id::text as id,
			t.name AS tenant,
			ca.account_name AS account,
			ca.id::text AS cloud_account_id,
			(
				CASE
					WHEN cr.meta ->> 'namespace' IS NOT NULL THEN cr.meta ->> 'namespace'
					WHEN cr.meta -> 'config' ->> 'namespace' IS NOT NULL THEN cr.meta -> 'config' ->> 'namespace'
					WHEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace'
					WHEN r.recommendation -> 'metadata' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'metadata' ->> 'namespace'
					ELSE r.recommendation ->> 'namespace'
				END
        	) AS namespace,
			COALESCE(cr.service_name, r.recommendation ->> 'service_name') AS service,
			cr.name AS resource_name,
			r.recommendation ->> 'controller_name'::text AS controller_name,
			-- Both COALESCE branches must be numeric so the expression's type
			-- matches the real recommendation_view (numeric) and Postgres can
			-- resolve ROUND(estimated_saving, 2), which only exists for
			-- numeric (42883 otherwise). r.estimated_savings is stored as
			-- double precision, so we cast the branch inline rather than
			-- wrapping the whole COALESCE -- an outer cast would force
			-- Postgres to first widen the numeric first-branch to double
			-- precision (its arg-precedence rule), then cast the double back
			-- to numeric, losing the exact decimal precision of the first
			-- branch on the round trip.
			COALESCE((r.recommendation ->> 'estimated_saving'::text)::numeric, r.estimated_savings::numeric) AS estimated_saving,
			r.created_at,
			r.updated_at,
			r.recommendation::text AS recommendation,
			r.recommendation_action,
			r.category,
			r.note,
			r.severity,
			r.status,
			r.rule_name,
			r.dismissed_reason,
			r.is_dismissed,
			r.snoozed_until,
			r.account_object_id,
			r.updated_by::text,
			r.finops_score,
			r.finops_band,
			r.finops_score_breakdown ->> 'safety_band' AS safety_band,
			r.finops_score_breakdown -> 'impact_summary' ->> 'safety_reason' AS safety_reason,
			(r.finops_score_breakdown -> 'impact_summary' ->> 'dependent_count')::int AS dependent_count,
			(r.finops_score_breakdown -> 'impact_summary' ->> 'production_dependents')::int AS production_dependents,
			r.finops_score_breakdown -> 'impact_summary' -> 'dependents' AS dependents,
			r.dedupe_group,
			-- One row per opportunity; see PrimaryRecommendationRank.
			(` + PrimaryRecommendationRank("r", "ca") + ` = 1) AS is_primary_recommendation
		FROM recommendation r
		LEFT JOIN cloud_resourses cr ON r.resource_id = cr.id
		JOIN tenant t ON r.tenant_id = t.id
		JOIN cloud_accounts ca ON r.cloud_account_id = ca.id
	`

// RecommendationDefaultColumns is the explicit column list queries should
// default to, instead of SELECT * (the recommendation JSON can exceed 100 KB
// per row). id and safety_band are part of the set on purpose: callers need
// the id to act on a row, and safety_band is the gate to present before any
// apply.
const RecommendationDefaultColumns = "id, namespace, service, resource_name, controller_name, category, rule_name, severity, status, estimated_saving, safety_band, created_at, updated_at"

// ToolPrompt implements core.NBToolPromptProvider: the SQL-construction rules,
// view schema, and canonical query shapes for recommendation_view. This is the
// single source both the recommendations agent and the FinOps agent render, so
// the two paths to this data cannot drift apart.
func (m RecommendationExecuteTool) ToolPrompt() []string {
	defaultColumns := RecommendationDefaultColumns
	return []string{
		"**Default status behavior (important):**\n  - If the user asks to *see/get/list/retrieve recommendations* without qualification, assume they want actionable items and **add `status = 'Open'` by default**.\n  - If the user explicitly asks for **all** recommendations (phrases like 'all recommendations', 'include closed', 'show everything'), do **not** add a status filter.\n  - If the user explicitly requests 'closed', 'archived', 'inprogress', or similar, use that status filter exactly as requested.\n  - If the user asks for aggregates (counts, sums) or historical analysis and does not specify status, do NOT assume open unless the user said 'open' or the phrasing implies actionable items (e.g., 'show me recommendations to act on').",
		"**ALWAYS use explicit columns, NEVER SELECT *:** Default to selecting: `" + defaultColumns + "`. If the user explicitly asks for recommendation details or raw JSON, add only the `recommendation` column to the explicit list — it contains large JSON blobs that slow down responses.",
		"**Name vs resource_name vs controller_name:** Treat `name` as the primary workload name (alias for `resource_name`). Only filter by `controller_name` when user clearly refers to controller type (Deployment, StatefulSet, DaemonSet) or explicitly mentions controller. If ambiguous, prefer `name` and document the assumption.",
		"**Namespace matching rules:** If user uses short token like 'prod' prefer fuzzy match `namespace ILIKE '%prod%'`. If user explicitly says 'production' or quotes namespace, prefer exact equality `namespace = 'production'` unless user asked fuzzy.",
		"**An aggregate is the answer — do not follow it with a listing:** when a COUNT/SUM/GROUP BY already answers what was asked, report it and stop. Re-listing the rows \"for context\" adds nothing the aggregate did not already state, and an unlimited ORDER BY over an account's recommendations is the slowest query this tool runs. List rows only when the user asked WHICH items, and then always with a LIMIT.",
		"**Ordering & Limits:** Use `ORDER BY created_at DESC` for recency requests. For interactive row lists, default to `LIMIT 50` and hard cap `LIMIT 100`. Do NOT apply `LIMIT` to aggregates (COUNT/SUM/AVG) or `DISTINCT` queries unless user asks for a limit.",
		"**Aggregations & NULL handling:** When computing SUM/AVG/PERCENT, exclude NULLs: add `AND estimated_saving IS NOT NULL` to denominators or aggregation WHERE clauses as appropriate. For savings totals, sum only positive values (`AND estimated_saving > 0`) — negative values are added-cost rows (e.g. growing nearly-full storage), not savings.",
		"**MANDATORY for savings totals — deduplicate alternatives:** every SUM of estimated_saving MUST add `AND is_primary_recommendation` . Rows sharing a `dedupe_group` are alternative ways to act on ONE opportunity (a commitment purchase comes back from AWS as 1yr/3yr × All/No-Upfront variants, and only one can be bought); `is_primary_recommendation` keeps the highest-saving variant of each. Without it a single EC2 commitment counted 11 times and inflated an account total by ~2.4x. Counts of actionable items should use it too. Do NOT apply it when the user asks to see the individual purchase options.",
		"**Report commitments as one opportunity:** when listing recommendations, show the primary row per dedupe_group and note that other purchase terms exist (e.g. \"best of 11 EC2 Savings Plan options\"), rather than listing every variant as a separate finding.",
		"**A commitment total is a conservative floor, not an exact figure:** one dedupe_group holds mutually-exclusive options (1yr vs 3yr, All- vs No-Upfront, and a Savings Plan vs Reserved Instances covering the same usage) and sometimes separately-purchasable per-instance-family Reserved Instances. Keeping the best single option per service never overstates, but can understate where per-family purchases would genuinely stack. Call it the \"best available commitment option per service\" and note the realisable figure depends on the purchase mix — never present it as an exact ceiling.",
		"**Split totals by savings type:** when reporting a total, separate workload optimizations (RightSizing, Configuration, K8sSpotRecommendation, InfraUpgrade) from commitment purchases (rule_name LIKE 'aws_native_ce%' or dedupe_group LIKE 'aws_commitment%'). They are not additive in practice — commitments are sized against CURRENT usage, so rightsizing first reduces what is worth committing to. State that caveat whenever both appear.",
		"**Date ranges:** Use `updated_at >= 'start' AND updated_at < 'end + 1 day'` semantics for 'between' queries. For 'on date' use `DATE(created_at) = 'YYYY-MM-DD'`.",
		"**Free-text searches:** For textual matches within `recommendation` or `rule_name` use `ILIKE '%term%'` and avoid adding status unless user requested it (except default Open behavior described above).",
		"**Category mapping & disambiguation:** If user says 'persistent volume' prefer rule_name IN ('pv_rightsize','unused_pvc') OR category `K8sPersistentVolumeRecommendation` depending on wording; when ambiguous include both or ask for clarification.",
		"**Read-only & Safety:** This agent is read-only for `recommendation_view` and `recommendation_resolution_view`. Refuse any DML/DDL (INSERT/UPDATE/DELETE). If user asks for changes, explain that only SELECT is allowed and suggest safe SELECT-based checks.",
		"**recommendation_view:** This view contains comprehensive information about Nudgebee recommendations across various categories.",
		"",
		"**Core Fields:**",
		"- cloud_account_id (STRING): Unique identifier for the cloud account (a UUID — `account` holds the human name). Results are ALREADY scoped to the account the user selected, so do NOT add an account filter of your own; filtering on a name here matches nothing and silently returns zero rows.",
		"- namespace (STRING): Kubernetes workload namespace where the resource is deployed. NULL for cloud-resource recommendations (RDS, EC2, ...) — they have no namespace",
		"- service (STRING): Source service of the resource — 'kubernetes' for K8s workload recommendations, the cloud service name for cloud-resource recommendations (e.g. AmazonRDS, AmazonEC2, AmazonS3)",
		"- resource_name (STRING): Name of the specific workload/resource being analyzed",
		"- controller_name (STRING): Kubernetes controller name (Deployment, StatefulSet, DaemonSet, etc.); NULL for cloud resources",
		"",
		"**Financial Fields:**",
		"- estimated_saving (NUMERIC): Estimated MONTHLY cost savings in USD if recommendation is implemented",
		"- is_primary_recommendation (BOOLEAN): TRUE for the highest-saving LIVE row within its dedupe_group (or per resource+category when no group); retired rows never outrank an open one. REQUIRED filter on every savings SUM — see the aggregation rules",
		"- dedupe_group (STRING): Marks rows that are alternative ways to act on the SAME opportunity, e.g. 'aws_commitment:<account>:AmazonEC2' for the 1yr/3yr × All/No-Upfront purchase variants of one Savings Plan. NULL for standalone recommendations",
		"",
		"**Safety / Impact Fields (blast radius from the dependency graph):**",
		"- safety_band (STRING): How safe it is to act — 'safe', 'review', 'risky', 'unknown'; NULL when impact has not been computed yet",
		"- safety_reason (STRING): One-line reason behind the band (e.g. '2 production dependent(s) would be affected')",
		"- dependent_count (INT), production_dependents (INT): Number of dependent services, and how many are production",
		"- dependents (JSON): Compact list of the dependent services (name, namespace, hops away); select only when the caller asks for the blast radius in detail",
		"- finops_score (INT 0-100), finops_band (STRING): Priority score and band ('Act Now', 'Critical', 'High', 'Medium', 'Low') — prioritization, NOT apply-safety; safety_band is the safety verdict",
		"",
		"**Temporal Fields:**",
		"- created_at (TIMESTAMP): When the recommendation was first created",
		"- updated_at (TIMESTAMP): When the recommendation was last modified",
		"",
		"**Content Fields:**",
		"- recommendation (JSON/TEXT): Detailed recommendation data including specific actions and metrics",
		"",
		"**Classification Fields:**",
		"- category (ENUM): Type of recommendation - Configuration, RightSizing, InfraUpgrade, Security, K8sSpotRecommendation",
		"- severity (ENUM): Impact level - Critical, High, Medium, Low, Info (ordered by priority)",
		"- status (ENUM): Current state - Open (actionable), InProgress (being worked on), Closed (resolved), Dismissed (user-suppressed), Archive (no longer relevant)",
		"- is_dismissed (BOOLEAN), dismissed_reason (STRING), snoozed_until (TIMESTAMP): Dismissal details. A snoozed recommendation is Dismissed with snoozed_until set and returns to Open automatically when the timestamp passes; snoozed_until NULL means a permanent dismissal.",
		"",
		"**Rule Classifications:**",
		"- category and rule_name mapping for specific recommendation types",
		"  * Security: image_scan, CIS, k8s-cis-1.23",
		"  * RightSizing: pod_right_sizing, replica_right_sizing, pv_rightsize, abandoned_resource, unused_pvc",
		"  * InfraUpgrade: k8s_helm_compatibility, helm_chart_upgrade, kube_proxy_version, k8s_api_deprecated, eks_cluster_upgrade, eks_add_ons_version",
		"  * Configuration: certificate_expiry, clusterroles_misconfigurations, configmaps_misconfigurations, daemonsets_misconfigurations, deployments_misconfigurations, horizontalpodautoscalers_misconfigurations, misconfigurations, namespaces_misconfigurations, networkpolicies_misconfigurations, nodes_misconfigurations, persistentvolumeclaims_misconfigurations, persistentvolumes_misconfigurations, poddisruptionbudgets_misconfigurations, pods_misconfigurations, rolebindings_misconfigurations, roles_misconfigurations, serviceaccounts_misconfigurations, services_misconfigurations, statefulsets_misconfigurations",
		"  * K8sSpotRecommendation: 'Spot instance recommendation'",
		"- Cloud-resource recommendations use provider-prefixed rule_names (aws_*, gcp_*, azure_* — e.g. aws_rds_instance_reserved, aws_ec2_underutilized) across the same categories; identify them by service (not 'kubernetes') or the rule_name prefix.",
		"",
		"**Query Tips:**",
		"- Use 'Open' status for actionable recommendations",
		"- Filter by category for specific recommendation types",
		"- Order by created_at DESC for latest recommendations",
		"- Use severity filtering for prioritization (Critical > High > Medium > Low > Info)",
		"- Combine category and rule_name for precise filtering",
		"- Rounding a savings total works directly: ROUND(SUM(estimated_saving), 2). estimated_saving is already numeric in the view.",
		"",
		"**Canonical savings queries (adapt, do not invent new shapes):**",
		"- Total by category: SELECT category, COUNT(*) as recommendation_count, ROUND(SUM(estimated_saving), 2) as total_savings FROM recommendation_view WHERE status = 'Open' AND estimated_saving > 0 AND is_primary_recommendation GROUP BY category ORDER BY total_savings DESC",
		"- Account total split by type: SELECT CASE WHEN dedupe_group LIKE 'aws_commitment%' OR rule_name LIKE 'aws_native_ce%' THEN 'commitment_purchases' ELSE 'workload_optimizations' END AS savings_type, COUNT(*) AS opportunities, ROUND(SUM(estimated_saving), 2) AS monthly_savings FROM recommendation_view WHERE status = 'Open' AND estimated_saving > 0 AND is_primary_recommendation GROUP BY 1",
	}
}

const ToolRecommendationExecuteSql = "recommendation_execute"

type RecommendationExecuteTool struct {
}

func (m RecommendationExecuteTool) Name() string {
	return ToolRecommendationExecuteSql
}

func (m RecommendationExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m RecommendationExecuteTool) Description() string {
	return "Executes a SQL query for recommendation_view and returns the result. Columns: id, namespace, service, resource_name, estimated_saving, category, severity, status, rule_name, is_dismissed, dismissed_reason, snoozed_until, recommendation, finops_score, finops_band, safety_band, safety_reason, dependent_count, production_dependents, dependents, dedupe_group, is_primary_recommendation. " +
		"Savings totals MUST filter is_primary_recommendation (rows sharing a dedupe_group are alternative ways to buy ONE opportunity — only one is purchasable, so summing them overstates savings)."
}

func (m RecommendationExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "recommendation_view SQL Query to execute",
			},
		},
		Required: []string{"command"},
	}
}

// recommendationMaxJSONChars caps the recommendation JSON field in tool
// responses to prevent token bloat in the ReAct scratchpad. The full JSON
// can exceed 100K chars; truncating to 500 keeps context small while
// preserving the most relevant leading content.
const recommendationMaxJSONChars = 500

// truncateRecommendationJSON caps the "recommendation" field in each row
// so that oversized JSON blobs do not inflate the LLM context window.
func truncateRecommendationJSON(r map[string]any, _ int, _ int) map[string]any {
	v, ok := r["recommendation"]
	if !ok || v == nil {
		return r
	}
	var s string
	switch val := v.(type) {
	case string:
		s = val
	case []byte:
		s = string(val)
	default:
		s = fmt.Sprintf("%v", val)
	}

	const suffix = "...(truncated)"
	if len(s) > recommendationMaxJSONChars {
		maxContentLen := recommendationMaxJSONChars - len(suffix)
		if maxContentLen < 0 {
			maxContentLen = 0
		}
		s = s[:maxContentLen] + suffix
	}
	r["recommendation"] = s
	return r
}

func (m RecommendationExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	resp, _, err := sqlToolCall(nbRequestContext, input.Command, "recommendation_view", recommendationView, 10, truncateRecommendationJSON)
	if err == nil {
		resp.References = []core.NBToolResponseReference{
			core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"optimize", "summary"}, "Recommendation Details", nil, ""),
		}
	}
	return resp, err
}
