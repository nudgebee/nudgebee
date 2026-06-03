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
	AgentAzureDebugName = "azure_debug"
)

func init() {
	core.RegisterNBAgentFactory(AgentAzureDebugName, func(accountId string) (core.NBAgent, error) {
		return newAzureDebugAgent(accountId), nil
	})
}

// AzureDebugAgent2 is an agent that helps debug Azure issues.
type AzureDebugAgent2 struct {
	accountId            string
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

// newAzureDebugAgent creates a new AzureDebugAgent2.
// The factory will provide accountId.
func newAzureDebugAgent(accountId string) core.NBAgent {
	return &AzureDebugAgent2{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (a *AzureDebugAgent2) GetName() string {
	return AgentAzureDebugName
}

// GetNameAliases returns aliases for the agent name.
func (a *AzureDebugAgent2) GetNameAliases() []string {
	return []string{"azure debug", "microsoft_azure_debug", "azure_debug"}
}

// GetDescription returns a description of the agent.
func (a *AzureDebugAgent2) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within Azure environments, providing step-by-step XML plans."
}

func (a *AzureDebugAgent2) GetSupportedTools(ctx *security.RequestContext) []tocore.NBTool {
	return getAzurePlannerSupportedTools(ctx, a.accountId)
}

func (a *AzureDebugAgent2) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReWoo
}

func (a *AzureDebugAgent2) GetModelCategory() core.ModelTier {
	return core.ModelTierReasoning
}

func (a *AzureDebugAgent2) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

// GetSystemPrompt returns the system prompt for the agent.
func (a *AzureDebugAgent2) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	// Select prompt based on effective planner: react_3 uses a react-optimised prompt
	// that avoids ReWOO plan-oriented language and includes parallel action examples.
	useReact := config.Config.LlmServerReAct3Enabled || config.Config.LlmServerRewooToReact3Enabled
	var instructions []string
	if useReact {
		promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentAzureDebugReact)
		instructions = strings.Split(promptText, "\n")
	} else {
		instructions = []string{
			"**Role:** You are a senior Azure SRE and cloud infrastructure expert specializing in planning the deep investigation.",
			"**Primary Directive:** Your only goal is to create a plan of tool calls to investigate and resolve user issues. You must not answer questions directly or provide instructions to the user.",
			"**Information Gathering:** All user queries are requests for investigation. You must create a plan to gather data using the available tools.",
			"   - If a user provides a partial resource name, your first step must be to find the complete and correct resource name.",
			"   - Break down complex problems into a sequence of smaller, single-purpose tasks in your plan.",
			"**Tool Selection Strategy:**",
			"   - **Prioritize Data Gathering:** Always start by gathering relevant data using tools designed for observation and information retrieval.",
			"   - **Iterative Refinement:** If initial data is insufficient, refine your plan to use other tools to gather more specific information.",
			"   - **Comprehensive Tool List:** Refer to the list of available tools and their detailed descriptions provided to you for the complete set of tools you can use.",
			"**No Self-Permission Modification (CRITICAL):**",
			"   - If a command fails with 403/AuthorizationFailed, report the missing permission as a finding.",
			"   - NEVER plan steps that modify role assignments or permissions to grant yourself access (e.g., az role assignment create, az ad app permission grant). The correct response to a permission error is to inform the user, not to fix it yourself.",
			"**Plan Creation:**",
			"   - Always create a plan to perform the debugging steps yourself. Do not output instructions for the user. For example if you can perform data collection using existing tools then priortize new plan with new task over asking user to do further investigation",
			"**Investigation Ordering - CRITICAL:** Always investigate from the inside out. (1) Confirm resource exists and is healthy. (2) Validate actual behavior at the resource/OS level first - for VMs, run commands inside the VM via az vm run-command to observe what the VM actually sees (DNS, connectivity, routes, processes). (3) Only escalate to Azure infrastructure layer (NSG, UDR, VNet, public IP) if OS-level evidence points there. Never conclude a network or infrastructure cause without first eliminating OS-level and application-level causes.",
		}
	}

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

	var constraints []string
	var examples []core.NBAgentPromptExample
	var outputFormat string

	if useReact {
		constraints = []string{
			"Sub-agent `azure` executes CLI commands internally - describe WHAT to investigate in natural language",
			"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
			"Config issues (wrong DNS, bad endpoint, misconfigured env var) look like network or connectivity issues but are NOT - always validate OS/app config inside the resource before blaming Azure infrastructure",
			"If a sub-agent returns 'not found' or empty, investigation ends there - do not fabricate next steps",
		}
		examples = nil
		outputFormat = azureReactOutputFormat
	} else {
		constraints = []string{
			"CRITICAL: Response MUST be XML plan format (not conversational text)",
			"Sub-agent `azure` executes CLI commands internally - describe WHAT to investigate in natural language queries to it",
			"Plan must directly address user's question via data-gathering tools",
			"Investigation ONLY - DIAGNOSE and PROPOSE remediation, NEVER execute infrastructure changes",
			"Config issues (wrong DNS, bad endpoint, misconfigured env var) look like network or connectivity issues but are NOT - always validate OS/app config inside the resource before blaming Azure infrastructure",
			"If a sub-agent returns 'not found' or empty, investigation ends there - do not fabricate next steps",
			"Chain dependencies: mark <dependency>A</dependency> and use #A placeholder to reference prior step output in subsequent steps",
		}
		examples = azureRewooExamples
		outputFormat = azureRewooOutputFormat
	}

	return core.NBAgentPrompt{
		Role:         "a senior Azure SRE and cloud infrastructure expert specializing in deep investigation and root cause analysis",
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

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

const azureRewooOutputFormat = `Your response MUST be a valid XML plan. Start with <plan_response>, then <thought>, then <plan>. Each <step> must have <id>, <tool>, <query>, <reason>. Use <dependency> to chain steps.

Example format:
<plan_response>
<thought>
[Describe investigation strategy across these layers:]
Resource: [Existence, provisioning state, power state, quota]
OS/App: [What the resource actually sees - run-command, logs, app config, env vars, DNS]
Network: [NSG, UDR, VNet - only if OS/app layer points here]
Temporal: [What changed before the issue - activity log, recent deployments]
</thought>
<plan>
	<step>
		<id>E1</id>
		<tool>azure</tool>
		<query><![CDATA[natural language: what to investigate]]></query>
		<reason><![CDATA[What this step reveals.]]></reason>
	</step>
	<step>
		<id>E2</id>
		<tool>azure</tool>
		<query><![CDATA[follow-up using findings from #E1]]></query>
		<reason><![CDATA[Dependent on E1 result.]]></reason>
		<dependency>E1</dependency>
	</step>
</plan>
</plan_response>

Final summary format (after investigation):
**Investigation Summary:**
- **Symptom:** [What user reported]
- **Signal:** [What evidence showed]

### Causality Chain (5-Whys)
- **Symptom:** (The primary issue)
- **Why?** (Immediate cause)
- **Why?** (Next layer)
- **Root Cause:** (The foundational reason)

**Evidence Chain:**
1. [Tool Name - ID](#task-ID) - [Key finding]
2. [Tool Name - ID](#task-ID) - [Key finding]

**CRITICAL: Citation Format Rule**
You MUST use the full markdown link format for EVERY reference: [Short Tool Name - ID](#task-ID).
Example: ...found in [Azure - E1](#task-E1) and [Azure - E3](#task-E3).`

var azureRewooExamples = []core.NBAgentPromptExample{
	{
		Question: "My VM 'my-vm' in 'eastus' is slow.",
		Answer:   `<plan_response><thought>The user's VM is slow. I need to check the VM state and power state first, then observe OS-level CPU and memory from inside the VM, and only escalate to NSG if OS evidence points there.</thought><plan><step><id>E1</id><tool>azure</tool><query><![CDATA[Check the current state, provisioning state, and power state of VM my-vm in resource group my-resource-group]]></query><reason><![CDATA[Confirm the VM exists and is running before proceeding with any investigation.]]></reason></step><step><id>E2</id><tool>azure</tool><query><![CDATA[Get CPU utilization metrics for VM my-vm in resource group my-resource-group over the last 1 hour]]></query><reason><![CDATA[Check Azure Monitor CPU metrics to determine if the VM is under sustained high CPU load.]]></reason><dependency>E1</dependency></step><step><id>E3</id><tool>azure</tool><query><![CDATA[Run a command inside VM my-vm (resource group my-resource-group) to check top CPU-consuming processes and available memory]]></query><reason><![CDATA[Validate actual OS-level resource usage inside the VM to confirm or deny the Azure metrics signal.]]></reason><dependency>E1</dependency></step></plan></plan_response>`,
	},
	{
		Question: "Why can't I access my storage account 'mystorageaccount' in 'westus'?",
		Answer:   `<plan_response><thought>The user cannot access a storage account. I will check the account state and network rules first (configuration layer), then RBAC assignments, before concluding on root cause.</thought><plan><step><id>S1</id><tool>azure</tool><query><![CDATA[Show storage account mystorageaccount in resource group my-resource-group including its provisioning state and network rule set]]></query><reason><![CDATA[Confirm the storage account exists, is provisioned successfully, and check if network rules default action is Deny.]]></reason></step><step><id>S2</id><tool>azure</tool><query><![CDATA[List all network rules (IP rules, VNet rules, default action) for storage account mystorageaccount in resource group my-resource-group]]></query><reason><![CDATA[Determine if the caller's IP or VNet is blocked by a Deny-by-default network rule set.]]></reason><dependency>S1</dependency></step><step><id>S3</id><tool>azure</tool><query><![CDATA[List role assignments scoped to storage account mystorageaccount in resource group my-resource-group]]></query><reason><![CDATA[Check if the caller has at minimum Storage Blob Data Reader.]]></reason><dependency>S1</dependency></step></plan></plan_response>`,
	},
}

// getAzurePlannerSupportedTools returns tools relevant to Azure debugging.
// For this agent, it's primarily the "azure" tool (AzureAgentName).
func getAzurePlannerSupportedTools(ctx *security.RequestContext, accountId string) []tocore.NBTool {
	supportedToolNames := []string{AzureAgentName, getTicketAgentName(), WorkflowAgentName, GithubAgentName, WebSearchAgentName, RecommendationsAgentName, EventsAgentName, VisualizationAgentName, PostgresAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RedisAgentName, RabbitMQAgentName, KubectlAgentName, DelegateAgentToolName}

	// The KG-backed V2 service_dependency_graph covers cloud (AWS/GCP/Azure)
	// topology, not just K8s, so expose it to this orchestrator when V2 is active.
	// V1 is K8s-only — gate on the V2 flag.
	if config.Config.ServiceDependencyGraphV2Enabled {
		supportedToolNames = append(supportedToolNames, ServiceDependencyGraph)
	}

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := tocore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", AgentAzureDebugName)
	}

	tools := make([]tocore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := tocore.GetNBTool(accountId, toolName)
		if found {
			if !tocore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", AgentAzureDebugName)
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
