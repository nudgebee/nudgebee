package agents

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	tocore "nudgebee/llm/tools/core"
	"sort"
	"strings"
)

const (
	AgentAzureOrchestratorName = "azure_orchestrator"
)

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentAzureOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newAzureOrchestratorAgent(accountId), nil
	}, "azure_debug")
}

// AzureOrchestratorAgent is an agent that helps debug Azure issues.
type AzureOrchestratorAgent struct {
	accountId            string
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newAzureOrchestratorAgent creates a new AzureOrchestratorAgent.
// The factory will provide accountId.
func newAzureOrchestratorAgent(accountId string) core.NBAgent {
	return &AzureOrchestratorAgent{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (a *AzureOrchestratorAgent) GetName() string {
	return AgentAzureOrchestratorName
}

// GetNameAliases returns aliases for the agent name.
func (a *AzureOrchestratorAgent) GetNameAliases() []string {
	return []string{"azure debug", "microsoft_azure_debug", "azure_debug"}
}

// GetDescription returns a description of the agent.
func (a *AzureOrchestratorAgent) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within Azure environments, providing step-by-step XML plans."
}

func (a *AzureOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	return getAzurePlannerSupportedTools(ctx, a.accountId)
}

func (a *AzureOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

// IsWatchCapable: drives action sub-agents whose async outcome completes later,
// so it may register a background watch.
func (a *AzureOrchestratorAgent) IsWatchCapable() bool { return true }

func (a *AzureOrchestratorAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *AzureOrchestratorAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// GetSystemPrompt returns the system prompt for the agent.
func (a *AzureOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAzureDebugReact)
	instructions := strings.Split(promptText, "\n")

	if !a.clusterSnapshotFound {
		a.clusterSnapshot = tools.GetCurrentAzureAccountState(a.accountId)
		a.clusterSnapshotFound = true
	}

	if len(a.clusterSnapshot) > 0 {
		regions := append([]string(nil), a.clusterSnapshot["region"]...)
		sort.Strings(regions)
		services := append([]string(nil), a.clusterSnapshot["service"]...)
		sort.Strings(services)
		instructions = append(instructions, "**Current Azure State:**")
		instructions = append(instructions, "Active Regions - "+strings.Join(regions, ","))
		instructions = append(instructions, "**Current Services:**")
		instructions = append(instructions, "Azure Services - "+strings.Join(services, ","))
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, "**Full Shell Capabilities:**")
		instructions = append(instructions, "The execution environment supports a full shell. You can use pipes (`|`), redirection, and standard Linux utilities (`grep`, `awk`, `sed`, `jq`, `sort`, `uniq`) in your planned queries.")
		instructions = append(instructions, "Encourage the use of these tools to filter and process output directly in the command line for efficiency.")
	}

	constraints := []string{
		"Sub-agent `azure` executes CLI commands internally - describe WHAT to investigate in natural language",
		"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
		"Config issues (wrong DNS, bad endpoint, misconfigured env var) look like network or connectivity issues but are NOT - always validate OS/app config inside the resource before blaming Azure infrastructure",
		"If a sub-agent returns 'not found' or empty, investigation ends there - do not fabricate next steps",
	}

	return core.NBAgentPrompt{
		Role:         "a senior Azure SRE and cloud infrastructure expert specializing in deep investigation and root cause analysis",
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
		OutputFormat: azureReactOutputFormat,
	}
}

const azureReactOutputFormat = `Choose the format based on the type of user request:

**FOR INVESTIGATION / TROUBLESHOOTING QUERIES** (e.g. "why is X failing", "debug Y", "show me recent issues"):

**Investigation Summary:**
- **Symptom:** [What user reported]
- **Signal:** [What evidence showed]

### Causality Chain (5-Whys)
- **Symptom:** (The primary issue)
- **Why?** (Immediate cause)
- **Why?** (Next layer)
- **Root Cause:** (The foundational reason)

**Evidence Chain:**
1. [Tool Name - ID](#task-ID) -> [Key finding]
2. [Tool Name - ID](#task-ID) -> [Key finding]

**CRITICAL: Citation Format Rule**
You MUST use the full markdown link format for EVERY reference: [Short Tool Name - ID](#task-ID).
Example: ...found in [Azure - E1](#task-E1) and [Azure - E3](#task-E3).
Exception: when citing an external resource that has its own real URL (e.g. a GitHub PR/issue link), use [Label](actual-url) with that real URL instead — never substitute a #task-ID anchor for it.

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

// getAzurePlannerSupportedTools returns tools relevant to Azure debugging.
// For this agent, it's primarily the "azure" tool (AzureAgentName).
func getAzurePlannerSupportedTools(ctx *security.RequestContext, accountId string) []tocore.NBTool {
	supportedToolNames := []string{AzureAgentName, getTicketAgentName(), WorkflowAgentName, GithubAgentName, WebSearchAgentName, RecommendationsAgentName, EventsAgentName, VisualizationAgentName, PostgresAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RedisAgentName, RabbitMQAgentName, KubectlAgentName, DelegateAgentToolName, tools.ToolIncidentAssembly, tools.SearchSkillsToolName}

	// The KG-backed service_dependency_graph covers cloud (AWS/GCP/Azure) topology,
	// not just K8s. The V1 flag guard here went away with the V1 agent.
	supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := tocore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", AgentAzureOrchestratorName)
	}

	tools := make([]tocore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := tocore.GetNBTool(accountId, toolName)
		if found {
			if !tocore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", AgentAzureOrchestratorName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("Azure Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId)
		}
	}

	// Include MCP integration tools (dynamic names, not in static supportedToolNames list)
	tools = append(tools, tocore.ListMCPIntegrationTools(accountId)...)

	// Conditionally add think tool for complex investigations
	if config.Config.LlmServerThinkToolEnabled {
		if thinkTool, ok := tocore.GetNBTool(accountId, "think"); ok {
			tools = append(tools, thinkTool)
		}
	}

	return tools
}
