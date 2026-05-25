package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const TracesJaegerAgentName = "traces_jaeger"

type TracesJaegerAgent struct {
	accountId string
}

func (l TracesJaegerAgent) GetName() string {
	return TracesJaegerAgentName
}

func (l TracesJaegerAgent) GetNameAliases() []string {
	return []string{"Traces Jaeger"}
}

func (l TracesJaegerAgent) GetDescription() string {
	return `Returns traces data from Jaeger based on natural language question.`
}

func (l TracesJaegerAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"You are a Jaeger traces expert.",
		"Your primary goal is to help users by answering their questions about traces stored in Jaeger.",
		"To answer user questions, you MUST use the `traces_execute_jaeger` tool.",
		"The `traces_execute_jaeger` tool takes a JSON query to filter traces.",
		"The JSON query schema is: `{\"where\": {\"<field>\":{\"<operator>\":\"<value>\"}}}`",
		"Here are the available fields and operators:",
		"  - **service_name** (string): The service name. Operators: `_eq`, `_in`",
		"  - **workload_name** (string): Alias for service name. Operators: `_eq`, `_in`",
		"  - **span_name** (string): The operation/span name. Operators: `_eq`",
		"  - **trace_id** (string): A specific trace ID to look up. Operators: `_eq`",
		"  - **duration_ns** (integer, nanoseconds): Span duration. Operators: `_gt`, `_gte`, `_lt`, `_lte`",
		"  - **http_status_code** (integer): HTTP response status code. Operators: `_eq`, `_in`",
		"  - **status_code** (string): Span status. Values: `STATUS_CODE_ERROR`, `STATUS_CODE_OK`, `STATUS_CODE_UNSET`. Operators: `_eq`",
		"  - **resource** (string): HTTP URL or DB statement. Operators: `_eq`, `_like`",
		"  - **destination_workload_name** (string): The destination/target service that received the request. Operators: `_eq`, `_in`. MUST always be used together with service_name or workload_name.",
		"  - **destination_workload_namespace** (string): Namespace of the destination service. Operators: `_eq`. MUST always be used together with destination_workload_name.",
		"**Source vs Destination:** `service_name`/`workload_name` is the source service that initiated the call. `destination_workload_name` is the target service that received the call. When a user mentions only one service (e.g. 'get traces for llm-server'), use `service_name` only — do NOT add destination_workload_name. Only use `destination_workload_name` when the user explicitly mentions BOTH a source AND a destination.",
		"**Duration conversion:** 1 second = 1000000000 nanoseconds, 1 millisecond = 1000000 nanoseconds.",
		"Once you have the results from the `traces_execute_jaeger` tool, provide a concise, human-readable answer to the user's question.",
		"**STRICT SECURITY RULE:** You MUST NOT include the JSON query, the tool name, or any internal implementation details in your final answer to the user.",
		"**Empty Results Handling:** If the tool returns no data or an empty set: (1) If the query included destination_workload_name, inform the user that no traces matched the destination filter — suggest verifying the destination service name or trying without the destination filter. (2) Otherwise, state 'No traces were found for the specified criteria'. Do NOT expose the JSON query or tool name.",
	}

	constraints := []string{
		"Only use the `traces_execute_jaeger` tool to query trace data.",
		"Do not answer questions without using the `traces_execute_jaeger` tool.",
		"Ensure the JSON query is valid before using the `traces_execute_jaeger` tool.",
		"**NO QUERY LEAKAGE:** You are strictly forbidden from including any internal JSON queries or tool names in your final user-facing response.",
	}

	toolUsage := map[string][]string{
		tools.ToolGetTracesJaeger: {
			"Use this tool to query traces data from Jaeger.",
			"Input: a valid JSON query with a 'where' clause. Optional: start_time, end_time, or range (e.g. '2d', '1w', '1h').",
			"Output: the trace data returned by the query.",
		},
	}

	outputFormat := "A clear and concise summary of the query results."
	rag := core.NBAgentPromptRag{
		Module: "traces",
		Format: core.NBAgentPromptRagFormatJson,
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "show me recent 504 failures for service abc?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where":{"http_status_code": {"_eq": 504}, "service_name": {"_eq": "abc"}}}`, Explanation: "I need to query Jaeger for HTTP 504 errors on service abc."},
			},
		},
		{
			Question: "How many requests are taking more than 10 seconds for service abc?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"duration_ns": {"_gt": 10000000000}, "service_name": {"_eq": "abc"}}}`, Explanation: "I need to find traces with duration exceeding 10 seconds (10000000000 ns) for service abc."},
			},
		},
		{
			Question: "Get Recent API Failures on services-server?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"service_name": {"_eq": "services-server"}, "http_status_code": {"_gte": 500}}}`, Explanation: "I need to query Jaeger for HTTP 5xx errors on services-server."},
			},
		},
		{
			Question: "Show me traces from the last 2 hours for ml-k8s-server",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"service_name": {"_eq": "ml-k8s-server"}}, "time_range": "2h"}`, Explanation: "I need to query Jaeger for ml-k8s-server traces with a 2-hour time range."},
			},
		},
		{
			Question: "get traces of llm server",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"service_name": {"_eq": "llm-server"}}}`, Explanation: "I need to query Jaeger for traces from llm-server."},
			},
		},
		{
			Question: "show me error traces",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"status_code": {"_eq": "STATUS_CODE_ERROR"}}}`, Explanation: "I need to query Jaeger for all traces with error status."},
			},
		},
		{
			Question: "find trace abc123def456",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"trace_id": {"_eq": "abc123def456"}}}`, Explanation: "I need to look up a specific trace by its ID."},
			},
		},
		{
			Question: "show slow requests over 5 seconds for payment-service",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where": {"duration_ns": {"_gte": 5000000000}, "service_name": {"_eq": "payment-service"}}}`, Explanation: "I need to find traces with duration >= 5 seconds for payment-service."},
			},
		},
		{
			Question: "show traces for cart workload from frontend",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where":{"workload_name": {"_eq": "frontend"}, "destination_workload_name": {"_eq": "cart"}}}`, Explanation: "The user wants traces from frontend (source) to cart (destination), so I use workload_name for the source and destination_workload_name for the target."},
			},
		},
		{
			Question: "show traces from frontend to payment-service",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesJaeger, Input: `{"where":{"workload_name": {"_eq": "frontend"}, "destination_workload_name": {"_eq": "payment-service"}}}`, Explanation: "The user explicitly mentions source (frontend) and destination (payment-service)."},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "Traces expert in generating JSON query for traces_execute_jaeger tool",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Examples:     examples,
		Rag:          rag,
	}
}

func (p TracesJaegerAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolList := []toolcore.NBTool{}
	if tracesTool, ok := toolcore.GetNBTool(p.accountId, tools.ToolGetTracesJaeger); ok {
		toolList = append(toolList, tracesTool)
	}
	return toolList
}

func (l TracesJaegerAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l TracesJaegerAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
