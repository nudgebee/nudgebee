package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const TracesDefaultAgentName = "traces_default"

// TracesDefaultAgent is the catch-all traces agent for providers that do not
// have a dedicated agent (TracesClickhouseAgent, TracesJaegerAgent, ...). It
// uses the unified Nudgebee trace JSON `where`-clause schema and routes through
// services-server, which translates the query for the configured provider
// (e.g. Dynatrace).
type TracesDefaultAgent struct {
	accountId string
	provider  services_server.ObservabilityProvider
}

func (l TracesDefaultAgent) GetName() string {
	return TracesDefaultAgentName
}

func (l TracesDefaultAgent) GetNameAliases() []string {
	return []string{"Traces"}
}

func (l TracesDefaultAgent) GetDescription() string {
	return `Returns trace data from the account's configured trace provider (e.g. Dynatrace) via services-server, based on a natural language question.`
}

func (l TracesDefaultAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	providerName := l.provider.Provider
	if providerName == "" {
		providerName = "the configured trace provider"
	}

	instructions := []string{
		fmt.Sprintf("You are a traces expert for %s.", providerName),
		fmt.Sprintf("Your goal is to answer the user's question by querying traces stored in %s.", providerName),
		"To answer user questions, you MUST use the `traces_execute_default` tool.",
		"The tool takes a JSON query in the unified Nudgebee trace schema. The services-server translates it for the underlying provider.",
		"The JSON query schema is: `{\"where\": {\"<field>\":{\"<operator>\":\"<value>\"}}}`",
		"Available fields and operators:",
		"  - **service_name** (string): The service name. Operators: `_eq`, `_in`",
		"  - **workload_name** (string): Source workload/service name. Operators: `_eq`, `_in`",
		"  - **span_name** (string): The operation/span name. Operators: `_eq`",
		"  - **trace_id** (string): A specific trace ID. Operators: `_eq`",
		"  - **duration_ns** (integer, nanoseconds): Span duration. Operators: `_gt`, `_gte`, `_lt`, `_lte`",
		"  - **http_status_code** (integer): HTTP response status code. Operators: `_eq`, `_in`",
		"  - **status_code** (string): Span status. Values: `STATUS_CODE_ERROR`, `STATUS_CODE_OK`, `STATUS_CODE_UNSET`. Operators: `_eq`",
		"  - **resource** (string): HTTP URL or DB statement. Operators: `_eq`, `_like`",
		"  - **destination_workload_name** (string): Target service that received the request. Operators: `_eq`, `_in`. MUST always be combined with service_name or workload_name.",
		"  - **destination_workload_namespace** (string): Namespace of the destination service. Operators: `_eq`. MUST always be used with destination_workload_name.",
		"**Source vs Destination:** `service_name`/`workload_name` is the source service. `destination_workload_name` is the target. When the user mentions only one service (e.g. 'get traces for llm-server'), use `service_name` only — do NOT add destination_workload_name.",
		"**Duration conversion:** 1 second = 1000000000 nanoseconds, 1 millisecond = 1000000 nanoseconds.",
		"Once you have results, provide a concise, human-readable answer.",
		"**STRICT SECURITY RULE:** You MUST NOT include the JSON query, the tool name, or any internal implementation details in your final answer to the user.",
		"**Empty Results Handling:** If the tool returns no data, state 'No traces were found for the specified criteria'. Do not expose internal queries.",
	}

	constraints := []string{
		"Only use the `traces_execute_default` tool to query trace data.",
		"Do not answer questions without using the `traces_execute_default` tool.",
		"Ensure the JSON query is valid before calling the tool.",
		"**NO QUERY LEAKAGE:** You are strictly forbidden from including any internal JSON queries or tool names in your final user-facing response.",
	}

	toolUsage := map[string][]string{
		tools.ToolGetTracesDefault: {
			fmt.Sprintf("Use this tool to query traces from %s via services-server.", providerName),
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
			Question: "show me recent 504 failures for service abc",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesDefault, Input: `{"where":{"http_status_code": {"_eq": 504}, "service_name": {"_eq": "abc"}}}`, Explanation: "Query traces for HTTP 504 errors on service abc."},
			},
		},
		{
			Question: "How many requests are taking more than 10 seconds for service abc?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesDefault, Input: `{"where": {"duration_ns": {"_gt": 10000000000}, "service_name": {"_eq": "abc"}}}`, Explanation: "Find traces over 10 seconds (10000000000 ns) for service abc."},
			},
		},
		{
			Question: "Show traces for ml-k8s-server in the last 2 hours",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesDefault, Input: `{"where": {"service_name": {"_eq": "ml-k8s-server"}}, "time_range": "2h"}`, Explanation: "Use time_range for relative time windows."},
			},
		},
		{
			Question: "show me error traces",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesDefault, Input: `{"where": {"status_code": {"_eq": "STATUS_CODE_ERROR"}}}`, Explanation: "Query for traces with error status."},
			},
		},
		{
			Question: "find trace abc123def456",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesDefault, Input: `{"where": {"trace_id": {"_eq": "abc123def456"}}}`, Explanation: "Look up a specific trace by its ID."},
			},
		},
		{
			Question: "show traces from frontend to payment-service",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesDefault, Input: `{"where":{"workload_name": {"_eq": "frontend"}, "destination_workload_name": {"_eq": "payment-service"}}}`, Explanation: "Source (frontend) → destination (payment-service)."},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "Traces expert generating JSON queries for the unified Nudgebee trace API",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Examples:     examples,
		Rag:          rag,
	}
}

func (p TracesDefaultAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolList := []toolcore.NBTool{}
	if tracesTool, ok := toolcore.GetNBTool(p.accountId, tools.ToolGetTracesDefault); ok {
		toolList = append(toolList, tracesTool)
	}
	return toolList
}

func (l TracesDefaultAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l TracesDefaultAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
