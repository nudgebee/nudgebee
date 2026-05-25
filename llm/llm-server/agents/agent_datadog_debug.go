package agents

import (
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const (
	AgentDatadogDebugName = "datadog_debug"
)

func init() {
	core.RegisterNBAgentFactoryAndTool(AgentDatadogDebugName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogDebugAgent(accountId), nil
	}, "An agent specialized in troubleshooting and debugging issues using Datadog Observability tool", "Provide a natural language query describing the Issue to be debugged.", "Markdown response with debugging steps and findings.")
}

type DatadogDebugAgent struct {
	accountId string
}

func NewDatadogDebugAgent(accountId string) core.NBAgent {
	return &DatadogDebugAgent{
		accountId: accountId,
	}
}

func (a *DatadogDebugAgent) GetName() string {
	return AgentDatadogDebugName
}

func (a *DatadogDebugAgent) GetNameAliases() []string {
	return []string{"datadog debug", "dd_debug"}
}

func (a *DatadogDebugAgent) GetDescription() string {
	return "An agent specialized in troubleshooting and debugging issues within Datadog environments, providing step-by-step XML plans."
}

func (a *DatadogDebugAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return getDatadogPlannerSupportedTools(ctx, a.accountId)
}

func (a *DatadogDebugAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReWoo
}

func (a *DatadogDebugAgent) GetCacheScope() core.CacheScope {
	return core.CacheScopeAccount
}

func (a *DatadogDebugAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	toolUsage := map[string][]string{
		DatadogLogAgentName: {
			"Use this tool to search Datadog logs based on a natural language query.",
			"Input: A natural language question about logs you want to retrieve from Datadog.",
			`Example query for this tool: "Find error logs for service my-api in the last hour"`,
		},
		DatadogMetricsAgentName: {
			"Use this tool to query Datadog metrics based on a natural language query.",
			"Input: A natural language question about metrics you want to retrieve from Datadog.",
			`Example query for this tool: "What is the 95th percentile of request latency for service my-web-app over the last 30 minutes?"`,
		},
		DatadogTracesAgentName: {
			"Use this tool to query Datadog traces based on a natural language query.",
			"Input: A natural language question about traces you want to retrieve from Datadog. Specify time range if needed (e.g., 'last 15 minutes', 'last hour').",
			`Example query for this tool: "Show me error traces for service:payment-service in the last 15 minutes."`,
		},
		DatadogEventsAgentName: {
			"Use this tool to query the Datadog event stream.",
			"Input: A natural language question about events you want to retrieve from Datadog (e.g., deployments, monitor alerts).",
			`Example query for this tool: "Show me all events with priority:normal from the kubernetes source in the last hour."`,
		},
		DatadogContainersAgentName: {
			"Use this tool to query Datadog for container, pod, node, or workload information.",
			"Input: A natural language question about containers, pods, nodes, or workloads you want to retrieve from Datadog.",
			`Example query for this tool: "Get me all pods in namespace 'default' that are not running."`,
		},
		DatadogIncidentAgentName: {
			"Use this tool to query Datadog for incident information.",
			"Input: A natural language question about incidents you want to retrieve from Datadog.",
			`Example query for this tool: "Get me all incidents with severity 'critical'."`,
		},
		AgentDatadogSoftwareCatalogName: {
			"Use this tool to query Datadog for software catalog information.",
			"Input: A natural language question about software catalog you want to retrieve from Datadog.",
		},
		AgentDatadogHostsName: {
			"Use this tool to query Datadog for host information.",
			"Input: A natural language question about hosts you want to retrieve from Datadog.",
			`Example query for this tool: "Get me all the hosts"`,
		},
		AgentDatadogServiceName: {
			"Use this tool to query Datadog for service related information.",
			"Input: A natural language question about services you want to retrieve from Datadog.",
			`Example query for this tool: "Get me all the services"`,
		},
	}

	return core.NBAgentPrompt{
		Role: "You are a super-intelligent AI that helps engineers troubleshoot and debug issues in Datadog environments. Your role is to act as a seasoned Datadog expert, guiding users to create XML-based execution plans.",
		Instructions: []string{
			"When a user asks a question, your goal is to create a plan as an XML document to help them solve the problem.",
			"Each <step> in the XML plan represents a step and MUST contain 'id', 'tool', 'query', and 'reason' fields.",
			"The 'id' field should be a unique string identifier for the step (e.g., 'M1', 'L2').",
			"The 'tool' field must specify the name of the tool to be used for that step (e.g., 'datadog_metrics', 'datadog_logs').",
			"The 'query' field must contain the specific natural language input or question for the specified tool.",
			"The 'reason' field should be a concise, single-sentence description of what this step aims to achieve.",
			"Optionally, a step can include a 'dependency' field, which is an array of 'id's of previous steps that this step depends on.",
			"You must identify the specific Datadog entities involved (e.g., service names, host names, monitor IDs, dashboard names, log queries, metric queries).",
			"If the user's query is unclear or lacks specific details (e.g., a specific service name when multiple might exist, or a time range), the plan should include steps to gather this necessary information or make reasonable assumptions and state them in the plan description.",
			"When dealing with metrics or logs, always consider the time range. If not specified, assume a recent time range (e.g., last 15 minutes, last hour) and state it in the plan.",
		},
		Constraints: []string{
			"Your response MUST be a valid XML plan. Do NOT include any text, explanation, or markdown formatting outside the XML structure. The entire response should be the XML plan itself.",
			"Each <step> in the XML plan MUST contain 'id', 'tool', 'query', and 'reason' fields.",
			"The 'tool' field must be one of the tools listed in the 'Instructions on tool usage' section.",
			"Only use the tools provided. Do not make up tools.",
			"Ensure that the generated plan is relevant to Datadog and uses Datadog-specific terminology and concepts.",
			"The 'query' for 'datadog_logs', 'datadog_metrics', 'datadog_traces', or 'datadog_events' tools should be a natural language question that these tools can understand to fetch data.",
		},
		ToolUsage: toolUsage,
		Examples: []core.NBAgentPromptExample{
			{
				Question: "My service 'cart-service' is showing a lot of errors in the 'prod' environment.",
				Answer:   `<plan_response><thought>The user's service is showing a lot of errors. I will gather error rate metrics and logs to identify the cause.</thought><plan><step><id>M1</id><tool>datadog_metrics</tool><query><![CDATA[What is the error rate for service:cart-service env:prod over the last 30 minutes?]]></query><reason><![CDATA[Check the error rate metric for 'cart-service' in 'prod' environment for the last 30 minutes using Datadog metrics.]]></reason></step><step><id>L1</id><tool>datadog_logs</tool><query><![CDATA[Show me error logs for service:cart-service env:prod from the last 30 minutes.]]></query><reason><![CDATA[Fetch error logs for 'cart-service' in 'prod' environment for the last 30 minutes from Datadog logs.]]></reason></step></plan></plan_response>`,
			},
			{
				Question: "CPU usage is high on host 'db-server-01'. What's going on?",
				Answer:   `<plan_response><thought>The user is reporting high CPU usage on a host. I will retrieve CPU metrics, identify top processes, and collect related logs to diagnose the issue.</thought><plan><step><id>CPU_METRIC</id><tool>datadog_metrics</tool><query><![CDATA[Get CPU utilization for host:db-server-01 for the last hour.]]></query><reason><![CDATA[Retrieve CPU utilization metrics for host 'db-server-01' for the past hour from Datadog.]]></reason></step><step><id>TOP_PROCESSES</id><tool>datadog_metrics</tool><query><![CDATA[What are the top CPU consuming processes on host:db-server-01 for the last hour?]]></query><reason><![CDATA[Identify the top CPU consuming processes on 'db-server-01' using Datadog metrics for the past hour.]]></reason></step><step><id>RELATED_LOGS</id><tool>datadog_logs</tool><query><![CDATA[Search for logs from host:db-server-01 with severity ERROR or WARN in the last hour.]]></query><reason><![CDATA[Fetch error or warning logs from 'db-server-01' in the last hour via Datadog logs.]]></reason></step></plan></plan_response>`,
			},
			{
				Question: "A pod in my 'production' namespace is crash-looping. Can you find out why?",
				Answer:   `<plan_response><thought>A pod is crash-looping. I need to identify the pod and collect its logs and events to determine the root cause.</thought><plan><step><id>C1</id><tool>datadog_containers</tool><query><![CDATA[Get all containers in namespace 'production' that are not in a running state.]]></query><reason><![CDATA[Identify non-running containers in the 'production' namespace to find the crash-looping pod.]]></reason></step><step><id>L1</id><tool>datadog_logs</tool><query><![CDATA[Based on the pod name from <OUTPUT_OF_C1>, show me the logs for that pod from the last 15 minutes.]]></query><reason><![CDATA[Fetch recent logs for the identified crash-looping pod to see error messages.]]></reason></step><step><id>E1</id><tool>datadog_events</tool><query><![CDATA[Based on the pod name from <OUTPUT_OF_C1>, show me all Kubernetes events for that pod from the last hour.]]></query><reason><![CDATA[Check for Kubernetes events like OOMKilled or other warnings related to the pod.]]></reason></step></plan></plan_response>`,
			},
		},
		Rag: core.NBAgentPromptRag{
			Module:      "planner",
			Records:     3,
			Format:      core.NBAgentPromptRagFormatString,
			QuestionKey: "Question",
			AnswerKey:   "Answer",
		},
		OutputFormat: `Your response MUST be a valid XML plan. The plan MUST start with a <plan_response> tag, followed by a tag containing <thought> and <plan> steps. Each <step> must have an <id>, <tool>, <query>, and <reason>. Do not include any text outside the main XML structure. For example:
<plan_response>
<thought>The user wants to check CPU utilization. I will use the datadog_metrics tool for this.</thought>
  <plan>
    <step>
      <id>M1</id>
      <tool>datadog_metrics</tool>
      <query><![CDATA[Average CPU utilization for service:auth-service last 15 minutes]]></query>
      <reason><![CDATA[Get average CPU utilization for auth-service for the last 15 minutes.]]></reason>
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
`,
	}
}

func getDatadogPlannerSupportedTools(ctx *security.RequestContext, accountId string) []toolcore.NBTool {
	// DatadogAgentName ("datadog") is for logs, DatadogMetricsAgentName ("datadog_metrics") is for metrics.
	// These are the names of other agents that this planner can use as tools.
	supportedToolNames := []string{
		DatadogLogAgentName,             // For logs
		DatadogMetricsAgentName,         // For metrics
		DatadogEventsAgentName,          // For Events
		DatadogContainersAgentName,      // For Containers, Pods, Nodes, Workloads
		DatadogTracesAgentName,          // For Traces
		DatadogIncidentAgentName,        // For Incidents
		AgentDatadogHostsName,           // For Hosts
		AgentDatadogSoftwareCatalogName, // For Software Catalog
		getTicketAgentName(),            // For JIRA/Ticketing
		WorkflowAgentName,               // For Workflows
		GithubAgentName,                 // For Github
		WebSearchAgentName,              // For Web Search
		RecommendationsAgentName,        // For Recommendations
		EventsAgentName,
		PostgresAgentName,     // For PostgreSQL
		MySQLAgentName,        // For MySQL
		MSSQLAgentName,        // For MSSQL
		OracleAgentName,       // For Oracle
		RedisAgentName,        // For Redis
		RabbitMQAgentName,     // For RabbitMQ
		DelegateAgentToolName, // For dynamic specialist sub-agents
	}

	summary, err := toolcore.GetAccountConfigSummary(ctx, accountId)
	if err != nil {
		slog.Error("agent: failed to get account config summary", "error", err, "agent", AgentDatadogDebugName)
	}

	tools := make([]toolcore.NBTool, 0, len(supportedToolNames))
	for _, toolName := range supportedToolNames {
		tool, found := toolcore.GetNBTool(accountId, toolName)
		if found {
			if !toolcore.IsToolConfigured(ctx, accountId, tool, summary) {
				slog.Warn("skipping tool as not configured", "tool", tool.Name(), "agent", AgentDatadogDebugName)
				continue
			}
			tools = append(tools, tool)
		} else {
			slog.Warn("Datadog Debug Planner: Tool not found in registry", "toolName", toolName, "accountId", accountId)
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
