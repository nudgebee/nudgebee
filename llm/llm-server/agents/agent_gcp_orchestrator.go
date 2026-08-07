package agents

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"sort"
	"strings"
	"text/template"
)

const (
	// AgentGcpOrchestratorName is the name for the GCP debug agent
	AgentGcpOrchestratorName = "gcp_orchestrator"
	// AgentGcpOrchestrator2Name is the always-direct eval handle (invocable by @name only).
	AgentGcpOrchestrator2Name = "gcp_orchestrator_2"
)

// GcpOrchestratorMode* are the values of config llm_server_gcp_orchestrator_mode,
// the boot-time knob for what the router-selected gcp_orchestrator runs.
const (
	GcpOrchestratorModeDelegating = "delegating" // v1: delegate GCP resource CLI to the `gcp` sub-agent (fallback for unknown mode)
	GcpOrchestratorModeDirect     = "direct"     // v2: hold gcloud_execute and run the gcloud CLI directly
	GcpOrchestratorModeLean       = "lean"       // minimal principle-level prompt + direct gcloud_execute (now the DEFAULT)
)

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentGcpOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newGcpOrchestratorAgent(accountId), nil
	}, "gcp_debug")

	// Explicit always-direct eval handle (see AgentGcpOrchestrator2Name). Same
	// implementation, distinct name → distinct cache key, invocable by @name only.
	core.RegisterNBAgentFactoryWithAliases(AgentGcpOrchestrator2Name, func(accountId string) (core.NBAgent, error) {
		return newGcpOrchestratorAgentNamed(accountId, AgentGcpOrchestrator2Name, true), nil
	}, "gcp_debug_2")
}

// GcpOrchestratorAgent is an agent that helps debug GCP issues. useGcpCliDirect
// selects v2 (hold gcloud_execute, run the CLI directly) vs v1 (delegate to the `gcp`
// sub-agent); it drives both the tool set and the prompt's {{if .use_gcp_cli_direct}}.
type GcpOrchestratorAgent struct {
	accountId            string
	name                 string
	useGcpCliDirect      bool
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newGcpOrchestratorAgent is the primary, router-selected agent. Its behavior is
// chosen by config.GcpOrchestratorMode (boot-time; rollback = change + redeploy).
// The configured default is "lean" (llm_server_*_orchestrator_mode); an
// unknown/typo value falls back to delegating (v1).
func newGcpOrchestratorAgent(accountId string) core.NBAgent {
	switch strings.ToLower(strings.TrimSpace(config.Config.GcpOrchestratorMode)) {
	case GcpOrchestratorModeLean:
		return newGcpLeanAgentNamed(accountId, AgentGcpOrchestratorName)
	case GcpOrchestratorModeDirect:
		return newGcpOrchestratorAgentNamed(accountId, AgentGcpOrchestratorName, true)
	default:
		return newGcpOrchestratorAgentNamed(accountId, AgentGcpOrchestratorName, false)
	}
}

// newGcpOrchestratorAgentNamed is the shared constructor. name drives identity and the
// cache key (so the primary and the eval handle stay isolated); useGcpCliDirect drives
// the gcloud CLI tool and the prompt's {{if .use_gcp_cli_direct}} branch.
func newGcpOrchestratorAgentNamed(accountId, name string, useGcpCliDirect bool) core.NBAgent {
	return &GcpOrchestratorAgent{
		accountId:       accountId,
		name:            name,
		useGcpCliDirect: useGcpCliDirect,
	}
}

func (a *GcpOrchestratorAgent) GetName() string {
	return a.name
}

func (a *GcpOrchestratorAgent) GetNameAliases() []string {
	if a.name == AgentGcpOrchestrator2Name {
		return []string{"gcp debug 2", "google_cloud_debug_2", "gcp_debug_2"}
	}
	return []string{"gcp debug", "google_cloud_debug", "gcp_debug"}
}

func (a *GcpOrchestratorAgent) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within GCP environments, providing step-by-step XML plans."
}

func (a *GcpOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return getGcpPlannerSupportedTools(ctx, a.accountId, a.GetName(), a.useGcpCliDirect)
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

// GetSystemPrompt returns the system prompt for the agent. useGcpCliDirect drives the
// prompt's {{if .use_gcp_cli_direct}} blocks: run gcloud_execute directly (v2) vs route GCP
// resource work through the `gcp` sub-agent (v1). All routing guidance lives in the prompt
// file (gated by that flag), not appended here, so the two variants can't drift.
func (a *GcpOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := renderGcpDebugReactPrompt(ctx, query, a.useGcpCliDirect)
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
		"If a tool execution fails due to permissions or errors, state the error and propose a different approach.",
		"If a command to list resources repeatedly returns empty, assume it's a permission issue and suggest the necessary permissions.",
		"Always verify that your actions directly address the user's question.",
	}
	if a.useGcpCliDirect {
		constraints = append(constraints,
			"Run GCP resource inspection with gcloud_execute directly (write the actual `gcloud ...` / `gsutil ...` CLI)",
			"If a resource is 'not found', investigation ends there - don't fabricate next steps")
	} else {
		constraints = append(constraints,
			"Focus on data collection - prioritize data-gathering tools like `gcp`, `prometheus`, `docs`, or `search`.")
	}
	outputFormat := gcpReactOutputFormat

	role := "a highly skilled DevOps, SRE and Software Development expert"
	if a.useGcpCliDirect {
		role = "a highly skilled DevOps, SRE and Software Development expert that runs the gcloud CLI directly via gcloud_execute"
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

// renderGcpDebugReactPrompt renders the shared agent_gcp_debug_react prompt for both GCP
// orchestrators. useGcpCliDirect drives the {{if .use_gcp_cli_direct}} blocks in the prompt
// file: false = delegate GCP resource CLI to the `gcp` sub-agent (v1), true = hold and run
// gcloud_execute directly (v2). Keeping all routing guidance in the prompt (gated by that
// flag) means the two variants cannot drift apart. On a template parse/execute error the
// unrendered prompt is returned so the agent keeps its instructions; a prompt-load failure
// is logged and leaves the prompt empty, which MustResolveAll makes unreachable for a
// registered name.
func renderGcpDebugReactPrompt(ctx *security.RequestContext, query core.NBAgentRequest, useGcpCliDirect bool) string {
	promptText, promptErr := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptGcpOrchestrator, query.AccountId)
	if promptErr != nil {
		// An empty system prompt would silently degrade the agent. MustResolveAll
		// covers default/v1 at startup; this catches a malformed provider- or
		// version-specific override added later.
		// Returning early is equivalent to falling through — promptText is already
		// "" and the template round-trip below would yield "" — but it says so
		// without running a parse whose only possible output is the empty string.
		ctx.GetLogger().Error("gcp orchestrator: system prompt failed to load", "error", promptErr)
		return ""
	}
	tmplData := map[string]any{"use_gcp_cli_direct": useGcpCliDirect}
	t, err := template.New("gcp_debug").Option("missingkey=zero").Parse(promptText)
	if err != nil {
		slog.Error("failed to parse gcp_debug prompt template, using raw prompt", "error", err)
		return promptText
	}
	var buf strings.Builder
	if err := t.Execute(&buf, tmplData); err != nil {
		slog.Error("failed to execute gcp_debug prompt template, using raw prompt", "error", err)
		return promptText
	}
	return buf.String()
}

func getGcpPlannerSupportedTools(ctx *security.RequestContext, accountId, agentName string, useGcpCliDirect bool) []toolcore.NBTool {
	supportedToolNames := []string{getTicketAgentName(), WorkflowAgentName, GithubAgentName, WebSearchAgentName, RecommendationsAgentName, EventsAgentName, VisualizationAgentName, PostgresAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RedisAgentName, RabbitMQAgentName, KubectlAgentName, ResourceSearchAgentName, DelegateAgentToolName, tools.ToolIncidentAssembly, tools.SearchSkillsToolName}

	// v1 (delegating) routes GCP resource inspection through the `gcp` sub-agent;
	// v2 (direct) holds gcloud_execute and drops the sub-agent hop.
	if useGcpCliDirect {
		supportedToolNames = append(supportedToolNames, tools.ToolExecuteGcpCliCommand)
	} else {
		supportedToolNames = append(supportedToolNames, GcpAgentName)
	}

	// The KG-backed service_dependency_graph covers cloud (AWS/GCP/Azure) topology,
	// not just K8s. The V1 flag guard here went away with the V1 agent.
	supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)

	// Cross-cutting extras, parity with the k8s orchestrator — flag-gated on the same
	// switches, so they only surface where already enabled account-wide. (think is already
	// injected at the tail of this function, so it is intentionally not repeated here.)
	if config.Config.RemediationAgentEnabled {
		supportedToolNames = append(supportedToolNames, RemediationAgentName)
	}
	if core.IsAgentsFollowupEnabled() {
		supportedToolNames = append(supportedToolNames, FollowupAgentName)
	}

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := toolcore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", agentName)
	}

	tools := make([]toolcore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := toolcore.GetNBTool(accountId, toolName)
		if found {
			if !toolcore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", agentName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("GCP Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId, "agent", agentName)
		}
	}

	// Per-account tools whose names are dynamic and so cannot appear in the static
	// supportedToolNames list: MCP integrations, plus the account's AI-invocable
	// automations. The automations are here because, held only by the automation
	// sub-agent, one is reachable only after the orchestrator decides to delegate —
	// the hop that registering automations as tools exists to remove. Free on
	// accounts without the feature flag: the source returns nothing at all.
	tools = append(tools, toolcore.ListAccountSourcedTools(accountId)...)

	// Conditionally add think tool for complex investigations
	if config.Config.LlmServerThinkToolEnabled {
		if thinkTool, ok := toolcore.GetNBTool(accountId, "think"); ok {
			tools = append(tools, thinkTool)
		}
	}

	return tools
}
