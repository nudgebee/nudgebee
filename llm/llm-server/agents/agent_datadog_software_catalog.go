package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func init() {
	toolDescription := `Get list of datadog software catelog using natural language question. Input should be a natural language request for software catalog data.`
	toolInput := "Provide software catalog data question in natural language"
	toolOutput := "The tool will return software catalog data from your question"

	core.RegisterNBAgentFactoryAndTool(AgentDatadogSoftwareCatalogName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogSoftwareCatalogAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

const AgentDatadogSoftwareCatalogName = "datadog_software_catalog"

type DatadogSoftwareCatalogAgent struct {
	accountId string
}

func NewDatadogSoftwareCatalogAgent(accountId string) DatadogSoftwareCatalogAgent {
	return DatadogSoftwareCatalogAgent{
		accountId: accountId,
	}
}

func (l DatadogSoftwareCatalogAgent) GetName() string {
	return AgentDatadogSoftwareCatalogName
}

func (l DatadogSoftwareCatalogAgent) GetNameAliases() []string {
	return []string{"Datadog Software Catalog"}
}

func (l DatadogSoftwareCatalogAgent) GetDescription() string {
	return `Retrieves entities from the Datadog Software Catalog. Input should be a natural language question about software entities, optionally with a query to filter.`
}

func (l DatadogSoftwareCatalogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific software catalog information they need.",
		"**Execute Query:** Always use the `datadog_software_catalog_execute` tool to retrieve software catalog entities.",
		"**Present Results:** Examine the results returned by the `datadog_software_catalog_execute` tool and provide a concise, markdown-formatted summary.",
		"**Error Handling:** Handle errors from `datadog_software_catalog_execute` gracefully and provide informative error messages to the user.",
		"**Available Filters:** id(uuid of entity), ref(reference), name(entity name), kind(entity kind), owner(entity owner), exclude_snapshot(exclude all entities with snapshot)",
	}

	constraints := []string{
		"You MUST use the `datadog_software_catalog_execute` tool to retrieve software catalog entities.",
		"You MUST NOT answer the question without using the tools.",
		"Do not ask for any clarification from the user, try to resolve using the available tools",
	}

	toolUsage := map[string][]string{
		tools.ToolDatadogSoftwareCatalog: {
			"Use this tool to retrieve a list of entities from the Datadog Software Catalog.",
			"Input: Optional query string to filter entities.",
			"Output: JSON response containing software catalog entities from Datadog.",
		},
		core.ToolLlm: {
			"Use this tool for reasoning, generating the initial Datadog event query string, or summarizing the results.",
			"Input: A clear instruction or question.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me all softwars.",
			Answer:      ``,
			Explanation: "for listing all softwares we dont need to generate empty query",
		},
		{
			Question:    "Show me entity with name xyz.",
			Answer:      `name:xyz`,
			Explanation: "for listing all softwares we dont need to generate empty query",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog Software Catalog analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
		Rag: core.NBAgentPromptRag{
			Module: "datadog",
		},
	}
}

func (p DatadogSoftwareCatalogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []string{tools.ToolDatadogSoftwareCatalog, core.ToolLlm}

	tools := make([]toolcore.NBTool, 0, len(toolsList))
	for _, toolName := range toolsList {
		if tool, ok := toolcore.GetNBTool(p.accountId, toolName); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

func (l DatadogSoftwareCatalogAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l DatadogSoftwareCatalogAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	return toolResponse
}
