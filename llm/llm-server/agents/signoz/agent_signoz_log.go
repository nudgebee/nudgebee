package signoz

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

const SignozLogAgentName = "signoz_logs"

func init() {
	toolDescription := `Retrieves Signoz logs. Input should be a natural language question about logs.`
	toolInput := "Provide logs question in natural language"
	toolOutput := "The tool will return the Signoz logs data retrieved your query"

	core.RegisterNBAgentFactoryAndTool(SignozLogAgentName, func(accountId string) (core.NBAgent, error) {
		return NewSignozLogAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

type SignozLogAgent struct {
	accountId string
}

func NewSignozLogAgent(accountId string) SignozLogAgent {
	return SignozLogAgent{accountId: accountId}
}

func (d SignozLogAgent) GetName() string { return SignozLogAgentName }

func (d SignozLogAgent) GetNameAliases() []string { return []string{"Signoz Logs"} }

func (d SignozLogAgent) GetDescription() string {
	return `Uses Signoz to provide logs based on the given question.`
}

func (d SignozLogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific log information they need.",
		"**Generate Signoz Query:** Use the `signoz_log_query_generator` tool to generate a Signoz log query. Log query can only contain json filter or full query object",
		"**Execute Query:** Always use the `signoz_log_execute` tool to execute the generated query from `signoz_log_query_generator` tool",
		"**Present Results:** Summarize the results returned by the `signoz_log_execute` tool.",
		"**Error Handling:** Handle errors from both tools gracefully and provide informative error messages.",
	}

	constraints := []string{
		"You MUST use the `signoz_log_query_generator` tool to generate the Signoz query. Note that generated query can only contains filter, which is expected",
		"You MUST use the `signoz_log_execute` tool to execute the Signoz query generated from`signoz_log_query_generator`",
		"You MUST NOT answer the question without using the `signoz_log_query_generator` && `signoz_log_execute` tool.",
		"Do not ask for any clarification from the user, try to resolve using the available tools",
	}

	toolUsage := map[string][]string{
		SignozLogQueryAgentName: {
			"Use this tool to generate a Signoz log query or only filters in json format.",
			"Input: Natural language request for logs",
			"Output: Valid Signoz log query or filters in JSON format.",
		},
		tools.ToolSignozLogExecute: {
			"Use this tool to execute a Signoz log query.",
			"Input: A valid Signoz log query.",
			"Output: JSON response from Signoz.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Signoz log analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples: []core.NBAgentPromptExample{
			{
				Question: "Get me logs of XYZ namespace",
				Answer: `
					user - Get me logs of XYZ namespace
					agent - 
					   tool - signoz_log_query_generator
					   input - Get me logs of XYZ namespace
					   output - {"filters":[{"key":{"key":"service.name"},"value":"my-api", "op":"="},{"key":{"key":"status"},"value":"error","op":"="}]}, "startdate": "2023-10-01 12:00:00", "enddate": "2023-10-01 13:00:00"}
					agent - 
					   tool - signoz_log_execute
					   input - {"filters":[{"key":{"key":"service.name"},"value":"my-api", "op":"="},{"key":{"key":"status"},"value":"error","op":"="}]}, "startdate": "2023-10-01 12:00:00", "enddate": "2023-10-01 13:00:00"}
					   output - <Response from executing Signoz query>
					ouput - <Summary of Response from executing Signoz query>

				`,
				Explanation: "For use 'signoz_log_query_generator' to generate valid query, this returned filters in json format. and then use genrated query (json) as input of 'signoz_log_execute' to get logs. and finally summerize response",
			},
		},
	}
}

func (d SignozLogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{}
	toolsList = append(toolsList, tools.SignozExecuteTool{})

	if tool, ok := toolcore.GetNBTool(d.accountId, SignozLogQueryAgentName); ok {
		toolsList = append(toolsList, tool)
	}
	return toolsList
}

func (d SignozLogAgent) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }

func (d SignozLogAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolSignozLogExecute) {
		newRoolResponse, err := tools.GetErrorLinesFromObservabilityLogString(toolResponse)
		if err != nil {
			return toolResponse
		}
		return newRoolResponse
	} else if strings.EqualFold(toolRequest.Tool, SignozLogQueryAgentName) {
		return fmt.Sprintf(`<output>%s</output>`, toolResponse)
	}
	return toolResponse
}
