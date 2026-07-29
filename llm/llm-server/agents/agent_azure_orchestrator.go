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
	"text/template"
)

const (
	// AgentAzureOrchestratorName is the name for the Azure debug agent
	AgentAzureOrchestratorName = "azure_orchestrator"
	// AgentAzureOrchestrator2Name is the always-direct eval handle (invocable by @name only).
	AgentAzureOrchestrator2Name = "azure_orchestrator_2"
)

// AzureOrchestratorMode* are the values of config llm_server_azure_orchestrator_mode,
// the boot-time knob for what the router-selected azure_orchestrator runs.
const (
	AzureOrchestratorModeDelegating = "delegating" // v1: delegate Azure resource CLI to the `azure` sub-agent (default)
	AzureOrchestratorModeDirect     = "direct"     // v2: hold azure_execute and run the az CLI directly
	AzureOrchestratorModeLean       = "lean"       // EXPERIMENTAL: minimal principle-level prompt + direct azure_execute
)

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentAzureOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newAzureOrchestratorAgent(accountId), nil
	}, "azure_debug")

	// Explicit always-direct eval handle (see AgentAzureOrchestrator2Name). Same
	// implementation, distinct name → distinct cache key, invocable by @name only.
	core.RegisterNBAgentFactoryWithAliases(AgentAzureOrchestrator2Name, func(accountId string) (core.NBAgent, error) {
		return newAzureOrchestratorAgentNamed(accountId, AgentAzureOrchestrator2Name, true), nil
	}, "azure_debug_2")
}

// AzureOrchestratorAgent is an agent that helps debug Azure issues. useAzureCliDirect
// selects v2 (hold azure_execute, run the CLI directly) vs v1 (delegate to the `azure`
// sub-agent); it drives both the tool set and the prompt's {{if .use_azure_cli_direct}}.
type AzureOrchestratorAgent struct {
	accountId            string
	name                 string
	useAzureCliDirect    bool
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newAzureOrchestratorAgent is the primary, router-selected agent. Its behavior is
// chosen by config.AzureOrchestratorMode (boot-time; rollback = change + redeploy).
// Unknown/empty mode falls back to delegating (v1), the safe default.
func newAzureOrchestratorAgent(accountId string) core.NBAgent {
	switch strings.ToLower(strings.TrimSpace(config.Config.AzureOrchestratorMode)) {
	case AzureOrchestratorModeLean:
		return newAzureLeanAgentNamed(accountId, AgentAzureOrchestratorName)
	case AzureOrchestratorModeDirect:
		return newAzureOrchestratorAgentNamed(accountId, AgentAzureOrchestratorName, true)
	default:
		return newAzureOrchestratorAgentNamed(accountId, AgentAzureOrchestratorName, false)
	}
}

// newAzureOrchestratorAgentNamed is the shared constructor. name drives identity and the
// cache key (so the primary and the eval handle stay isolated); useAzureCliDirect drives
// the az CLI tool and the prompt's {{if .use_azure_cli_direct}} branch.
func newAzureOrchestratorAgentNamed(accountId, name string, useAzureCliDirect bool) core.NBAgent {
	return &AzureOrchestratorAgent{
		accountId:         accountId,
		name:              name,
		useAzureCliDirect: useAzureCliDirect,
	}
}

// GetName returns the name of the agent.
func (a *AzureOrchestratorAgent) GetName() string {
	return a.name
}

// GetNameAliases returns aliases for the agent name.
func (a *AzureOrchestratorAgent) GetNameAliases() []string {
	if a.name == AgentAzureOrchestrator2Name {
		return []string{"azure debug 2", "microsoft_azure_debug_2", "azure_debug_2"}
	}
	return []string{"azure debug", "microsoft_azure_debug", "azure_debug"}
}

// GetDescription returns a description of the agent.
func (a *AzureOrchestratorAgent) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within Azure environments, providing step-by-step XML plans."
}

func (a *AzureOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	return getAzurePlannerSupportedTools(ctx, a.accountId, a.GetName(), a.useAzureCliDirect)
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

// GetSystemPrompt returns the system prompt for the agent. useAzureCliDirect drives the
// prompt's {{if .use_azure_cli_direct}} blocks: run azure_execute directly (v2) vs route
// Azure resource work through the `azure` sub-agent (v1). All routing guidance lives in the
// prompt file (gated by that flag), not appended here, so the two variants can't drift.
func (a *AzureOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := renderAzureDebugReactPrompt(a.useAzureCliDirect)
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
		"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
		"Config issues (wrong DNS, bad endpoint, misconfigured env var) look like network or connectivity issues but are NOT - always validate OS/app config inside the resource before blaming Azure infrastructure",
	}
	if a.useAzureCliDirect {
		constraints = append(constraints,
			"Run Azure resource inspection with azure_execute directly (write the actual `az ...` CLI)",
			"If a resource is 'not found', investigation ends there - do not fabricate next steps")
	} else {
		constraints = append(constraints,
			"Sub-agent `azure` executes CLI commands internally - describe WHAT to investigate in natural language",
			"If a sub-agent returns 'not found' or empty, investigation ends there - do not fabricate next steps")
	}

	role := "a senior Azure SRE and cloud infrastructure expert specializing in deep investigation and root cause analysis"
	if a.useAzureCliDirect {
		role = "a senior Azure SRE and cloud infrastructure expert that runs the az CLI directly via azure_execute, specializing in deep investigation and root cause analysis"
	}

	return core.NBAgentPrompt{
		Role:         role,
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

// renderAzureDebugReactPrompt renders the shared agent_azure_debug_react prompt for both Azure
// orchestrators. useAzureCliDirect drives the {{if .use_azure_cli_direct}} blocks in the prompt
// file: false = delegate Azure resource CLI to the `azure` sub-agent (v1), true = hold and run
// azure_execute directly (v2). Keeping all routing guidance in the prompt (gated by that flag)
// means the two variants cannot drift apart. On any parse/execute error the raw prompt is
// returned so the agent retains its full instructions.
func renderAzureDebugReactPrompt(useAzureCliDirect bool) string {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAzureDebugReact)
	tmplData := map[string]any{"use_azure_cli_direct": useAzureCliDirect}
	t, err := template.New("azure_debug").Option("missingkey=zero").Parse(promptText)
	if err != nil {
		slog.Error("failed to parse azure_debug prompt template, using raw prompt", "error", err)
		return promptText
	}
	var buf strings.Builder
	if err := t.Execute(&buf, tmplData); err != nil {
		slog.Error("failed to execute azure_debug prompt template, using raw prompt", "error", err)
		return promptText
	}
	return buf.String()
}

// getAzurePlannerSupportedTools returns tools relevant to Azure debugging.
func getAzurePlannerSupportedTools(ctx *security.RequestContext, accountId, agentName string, useAzureCliDirect bool) []tocore.NBTool {
	supportedToolNames := []string{getTicketAgentName(), WorkflowAgentName, GithubAgentName, WebSearchAgentName, RecommendationsAgentName, EventsAgentName, VisualizationAgentName, PostgresAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RedisAgentName, RabbitMQAgentName, KubectlAgentName, DelegateAgentToolName, tools.ToolIncidentAssembly, tools.SearchSkillsToolName}

	// v1 (delegating) routes Azure resource inspection through the `azure` sub-agent;
	// v2 (direct) holds azure_execute and drops the sub-agent hop.
	if useAzureCliDirect {
		supportedToolNames = append(supportedToolNames, tools.ToolExecuteAzureCliCommand)
	} else {
		supportedToolNames = append(supportedToolNames, AzureAgentName)
	}

	// The KG-backed service_dependency_graph covers cloud (AWS/GCP/Azure) topology,
	// not just K8s. The V1 flag guard here went away with the V1 agent.
	supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := tocore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", agentName)
	}

	tools := make([]tocore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := tocore.GetNBTool(accountId, toolName)
		if found {
			if !tocore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", agentName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("Azure Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId, "agent", agentName)
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
