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
	AgentGcpDebugName = "gcp_debug"
)

func init() {
	core.RegisterNBAgentFactory(AgentGcpDebugName, func(accountId string) (core.NBAgent, error) {
		return newGcpDebugAgent(accountId), nil
	})
}

type GcpDebugAgent struct {
	accountId            string
	clusterSnapshot      map[string][]string
	clusterSnapshotFound bool
}

func newGcpDebugAgent(accountId string) core.NBAgent {
	return &GcpDebugAgent{
		accountId: accountId,
	}
}

func (a *GcpDebugAgent) GetName() string {
	return AgentGcpDebugName
}

func (a *GcpDebugAgent) GetNameAliases() []string {
	return []string{"gcp debug", "google_cloud_debug", "gcp_debug"}
}

func (a *GcpDebugAgent) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within GCP environments, providing step-by-step XML plans."
}

func (a *GcpDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return getGcpPlannerSupportedTools(ctx, a.accountId)
}

func (a *GcpDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReWoo
}

func (a *GcpDebugAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

func (a *GcpDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	// Select prompt based on effective planner: react_3 uses a react-optimised prompt
	// that avoids ReWOO plan-oriented language and includes parallel action examples.
	useReact := config.Config.LlmServerReAct3Enabled || config.Config.LlmServerRewooToReact3Enabled
	var instructions []string
	if useReact {
		promptText := prompts_repo.GetPrompt(prompts_repo.PromptAgentGcpDebugReact)
		instructions = strings.Split(promptText, "\n")
	} else {
		instructions = []string{
			"**Primary Directive:** Your only goal is to create a plan of tool calls to investigate and resolve user issues. You must not answer questions directly or provide instructions to the user.",
			"**Information Gathering:** All user queries are requests for investigation. You must create a plan to gather data using the available tools.",
			"   - If a user provides a partial resource name, your first step must be to find the complete and correct resource name.",
			"   - Break down complex problems into a sequence of smaller, single-purpose tasks in your plan.",
			"**Tool Selection Strategy:**",
			"   - **Prioritize Data Gathering:** Always start by gathering relevant data using tools designed for observation and information retrieval (e.g., for GCP issues, use `gcp` for CLI commands, `web_search` for documentation, and `events` for recent alerts).",
			"   - **For Cloud Storage ACLs, prefer using 'gsutil acl get' as 'gcloud storage buckets get-acl' is not a valid command.**",
			"   - **Leverage Specialized Tools:** Use tools like `service_dependency_graph` for relationship analysis, and `web_search` for external knowledge or conceptual understanding.",
			"   - **Iterative Refinement:** If initial data is insufficient, refine your plan to use other tools to gather more specific information.",
			"   - **Comprehensive Tool List:** Refer to the 'Instructions on tool usage' section for a complete list of all available tools and their detailed descriptions.",
			"**No Self-Permission Modification (CRITICAL):**",
			"   - If a command fails with a permission error, report the missing permission as a finding.",
			"   - NEVER plan steps that modify IAM bindings, policies, or roles to grant yourself access (e.g., add-iam-policy-binding, set-iam-policy). The correct response to a permission error is to inform the user, not to fix it yourself.",
			"**Plan Creation:**",
			"   - Always create a plan to perform the debugging steps yourself. Do not output instructions for the user.",
		}
	}

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

	var constraints []string
	var examples []core.NBAgentPromptExample
	var outputFormat string

	if useReact {
		constraints = []string{
			"Focus on data collection - prioritize data-gathering tools like `gcp`, `prometheus`, `docs`, or `search`.",
			"If a tool execution fails due to permissions or errors, state the error and propose a different approach.",
			"If a command to list resources repeatedly returns empty, assume it's a permission issue and suggest the necessary permissions.",
			"Always verify that your actions directly address the user's question.",
		}
		examples = nil
		outputFormat = gcpReactOutputFormat
	} else {
		constraints = []string{
			"**CRITICAL:** Your response MUST be a plan in XML format. You are forbidden from responding with conversational text or instructions for the user.",
			"**CRITICAL:** The first step in your plan MUST NOT be an analysis step. Your plan should focus exclusively on data collection; synthesis is handled automatically after execution. Prioritize data-gathering tools like `gcp`, `web_search`, or `events`.",
			"If a tool execution fails due to permissions or errors, state the nature of the error in your reasoning and propose a different approach if possible.",
			"If a command to list resources (like billing accounts or projects) repeatedly returns an empty result, assume it's a permission issue, state this assumption, and suggest the necessary permissions to the user.",
			"Always verify that your plan directly addresses the user's question and doesn't ask the user to perform steps you can do with your tools.",
		}
		examples = gcpRewooExamples
		outputFormat = gcpRewooOutputFormat
	}

	return core.NBAgentPrompt{
		Role:         "a highly skilled DevOps, SRE and Software Development expert",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage: func() map[string][]string {
			tu := make(map[string][]string)
			for _, tool := range a.GetSupportedTools(ctx) {
				tu[tool.Name()] = []string{tool.Description()}
			}
			return tu
		}(),
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

**Resolution:**
- Immediate fix: [specific command/action]
- Long-term recommendation: [prevention]

**FOR ALL OTHER QUERIES** (generation, listing, explanation, how-to, etc.):
Answer the user's question directly in clear markdown. Do NOT use the investigation format above. Use code blocks, tables, or bullet points as appropriate for the content.`

const gcpRewooOutputFormat = `Your response MUST be a valid XML plan. The plan MUST start with a <plan_response> tag, followed by a <thought> tag containing thought behind plan and then <plan> tag with steps. Each <step> must have an <id>, <tool>, <query>, and <reason>. Do not include any text outside the main XML structure. For example:
<plan_response>
<thought>The user wants to list GCE instances. I will use the gcp tool to do this.</thought>
<plan>
	<step>
	<id>E1</id>
	<tool>gcp</tool>
	<query><![CDATA[gcloud compute instances list --project YOUR_PROJECT_ID]]></query>
	<reason><![CDATA[List GCE instances in YOUR_PROJECT_ID.]]></reason>
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
Example: ...found in [Resource Search - E1](#task-E1) and [Logs - E3](#task-E3).`

var gcpRewooExamples = []core.NBAgentPromptExample{
	{
		Question: "My GCE instance my-instance in us-central1-a is slow.",
		Answer:   `<plan_response><thought>The user's GCE instance is slow. I need to check the instance state, CPU utilization, and firewall rules.</thought><plan><step><id>E1</id><tool>gcp</tool><query><![CDATA[gcloud compute instances describe my-instance --zone us-central1-a]]></query><reason><![CDATA[Describe the GCE instance my-instance in zone us-central1-a to check its current state and configuration.]]></reason></step><step><id>E2</id><tool>gcp</tool><query><![CDATA[gcloud monitoring metrics list --project=YOUR_PROJECT_ID --filter='metric.type="compute.googleapis.com/instance/cpu/utilization" AND resource.labels.instance_id="my-instance"' --interval=start="$(date -u -d '3 hours ago' +%Y-%m-%dT%H:%M:%SZ)"/end="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --aggregation.alignmentPeriod=300s --aggregation.perSeriesAligner=ALIGN_MEAN]]></query><reason><![CDATA[Check Cloud Monitoring CPU utilization metrics for instance my-instance in the last 3 hours.]]></reason></step><step><id>E3</id><tool>gcp</tool><query><![CDATA[gcloud compute firewall-rules list --project=YOUR_PROJECT_ID --filter='network="default" AND targetTags.list():"my-instance-tag"']]></query><reason><![CDATA[Inspect the firewall rules associated with instance my-instance.]]></reason></step></plan></plan_response>`,
	},
	{
		Question: "Why can't I access my GCS bucket 'my-secure-bucket'?",
		Answer:   `<plan_response><thought>The user is unable to access a GCS bucket. I will check the IAM policy and ACLs for the bucket.</thought><plan><step><id>S1</id><tool>gcp</tool><query><![CDATA[gsutil iam get gs://my-secure-bucket]]></query><reason><![CDATA[Get the IAM policy for GCS bucket 'my-secure-bucket'.]]></reason></step><step><id>S2</id><tool>gcp</tool><query><![CDATA[gsutil acl get gs://my-secure-bucket]]></query><reason><![CDATA[Get the ACL for GCS bucket 'my-secure-bucket'.]]></reason></step><step><id>S3</id><tool>gcp</tool><query><![CDATA[gcloud projects get-iam-policy YOUR_PROJECT_ID --flatten='bindings[].members' --format='value(bindings.role)' --filter='bindings.members:user:YOUR_USER_EMAIL AND bindings.role:roles/storage.objectViewer']]></query><reason><![CDATA[Check IAM permissions for the user trying to access 'my-secure-bucket'.]]></reason></step></plan></plan_response>`,
	},
	{
		Question: "My Cloud Function my-function in region us-east1 is timing out.",
		Answer:   `<plan_response><thought>The user's Cloud Function is timing out. I will check the logs, function configuration, and look for errors.</thought><plan><step><id>L1</id><tool>gcp</tool><query><![CDATA[gcloud functions logs read my-function --region us-east1 --limit 10 --project YOUR_PROJECT_ID]]></query><reason><![CDATA[Get the last 10 log entries for the Cloud Function.]]></reason></step><step><id>L2</id><tool>gcp</tool><query><![CDATA[gcloud functions describe my-function --region us-east1 --project YOUR_PROJECT_ID]]></query><reason><![CDATA[Check the Cloud Function configuration, specifically timeout and memory settings.]]></reason></step><step><id>L3</id><tool>gcp</tool><query><![CDATA[gcloud logging read 'resource.type="cloud_function" resource.labels.function_name="my-function" resource.labels.region="us-east1" severity>=ERROR timestamp>="$(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%SZ)"' --project YOUR_PROJECT_ID --format json]]></query><reason><![CDATA[Query Cloud Logging for error traces.]]></reason></step></plan></plan_response>`,
	},
	{
		Question: "My Cloud Run service payment-api has high latency.",
		Answer:   `<plan_response><thought>High latency on a Cloud Run service. I should check recent logs for slow requests, error logs, and service configuration for resource pressure.</thought><plan><step><id>T1</id><tool>gcp</tool><query><![CDATA[gcloud logging read 'resource.type="cloud_run_revision" resource.labels.service_name="payment-api" httpRequest.latency>"1s" timestamp>="'"$(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%SZ)"'"' --project=MY_PROJECT --limit=20 --format=json]]></query><reason><![CDATA[Find recent slow requests for the payment-api service to identify latency bottlenecks.]]></reason></step><step><id>T2</id><tool>gcp</tool><query><![CDATA[gcloud logging read 'resource.type="cloud_run_revision" resource.labels.service_name="payment-api" severity>=WARNING timestamp>="'"$(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%SZ)"'"' --project=MY_PROJECT --limit=50 --format=json]]></query><reason><![CDATA[Check recent warning/error logs for payment-api to correlate with the latency spikes.]]></reason></step><step><id>T3</id><tool>gcp</tool><query><![CDATA[gcloud run services describe payment-api --region=us-central1 --project=MY_PROJECT]]></query><reason><![CDATA[Review Cloud Run service configuration (concurrency, memory, CPU limits) that could contribute to latency.]]></reason></step></plan></plan_response>`,
	},
}

func getGcpPlannerSupportedTools(ctx *security.RequestContext, accountId string) []toolcore.NBTool {
	supportedToolNames := []string{GcpAgentName, getTicketAgentName(), WorkflowAgentName, GithubAgentName, WebSearchAgentName, RecommendationsAgentName, EventsAgentName, VisualizationAgentName, PostgresAgentName, MySQLAgentName, MSSQLAgentName, OracleAgentName, RedisAgentName, RabbitMQAgentName, KubectlAgentName, DelegateAgentToolName}

	// shell_execute is injected automatically by FilterAndInjectDefaultTools when enabled.
	// It auto-injects cloud credentials based on account type.

	summary, err := toolcore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", AgentGcpDebugName)
	}

	tools := make([]toolcore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := toolcore.GetNBTool(accountId, toolName)
		if found {
			if !toolcore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", AgentGcpDebugName)
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
