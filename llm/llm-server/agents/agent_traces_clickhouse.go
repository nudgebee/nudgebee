package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const TracesClickhouseAgentName = "traces_clickhouse"

type TracesClickhouseAgent struct {
	accountId string
}

func (l TracesClickhouseAgent) GetName() string {
	return TracesClickhouseAgentName
}

func (l TracesClickhouseAgent) GetNameAliases() []string {
	return []string{"Traces"}
}

func (l TracesClickhouseAgent) GetDescription() string {
	return `Returns trace data based on natural language question.`
}

func (l TracesClickhouseAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Understand the Question:** Carefully analyze the user's question to identify the information they need from the trace data.",
		"**Use Traces Execute Tool:** Always use the 'traces_execute' tool to query the data. Do not attempt to answer questions without using this tool.",
		"**Construct SQL Query:** Generate a valid SQL query against the 'traces_view' view.",
		"**Filtering:** Correctly identify and apply filtering criteria (e.g., http_status_code, duration_ns, workload_name) to narrow down the results.",
		"**Datetime Handling:** When filtering by time in ClickHouse, use direct comparison with NOW() and interval functions. For example: timestamp >= (NOW() - toIntervalHour(2)). When doing arithmetic with intervals on string timestamps, ALWAYS wrap the string in parseDateTimeBestEffort() first: parseDateTimeBestEffort('2023-01-01') - toIntervalMinute(5). Never apply interval arithmetic directly to string timestamps.",
		"**Ordering and Limiting:** Order the results by 'timestamp' in descending order to get the most recent traces. Limit the number of results to a reasonable amount (e.g., 20) unless otherwise specified.",
		"**Answer from Results:** Examine the results returned by the 'traces_execute' tool and provide a concise, human-readable answer. **STRICT SECURITY RULE:** You MUST NOT include the SQL query, the tool name, or any internal implementation details in your final answer to the user.",
		"**Empty Results Handling:** If the tool returns no data or an empty set, simply state 'No traces were found for the specified criteria' or a similar user-friendly message. Do NOT explain the query steps or show the SQL used.",
		"**Numeric Representation:** Use human-readable numbers when appropriate.",
		"**Summarization:** Summarize each row of the query response to provide context.",
		"**Verify Tool Usage:** Before providing the final answer, ensure that you have used the 'traces_execute' tool correctly. **CRITICAL:** Ensure your final user-facing response does NOT contain the thought process or the raw SQL query.",
		"**Resource Discovery - Use resource_search tool for:**",
		"   - Resource type typos: {\"resource_type\": \"podss\", \"search_type\": \"fuzzy\"}",
		"   - Namespace typos: {\"namespace\": \"nudgebe\", \"search_type\": \"namespace\"}",
		"   - App/workload searches: {\"resource_name\": \"llm-server\", \"namespace\": \"default\", \"search_type\": \"suggestions\"}",
		"   - When kubectl returns 'not found': Use search tool before manual grep attempts",
		"**Always try resource_search when:**",
		"   - User mentions app/service names without exact k8s resource details",
		"   - User says 'deployment' (could be Deployment, StatefulSet, or DaemonSet)",
		"   - User says 'app' or 'workload' (search returns all workload types + pods)",
		"   - kubectl commands fail with 'not found' or 'no resources found'",
		"   - Resource names seem ambiguous or could have variations",
	}

	constraints := []string{
		"You are a ClickHouse SQL expert, especially skilled in querying our internal 'traces_view' schema.",
		"You MUST ONLY use the 'traces_execute' tool to interact with the 'traces_view' data.",
		"You MUST NOT answer questions without first using the 'traces_execute' tool to query the database.",
		"You must generate the SQL query using ClickHouse syntax.",
		"You MUST use ilike for string matching of workload_name, destination_workload_name and endpoint fields for better coverage.",
		"**IMPORTANT: Use resource_search tool proactively** - Don't wait for kubectl to fail first",
		"**When in doubt about resource names:** Always use resource_search tool to find exact matches",
		"**Never guess resource names:** Use resource_search tool to get accurate suggestions",
		"**NO SQL LEAKAGE:** You are strictly forbidden from including any SQL queries, database names, or view names (like 'traces_view') in your final user-facing response.",
	}

	toolUsage := map[string][]string{
		tools.ToolGetTracesClickhouse: {
			"Use this tool to execute SQL queries against the 'traces_view' view.",
			"Always use this tool to get trace data.",
			"Input: valid sql query",
			"Output: the data returned by the sql query.",
		},
		ResourceSearchAgentName: {
			"Use this tool for fuzzy resource matching and generating search strategies when resources are not found.",
			"Input: JSON with search_type ('fuzzy', 'suggestions', 'namespace'), resource_name, resource_type, namespace",
			"Output: JSON with suggestions and search strategies",
			"Examples: {\"resource_type\": \"podss\", \"search_type\": \"fuzzy\"} or {\"resource_name\": \"llm-server\", \"namespace\": \"default\", \"search_type\": \"suggestions\"}",
		},
	}
	outputFormat := "The output should be a clear and concise summary of the query results."
	rag := core.NBAgentPromptRag{
		Module: "traces",
		Format: core.NBAgentPromptRagFormatJson,
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "show me recent 504 failures?",
			Answer:      "SELECT * FROM traces_view where http_status_code = 504 order by timestamp desc limit 20",
			Explanation: "We order by timestamp to get the latest traces & by default restrict to 20 records and using http_status code to compare statuses",
		},
		{
			Question:    "How many apis are taking more than 10seconds ?",
			Answer:      "SELECT count(*) AS count FROM traces_view WHERE (duration_ns >= 10000) order by timestamp desc limit 20",
			Explanation: "duration_ns is used to filter based on response time.",
		},
		{
			Question:    "Get Recent Api Failures on services-server?",
			Answer:      "SELECT * FROM traces_view WHERE http_status_code in (504, 500, 503) and destination_workload_name ilike 'services-server%' order by timestamp desc limit 20",
			Explanation: "'services-server' is identified as destination_workload_name and using like query",
		},
		{
			Question:    "Show me traces from the last 2 hours for ml-k8s-server",
			Answer:      "SELECT * FROM traces_view WHERE destination_workload_name ilike 'ml-k8s-server%' AND destination_workload_namespace = 'nudgebee' AND timestamp >= (NOW() - toIntervalHour(2)) ORDER BY timestamp DESC LIMIT 20",
			Explanation: "We're filtering for a specific workload name and using the correct ClickHouse syntax for datetime comparison with toIntervalHour()",
		},
		{
			Question:    "get traces of llm server",
			Answer:      "First use resource_search tool with {\"resource_name\": \"llm server\", \"search_type\": \"suggestions\"} to find the exact pod name and namespace",
			Explanation: "Resource name 'llm server' is ambiguous - need to use resource_search tool to find the actual pod name and namespace before running traces query",
		},
		{
			Question:    "get traces of llm server after 2025-01-01",
			Answer:      "SELECT * FROM traces_view WHERE destination_workload_name ilike 'llm-server%' AND timestamp >= (parseDateTimeBestEffort('2025-01-01')) ORDER BY timestamp DESC LIMIT 20",
			Explanation: "As timestamp(2025-01-01) is provided as string, i will use parseDateTimeBestEffort function to convert string to datetime",
		},
		{
			Question:    "get traces of llm server in nudgebee namespace",
			Answer:      "SELECT * FROM traces_view WHERE destination_workload_name ilike 'llm-server%' AND destination_workload_namespace = 'nudgebee' ORDER BY timestamp DESC LIMIT 20",
			Explanation: "We're filtering for a specific workload name and namespace.",
		},
		{
			Question:    "get traces for llm-server of 'v1/chat' endpoint",
			Answer:      "SELECT * FROM traces_view WHERE destination_workload_name='llm-server' AND endpoint ilike '%/v1/chat' ORDER BY timestamp DESC LIMIT 20",
			Explanation: "We're filtering for a specific workload name and endpoint. endpoint uses like query",
		},
		{
			Question:    "get traces for llm-server in nudgebee namespace of 'v1/promql-query' endpoint",
			Answer:      "SELECT * FROM traces_view WHERE destination_workload_name='llm-server' AND destination_workload_namespace = 'nudgebee' AND endpoint ilike '%/v1/promql-query' ORDER BY timestamp DESC LIMIT 20",
			Explanation: "We're filtering for a specific workload name , namespace and endpoint. endpoint uses like query",
		},
	}

	schema := []string{
		"**traces_view:** This view contains information about traces.",
		"timestamp = Request Time (DateTime type). For time-based filtering with ClickHouse, use direct comparison with date functions. For relative time ranges, use: timestamp >= (NOW() - toIntervalHour(2)). For fixed timestamps, use: timestamp BETWEEN '2023-01-01 00:00:00' AND '2023-01-01 01:00:00'. IMPORTANT: When performing arithmetic operations (+ or -) with intervals on string timestamps, you MUST wrap the string in parseDateTimeBestEffort() first. Example: parseDateTimeBestEffort('2023-01-01T00:00:00Z') - toIntervalMinute(5). Never use interval arithmetic directly on string timestamps.",
		"workload_name = Source Workload Name, Use like for better coverage",
		"workload_namespace = Source Workload Namespace",
		"trace_id = Unique identifier for the trace",
		"span_id = Unique identifier for the span",
		"parent_span_id = Identifier of the parent span",
		"trace_state = Trace state information",
		"span_kind = Kind of span (e.g., SERVER, CLIENT)",
		"duration_ns = time taken to process request in nanoseconds",
		"resource = Full URL path with hostname of Request",
		"endpoint = API endpoint of request or db query or function call, Must use ilike to match endpoints",
		"destination_workload_namespace = Destination Workload Namespace",
		"destination_name = Destination Name",
		"destination_workload_name = Destination Workload Name, Use like for better coverage",
		"http_status_code = numeric http status code of API request, EXAMPLE 200, 404. Never use quotes while comparing numeric values",
	}

	return core.NBAgentPrompt{
		Role:         "a ClickHouse SQL expert",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Examples:     examples,
		Rag:          rag,
		Schema:       schema,
	}
}

func (p TracesClickhouseAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolList := []toolcore.NBTool{tools.TracesExecuteClickhouseTool{}}
	if resourceSearchTool, ok := toolcore.GetNBTool(p.accountId, ResourceSearchAgentName); ok {
		toolList = append(toolList, resourceSearchTool)
	}
	return toolList
}

func (l TracesClickhouseAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l TracesClickhouseAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
