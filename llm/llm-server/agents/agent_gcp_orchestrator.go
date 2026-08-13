package agents

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"sort"
	"strings"
)

const (
	AgentGcpOrchestratorName = "gcp_orchestrator"
)

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentGcpOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newGcpOrchestratorAgent(accountId), nil
	}, "gcp_debug")
}

type GcpOrchestratorAgent struct {
	accountId            string
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

func newGcpOrchestratorAgent(accountId string) core.NBAgent {
	return &GcpOrchestratorAgent{
		accountId: accountId,
	}
}

func (a *GcpOrchestratorAgent) GetName() string {
	return AgentGcpOrchestratorName
}

func (a *GcpOrchestratorAgent) GetNameAliases() []string {
	return []string{"gcp debug", "google_cloud_debug", "gcp_debug"}
}

func (a *GcpOrchestratorAgent) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within GCP environments, providing step-by-step XML plans."
}

func (a *GcpOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return getGcpPlannerSupportedTools(ctx, a.accountId)
}

func (a *GcpOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

// IsWatchCapable: drives action sub-agents whose async outcome completes later,
// so it may register a background watch.
func (a *GcpOrchestratorAgent) IsWatchCapable() bool { return true }

func (a *GcpOrchestratorAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *GcpOrchestratorAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

func (a *GcpOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentGcpDebugReact)
	instructions := strings.Split(promptText, "\n")

	if !a.clusterSnapshotFound {
		a.clusterSnapshot = tools.GetCurrentGcpAccountState(a.accountId)
		a.clusterSnapshotFound = true
	}

	if len(a.clusterSnapshot) > 0 {
		regions := append([]string(nil), a.clusterSnapshot["region"]...)
		sort.Strings(regions)
		services := append([]string(nil), a.clusterSnapshot["service"]...)
		sort.Strings(services)
		instructions = append(instructions, "**Current GCP State:**")
		instructions = append(instructions, "Active Regions - "+strings.Join(regions, ","))
		instructions = append(instructions, "**Current Services:**")
		instructions = append(instructions, "GCP Services - "+strings.Join(services, ","))
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, "**Full Shell Capabilities:**")
		instructions = append(instructions, "The execution environment supports a full shell. You can use pipes (`|`), redirection, and standard Linux utilities (`grep`, `awk`, `sed`, `jq`, `sort`, `uniq`) in your planned queries.")
		instructions = append(instructions, "Encourage the use of these tools to filter and process output directly in the command line for efficiency.")
	}

	constraints := []string{
		"Focus on data collection - prioritize data-gathering tools like `gcp`, `prometheus`, `docs`, or `search`.",
		"If a tool execution fails due to permissions or errors, state the error and propose a different approach.",
		"If a command to list resources repeatedly returns empty, assume it's a permission issue and suggest the necessary permissions.",
		"Always verify that your actions directly address the user's question.",
	}
	outputFormat := gcpReactOutputFormat

	return core.NBAgentPrompt{
		Role:         "a highly skilled DevOps, SRE and Software Development expert",
		Instructions: instructions,
		Constraints:  constraints,
		// ToolUsage intentionally omitted: the planner already renders each tool's
		// Description() once via {{.tool_descriptions}}. Seeding it here duplicated that
		// same text in the <tool_usage_instructions> block of this orchestrator's
		// (account-cached) prompt prefix.
		Rag: core.NBAgentPromptRag{
			Module:      "planner",
			Records:     3,
			Format:      core.NBAgentPromptRagFormatString,
			QuestionKey: "Question",
			AnswerKey:   "Answer",
		},
		OutputFormat: outputFormat,
	}
}

const gcpReactOutputFormat = `Choose the format based on the type of user request:

**FOR INVESTIGATION / TROUBLESHOOTING QUERIES** (e.g. "why is X failing", "debug Y", "show me recent issues"):

**Investigation Summary:**
- **Symptom:** [What user reported]
- **Signal:** [What metrics/logs showed]

### Causality Chain (5-Whys)
- **Symptom:** (The primary issue reported/observed)
- **Why?** (Immediate cause of the symptom)
- **Why?** (Next layer of causality)
- **Root Cause:** (The foundational reason discovered)

**Evidence Chain:**
1. [Tool Name - ID](#task-ID) -> [Key finding]
2. [Tool Name - ID](#task-ID) -> [Key finding]

**CRITICAL: Citation Format Rule**
You MUST use the full markdown link format for EVERY reference: [Short Tool Name - ID](#task-ID).
Example: ...found in [GCP - E1](#task-E1) and [Logs - E3](#task-E3).
Exception: when citing an external resource that has its own real URL (e.g. a GitHub PR/issue link), use [Label](actual-url) with that real URL instead — never substitute a #task-ID anchor for it.

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

func getGcpPlannerSupportedTools(ctx *security.RequestContext, accountId string) []toolcore.NBTool {
	supportedToolNames := []string{GcpAgentName, getTicketAgentName(), WorkflowAgentName, GithubAgentName, WebSearchAgentName, RecommendationsAgentName, EventsAgentName, VisualizationAgentName, PostgresAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RedisAgentName, RabbitMQAgentName, KubectlAgentName, DelegateAgentToolName}

	// The KG-backed service_dependency_graph covers cloud (AWS/GCP/Azure) topology,
	// not just K8s. The V1 flag guard here went away with the V1 agent.
	supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := toolcore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", AgentGcpOrchestratorName)
	}

	tools := make([]toolcore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := toolcore.GetNBTool(accountId, toolName)
		if found {
			if !toolcore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", AgentGcpOrchestratorName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("GCP Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId)
		}
	}

	// Include MCP integration tools (dynamic names, not in static supportedToolNames list)
	tools = append(tools, toolcore.ListMCPIntegrationTools(accountId)...)

	// Conditionally add think tool for complex investigations
	if config.Config.LlmServerThinkToolEnabled {
		if thinkTool, ok := toolcore.GetNBTool(accountId, "think"); ok {
			tools = append(tools, thinkTool)
		}
	}

	return tools
}
