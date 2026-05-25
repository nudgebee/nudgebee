package agents

import (
	"log/slog"
	"strings"
	"text/template"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const FinOpsAgentName = "finops"

func init() {
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

func (a *FinOpsAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

func (a *FinOpsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentFinops)

	tmplData := map[string]any{
		"data_protection_rules": prompts_repo.GetPrompt(prompts_repo.PromptSharedDataProtectionRules),
		"code_analysis_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedCodeAnalysisRules),
		"time_handling_rules":   prompts_repo.GetPrompt(prompts_repo.PromptSharedTimeHandlingRules),
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
		"This agent is READ-ONLY. Never execute infrastructure changes or propose actions that modify cloud resources.",
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
	}

	schema := []string{
		"**anomaly table** (for anomaly_execute tool): Table name is 'anomaly' (singular, NOT 'anomalies').",
		"Columns: id text, created_at timestamp, updated_at timestamp, name text (workload/resource name), namespace text (cloud account name for cost anomalies), reference_value jsonb (baseline stats), current_value text, anomaly_type text, is_anomaly boolean, evaluated_at timestamp, pod_name text, config_id text",
		"**Cost anomaly types:** 'CloudSpendService' (per-service spike, name=service, namespace=account), 'CloudSpendAccount' (per-account spike, name=account).",
		"**reference_value JSON fields for cost anomalies:** pct_change (% increase), total_impact ($ spike amount), z_score (statistical severity), start_date, anomaly_days (duration), service_name, baseline_days, anomaly_status (OPEN/CLOSED).",
		"Cost anomaly query: SELECT name, namespace, anomaly_type, reference_value, evaluated_at FROM anomaly WHERE anomaly_type IN ('CloudSpendService', 'CloudSpendAccount') AND evaluated_at >= '[[Time:-30d]]' ORDER BY evaluated_at DESC LIMIT 20",
	}

	outputFormat := `Choose the format based on the type of user request. Lead with the headline, not the methodology.

**FOR SPEND OVERVIEW** ("what are we spending", "show me costs"):
Lead: "Your cloud spend is $X/month, [up/down] Y% from last month."
Then: table of top services/accounts with trend indicators:
| Service | Current | Previous | Change |
Mark: (stable) for <5%, NEW for absent in prior period, GONE for terminated.
Close with: available savings from recommendations + suggested drill-down areas.

**FOR BILL SPIKE / ANOMALY INVESTIGATION** ("why did costs go up", "explain this spike"):
Lead: "Your bill increased by $X (Y%) — driven primarily by [service/resource]."
Then: What Changed section — focus on NEW resources, big movers (>30% increase), anomalies.
- **Signal:** Specific service/account spike with $ amount [source tool]
- **Root Cause:** What caused it (new resources, scaling, pricing change, abandoned resources)
- **Evidence:** Data that confirmed this [source tool]
- **Impact:** $/month additional spend
- **Recommendation:** Specific action to reduce cost

**FOR OPTIMIZATION** ("how can I save money", "what should I optimize"):
Lead: "You have $X/month in potential savings across N recommendations."
Then: Top 5 ranked by $ impact, each with:
- What to change and estimated monthly savings
- Risk level: Low / Medium / High with reason (HPA present? bursty traffic? shared resource?)
- Evidence from metrics [source tool]
Close with: total savings if all implemented + recommended order (quick wins first).

**FOR RESOURCE DRILL-DOWN** ("tell me about this instance", "is this right-sized"):
- Resource identification and current configuration
- Utilization: p50 and p95 CPU/memory over 14 days [prometheus_execute]
- HPA / autoscaling status [kubectl_execute]
- Recommended change with savings estimate
- Risk: Low/Medium/High with explanation

CRITICAL: Cite the source tool for every data point: [spend_summary], [recommendations], [anomaly_execute], [prometheus_execute], etc.`

	return core.NBAgentPrompt{
		Role:         "a FinOps cost optimization supervisor that orchestrates cloud cost analysis across AWS, GCP, Azure, and Kubernetes, and surfaces actionable savings opportunities",
		Instructions: instructions,
		Constraints:  constraints,
		Schema:       schema,
		ToolUsage: func() map[string][]string {
			tu := make(map[string][]string)
			for _, tool := range a.GetSupportedTools(ctx) {
				tu[tool.Name()] = []string{tool.Description()}
			}
			return tu
		}(),
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
