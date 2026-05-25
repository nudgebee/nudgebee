package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Returns recommendations for RightSizing(pod, pv, replica, abandoned_resource), Security(image, CIS), InfraUpgrade(helm chart, k8s api), K8sSpotRecommendation(Spot instance), Configuration(misconfigurations, certificate_expiry, ...) based on the given question. 
	Recommendations can be related to identifying unused/abandoned k8s services/resources/deployments/pv/pvc, security vulnerabilities, or performance optimizations in Kubernetes clusters and cloud infrastructure.`
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
	return `Returns Nudgebee recommendations for RightSizing, Security, InfraUpgrade, K8sSpotRecommendation, Configuration,K8sVersionUpgrade based on the given question.`
}

func (l RecommendationsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// defaultColumns is the explicit column list used in instructions and example
	// queries to prevent SELECT * from pulling the large recommendation JSON into
	// the ReAct scratchpad (can exceed 100 KB per row).
	const defaultColumns = "namespace, resource_name, controller_name, category, rule_name, severity, status, estimated_saving, created_at, updated_at"

	instructions := []string{
		"**Understand the Question Precisely:** Parse user's natural-language question to identify filters: category, severity, status, rule_name, namespace, name/resource_name, controller_name, date ranges, numeric thresholds. Normalize synonyms (e.g., 'prod' -> '%prod%', 'last 30 days' -> INTERVAL '30 days').",
		"**MANDATORY: Use recommendation_execute:** Always call the 'recommendation_execute' tool to retrieve data. Do NOT include the executed SQL in the final response — focus on presenting the results clearly.",
		"**Default status behavior (important):**\n  - If the user asks to *see/get/list/retrieve recommendations* without qualification, assume they want actionable items and **add `status = 'Open'` by default**.\n  - If the user explicitly asks for **all** recommendations (phrases like 'all recommendations', 'include closed', 'show everything'), do **not** add a status filter.\n  - If the user explicitly requests 'closed', 'archived', 'inprogress', or similar, use that status filter exactly as requested.\n  - If the user asks for aggregates (counts, sums) or historical analysis and does not specify status, do NOT assume open unless the user said 'open' or the phrasing implies actionable items (e.g., 'show me recommendations to act on').",
		"**ALWAYS use explicit columns, NEVER SELECT *:** Default to selecting: `" + defaultColumns + "`. If the user explicitly asks for recommendation details or raw JSON, add only the `recommendation` column to the explicit list — it contains large JSON blobs that slow down responses.",
		"**Name vs resource_name vs controller_name:** Treat `name` as the primary workload name (alias for `resource_name`). Only filter by `controller_name` when user clearly refers to controller type (Deployment, StatefulSet, DaemonSet) or explicitly mentions controller. If ambiguous, prefer `name` and document the assumption.",
		"**Namespace matching rules:** If user uses short token like 'prod' prefer fuzzy match `namespace ILIKE '%prod%'`. If user explicitly says 'production' or quotes namespace, prefer exact equality `namespace = 'production'` unless user asked fuzzy.",
		"**Ordering & Limits:** Use `ORDER BY created_at DESC` for recency requests. For interactive row lists, default to `LIMIT 50` and hard cap `LIMIT 100`. Do NOT apply `LIMIT` to aggregates (COUNT/SUM/AVG) or `DISTINCT` queries unless user asks for a limit.",
		"**Aggregations & NULL handling:** When computing SUM/AVG/PERCENT, exclude NULLs: add `AND estimated_saving IS NOT NULL` to denominators or aggregation WHERE clauses as appropriate.",
		"**Date ranges:** Use `updated_at >= 'start' AND updated_at < 'end + 1 day'` semantics for 'between' queries. For 'on date' use `DATE(created_at) = 'YYYY-MM-DD'`.",
		"**Free-text searches:** For textual matches within `recommendation` or `rule_name` use `ILIKE '%term%'` and avoid adding status unless user requested it (except default Open behavior described above).",
		"**Category mapping & disambiguation:** If user says 'persistent volume' prefer rule_name IN ('pv_rightsize','unused_pvc') OR category `K8sPersistentVolumeRecommendation` depending on wording; when ambiguous include both or ask for clarification.",
		"**Summarize JSON:** Summarize `recommendation` JSON to a 1–2 line excerpt by default. Return full JSON only if the user explicitly requests raw JSON output.",
		"**Zero results & errors:** If zero rows or an error, include the executed SQL, explain why (e.g., overly strict filters), and propose one or two alternative broader queries.",
		"**Read-only & Safety:** This agent is read-only for `recommendation_view`. Refuse any DML/DDL (INSERT/UPDATE/DELETE). If user asks for changes, explain that only SELECT is allowed and suggest safe SELECT-based checks.",
	}

	constraints := []string{
		"You are a PostgreSQL expert for `recommendation_view` and MUST ONLY run read-only SELECT queries.",
		"You MUST ONLY use the `recommendation_execute` tool for data access.",
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
	}
	outputFormat := "The output should be a clear and concise summary of the query results in markdown format language using markdown format"
	schema := []string{
		"**recommendation_view:** This view contains comprehensive information about Nudgebee recommendations across various categories.",
		"",
		"**Core Fields:**",
		"- cloud_account_id (STRING): Unique identifier for the cloud account, matches accountId parameter",
		"- namespace (STRING): Kubernetes workload namespace where the resource is deployed",
		"- resource_name (STRING): Name of the specific workload/resource being analyzed",
		"- controller_name (STRING): Kubernetes controller name (Deployment, StatefulSet, DaemonSet, etc.)",
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
		"- status (ENUM): Current state - Open (actionable), InProgress (being worked on), Closed (resolved), Archive (no longer relevant)",
		"",
		"**Rule Classifications:**",
		"- category and rule_name mapping for specific recommendation types",
		"  * Security: image_scan, CIS, k8s-cis-1.23",
		"  * RightSizing: pod_right_sizing, replica_right_sizing, pv_rightsize, abandoned_resource, unused_pvc",
		"  * InfraUpgrade: k8s_helm_compatibility, helm_chart_upgrade, kube_proxy_version, k8s_api_deprecated, eks_cluster_upgrade, eks_add_ons_version",
		"  * Configuration: certificate_expiry, clusterroles_misconfigurations, configmaps_misconfigurations, daemonsets_misconfigurations, deployments_misconfigurations, horizontalpodautoscalers_misconfigurations, misconfigurations, namespaces_misconfigurations, networkpolicies_misconfigurations, nodes_misconfigurations, persistentvolumeclaims_misconfigurations, persistentvolumes_misconfigurations, poddisruptionbudgets_misconfigurations, pods_misconfigurations, rolebindings_misconfigurations, roles_misconfigurations, serviceaccounts_misconfigurations, services_misconfigurations, statefulsets_misconfigurations",
		"  * K8sSpotRecommendation: 'Spot instance recommendation'",
		"",
		"**Query Tips:**",
		"- Use 'Open' status for actionable recommendations",
		"- Filter by category for specific recommendation types",
		"- Order by created_at DESC for latest recommendations",
		"- Use severity filtering for prioritization (Critical > High > Medium > Low > Info)",
		"- Combine category and rule_name for precise filtering",
	}
	examples := []core.NBAgentPromptExample{
		// Basic Queries
		{
			Question:    "What are latest recommendations?",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE status = 'Open' ORDER BY created_at DESC LIMIT 10",
			Explanation: "Retrieves the 10 most recent recommendations with an 'Open' status using explicit columns.",
		},
		{
			Question:    "How many recommendations are there for persistent volume?",
			Answer:      "SELECT count(*) AS count FROM recommendation_view WHERE category = 'RightSizing' AND status = 'Open' AND rule_name in ('pv_rightsize', 'unused_pvc')",
			Explanation: "Counts the number of 'Open' recommendations in the 'RightSizing' category.",
		},

		// Category-based Queries
		{
			Question:    "Get all high security recommendations.",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'Security' AND severity = 'High' AND status = 'Open' ORDER BY created_at DESC LIMIT 10",
			Explanation: "Retrieves the 10 most recent 'High' severity 'Open' recommendations in the 'Security' category.",
		},
		{
			Question:    "Get all recommendations for right sizing.",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE status = 'Open' AND category = 'RightSizing' ORDER BY created_at DESC LIMIT 10",
			Explanation: "Retrieves the 10 most recent 'Open' recommendations in the 'RightSizing' category.",
		},
		{
			Question:    "Get the list of best practices",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'Configuration' AND status = 'Open' ORDER BY created_at DESC LIMIT 50",
			Explanation: "Retrieves 'Open' recommendations in the 'Configuration' category, focusing on best practices for configuration management.",
		},

		// Rule-specific Queries
		{
			Question:    "Get all images issues.",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'Security' AND rule_name = 'image_scan' AND status = 'Open' ORDER BY created_at DESC LIMIT 10",
			Explanation: "Retrieves the 10 most recent 'Open' recommendations related to image scanning.",
		},
		{
			Question:    "Get me list of abandoned services and PVCs",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'RightSizing' AND status = 'Open' AND rule_name IN ('abandoned_resource', 'unused_pvc') ORDER BY created_at DESC LIMIT 10",
			Explanation: "Retrieves the 10 most recent 'Open' recommendations in the 'RightSizing' category related to abandoned services and PVCs.",
		},

		// Multi-criteria and Financial Impact
		{
			Question:    "Show me critical security recommendations with high savings potential",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category = 'Security' AND severity = 'Critical' AND status = 'Open' AND estimated_saving > 100 ORDER BY estimated_saving DESC LIMIT 15",
			Explanation: "Retrieves critical security recommendations that also offer significant cost savings (>$100), ordered by savings potential.",
		},

		// Aggregation and Analytics
		{
			Question:    "What are the total estimated savings by category for open recommendations?",
			Answer:      "SELECT category, COUNT(*) as recommendation_count, ROUND(SUM(estimated_saving), 2) as total_savings, ROUND(AVG(estimated_saving), 2) as avg_savings FROM recommendation_view WHERE status = 'Open' GROUP BY category ORDER BY total_savings DESC",
			Explanation: "Aggregates open recommendations by category showing count, total savings, and average savings per category.",
		},
		{
			Question:    "Show me workloads with multiple high-severity recommendations",
			Answer:      "SELECT namespace, resource_name, controller_name, COUNT(*) as recommendation_count, STRING_AGG(DISTINCT category, ', ') as categories FROM recommendation_view WHERE severity IN ('Critical', 'High') AND status = 'Open' GROUP BY namespace, resource_name, controller_name HAVING COUNT(*) > 1 ORDER BY recommendation_count DESC",
			Explanation: "Groups recommendations by workload to identify resources with multiple high-severity issues across different categories.",
		},

		// Temporal and Production Focus
		{
			Question:    "Find recommendations in production namespaces that haven't been updated in the last 30 days",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE (namespace ILIKE '%prod%' OR namespace ILIKE '%production%') AND status = 'Open' AND updated_at < NOW() - INTERVAL '30 days' ORDER BY created_at ASC LIMIT 20",
			Explanation: "Identifies stale recommendations in production environments that may need attention.",
		},

		// Combination Queries
		{
			Question:    "Get me all security and configuration issues combined",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category IN ('Security', 'Configuration') AND status = 'Open' ORDER BY severity DESC, created_at DESC LIMIT 20",
			Explanation: "Retrieves both security and configuration recommendations together, ordered by severity then recency.",
		},
		{
			Question:    "Show me rightsizing and spot instance recommendations for cost optimization",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE category IN ('RightSizing', 'K8sSpotRecommendation') AND status = 'Open' ORDER BY estimated_saving DESC LIMIT 15",
			Explanation: "Combines rightsizing and spot instance recommendations for comprehensive cost optimization analysis.",
		},
		{
			Question:    "Get all storage-related recommendations including PV, PVC, and S3 issues",
			Answer:      "SELECT " + defaultColumns + " FROM recommendation_view WHERE (category = 'K8sPersistentVolumeRecommendation' OR rule_name IN ('unused_pvc', 'pv_rightsize', 's3_abandoned_buckets', 'volumes_not_attached_for_a_long_time')) AND status = 'Open' ORDER BY estimated_saving DESC LIMIT 20",
			Explanation: "Comprehensive storage optimization covering Kubernetes persistent volumes and cloud storage resources.",
		},

		// Advanced Scenarios
		{
			Question:    "Show recommendations with potential impact analysis including severity and savings",
			Answer:      "SELECT " + defaultColumns + ", CASE WHEN severity = 'Critical' AND estimated_saving > 200 THEN 'High Impact' WHEN severity IN ('Critical', 'High') OR estimated_saving > 100 THEN 'Medium Impact' ELSE 'Low Impact' END as impact_level FROM recommendation_view WHERE status = 'Open' ORDER BY CASE WHEN severity = 'Critical' AND estimated_saving > 200 THEN 1 WHEN severity IN ('Critical', 'High') OR estimated_saving > 100 THEN 2 ELSE 3 END, estimated_saving DESC LIMIT 25",
			Explanation: "Categorizes recommendations by impact level combining severity and financial impact for prioritization.",
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
	tools := []toolcore.NBTool{tools.RecommendationExecuteTool{}}
	return tools
}

func (l RecommendationsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}
