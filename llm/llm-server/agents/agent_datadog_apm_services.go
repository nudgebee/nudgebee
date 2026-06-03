package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Get list of datadog services using natural language question. Input should be a natural language request for services data.`
	toolInput := "Provide services data question in natural language"
	toolOutput := "The tool will return services data from your question"

	core.RegisterNBAgentFactoryAndTool(AgentDatadogServiceName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogServiceAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

const AgentDatadogServiceName = "datadog_service"

type DatadogServiceAgent struct {
	accountId string
}

func NewDatadogServiceAgent(accountId string) DatadogServiceAgent {
	return DatadogServiceAgent{
		accountId: accountId,
	}
}

func (l DatadogServiceAgent) GetName() string {
	return AgentDatadogServiceName
}

func (l DatadogServiceAgent) GetNameAliases() []string {
	return []string{"Datadog Services", "APM Services", "Services"}
}

func (l DatadogServiceAgent) GetDescription() string {
	return `Retrieves service details from Datadog. Input should be a natural language question about services.`
}

func (l DatadogServiceAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific service information they need. use filters which are available, do not include any other filters which are not listed",
		"**Execute Query:** Always use the `datadog_services_execute` tool to retrieve service information.",
		"**Present Results:** Examine the results returned by the `datadog_services_execute` tool and provide a concise, markdown-formatted summary.",
		"**Error Handling:** Handle errors from `datadog_services_execute` gracefully and provide informative error messages to the user.",
		"if user is asking for all services then simply use env:* as filter",
		"**Filters:** use the following filters and do not include any other filters in the query: env (environment, use * if not known), service (name of the service), infra_type, language, type, service_health (Critical, Warning, OK), ",
	}

	constraints := []string{
		"if user is asking for all services then simply return env:* (default filter for querying serices)",
		"You MUST use the `datadog_services_execute` tool to retrieve service details.",
		"You MUST NOT answer the question without using the tools.",
		"Do not ask for any clarification from the user, try to resolve using the available tools",
	}

	toolUsage := map[string][]string{
		tools.ToolDatadogServices: {
			"Use this tool to retrieve a list of services or specific service details from Datadog.",
			"Input: datadog service query",
			"Output: JSON response containing service details from Datadog.",
		},
		core.ToolLlm: {
			"Use this tool for reasoning, generating the initial Datadog event query string, or summarizing the results.",
			"Input: A clear instruction or question.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me all services.",
			Answer:      `env:*`,
			Explanation: "for listing all serivces if env is not specified use *",
		},
		{
			Question:    "Show me all services in environment abc.",
			Answer:      `env:abc`,
			Explanation: "for listing all serivces in env abc",
		},
		{
			Question:    "Get me details of service xyz",
			Answer:      `service:xyz env:*`,
			Explanation: "Generate query for especifc service xyz",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog APM service analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "datadog",
		},
	}
}

func (p DatadogServiceAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []string{tools.ToolDatadogServices, core.ToolLlm}

	tools := make([]toolcore.NBTool, 0, len(toolsList))
	for _, toolName := range toolsList {
		if tool, ok := toolcore.GetNBTool(p.accountId, toolName); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

func (l DatadogServiceAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l DatadogServiceAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
