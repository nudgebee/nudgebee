package agents

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"text/template"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const FinOpsAgentName = "finops"

// finOpsAccountContextCacheNS caches the rendered <account_context> block per
// account. Spend and recommendation figures change on billing-ingest / scan
// cycles (hours), so a 5-minute TTL keeps the prompt fresh without re-querying
// on every conversation turn.
const finOpsAccountContextCacheNS = "llm_finops_account_ctx"

func init() {
	common.CacheCreateNamespace(finOpsAccountContextCacheNS, common.CacheNamespaceWithExpiration(5*time.Minute))

	toolDescription := `Answers questions about cloud cost, spend, savings, budgets, optimization opportunities, ` +
		`rightsizing financial impact, idle/unattached resources, cost anomalies, and commitment coverage. ` +
		`Provides evidence-backed cost analysis with dollar figures and actionable next steps.`
	toolInput := "Provide a question about cloud cost, spend, or optimization in natural language."
	toolOutput := "Returns a cost analysis with dollar figures, evidence citations, and recommended actions."

	core.RegisterNBAgentFactoryAndTool(FinOpsAgentName, func(accountId string) (core.NBAgent, error) {
		return &FinOpsAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

// FinOpsAgent is a ReAct3 supervisor that orchestrates cloud cost analysis
// by delegating to specialized sub-tools and agents. It does not query
// databases directly — all data retrieval flows through its tool set.
type FinOpsAgent struct {
	accountId string
}

func (a *FinOpsAgent) GetName() string {
	return FinOpsAgentName
}

func (a *FinOpsAgent) GetNameAliases() []string {
	return []string{"finops", "cost", "spend", "FinOps"}
}

func (a *FinOpsAgent) GetDescription() string {
	return "FinOps cost optimization supervisor. Analyzes cloud spend, surfaces savings opportunities, " +
		"and provides evidence-backed cost reduction recommendations by orchestrating spend, recommendation, " +
		"cloud debug, and observability tools."
}

func (a *FinOpsAgent) GetPlannerType() core.AgentPlannerType {
	// Declared as ReAct; auto-upgrades to ReAct3 when LlmServerReAct3Enabled is true.
	return core.AgentPlannerTypeReAct
}

func (a *FinOpsAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *FinOpsAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

func (a *FinOpsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentFinops)

	tmplData := map[string]any{
		"data_protection_rules": prompts_repo.GetPrompt(prompts_repo.PromptSharedDataProtectionRules),
		"code_analysis_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedCodeAnalysisRules),
		"time_handling_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
		"account_context":       a.fetchFinOpsAccountContext(ctx),
	}
	if t, err := template.New("finops").Option("missingkey=zero").Parse(promptText); err == nil {
		var buf strings.Builder
		if err = t.Execute(&buf, tmplData); err == nil {
			promptText = buf.String()
		} else {
			slog.Warn("agent: failed to render finops prompt template, using raw prompt", "error", err, "agent", FinOpsAgentName)
		}
	} else {
		slog.Warn("agent: failed to parse finops prompt template, using raw prompt", "error", err, "agent", FinOpsAgentName)
	}

	instructions := strings.Split(promptText, "\n")

	constraints := []string{
		"This agent is READ-ONLY. Never execute infrastructure changes or modify cloud resources. To help a user act on a recommendation, hand them off to the optimise UI with the propose_recommendation_apply tool.",
		"Every cost answer MUST include a dollar figure. If data is unavailable, state that explicitly.",
		"Always cite which tool provided the data (spend_summary, recommendations, prometheus, etc.).",
		"Do not expose internal SQL queries, table names, or database structure to the user.",
		"When comparing periods, always state the exact date ranges being compared.",
		"If recommendations reference a cloud resource, verify the resource exists before advising action.",
		"The anomaly table is named 'anomaly' (singular). Never use 'anomalies' in SQL queries.",
		"Always provide arguments to spend_summary. Minimum: {\"group_by\":\"cloud_account\"}. Never call with empty input.",
		"For cloud-specific investigation, use delegate_agent with explicit tool selection (e.g., {\"tools\": [\"aws\"]}). Do NOT call aws_debug, gcp_debug, or azure_debug directly -- they are not in your tool list.",
		"Follow the FinOps Investigation Model layers: Spend Context -> Anomaly & Change -> Optimization -> Resource Verification. Do not skip layers.",
		"For spike/increase questions, NEVER guess the cause. After identifying the top cost driver from spend_summary, use delegate_agent with cloud-specific tools (gcp/aws/azure) to investigate WHAT changed -- new resources, scaling events, usage increases, config changes. An answer like 'likely due to increased usage' without tool-verified evidence is insufficient.",
		"Only count recommendations with status='Open' as actionable savings. Archive/Closed recommendations were already handled. If total savings exceeds current spend, flag this and verify the numbers.",
		"To help a user act on a recommendation, ALWAYS first present its safety band (safe/review/risky/unknown), its blast radius (dependent services / production dependents), and the estimated monthly savings, read from the recommendation data; then use propose_recommendation_apply to give the user a review-and-apply link. You do not apply changes yourself -- the user applies in the UI.",
		"For a recommendation whose safety band is 'risky' or 'unknown', call out the impact explicitly before handing off. Never imply a recommendation has been applied; you only propose and link.",
	}

	schema := []string{
		"**anomaly table** (for anomaly_execute tool): Table name is 'anomaly' (singular, NOT 'anomalies').",
		"Columns: id text, created_at timestamp, updated_at timestamp, name text (workload/resource name), namespace text (cloud account name for cost anomalies), reference_value jsonb (baseline stats), current_value text, anomaly_type text, is_anomaly boolean, evaluated_at timestamp, pod_name text, config_id text",
		"**Cost anomaly types:** 'CloudSpendService' (per-service spike, name=service, namespace=account), 'CloudSpendAccount' (per-account spike, name=account).",
		"**reference_value JSON fields for cost anomalies:** pct_change (% increase), total_impact ($ spike amount), z_score (statistical severity), start_date, anomaly_days (duration), service_name, baseline_days, anomaly_status (OPEN/CLOSED).",
		"Cost anomaly query: SELECT name, namespace, anomaly_type, reference_value, evaluated_at FROM anomaly WHERE anomaly_type IN ('CloudSpendService', 'CloudSpendAccount') AND evaluated_at >= '[[Time:-30d]]' ORDER BY evaluated_at DESC LIMIT 20",
	}

	outputFormat := `Lead with the headline, not the methodology. A Markdown TABLE is the primary carrier of every multi-data-point answer; prose is supplementary.

1. **Table first.** Any answer with more than one data point opens with a Markdown table. Reason about which columns fit the question — do not pattern-match to a fixed template.
2. **Every row is actionable.** Each row ends in a risk label (Low/Medium/High + reason), a concrete next step (verb + object, e.g. "Resize to 40Gi"), or a status signal (NEW/GONE/STABLE/ANOMALY).
3. **Column shapes by answer type (adapt as needed):**
   - Spend breakdown: Resource/Service | Current ($) | Previous ($) | Change (%) | Signal | Savings Available ($)
   - Optimization/rightsizing: Resource | Namespace | Provisioned (current) | Used (p95) | Utilization % | Recommended Target | Est. Saving ($/mo) | Risk | Action
   - Spike/anomaly: Service | Spike Amount ($) | % Change | Start Date | Root-Cause Signal | Recommended Action
   - Forecast: Metric | Value | vs Last Month | Signal
   - Resource drill-down: Attribute | Current | Recommended | Reason
4. **Always show the absolute, not just the delta.** For any sizing/capacity finding show the current allocated amount (provisioned), observed usage (with percentile), utilization %, and a concrete recommended target — e.g. "150Gi provisioned, 34Gi used (23%), resize to 50Gi"; never just "116Gi unused" or a bare "downsize". Gather absolute provisioned AND used, not only their difference.
5. **One short paragraph after the table** — headline finding + top 1-2 next steps. No bullet lists restating rows.
6. **Surface data quality.** Null or clearly-wrong values render as "—"/"⚠" with a footnote; never present corrupt data as fact, never silently drop it.
7. **Cite inline.** Append the source tool in the cell or header: [spend_summary], [recommendations], [anomaly_execute], [prometheus_execute], [kubectl_execute].
8. **Make rows clickable.** For optimization/rightsizing/recommendation tables, the Action cell is a Markdown link [<label>](<url>): <label> is a DYNAMIC, intent-aware next step for that row (e.g. "Resize to 10Gi ▸", "Delete unused PVC ▸" — derive it, don't use a fixed string); <url> is the optimize deep-link base from your account context with <Category> and <resource_name> filled in for that row. One click takes the user into the optimise workflow filtered to that resource. Omit the link for purely informational rows.
9. **Chart when it helps.** When a visual makes the finding land faster, embed ONE ` + "```nb-chart" + ` fenced JSON block right after the table, choosing type and data DYNAMICALLY: bar (compare a measure across resources, e.g. provisioned vs used), doughnut/pie (share of a total, e.g. spend by service), line/area (trend over time). Spec: {"type":"bar","title":"...","labels":[...],"series":[{"key":"Provisioned","data":[...]},{"key":"Used","data":[...]}],"format":"gi|usd|percent|number"} — doughnut/pie use "values":[...] instead of series. Keep it ≤12 labels / ≤4 series and reuse the table's own numbers (never raw time-series). Skip the chart for single-row, clarification, or pure-text answers.

Mark rows (stable) for <5% change, NEW for absent-in-prior-period, GONE for terminated. Every cost answer includes a dollar figure (state explicitly if unavailable).`

	return core.NBAgentPrompt{
		Role:         "a FinOps cost optimization supervisor that orchestrates cloud cost analysis across AWS, GCP, Azure, and Kubernetes, and surfaces actionable savings opportunities",
		Instructions: instructions,
		Constraints:  constraints,
		Schema:       schema,
		// ToolUsage intentionally omitted: the planner already renders each tool's
		// Description() once via {{.tool_descriptions}}. Seeding it here duplicated that
		// same text in the <tool_usage_instructions> block of this orchestrator's
		// (account-cached) prompt prefix.
		OutputFormat: outputFormat,
		Rag: core.NBAgentPromptRag{
			Module:      "finops",
			Records:     3,
			Format:      core.NBAgentPromptRagFormatString,
			QuestionKey: "Question",
			AnswerKey:   "Answer",
		},
	}
}

// topServiceRow is a single (service, 30-day spend) pair for the account context.
type topServiceRow struct {
	ServiceName string  `db:"service_name"`
	Amount      float64 `db:"amount"`
}

// fetchFinOpsAccountContext builds the live <account_context> block injected into
// the system prompt. It gives the LLM the account's cloud footprint and spend
// baseline up front so it can skip orientation tool calls and lead answers with
// the right figures. The rendered block is cached per account (5-min TTL).
//
// It degrades gracefully: the cloud-footprint section (from the already-cached
// AccountConfigSummary) is always present, while the spend / recommendation
// sections are omitted silently if their queries fail — a partial context is
// more useful to the agent than none.
func (a *FinOpsAgent) fetchFinOpsAccountContext(ctx *security.RequestContext) string {
	cacheKey := "finops_ctx:" + a.accountId
	if cached, found := common.CacheGet(finOpsAccountContextCacheNS, cacheKey); found {
		return string(cached)
	}

	var b strings.Builder
	b.WriteString("<account_context>\n")

	// Cloud footprint — sourced from the already-cached AccountConfigSummary, so
	// this part is effectively free and never fails independently of the cache.
	summary, err := toolcore.GetAccountConfigSummary(ctx, a.accountId)
	if err != nil {
		slog.Warn("finops: failed to get account config summary for context", "error", err, "account_id", a.accountId)
	} else {
		if providers := sortedKeys(summary.CloudProviders); len(providers) > 0 {
			b.WriteString("Cloud providers: " + strings.Join(providers, ", ") + "\n")
		}
		if integrations := sortedKeys(summary.IntegrationTypes); len(integrations) > 0 {
			b.WriteString("Integrations: " + strings.Join(integrations, ", ") + "\n")
		}
		if summary.HasAgent {
			b.WriteString("K8s agent: connected\n")
		} else {
			b.WriteString("K8s agent: not connected (kubectl/prometheus tools unavailable)\n")
		}
	}

	// Spend baseline + recommendation summary — best-effort. Any failure leaves
	// the cloud-footprint section intact and is logged, not surfaced.
	a.appendSpendContext(&b)

	// Optimize-page deep-link base. Surfaced so the agent can render per-row
	// "Action" links that jump the user straight to the optimise recommendations
	// table, pre-filtered to a specific resource. The optimise page reads the
	// account/category/search query params and the #recommendations anchor.
	baseURL := strings.TrimRight(config.Config.BaseUrl, "/")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	fmt.Fprintf(&b, "Optimize page deep-link base: %s/optimise?account=%s&category=<Category>&search=<resource_name>#recommendations\n", baseURL, a.accountId)

	b.WriteString("As of: " + time.Now().UTC().Format("2006-01-02") + "\n")
	b.WriteString("</account_context>")

	rendered := b.String()
	if err := common.CacheSet(finOpsAccountContextCacheNS, cacheKey, []byte(rendered), common.CacheSetWithExpiration(5*time.Minute)); err != nil {
		slog.Warn("finops: failed to cache account context", "error", err, "account_id", a.accountId)
	}
	return rendered
}

// appendSpendContext writes the 30-day spend, top services, and open
// recommendation lines. Each query is independent and best-effort: a failure
// logs and skips only its own line.
func (a *FinOpsAgent) appendSpendContext(b *strings.Builder) {
	tenantId, err := security.GetTenantIdFromAccountId(a.accountId)
	if err != nil || tenantId == "" {
		slog.Warn("finops: cannot resolve tenant for account context", "error", err, "account_id", a.accountId)
		return
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Warn("finops: db manager unavailable for account context", "error", err)
		return
	}

	now := time.Now().UTC()
	windowEnd := now.Truncate(24 * time.Hour)
	windowStart := windowEnd.AddDate(0, 0, -30)

	// 30-day spend.
	var spend float64
	if err := dbManager.Db.Get(&spend,
		`SELECT COALESCE(SUM(amount), 0) FROM spends
		 WHERE tenant = $1 AND cloud_account = $2 AND date >= $3 AND date < $4`,
		tenantId, a.accountId, windowStart, windowEnd); err != nil {
		slog.Warn("finops: 30-day spend query failed for account context", "error", err, "account_id", a.accountId)
	} else {
		fmt.Fprintf(b, "30-day spend: $%.2f\n", spend)
	}

	// Top 3 services by 30-day spend.
	topServices := []topServiceRow{}
	if err := dbManager.Db.Select(&topServices,
		`SELECT cr.service_name AS service_name, ROUND(SUM(s.amount)::numeric, 2)::float AS amount
		 FROM spends s
		 JOIN cloud_resourses cr ON cr.id = s.cloud_resource_id
		 WHERE s.tenant = $1 AND s.cloud_account = $2 AND s.date >= $3 AND s.date < $4
		   AND cr.service_name IS NOT NULL AND s.amount > 0
		 GROUP BY cr.service_name
		 ORDER BY SUM(s.amount) DESC
		 LIMIT 3`,
		tenantId, a.accountId, windowStart, windowEnd); err != nil {
		slog.Warn("finops: top services query failed for account context", "error", err, "account_id", a.accountId)
	} else if len(topServices) > 0 {
		parts := make([]string, 0, len(topServices))
		for _, s := range topServices {
			parts = append(parts, fmt.Sprintf("%s ($%.2f)", s.ServiceName, s.Amount))
		}
		b.WriteString("Top services: " + strings.Join(parts, ", ") + "\n")
	}

	// Open recommendation count + quantified savings.
	var rec struct {
		Count        int     `db:"cnt"`
		Quantified   int     `db:"quantified"`
		TotalSavings float64 `db:"total_savings"`
	}
	if err := dbManager.Db.Get(&rec,
		`SELECT COUNT(*) AS cnt,
		        COUNT(*) FILTER (WHERE estimated_savings > 0) AS quantified,
		        COALESCE(SUM(estimated_savings) FILTER (WHERE estimated_savings > 0), 0) AS total_savings
		 FROM recommendation
		 WHERE cloud_account_id = $1 AND status = 'Open'`,
		a.accountId); err != nil {
		slog.Warn("finops: open recommendation query failed for account context", "error", err, "account_id", a.accountId)
	} else if rec.Count > 0 {
		fmt.Fprintf(b, "Open recommendations: %d (savings quantified for %d, total $%.2f/month)\n",
			rec.Count, rec.Quantified, rec.TotalSavings)
	}
}

// sortedKeys returns the true-valued keys of a string-keyed bool set, sorted for
// deterministic prompt output (so the account-scoped prompt cache stays stable).
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (a *FinOpsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	supportedToolNames := []string{
		// New FinOps-specific tools
		tools.ToolSpendSummary,
		tools.ToolSpendForecast,
		tools.ToolSpendAllocation,

		// Existing agents-as-tools (reused unchanged)
		RecommendationsAgentName,
		DelegateAgentToolName,

		// Existing direct tools (reused unchanged)
		tools.ToolExecuteKubectlCommand,
		tools.ToolQueryPrometheus,
		tools.ToolCloudResourceSearch,
		tools.ToolAnomalyExecuteSql,

		// Hand-off tool (read-only): returns a deep link to the optimise UI's
		// review-and-apply flow. The agent proposes; the user applies in the UI.
		tools.ToolProposeRecommendationApply,
	}

	summary, err := toolcore.GetAccountConfigSummary(ctx, a.accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", FinOpsAgentName)
	}

	result := make([]toolcore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := toolcore.GetNBTool(a.accountId, toolName)
		if found {
			if !toolcore.IsToolConfigured(ctx, a.accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", FinOpsAgentName)
				continue
			}
			result = append(result, tool)
		} else {
			slog.Warn("FinOps Agent: Tool not found in registry", "toolName", toolName, "accountId", a.accountId)
		}
	}

	// Include MCP integration tools
	result = append(result, toolcore.ListMCPIntegrationTools(a.accountId)...)

	// Conditionally add think tool for complex cost analysis
	if config.Config.LlmServerThinkToolEnabled {
		if thinkTool, ok := toolcore.GetNBTool(a.accountId, "think"); ok {
			result = append(result, thinkTool)
		}
	}

	return result
}
