// Deprecated: ESLogAgent is no longer used for log provider routing.
// The "es" provider is now handled by LogDefaultAgent (agent_log_default.go),
// which uses NBLogTool with the ES QueryBuilder → Elasticsearch DSL translation path.
//
// ESLogAgent is retained solely because api/chains.go (/elastic-search-query endpoint)
// instantiates it directly as a standalone ES DSL query-generation chain.
package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
	"time"
)

func init() {
	core.RegisterNBAgentFactory(AgentElasticSearchName, func(accountId string) (core.NBAgent, error) {
		return newESLogAgent(accountId), nil
	})
}

const AgentElasticSearchName = "elastic_search"

type ESLogAgent struct {
	accountId string // This agent is typically called by the generic 'logs' agent.
}

func newESLogAgent(accountId string) ESLogAgent {
	return ESLogAgent{
		accountId: accountId,
	}
}

func (l ESLogAgent) GetName() string {
	return AgentElasticSearchName
}

func (l ESLogAgent) GetNameAliases() []string {
	return []string{"Elastic Search Logs"}
}

func (l ESLogAgent) GetDescription() string {
	return `Retrieves logs from Elasticsearch. Input should be a natural language question about logs.`
}

func (l ESLogAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {

	instructions := []string{
		"**Process:** Carefully analyze the user's request to identify the specific log information they need.",
		"**Step 1:** Use the `elastic_search_query` tool to generate a correct ElasticSearch query from the natural language request.",
		"**Step 2:** Use the `elastic_search_execute` tool to execute the generated query and retrieve logs.",
		"**Step 3:** Examine the results and provide a concise, technical summary.",
		"**Validation:** Always execute the generated query to validate it. Do not assume results without execution.",
		"**Error Handling:** If a tool returns an error, analyze it and try to fix the query or explain the issue clearly.",
	}

	constraints := []string{
		"You MUST use the `elastic_search_query` tool to generate the query.",
		"You MUST use the `elastic_search_execute` tool to get the log data.",
		"NEVER return the generated JSON query as a final answer. Users want logs, not queries.",
		"You MUST NOT provide a final answer until you have received and analyzed results from `elastic_search_execute`.",
		"Do not ask for any clarification from the user, try to resolve using the available tools.",
	}

	toolUsage := map[string][]string{
		ElasticSearchQueryAgentName: {
			"Input: Natural language request for logs",
			"Output: Valid ElasticSearch JSON query object",
		},
		tools.ToolGetLogsES: {
			"Input: The ElasticSearch query object.",
			"Usage: Provide the JSON query object directly as input.",
			"Output: response containing log hits.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in ElasticSearch log analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples: []core.NBAgentPromptExample{
			{
				Question: "Show me logs for app 'llm-server' in namespace 'nudgebee' from the last 1 hour",
				Answer: `
I will first use the elastic_search_query tool to generate the JSON query for the llm-server app in the nudgebee namespace.
tool - elastic_search_query
input - Get me logs for app 'llm-server' in namespace 'nudgebee' from the last 1 hour
output - {"query": {"bool": {"must": [{"regexp": {"kubernetes.pod_name.keyword": {"case_insensitive": true, "value": "llm-server.*"}}}, {"term": {"kubernetes.namespace_name.keyword": {"case_insensitive": true, "value": "nudgebee"}}}, {"range": {"@timestamp": {"gte": "now-1h"}}}]}}}

Now I will use the elastic_search_execute tool to execute this query and retrieve the actual logs.
tool - elastic_search_execute
input - {"query": {"bool": {"must": [{"regexp": {"kubernetes.pod_name.keyword": {"case_insensitive": true, "value": "llm-server.*"}}}, {"term": {"kubernetes.namespace_name.keyword": {"case_insensitive": true, "value": "nudgebee"}}}, {"range": {"@timestamp": {"gte": "now-1h"}}}]}}}
output - {"result": [{"@timestamp": "2024-01-01T10:00:00Z", "log": "Starting server..."}]}

The logs show the llm-server app started successfully.
`,
				Explanation: "A two-step process: generate the JSON query first, then pass it directly to the execution tool.",
			},
		},
		Rag: core.NBAgentPromptRag{
			Module: "elasticsearch",
		},
	}
}

func (p ESLogAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	suppportedTools := []toolcore.NBTool{}
	if tool, ok := toolcore.GetNBTool(p.accountId, ElasticSearchQueryAgentName); ok {
		suppportedTools = append(suppportedTools, tool)
	}
	suppportedTools = append(suppportedTools, tools.ElasticSearchExecuteTool{})

	return suppportedTools
}

func (l ESLogAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (l ESLogAgent) PostProcessResponse(ctx *security.RequestContext, request core.NBAgentRequest, resp core.NBAgentResponse) core.NBAgentResponse {
	responseStr := ""
	responseLogQL := ""
	for _, stepRes := range resp.AgentStepResponse {
		if stepRes.Call.FunctionCall.Name == "elastic_search_execute" {
			responseStr = stepRes.Response.Content
		}
		if stepRes.Call.FunctionCall.Name == ElasticSearchQueryAgentName {
			responseLogQL = stepRes.Response.Content
		}
	}
	if responseStr != "" && responseLogQL != "" {
		// Save raw logs to workspace
		logFileName := fmt.Sprintf("logs_es_%d.txt", time.Now().UnixNano())
		wm := workspace.NewWorkspaceManager()

		if err := wm.SaveFile(ctx, l.accountId, request.ConversationId, logFileName, responseStr); err == nil {
			resp.References = append(resp.References, toolcore.NBToolResponseReference{
				Text:        logFileName,
				Url:         logFileName,
				Type:        "file",
				Description: "Raw log data from ElasticSearch",
			})
		}

		// Create JSON where responseLogQL is the key and responseStr is the value
		result := map[string]string{
			"elastic_search_query": responseLogQL,
			"logs":                 responseStr,
		}
		jsonBytes, err := common.MarshalJson(result)
		if err == nil {
			resp.Response = []string{string(jsonBytes)}
		}
	}
	return resp
}

func (l ESLogAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolGetLogsES) {
		responseMap := map[string]any{}
		err := common.UnmarshalJson([]byte(toolResponse), &responseMap)
		if err != nil {
			return toolResponse
		}
		// check if the response is a valid json and reduce size
		if result, ok := responseMap["result"]; ok {
			updatedResult := result.([]any)
			for i, res := range updatedResult {
				if resMap, ok := res.(map[string]any); ok {
					if source, ok := resMap["_source"]; ok {
						sourceMap := source.(map[string]any)
						updatedSource := map[string]any{}
						for key, value := range sourceMap {
							if key == "@timestamp" || key == "timestamp" || key == "log" || key == "message" || key == "kubernetes" {
								updatedSource[key] = value
							}
						}
						updatedResult[i] = updatedSource
					}
				}
			}
			responseMap["result"] = updatedResult
			toolResponse1, err := common.MarshalJson(responseMap)
			if err != nil {
				return toolResponse
			}
			return string(toolResponse1)
		}
		return toolResponse
	} else if strings.EqualFold(toolRequest.Tool, ElasticSearchQueryAgentName) {
		// Detect and clean double-marshaled/escaped JSON
		cleanedResponse := toolResponse
		var temp any
		if err := common.UnmarshalJson([]byte(toolResponse), &temp); err == nil {
			// If it was a string containing JSON, unmarshal again to get the actual object
			if str, ok := temp.(string); ok {
				var inner any
				if err := common.UnmarshalJson([]byte(str), &inner); err == nil {
					if finalJson, err := common.MarshalJson(inner); err == nil {
						cleanedResponse = string(finalJson)
					}
				}
			}
		}
		return fmt.Sprintf("<output>\n%s\n</output>", cleanedResponse)
	}
	return toolResponse
}
