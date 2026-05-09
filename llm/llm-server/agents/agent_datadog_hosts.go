package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Get list of datadog hosts list using natural language question. Input should be a natural language request for hosts data.`
	toolInput := "Provide hosts data question in natural language"
	toolOutput := "The tool will return hosts data from your question"

	core.RegisterNBAgentFactoryAndTool(AgentDatadogHostsName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogHostsAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

const AgentDatadogHostsName = "datadog_hosts"

type DatadogHostsAgent struct {
	accountId string
}

func NewDatadogHostsAgent(accountId string) DatadogHostsAgent {
	return DatadogHostsAgent{
		accountId: accountId,
	}
}

func (l DatadogHostsAgent) GetName() string {
	return AgentDatadogHostsName
}

func (l DatadogHostsAgent) GetNameAliases() []string {
	return []string{"Datadog Hosts"}
}

func (l DatadogHostsAgent) GetDescription() string {
	return `Retrieves host details from Datadog. Input should be a natural language question about hosts.`
}

func (l DatadogHostsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific host information they need.",
		"**Execute Query:** Always use the `datadog_hosts_execute` tool to retrieve host details.",
		"**Present Results:** Examine the results returned by the `datadog_hosts_execute` tool and provide a concise, markdown-formatted summary.",
		"**Error Handling:** Handle errors from `datadog_hosts_execute` gracefully and provide informative error messages to the user.",
		"**Filters:** Use fields like `host` (generic, works across), kube_node, for aws - aws_account, cloud_provider, region",
	}

	constraints := []string{
		"You MUST use the `datadog_hosts_execute` tool to retrieve host details.",
		"You MUST NOT answer the question without using the tools.",
		"Do not ask for any clarification from the user, try to resolve using the available tools",
	}

	toolUsage := map[string][]string{
		tools.ToolDatadogHosts: {
			"Use this tool to retrieve a list of hosts or specific host details from Datadog.",
			"Input: datadog host query",
			"Output: JSON response containing host details from Datadog.",
		},
		core.ToolLlm: {
			"Use this tool for reasoning, generating the initial Datadog event query string, or summarizing the results.",
			"Input: A clear instruction or question.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me all nodes.",
			Answer:      ``,
			Explanation: "for listing all nodes we dont need to generate empty query",
		},
		{
			Question:    "Get me details of node xyz",
			Answer:      `host:xyz`,
			Explanation: "Generate query for especifc Host xyz",
		},
		{
			Question:    "Get me all hosts in AWS account xyz",
			Answer:      `cloud_provider:aws aws_account:xyz`,
			Explanation: "searches for aws specific hosts.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog host analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "datadog",
		},
	}
}

func (p DatadogHostsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []string{tools.ToolDatadogHosts, core.ToolLlm}

	tools := make([]toolcore.NBTool, 0, len(toolsList))
	for _, toolName := range toolsList {
		if tool, ok := toolcore.GetNBTool(p.accountId, toolName); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

func (l DatadogHostsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l DatadogHostsAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
