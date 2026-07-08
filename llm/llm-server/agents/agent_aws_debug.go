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

// AWS Agent name constants
const (
	// AgentAwsDebugName is the name for the AWS debug agent
	AgentAwsDebugName = "aws_debug"
	// AwsAgentName is the name for the AWS agent
	AwsAgentName = "aws"
)

func init() {
	core.RegisterNBAgentFactory(AgentAwsDebugName, func(accountId string) (core.NBAgent, error) {
		return newAwsDebugAgent(accountId), nil
	})
}

// AwsDebugAgent is an agent that helps debug AWS issues.
type AwsDebugAgent struct {
	accountId            string
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newAwsDebugAgent creates a new AwsDebugAgent.
// The factory will provide accountId.
func newAwsDebugAgent(accountId string) core.NBAgent {
	return &AwsDebugAgent{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (a *AwsDebugAgent) GetName() string {
	return AgentAwsDebugName
}

// GetNameAliases returns aliases for the agent name.
func (a *AwsDebugAgent) GetNameAliases() []string {
	return []string{"aws debug", "amazon_aws_debug", "aws_debug"}
}

// GetDescription returns a description of the agent.
func (a *AwsDebugAgent) GetDescription() string {
	return "An expert AWS investigation and troubleshooting orchestrator that delegates to specialized sub-agents: `aws_observability` for CloudWatch Logs/Metrics/Alarms/X-Ray, and `aws` for all other AWS resources (EC2, RDS, S3, VPC, Lambda, Cost, etc.). Generates comprehensive investigation plans."
}

func (a *AwsDebugAgent) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	return getAwsPlannerSupportedTools(ctx, a.accountId)
}

func (a *AwsDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeOrchestrating
}

func (a *AwsDebugAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *AwsDebugAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// GetSystemPrompt returns the system prompt for the agent.
func (a *AwsDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAwsDebugReact)
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
		"Sub-agents generate AWS CLI commands internally - describe WHAT to investigate in natural language",
		"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
		"If logs show 'connection to IP X failed', ask 'Where did X come from?' (UserData, config)",
		"Config issues (wrong IP/endpoint) look like network issues but are NOT - validate config first",
		"NEVER query logs without first verifying log groups exist",
		"If sub-agent reports 'not found', investigation ends there - don't fabricate next steps",
	}

	return core.NBAgentPrompt{
		Role:         "an expert AWS investigation and troubleshooting orchestrator that delegates to specialized sub-agents",
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

// awsReactOutputFormat is the output format for react_3 planners — conditionally applies
// the investigation format only for troubleshooting queries.
const awsReactOutputFormat = `Choose the format based on the type of user request:

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
func getAwsPlannerSupportedTools(ctx *security.RequestContext, accountId string) []tocore.NBTool {
	supportedToolNames := []string{
		"aws_observability",
		AwsAgentName,
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

	// The KG-backed service_dependency_graph covers cloud (AWS/GCP/Azure) topology,
	// not just K8s, so expose it to this orchestrator. The old V1 variant that
	// was K8s-only has been removed; the V2 flag guard here went with it.
	supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := tocore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", AgentAwsDebugName)
	}

	tools := make([]tocore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := tocore.GetNBTool(accountId, toolName)
		if found {
			// Check if tool is configured before adding it
			if !tocore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", AgentAwsDebugName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("AWS Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId)
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
