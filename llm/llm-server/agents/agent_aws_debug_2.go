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

// AwsDebugAgent2 is an agent that helps debug AWS issues.
type AwsDebugAgent2 struct {
	accountId            string
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newAwsDebugAgent creates a new AwsDebugAgent2.
// The factory will provide accountId.
func newAwsDebugAgent(accountId string) core.NBAgent {
	return &AwsDebugAgent2{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (a *AwsDebugAgent2) GetName() string {
	return AgentAwsDebugName
}

// GetNameAliases returns aliases for the agent name.
func (a *AwsDebugAgent2) GetNameAliases() []string {
	return []string{"aws debug", "amazon_aws_debug", "aws_debug"}
}

// GetDescription returns a description of the agent.
func (a *AwsDebugAgent2) GetDescription() string {
	return "An expert AWS investigation and troubleshooting orchestrator that delegates to specialized sub-agents: `aws_observability` for CloudWatch Logs/Metrics/Alarms/X-Ray, and `aws` for all other AWS resources (EC2, RDS, S3, VPC, Lambda, Cost, etc.). Generates comprehensive investigation plans."
}

func (a *AwsDebugAgent2) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	return getAwsPlannerSupportedTools(ctx, a.accountId)
}

func (a *AwsDebugAgent2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReWoo
}

func (a *AwsDebugAgent2) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *AwsDebugAgent2) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// GetSystemPrompt returns the system prompt for the agent.
func (a *AwsDebugAgent2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	// Select prompt based on effective planner: react_3 uses a react-optimised prompt
	// that avoids ReWOO plan-oriented language and includes parallel action examples.
	promptRepoKey := prompts_repo.PromptAgentAwsDebug2
	if config.Config.LlmServerReAct3Enabled || config.Config.LlmServerRewooToReact3Enabled {
		promptRepoKey = prompts_repo.PromptAgentAwsDebugReact
	}
	promptText := prompts_repo.GetPrompt(promptRepoKey)
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

	useReact := config.Config.LlmServerReAct3Enabled || config.Config.LlmServerRewooToReact3Enabled

	// Build constraints, examples, and output format based on effective planner type
	var constraints []string
	var examples []core.NBAgentPromptExample
	var outputFormat string

	if useReact {
		constraints = []string{
			"Sub-agents generate AWS CLI commands internally - describe WHAT to investigate in natural language",
			"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
			"If logs show 'connection to IP X failed', ask 'Where did X come from?' (UserData, config)",
			"Config issues (wrong IP/endpoint) look like network issues but are NOT - validate config first",
			"NEVER query logs without first verifying log groups exist",
			"If sub-agent reports 'not found', investigation ends there - don't fabricate next steps",
		}
		// No examples needed - the react prompt file has parallel action examples inline
		examples = nil
		outputFormat = awsReactOutputFormat
	} else {
		constraints = []string{
			"CRITICAL: Response MUST be XML plan format (not conversational text)",
			"Sub-agents generate AWS CLI commands internally - describe WHAT to investigate in natural language",
			"Plan must directly address user's question via appropriate specialized agents",
			"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
			"If logs show 'connection to IP X failed', ask 'Where did X come from?' (UserData, config)",
			"Config issues (wrong IP/endpoint) look like network issues but are NOT - validate config first",
			"Chain dependencies: mark <dependency>A</dependency>, use #A placeholder to reference prior step output",
			"NEVER plan to query logs without first verifying log groups exist",
			"If sub-agent reports 'not found', investigation ends there - don't fabricate next steps",
		}
		examples = awsRewooExamples
		outputFormat = awsRewooOutputFormat
	}

	return core.NBAgentPrompt{
		Role:         "an expert AWS investigation and troubleshooting orchestrator that delegates to specialized sub-agents",
		Instructions: instructions,
		Constraints:  constraints,
		// ToolUsage intentionally omitted: the planner already renders each tool's
		// Description() once via {{.tool_descriptions}}. Seeding it here duplicated that
		// same text in the <tool_usage_instructions> block of this orchestrator's
		// (account-cached) prompt prefix.
		Examples: examples,
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

**Blast Radius:**
- Affected resources: [list]
- Potential downstream impact: [description]

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

// awsRewooOutputFormat is the output format for ReWOO planners — includes XML plan template + final answer.
const awsRewooOutputFormat = `Your response MUST be a valid XML plan. The plan MUST start with a <plan_response> tag, followed by a <thought> tag containing reason behind the plan, followed by <plan> tag with actual plan. Each <step> must have an <id>, <tool>, <query>, and <reason>. Optional: <dependency> for step chaining, <condition> for conditional execution.

Example format:
<plan_response>
<thought>
[Describe your investigation strategy using the three-layer model]
Infrastructure: [Resource status, configuration, limits]
Network: [Security Groups, NACLs, Routes, NAT/IGW]
Application: [Logs, traces, errors]
Blast radius: [What else might be affected]
Temporal: [What might have changed before the issue]
</thought>
<plan>
	<step>
		<id>O1</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Get alarm details: extract resource identifiers and timestamp]]></query>
		<reason><![CDATA[Identify what resource and when the issue started.]]></reason>
	</step>
</plan>
</plan_response>

Final summary format (after investigation):
**Investigation Summary:**
- **Symptom:** [What user reported]
- **Signal:** [What metrics/logs showed]

### Causality Chain (5-Whys)
- **Symptom:** (The primary issue reported/observed)
- **Why?** (Immediate cause of the symptom)
- **Why?** (Next layer of causality)
- **Root Cause:** (The foundational reason discovered)

**Evidence Chain:**
1. [Tool Name - ID](#task-ID) → [Key finding]
2. [Tool Name - ID](#task-ID) → [Key finding]

**CRITICAL: Citation Format Rule**
You MUST NOT use simple citations like [E1] or [E1, E3].
You MUST use the full markdown link format for EVERY reference: [Short Tool Name - ID](#task-ID).
Example: ...found in [Resource Search - E1](#task-E1) and [Logs - E3](#task-E3).

**Blast Radius:**
- Affected resources: [list]
- Potential downstream impact: [description]

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]`

// awsRewooExamples contains the ReWOO-style XML plan examples for the AWS debug agent.
var awsRewooExamples = []core.NBAgentPromptExample{
	{
		Question: "RDS alarm 'HighDBConnections' is firing. What's the root cause?",
		Answer: `<plan_response>
<thought>RDS connection alarm. Three-layer investigation:
1) Infrastructure: RDS status, max_connections config, current connection count
2) Network: Security Groups, can apps reach RDS?
3) Application: Which apps are connecting, are they leaking connections?
Plus temporal correlation - what changed before connections spiked?</thought>
<plan>
	<step>
		<id>O1</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Get CloudWatch alarm 'HighDBConnections' details: threshold, current value, which RDS instance, when alarm started]]></query>
		<reason><![CDATA[Identify the RDS instance and establish timeline for temporal correlation.]]></reason>
	</step>
	<step>
		<id>A1</id>
		<tool>aws</tool>
		<query><![CDATA[Describe RDS instance from #O1: instance status, max_connections parameter, VPC, Security Groups, instance class]]></query>
		<reason><![CDATA[Infrastructure layer: Check RDS health and connection capacity configuration.]]></reason>
		<dependency>O1</dependency>
	</step>
	<step>
		<id>A2</id>
		<tool>aws</tool>
		<query><![CDATA[Find EC2/Lambda/ECS in same VPC as RDS from #A1 that might be connecting to database - check Security Group references]]></query>
		<reason><![CDATA[Blast radius: Identify which applications are potential connection sources.]]></reason>
		<dependency>A1</dependency>
	</step>
	<step>
		<id>O2</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Check CloudTrail for changes 30 min before alarm time from #O1: deployments, scaling events, Lambda config changes, ECS service updates]]></query>
		<reason><![CDATA[Temporal correlation: What changed before connections spiked?]]></reason>
		<dependency>O1</dependency>
	</step>
	<step>
		<id>O3</id>
		<tool>aws_observability</tool>
		<query><![CDATA[If #A2 found applications: FIRST discover their CloudWatch log groups (check /aws/lambda/, /ecs/, /demo/ prefixes). THEN query for database connection patterns - look for connection open/close imbalance indicating leaks]]></query>
		<reason><![CDATA[Application layer: Must discover log groups first. Check for connection leak pattern (many opens, few closes).]]></reason>
		<dependency>A2</dependency>
	</step>
</plan>
</plan_response>`,
	},
	{
		Question: "NAT Gateway ConnectionError alarm is firing",
		Answer: `<plan_response>
<thought>NAT Gateway alarm - shared resource for all private subnet egress. Check: NAT status → affected subnets → recent changes.</thought>
<plan>
	<step>
		<id>O1</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Get alarm details: which NAT Gateway, alarm threshold, when it started]]></query>
		<reason><![CDATA[Identify NAT Gateway ID and timeline.]]></reason>
	</step>
	<step>
		<id>A1</id>
		<tool>aws</tool>
		<query><![CDATA[Describe NAT Gateway from #O1: State, VPC, Elastic IP]]></query>
		<reason><![CDATA[Check NAT Gateway status and config.]]></reason>
		<dependency>O1</dependency>
	</step>
	<step>
		<id>A2</id>
		<tool>aws</tool>
		<query><![CDATA[Find route tables using this NAT Gateway - identify affected subnets]]></query>
		<reason><![CDATA[Blast radius: Which subnets lost internet access?]]></reason>
		<dependency>A1</dependency>
	</step>
	<step>
		<id>O2</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Check CloudTrail 30 min before alarm: DeleteNatGateway, ReplaceRoute, ModifyVpcAttribute]]></query>
		<reason><![CDATA[What changed before NAT failed?]]></reason>
		<dependency>O1</dependency>
	</step>
</plan>
</plan_response>`,
	},
	{
		Question: "ALB alarm 'Frontend-High-5xx-Errors' is firing. Investigate.",
		Answer: `<plan_response>
<thought>
ALB 5xx alarm. Multi-hop correlation investigation:
Alarm → Metric (which target group?) → Targets (instance IDs) → Logs (join by instance_id + timestamp) → Config file (backend IP value) → UserData (provenance) → Backend validation (rule out false positive).
Key: Use identifiers as join keys - LoadBalancerFullName, TargetGroupArn, InstanceID, LogStreamName=instance_id.
CRITICAL: Check config provenance BEFORE blaming infrastructure.
</thought>
<plan>
	<step>
		<id>O1</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Get alarm 'Frontend-High-5xx-Errors' details: threshold, current value, LoadBalancerFullName dimension, StateTransitionedTimestamp (alarm start time)]]></query>
		<reason><![CDATA[Extract LoadBalancerFullName (join key) and alarm timestamp (temporal anchor).]]></reason>
	</step>
	<step>
		<id>O2</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Get HTTPCode_Target_5XX_Count metric for LoadBalancer from #O1, grouped by TargetGroup dimension, time window around alarm timestamp from #O1]]></query>
		<reason><![CDATA[Narrow scope: Which specific target group is generating 5xx errors?]]></reason>
		<dependency>O1</dependency>
	</step>
	<step>
		<id>A1</id>
		<tool>aws</tool>
		<query><![CDATA[Describe target health for target group identified in #O2 - get target IDs (instance IDs) and health states]]></query>
		<reason><![CDATA[Map TargetGroupArn → Instance IDs (join keys for logs). Note: Targets can be healthy yet return 500.]]></reason>
		<dependency>O2</dependency>
	</step>
	<step>
		<id>A2</id>
		<tool>aws</tool>
		<query><![CDATA[Describe EC2 instances for target instance IDs from #A1 - get instance names, IAM role, tags]]></query>
		<reason><![CDATA[Identify workload name and config. Instance ID is join key for log streams.]]></reason>
		<dependency>A1</dependency>
	</step>
	<step>
		<id>O3</id>
		<tool>aws_observability</tool>
		<query><![CDATA[FIRST: Discover log groups (check /demo/, /aws/ec2/ prefixes). THEN: Query logs for instance IDs from #A2 in time window from #O1 - look for connection errors, backend failures, endpoint references]]></query>
		<reason><![CDATA[Correlate by instance_id (log stream name) and timestamp. Logs reveal what the application is attempting.]]></reason>
		<dependency>A2</dependency>
		<dependency>O1</dependency>
	</step>
	<step>
		<id>A3</id>
		<tool>aws</tool>
		<query><![CDATA[IF logs from #O3 show connection to specific IP/endpoint (e.g., 'Connecting to 0.0.0.0' or backend reference): Check EC2 UserData for frontend instance from #A2 to find where that IP/endpoint is configured]]></query>
		<reason><![CDATA[Configuration provenance: Trace bad value to deployment-time config. Rule out runtime changes first.]]></reason>
		<dependency>O3</dependency>
	</step>
	<step>
		<id>A4</id>
		<tool>aws</tool>
		<query><![CDATA[IF config shows backend dependency: List backend instances (check ASG/tags) and verify they exist, are running, and are healthy]]></query>
		<reason><![CDATA[Validation: Rule out backend unavailability as cause. Confirm this is config issue, not backend failure.]]></reason>
		<dependency>A3</dependency>
	</step>
	<step>
		<id>O4</id>
		<tool>aws_observability</tool>
		<query><![CDATA[Check CloudTrail for UserData changes, instance launches, or config updates 30 min before alarm timestamp from #O1]]></query>
		<reason><![CDATA[Temporal correlation: Was this config recently changed, or deployment drift?]]></reason>
		<dependency>O1</dependency>
	</step>
</plan>
</plan_response>`,
	},
}

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

	// The KG-backed V2 service_dependency_graph covers cloud (AWS/GCP/Azure)
	// topology, not just K8s, so expose it to this orchestrator when V2 is active.
	// V1 is K8s-only — gate on the V2 flag so AWS debugging never gets the
	// K8s-only runtime-metrics variant.
	if config.Config.ServiceDependencyGraphV2Enabled {
		supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)
	}

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
