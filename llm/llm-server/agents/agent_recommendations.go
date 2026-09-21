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
	toolOutput := "Returns the recommendations as a user-ready markdown table (with recommendation ids and safety bands). " +
		"When this answers the user's question, relay the markdown as-is — do NOT re-encode it into JSON or restructure it."

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
	// id and safety_band are part of the default set on purpose: callers (the
	// FinOps agent's apply/hand-off tools) need the recommendation id to act on
	// a row, and safety_band is the gate they must present before any apply —
	// omitting them forced callers to answer "which one?" and "how safe?" with
	// guesses.
	defaultColumns := tools.RecommendationDefaultColumns

	instructions := []string{
		"**Understand the Question Precisely:** Parse user's natural-language question to identify filters: category, severity, status, rule_name, namespace, service, name/resource_name, controller_name, date ranges, numeric thresholds. Normalize synonyms (e.g., 'prod' -> '%prod%', 'last 30 days' -> INTERVAL '30 days', 'RDS' -> service ILIKE '%rds%').",
		"**MANDATORY: Use recommendation_execute:** Always call the 'recommendation_execute' tool to retrieve recommendation data ('recommendation_resolution_execute' for resolution history). Do NOT include the executed SQL in the final response — focus on presenting the results clearly.",
		"**Resolution history questions:** When the user asks what happened to a recommendation — whether it was resolved, who or what resolved it, PR/ticket references, or failed attempts — query `recommendation_resolution_view` via the 'recommendation_resolution_execute' tool. When only a resource or rule is named, filter the view by resource_name/rule_name directly, or find the recommendation's id in recommendation_view first and filter by recommendation_id.",
		"**Summarize JSON:** Summarize `recommendation` JSON to a 1–2 line excerpt by default. Return full JSON only if the user explicitly requests raw JSON output.",
		"**Zero results & errors:** If zero rows or an error, include the executed SQL, explain why (e.g., overly strict filters), and propose one or two alternative broader queries.",
	}
	// The SQL-construction rules, view schema and canonical query shapes live on
	// the tools (single source; the FinOps agent renders the same lines), so the
	// two paths to this data cannot drift apart.
	instructions = append(instructions, tools.RecommendationExecuteTool{}.ToolPrompt()...)
	instructions = append(instructions, tools.RecommendationResolutionExecuteTool{}.ToolPrompt()...)

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
	outputFormat := "Output a Markdown table as the primary format. Columns: Namespace | Resource | Category | Severity | Est. Saving ($/mo) | Safety | Rule | Status | Age. " +
		"Safety renders safety_band ('—' when NULL). ALWAYS include each row's recommendation id — append an Id column (or an id list after the table when the table is wide); callers need the id to act on a recommendation, so never drop it. " +
		"Fit the first column to the rows: Namespace applies only to Kubernetes recommendations (service = 'kubernetes'). When the rows are cloud-resource recommendations (namespace NULL, service = a cloud service), replace Namespace with Service and render the short service name (AmazonRDS -> RDS, AmazonEC2 -> EC2); when the result set mixes both, show both columns with \"—\" where a value does not apply. " +
		"Sort rows by estimated_saving descending (nulls last). For a null/zero estimated_saving show \"—\", never \"$0.00\". A negative estimated_saving means resolving it ADDS cost (e.g. growing nearly-full storage) — render it as added cost (e.g. \"+$12/mo cost\"), never as savings. " +
		"After the table, add one line: the row count and total quantified savings (sum of positive estimated_saving only). Append [recommendation_execute] to a column header or cell of the table (in-table placement survives callers that relay only the table)."
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
		// 2. Aggregate — GROUP BY with COUNT/SUM, excluding NULL savings and
		// deduplicating alternative purchase options.
		{
			Question:    "What are the total estimated savings by category for open recommendations?",
			Answer:      "SELECT category, COUNT(*) as recommendation_count, ROUND(SUM(estimated_saving)::numeric, 2) as total_savings FROM recommendation_view WHERE status = 'Open' AND estimated_saving > 0 AND is_primary_recommendation GROUP BY category ORDER BY total_savings DESC",
			Explanation: "Aggregates by category; no LIMIT on aggregates; only positive savings summed (NULLs and added-cost negatives excluded); is_primary_recommendation collapses alternative purchase variants so one opportunity counts once.",
		},
		// 3. Split total — workload optimizations vs commitment purchases, the
		// shape every account-level savings answer should take.
		{
			Question: "How much can we save in total on this account?",
			Answer:   "SELECT CASE WHEN dedupe_group LIKE 'aws_commitment%' OR rule_name LIKE 'aws_native_ce%' THEN 'commitment_purchases' ELSE 'workload_optimizations' END AS savings_type, COUNT(*) AS opportunities, ROUND(SUM(estimated_saving)::numeric, 2) AS monthly_savings FROM recommendation_view WHERE status = 'Open' AND estimated_saving > 0 AND is_primary_recommendation GROUP BY 1",
			Explanation: "Splits the total into workload optimizations and commitment purchases (they are not additive — commitments are sized against current usage, so right-size first), " +
				"and dedupes so each commitment counts its best single purchase option rather than every term/payment variant.",
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
