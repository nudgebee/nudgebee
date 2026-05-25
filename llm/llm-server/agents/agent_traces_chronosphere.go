package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

const TracesChronosphereAgentName = "traces_chronosphere"

type TracesChronosphereAgent struct {
	accountId string
}

func (l TracesChronosphereAgent) GetName() string {
	return TracesChronosphereAgentName
}

func (l TracesChronosphereAgent) GetNameAliases() []string {
	return []string{"Traces Chronosphere"}
}

func (l TracesChronosphereAgent) GetDescription() string {
	return `Returns traces data based on natural language question.`
}

func (l TracesChronosphereAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"You are a Chronosphere traces expert.",
		"Your primary goal is to help users by answering their questions about traces.",
		"To answer user questions, you MUST use the `traces_execute_chronosphere` tool.",
		"The `traces_execute_chronosphere` tool takes a JSON query to filter traces.",
		"The JSON query schema is: `{\"where\": {\"<field>\":{\"<operator>\":\"<value>\"}}}`",
		"Here are the available fields and operators:",
		"  - **Fields**: `service_name`, `trace_id`, `span_id`, `component`, `controller`, `controller.action`, `deployment.environment`, `k8s_cluster`, `http.host`, `http.method`, `http.status_code` (integer), `http.url`, `request.format`, `process.command`",
		"  - **Operators**: `_eq`, `_neq`, `_gt`, `_gte`, `_lt`, `_lte`, `_in`, `_nin`",
		"Once you have the results from the `traces_execute_chronosphere` tool, provide a concise, human-readable answer to the user's question.",
		"**STRICT SECURITY RULE:** You MUST NOT include the JSON query, the tool name, or any internal implementation details in your final answer to the user.",
		"**Empty Results Handling:** If the tool returns no data or an empty set, simply state 'No traces were found for the specified criteria' or a similar user-friendly message. Do NOT explain the query steps or show the JSON query used.",
	}

	constraints := []string{
		"Only use the `traces_execute_chronosphere` tool to query trace data.",
		"Do not answer questions without using the `traces_execute_chronosphere` tool.",
		"Ensure the JSON query is valid before using the `traces_execute_chronosphere` tool.",
		"**NO QUERY LEAKAGE:** You are strictly forbidden from including any internal JSON queries or tool names in your final user-facing response.",
	}

	toolUsage := map[string][]string{
		tools.ToolGetTracesChronosphere: {
			"Use this tool to query traces data.",
			"Input: a valid JSON query with a 'where' clause. Optional: start_time, end_time, or range (e.g. '2d', '1w', '1mo' for months).",
			"Output: the data returned by the query.",
		},
	}

	outputFormat := "A clear and concise summary of the query results."
	rag := core.NBAgentPromptRag{
		Module: "traces",
		Format: core.NBAgentPromptRagFormatJson,
	}

	examples := []core.NBAgentPromptExample{
		{
			Question: "show me recent 504 failures for services abc?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesChronosphere, Input: `{"where":{"http.status_code": {"_eq": 504}, "service_name": {"_eq": "abc"}}}`, Explanation: "I need to query Chronosphere for HTTP 504 errors on service abc."},
			},
		},
		{
			Question: "How many apis are taking more than 10seconds for service abc?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesChronosphere, Input: `{"where": {"duration_ns": {"_gt": 10000000000}, "service_name": {"_eq": "abc"}}}`, Explanation: "I need to find traces with duration exceeding 10 seconds (10000000000 ns) for service abc."},
			},
		},
		{
			Question: "Get Recent Api Failures on services-server?",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesChronosphere, Input: `{"where": {"service_name": {"_eq": "services-server"}, "http.status_code": {"_gte": 500}}}`, Explanation: "I need to query Chronosphere for HTTP 5xx errors on services-server."},
			},
		},
		{
			Question: "Show me traces from the last 2 hours for ml-k8s-server",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesChronosphere, Input: `{"where": {"service_name": {"_eq": "ml-k8s-server"}}, "time_range": "2h"}`, Explanation: "I need to query Chronosphere for ml-k8s-server traces with a 2-hour time range."},
			},
		},
		{
			Question: "get traces of llm server",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesChronosphere, Input: `{"where": {"service_name": {"_eq": "llm-server"}}}`, Explanation: "I need to query Chronosphere for traces from llm-server."},
			},
		},
		{
			Question: "get traces of llm server after 2025-01-01",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolGetTracesChronosphere, Input: `{"where": {"service_name": {"_eq": "llm-server"}}, "start_time": "2025-01-01T00:00:00Z"}`, Explanation: "I need to query Chronosphere for llm-server traces after 2025-01-01."},
			},
		},
	}

	return core.NBAgentPrompt{
		Role:         "Traces expert in generating JSON query for traces_execute_chronosphere tool",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		OutputFormat: outputFormat,
		Examples:     examples,
		Rag:          rag,
	}
}

func (p TracesChronosphereAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolList := []toolcore.NBTool{}
	if tracesTool, ok := toolcore.GetNBTool(p.accountId, tools.ToolGetTracesChronosphere); ok {
		toolList = append(toolList, tracesTool)
	}
	return toolList
}

func (l TracesChronosphereAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l TracesChronosphereAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
