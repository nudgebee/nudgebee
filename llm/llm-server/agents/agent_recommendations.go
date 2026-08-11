package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Returns recommendations for RightSizing(pod, pv, replica, abandoned_resource), Security(image, CIS), InfraUpgrade(helm chart, k8s api), K8sSpotRecommendation(Spot instance), Configuration(misconfigurations, certificate_expiry, ...) based on the given question.
	Recommendations can be related to identifying unused/abandoned k8s services/resources/deployments/pv/pvc, security vulnerabilities, or performance optimizations in Kubernetes clusters and cloud infrastructure.
	Also answers resolution questions — what was done or attempted about a recommendation: pull requests, tickets, deployment changes, workflow runs, who initiated them, and whether they succeeded or failed.`
	toolInput := "Provide question related to recommendations in natural language."
	toolOutput := "The tool will return return the response based on the user question."

	core.RegisterNBAgentFactoryAndTool(RecommendationsAgentName, func(accountId string) (core.NBAgent, error) {
		return newRecommendationAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

const RecommendationsAgentName = "recommendations"

func newRecommendationAgent(accountId string) RecommendationsAgent {
	return RecommendationsAgent{
		accountId: accountId,
	}
}

type RecommendationsAgent struct {
	accountId string
}

func (l RecommendationsAgent) GetName() string {
	return RecommendationsAgentName
}

func (l RecommendationsAgent) GetNameAliases() []string {
	return []string{"Recommendations"}
}

func (l RecommendationsAgent) GetDescription() string {
	return `Returns Nudgebee recommendations for RightSizing, Security, InfraUpgrade, K8sSpotRecommendation, Configuration,K8sVersionUpgrade based on the given question, and the resolution history of those recommendations (PRs, tickets, deployment changes, attempt outcomes).`
}

func (l RecommendationsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// defaultColumns is the explicit column list used in instructions and example
	// queries to prevent SELECT * from pulling the large recommendation JSON into
	// the ReAct scratchpad (can exceed 100 KB per row).
	const defaultColumns = "namespace, service, resource_name, controller_name, category, rule_name, severity, status, estimated_saving, created_at, updated_at"

	instructions := []string{
		"**Understand the Question Precisely:** Parse user's natural-language question to identify filters: category, severity, status, rule_name, namespace, service, name/resource_name, controller_name, date ranges, numeric thresholds. Normalize synonyms (e.g., 'prod' -> '%prod%', 'last 30 days' -> INTERVAL '30 days', 'RDS' -> service ILIKE '%rds%').",
		"**MANDATORY: Use recommendation_execute:** Always call the 'recommendation_execute' tool to retrieve recommendation data ('recommendation_resolution_execute' for resolution history). Do NOT include the executed SQL in the final response — focus on presenting the results clearly.",
		"**Resolution history questions:** When the user asks what happened to a recommendation — whether it was resolved, who or what resolved it, PR/ticket references, or failed attempts — query `recommendation_resolution_view` via the 'recommendation_resolution_execute' tool. When only a resource or rule is named, filter the view by resource_name/rule_name directly, or find the recommendation's id in recommendation_view first and filter by recommendation_id.",
		"**Default status behavior (important):**\n  - If the user asks to *see/get/list/retrieve recommendations* without qualification, assume they want actionable items and **add `status = 'Open'` by default**.\n  - If the user explicitly asks for **all** recommendations (phrases like 'all recommendations', 'include closed', 'show everything'), do **not** add a status filter.\n  - If the user explicitly requests 'closed', 'archived', 'inprogress', or similar, use that status filter exactly as requested.\n  - If the user asks for aggregates (counts, sums) or historical analysis and does not specify status, do NOT assume open unless the user said 'open' or the phrasing implies actionable items (e.g., 'show me recommendations to act on').",
		"**ALWAYS use explicit columns, NEVER SELECT *:** Default to selecting: `" + defaultColumns + "`. If the user explicitly asks for recommendation details or raw JSON, add only the `recommendation` column to the explicit list — it contains large JSON blobs that slow down responses.",
		"**Name vs resource_name vs controller_name:** Treat `name` as the primary workload name (alias for `resource_name`). Only filter by `controller_name` when user clearly refers to controller type (Deployment, StatefulSet, DaemonSet) or explicitly mentions controller. If ambiguous, prefer `name` and document the assumption.",
		"**Namespace matching rules:** If user uses short token like 'prod' prefer fuzzy match `namespace ILIKE '%prod%'`. If user explicitly says 'production' or quotes namespace, prefer exact equality `namespace = 'production'` unless user asked fuzzy.",
		"**Ordering & Limits:** Use `ORDER BY created_at DESC` for recency requests. For interactive row lists, default to `LIMIT 50` and hard cap `LIMIT 100`. Do NOT apply `LIMIT` to aggregates (COUNT/SUM/AVG) or `DISTINCT` queries unless user asks for a limit.",
		"**Aggregations & NULL handling:** When computing SUM/AVG/PERCENT, exclude NULLs: add `AND estimated_saving IS NOT NULL` to denominators or aggregation WHERE clauses as appropriate. For savings totals, sum only positive values (`AND estimated_saving > 0`) — negative values are added-cost rows (e.g. growing nearly-full storage), not savings.",
		"**Date ranges:** Use `updated_at >= 'start' AND updated_at < 'end + 1 day'` semantics for 'between' queries. For 'on date' use `DATE(created_at) = 'YYYY-MM-DD'`.",
		"**Free-text searches:** For textual matches within `recommendation` or `rule_name` use `ILIKE '%term%'` and avoid adding status unless user requested it (except default Open behavior described above).",
		"**Category mapping & disambiguation:** If user says 'persistent volume' prefer rule_name IN ('pv_rightsize','unused_pvc') OR category `K8sPersistentVolumeRecommendation` depending on wording; when ambiguous include both or ask for clarification.",
		"**Summarize JSON:** Summarize `recommendation` JSON to a 1–2 line excerpt by default. Return full JSON only if the user explicitly requests raw JSON output.",
		"**Zero results & errors:** If zero rows or an error, include the executed SQL, explain why (e.g., overly strict filters), and propose one or two alternative broader queries.",
		"**Read-only & Safety:** This agent is read-only for `recommendation_view` and `recommendation_resolution_view`. Refuse any DML/DDL (INSERT/UPDATE/DELETE). If user asks for changes, explain that only SELECT is allowed and suggest safe SELECT-based checks.",
	}

	constraints := []string{
		"You are a PostgreSQL expert for `recommendation_view` and `recommendation_resolution_view` and MUST ONLY run read-only SELECT queries.",
		"You MUST ONLY use the `recommendation_execute` and `recommendation_resolution_execute` tools for data access.",
		"Apply the default `status='Open'` behavior unless user explicitly asks for 'all', or a different status.",
		"NEVER use SELECT * — always use explicit column lists. Only add the `recommendation` column when the user explicitly asks for details/raw JSON.",
		"Enforce a hard maximum row limit of 100 unless user explicitly requests more and the system allows it.",
		"Timestamps must be returned in ISO 8601 (UTC) unless the user requests a different timezone.",
	}

	toolUsage := map[string][]string{
		tools.ToolRecommendationExecuteSql: {
			"Use this tool to execute validated, read-only SQL queries against the `recommendation_view` view.",
			"Always pass the final SQL string that will be executed. Do NOT include the SQL in the final response to the user.",
			"Input: a safe SELECT query; Output: rows returned by the query or an error.",
			"On error, capture the error message and return an explanation + a non-destructive fallback query suggestion.",
			"Output: the data returned by the sql query.",
		},
		tools.ToolRecommendationResolutionExecuteSql: {
			"Use this tool for resolution history — what was attempted or done about a recommendation: pull requests, tickets, deployment changes, workflow runs, their outcomes and references.",
			"Query `recommendation_resolution_view` with read-only SELECTs; filter by recommendation_id, resource_name, rule_name, type, resolver_type, or status.",
			"Input: a safe SELECT query; Output: resolution attempt rows or an error.",
		},
	}
	outputFormat := "Output a Markdown table as the primary format. Columns: Namespace | Resource | Category | Severity | Est. Saving ($/mo) | Rule | Status | Age. " +
		"Fit the first column to the rows: Namespace applies only to Kubernetes recommendations (service = 'kubernetes'). When the rows are cloud-resource recommendations (namespace NULL, service = a cloud service), replace Namespace with Service and render the short service name (AmazonRDS -> RDS, AmazonEC2 -> EC2); when the result set mixes both, show both columns with \"—\" where a value does not apply. " +
		"Sort rows by estimated_saving descending (nulls last). For a null/zero estimated_saving show \"—\", never \"$0.00\". A negative estimated_saving means resolving it ADDS cost (e.g. growing nearly-full storage) — render it as added cost (e.g. \"+$12/mo cost\"), never as savings. " +
		"After the table, add one line: the row count and total quantified savings (sum of positive estimated_saving only). Append [recommendation_execute] after the table heading."
	schema := []string{
		"**recommendation_view:** This view contains comprehensive information about Nudgebee recommendations across various categories.",
		"",
		"**Core Fields:**",
		"- cloud_account_id (STRING): Unique identifier for the cloud account, matches accountId parameter",
		"- namespace (STRING): Kubernetes workload namespace where the resource is deployed. NULL for cloud-resource recommendations (RDS, EC2, ...) — they have no namespace",
		"- service (STRING): Source service of the resource — 'kubernetes' for K8s workload recommendations, the cloud service name for cloud-resource recommendations (e.g. AmazonRDS, AmazonEC2, AmazonS3)",
		"- resource_name (STRING): Name of the specific workload/resource being analyzed",
		"- controller_name (STRING): Kubernetes controller name (Deployment, StatefulSet, DaemonSet, etc.); NULL for cloud resources",
		"",
		"**Financial Fields:**",
		"- estimated_saving (DECIMAL): Estimated cost savings in USD if recommendation is implemented",
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
		"",
		"**recommendation_resolution_view:** One row per resolution attempt on a recommendation (how it is being, or was, resolved). Queried via the 'recommendation_resolution_execute' tool.",
		"- recommendation_id (STRING): The recommendation the attempt belongs to (join key with recommendation_view id)",
		"- type (ENUM): Artifact created - PullRequest, Ticket, DeploymentChange, CloudResource, WorkflowExecution, EventResolution",
		"- type_reference_id (STRING): Reference to that artifact - PR URL, ticket id, change id",
		"- resolver_type (ENUM): Who initiated it - User, AutoOptimize, AutoRunbook, NBLLM (the AI agent)",
		"- status (ENUM): Attempt state - InProgress (artifact open, work ongoing), Success (completed), Failed (rejected or errored)",
		"- status_message (STRING): Human-readable outcome detail (failure reason, close note)",
		"- pr_lifecycle_state (STRING): For PullRequest attempts, the PR's lifecycle state if tracked",
		"- recommendation_status (ENUM): Current status of the parent recommendation",
		"- resource_name, rule_name, category, severity (STRING/ENUM): Context from the parent recommendation",
		"- created_at, updated_at (TIMESTAMP): When the attempt was registered and last updated",
	}
	// Four structurally-distinct examples, one per query shape. They teach the
	// patterns (explicit columns, status filter, aggregation, financial threshold,
	// nulls-last savings ordering) the agent generalizes from — not an exhaustive
	// catalog. Construct other queries by applying the instruction rules above.
	examples := []core.NBAgentPromptExample{
		// 1. Basic list — explicit columns + default Open status.
		{
			Question:    "What are the latest recommendations?",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE status = 'Open' ORDER BY created_at DESC LIMIT 10",
			Explanation: "Lists recent Open recommendations using explicit columns (never SELECT *).",
		},
		// 2. Aggregate — GROUP BY with COUNT/SUM, excluding NULL savings from the sum.
		{
			Question:    "What are the total estimated savings by category for open recommendations?",
			Answer:      "SELECT category, COUNT(*) as recommendation_count, ROUND(SUM(estimated_saving), 2) as total_savings FROM recommendation_view WHERE status = 'Open' AND estimated_saving > 0 GROUP BY category ORDER BY total_savings DESC",
			Explanation: "Aggregates by category; no LIMIT on aggregates; only positive savings summed (NULLs and added-cost negatives excluded).",
		},
		// 3. Multi-criteria with a financial threshold, ranked by savings.
		{
			Question:    "Show me critical security recommendations with high savings potential",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'Security' AND severity = 'Critical' AND status = 'Open' AND estimated_saving > 100 ORDER BY estimated_saving DESC LIMIT 15",
			Explanation: "Combines category, severity, and a savings threshold, ordered by dollar impact.",
		},
		// 4. Storage rightsizing — rule_name filter, savings ranked nulls-last.
		{
			Question:    "Which PVCs are over-provisioned or abandoned?",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'RightSizing' AND status = 'Open' AND rule_name IN ('pv_rightsize', 'unused_pvc', 'abandoned_resource') ORDER BY estimated_saving DESC NULLS LAST LIMIT 20",
			Explanation: "Storage rightsizing via rule_name; NULLS LAST keeps unquantified rows from sorting above real savings.",
		},
		// 5. Resolution history — the resolution view, filtered by resource + rule.
		{
			Question:    "Was anything done about the pod rightsizing for checkout-service?",
			Answer:      "SELECT type, type_reference_id, resolver_type, status, status_message, recommendation_status, updated_at FROM recommendation_resolution_view WHERE resource_name = 'checkout-service' AND rule_name = 'pod_right_sizing' ORDER BY updated_at DESC LIMIT 10",
			Explanation: "Resolution attempts for one workload's recommendation via recommendation_resolution_execute; latest attempts first, with artifact references and outcomes.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "a PostgreSQL database expert",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Schema:       schema,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "recommendations",
			Format: core.NBAgentPromptRagFormatJson,
		},
	}
}

func (p RecommendationsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	tools := []toolcore.NBTool{tools.RecommendationExecuteTool{}, tools.RecommendationResolutionExecuteTool{}}
	return tools
}

func (l RecommendationsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
