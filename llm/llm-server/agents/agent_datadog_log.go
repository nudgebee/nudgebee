package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

func init() {
	toolDescription := `Retrieves Datadog logs. Input should be a natural language question about logs.`
	toolInput := "Provide logs question in natural language"
	toolOutput := "The tool will return the datadog logs data retrieved your query"

	core.RegisterNBAgentFactoryAndTool(DatadogLogAgentName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogLogAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

const DatadogLogAgentName = "datadog_logs"

type DatadogLogAgent struct {
	accountId string
}

func NewDatadogLogAgent(accountId string) DatadogLogAgent {
	return DatadogLogAgent{accountId: accountId}
}

func (d DatadogLogAgent) GetName() string { return DatadogLogAgentName }

func (d DatadogLogAgent) GetNameAliases() []string { return []string{"Datadog Logs"} }

func (d DatadogLogAgent) GetDescription() string {
	return `Uses Datadog to provide logs based on the given question.`
}

func (d DatadogLogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific log information they need.",
		"**Generate Datadog Query:** Use the `datadog_log_query` tool to generate a Datadog log query.",
		"**Execute Query:** Always use the `datadog_log_execute` tool to execute the generated query.",
		"**Present Results:** Summarize the results returned by the `datadog_log_execute` tool.",
		"**Error Handling:** Handle errors from both tools gracefully and provide informative error messages.",
	}

	constraints := []string{
		"You MUST use the `datadog_log_query` tool to generate the Datadog query.",
		"You MUST use the `datadog_log_execute` tool to execute the Datadog query.",
		"You MUST NOT answer the question without using the tools.",
		"Do not ask for any clarification from the user, try to resolve using the available tools",
	}

	toolUsage := map[string][]string{
		DatadogLogQueryAgentName: {
			"Use this tool to generate a Datadog log query.",
			"Input: Natural language request for logs",
			"Output: Valid Datadog log query",
		},
		tools.ToolDatadogLogExecute: {
			"Use this tool to execute a Datadog log query.",
			"Input: A valid Datadog log query string (e.g. `service:my-api status:error`). Do NOT append CLI-style flags like `--start-time` / `--end-time` to the query.",
			"To specify a time window, provide a JSON object with `command`, `start_time`, and `end_time` fields: `{\"command\": \"<query>\", \"start_time\": \"<ISO8601>\", \"end_time\": \"<ISO8601>\"}`.",
			"WRONG (Datadog will reject): `host:rxtsqldev01 status:(error OR warn) --start-time 2026-04-28T03:44:44Z --end-time 2026-04-28T03:59:44Z`",
			"WRONG (executor sees JSON blob, not query): `{\"query\": \"host:rxtsqldev01 status:error\", \"start_time\": \"...\", \"end_time\": \"...\"}` — use `command`, not `query`, as the field name.",
			"RIGHT (no time window, defaults to last 1h): `host:rxtsqldev01 status:(error OR warn OR critical)`",
			"RIGHT (with time window): `{\"command\": \"host:rxtsqldev01 status:(error OR warn OR critical)\", \"start_time\": \"2026-04-28T03:44:44Z\", \"end_time\": \"2026-04-28T03:59:44Z\"}`",
			"Output: JSON response from Datadog.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog log analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
	}
}

func (d DatadogLogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	if tool, ok := toolcore.GetNBTool(d.accountId, DatadogLogQueryAgentName); ok {
		return []toolcore.NBTool{tool, tools.DatadogLogExecuteTool{}}
	}
	return []toolcore.NBTool{tools.DatadogLogExecuteTool{}}
}

func (d DatadogLogAgent) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }

func (d DatadogLogAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolDatadogLogExecute) {
		newRoolResponse, err := tools.GetErrorLinesFromObservabilityLogString(toolResponse)
		if err != nil {
			return toolResponse
		}
		return newRoolResponse
	} else if strings.EqualFold(toolRequest.Tool, DatadogLogQueryAgentName) {
		return fmt.Sprintf(`<output>%s</output>`, toolResponse)
	}
	return toolResponse
}
