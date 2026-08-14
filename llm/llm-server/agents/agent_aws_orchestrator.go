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

// AWS Agent name constants
const (
	// AgentAwsOrchestratorName is the name for the AWS debug agent
	AgentAwsOrchestratorName = "aws_orchestrator"
	// AgentAwsOrchestrator2Name is the always-direct eval handle (invocable by @name only).
	AgentAwsOrchestrator2Name = "aws_orchestrator_2"
	// AwsAgentName is the name for the AWS agent
	AwsAgentName = "aws"
)

// AwsOrchestratorMode* are the values of config llm_server_aws_orchestrator_mode,
// the boot-time knob for what the router-selected aws_orchestrator runs.
const (
	AwsOrchestratorModeDelegating = "delegating" // v1: delegate AWS resource CLI to the `aws` sub-agent (fallback for unknown mode)
	AwsOrchestratorModeDirect     = "direct"     // v2: hold aws_execute and run the AWS CLI directly
	AwsOrchestratorModeLean       = "lean"       // minimal principle-level prompt + direct aws_execute — the DEFAULT
)

func init() {
	core.RegisterNBAgentFactoryWithAliases(AgentAwsOrchestratorName, func(accountId string) (core.NBAgent, error) {
		return newAwsOrchestratorAgent(accountId), nil
	}, "aws_debug")

	// Explicit always-direct eval handle (see AgentAwsOrchestrator2Name). Same
	// implementation, distinct name → distinct cache key, invocable by @name only.
	core.RegisterNBAgentFactoryWithAliases(AgentAwsOrchestrator2Name, func(accountId string) (core.NBAgent, error) {
		return newAwsOrchestratorAgentNamed(accountId, AgentAwsOrchestrator2Name, true), nil
	}, "aws_debug_2")
}

// AwsOrchestratorAgent is an agent that helps debug AWS issues. useAwsCliDirect
// selects v2 (hold aws_execute, run the CLI directly) vs v1 (delegate to the `aws`
// sub-agent); it drives both the tool set and the prompt's {{if .use_aws_cli_direct}}.
type AwsOrchestratorAgent struct {
	accountId            string
	name                 string
	useAwsCliDirect      bool
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newAwsOrchestratorAgent is the primary, router-selected agent. Its behavior is
// chosen by config.AwsOrchestratorMode (boot-time; rollback = change + redeploy).
// Unknown/empty mode falls back to delegating (v1), the safe default.
func newAwsOrchestratorAgent(accountId string) core.NBAgent {
	switch strings.ToLower(strings.TrimSpace(config.Config.AwsOrchestratorMode)) {
	case AwsOrchestratorModeLean:
		return newAwsLeanAgentNamed(accountId, AgentAwsOrchestratorName)
	case AwsOrchestratorModeDirect:
		return newAwsOrchestratorAgentNamed(accountId, AgentAwsOrchestratorName, true)
	default:
		return newAwsOrchestratorAgentNamed(accountId, AgentAwsOrchestratorName, false)
	}
}

// newAwsOrchestratorAgentNamed is the shared constructor. name drives identity and the
// cache key (so the primary and the eval handle stay isolated); useAwsCliDirect drives
// the aws CLI tool and the prompt's {{if .use_aws_cli_direct}} branch.
func newAwsOrchestratorAgentNamed(accountId, name string, useAwsCliDirect bool) core.NBAgent {
	return &AwsOrchestratorAgent{
		accountId:       accountId,
		name:            name,
		useAwsCliDirect: useAwsCliDirect,
	}
}

// GetName returns the name of the agent.
func (a *AwsOrchestratorAgent) GetName() string {
	return a.name
}

// GetNameAliases returns aliases for the agent name.
func (a *AwsOrchestratorAgent) GetNameAliases() []string {
	if a.name == AgentAwsOrchestrator2Name {
		return []string{"aws debug 2", "amazon_aws_debug_2", "aws_debug_2"}
	}
	return []string{"aws debug", "amazon_aws_debug", "aws_debug"}
}

// GetDescription returns a description of the agent.
func (a *AwsOrchestratorAgent) GetDescription() string {
	return "An expert AWS investigation and troubleshooting orchestrator. Uses `aws_observability` for CloudWatch Logs/Metrics/Alarms/X-Ray, and inspects all other AWS resources (EC2, RDS, S3, VPC, Lambda, Cost, etc.) — via the `aws` sub-agent (delegating mode) or `aws_execute` directly (direct mode). Generates comprehensive investigation plans."
}

func (a *AwsOrchestratorAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	return getAwsPlannerSupportedTools(ctx, a.accountId, a.GetName(), a.useAwsCliDirect)
}

func (a *AwsOrchestratorAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

// IsWatchCapable: drives action sub-agents whose async outcome completes later,
// so it may register a background watch.
func (a *AwsOrchestratorAgent) IsWatchCapable() bool { return true }

func (a *AwsOrchestratorAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *AwsOrchestratorAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// GetSystemPrompt returns the system prompt for the agent. useAwsCliDirect drives the
// prompt's {{if .use_aws_cli_direct}} blocks: run aws_execute directly (v2) vs route AWS
// resource work through the `aws` sub-agent (v1). All routing guidance lives in the
// prompt file (gated by that flag), not appended here, so the two variants can't drift.
func (a *AwsOrchestratorAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := renderAwsDebugReactPrompt(a.useAwsCliDirect)
	instructions := strings.Split(promptText, "\n")

	if !a.clusterSnapshotFound {
		a.clusterSnapshot = tools.GetCurrentAwsAccountState(a.accountId)
		a.clusterSnapshotFound = true
	}

	if len(a.clusterSnapshot) > 0 {
		regions := append([]string(nil), a.clusterSnapshot["region"]...)
		sort.Strings(regions)
		services := append([]string(nil), a.clusterSnapshot["service"]...)
		sort.Strings(services)
		instructions = append(instructions, "**Current AWS State:**")
		instructions = append(instructions, "Active Regions - "+strings.Join(regions, ","))
		instructions = append(instructions, "**Current Services:**")
		instructions = append(instructions, "AWS Services - "+strings.Join(services, ","))
	}

	if config.Config.LlmServerShellToolEnabled {
		instructions = append(instructions, "**Full Shell Capabilities:**")
		instructions = append(instructions, "The execution environment supports a full shell. You can use pipes (`|`), redirection, and standard Linux utilities (`grep`, `awk`, `sed`, `jq`, `sort`, `uniq`) in your planned queries.")
		instructions = append(instructions, "Encourage the use of these tools to filter and process output directly in the command line for efficiency.")
	}

	constraints := []string{
		"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
		"If logs show 'connection to IP X failed', ask 'Where did X come from?' (UserData, config)",
		"Config issues (wrong IP/endpoint) look like network issues but are NOT - validate config first",
		"NEVER query logs without first verifying log groups exist",
	}
	if a.useAwsCliDirect {
		constraints = append(constraints,
			"Run AWS resource inspection with aws_execute directly (write the actual `aws ...` CLI); only CloudWatch/log/X-Ray work goes to aws_observability",
			"If a resource is 'not found', investigation ends there - don't fabricate next steps")
	} else {
		constraints = append(constraints,
			"Sub-agents generate AWS CLI commands internally - describe WHAT to investigate in natural language",
			"If sub-agent reports 'not found', investigation ends there - don't fabricate next steps")
	}

	role := "an expert AWS investigation and troubleshooting orchestrator that delegates to specialized sub-agents"
	if a.useAwsCliDirect {
		role = "an expert AWS investigation and troubleshooting orchestrator that runs the AWS CLI directly via aws_execute and delegates observability to aws_observability"
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
		OutputFormat: awsReactOutputFormat,
	}
}

// renderAwsDebugReactPrompt renders the shared agent_aws_debug_react prompt for both AWS
// orchestrators. useAwsCliDirect drives the {{if .use_aws_cli_direct}} blocks in the
// prompt file: false = delegate AWS resource CLI to the `aws` sub-agent (v1), true = hold
// and run aws_execute directly (v2). Keeping all routing guidance in the prompt (gated by
// that flag) means the two variants cannot drift apart. On any parse/execute error the raw
// prompt is returned so the agent retains its full instructions. Takes no RequestContext:
// rendering is context-free, and the rare error path logs without one (avoids a nil-ctx deref).
func renderAwsDebugReactPrompt(useAwsCliDirect bool) string {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAwsDebugReact)
	tmplData := map[string]any{"use_aws_cli_direct": useAwsCliDirect}
	t, err := template.New("aws_debug").Option("missingkey=zero").Parse(promptText)
	if err != nil {
		slog.Error("failed to parse aws_debug prompt template, using raw prompt", "error", err)
		return promptText
	}
	var buf strings.Builder
	if err := t.Execute(&buf, tmplData); err != nil {
		slog.Error("failed to execute aws_debug prompt template, using raw prompt", "error", err)
		return promptText
	}
	return buf.String()
}

// awsReactOutputFormat is the output format for react_3 planners — conditionally applies
// the investigation format only for troubleshooting queries.
const awsReactOutputFormat = `Choose the format based on the type of user request:

**FOR INVESTIGATION / TROUBLESHOOTING QUERIES** (e.g. "why is X failing", "debug Y", "show me recent issues"):

**Investigation Summary:**
- **Symptom:** [What user reported]
- **Signal:** [What metrics/logs showed]

### Causality Chain (Root Cause)
- **Symptom:** (The primary issue reported/observed)
- **Why?** (Immediate cause of the symptom)
- **Why?** (Next layer of causality)
- **Root Cause:** (The foundational reason discovered)

**Evidence Chain:**
1. [Tool Name - ID](#task-ID) -> [Key finding]
2. [Tool Name - ID](#task-ID) -> [Key finding]

**CRITICAL: Citation Format Rule**
You MUST use the full markdown link format for EVERY reference: [Short Tool Name - ID](#task-ID).
Example: ...found in [AWS - E1](#task-E1) and [CloudWatch Logs - E3](#task-E3).
Exception: when citing an external resource that has its own real URL (e.g. a GitHub PR/issue link), use [Label](actual-url) with that real URL instead — never substitute a #task-ID anchor for it.

**Blast Radius:**
- Affected resources: [list]
- Potential downstream impact: [description]

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

func getAwsTicketAgentName() string {
	if config.Config.TicketV2Enabled {
		return "tickets_v2"
	}
	return "tickets"
}

// getAwsPlannerSupportedTools returns tools relevant to AWS debugging.
// This orchestrator agent primarily delegates to aws_observability and aws sub-agents.
func getAwsPlannerSupportedTools(ctx *security.RequestContext, accountId, agentName string, useAwsCliDirect bool) []tocore.NBTool {
	supportedToolNames := []string{
		"aws_observability",
		tools.ToolExecuteAwsCliCommand,
		getAwsTicketAgentName(),
		"github",          // GithubAgentName
		"websearch",       // SearchAgentName
		"recommendations", // RecommendationsAgentName
		"events",          // EventsAgentName
		"visualizer",      // VisualizationAgentName
		"postgres",        // PostgresAgentName
		"mysql",           // MySQLAgentName
		"mssql",           // MSSQLAgentName
		"oracle",          // OracleAgentName
		"redis",           // RedisAgentName
		"rabbitmq",        // RabbitMQAgentName
		"kubectl",         // KubectlAgentName
		DelegateAgentToolName,
	}

	// v1 (delegating) routes AWS resource inspection through the `aws` sub-agent;
	// v2 (direct) holds aws_execute (always present) and drops the sub-agent hop.
	if !useAwsCliDirect {
		supportedToolNames = append(supportedToolNames, AwsAgentName)
	}

	// The KG-backed service_dependency_graph covers cloud (AWS/GCP/Azure) topology,
	// not just K8s, so expose it to this orchestrator. The old V1 variant that
	// was K8s-only has been removed; the V2 flag guard here went with it.
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
			// Check if tool is configured before adding it
			if !tocore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", agentName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("AWS Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId, "agent", agentName)
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
