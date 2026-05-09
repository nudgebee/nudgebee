package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools" // Import the tools package to reference the execute tool
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

// DatadogTracesAgentName is the name of the Datadog Traces agent.
const DatadogTracesAgentName = "datadog_traces"

func init() {
	toolDescription := `Retrieves Datadog traces. Input should be a natural language question about traces.`
	toolInput := "Provide traces question in natural language"
	toolOutput := "The tool will return the datadog traces data retrieved your query"
	core.RegisterNBAgentFactoryAndTool(DatadogTracesAgentName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogTracesAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

// DatadogTracesAgent is an agent specialized in querying Datadog traces.
// It acts as a ReAct agent that uses a tool to execute Datadog trace queries.
type DatadogTracesAgent struct {
	accountId string
}

// NewDatadogTracesAgent creates a new instance of the DatadogTracesAgent.
func NewDatadogTracesAgent(accountId string) core.NBAgent {
	return &DatadogTracesAgent{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (d DatadogTracesAgent) GetName() string { return DatadogTracesAgentName }

// GetNameAliases returns aliases for the agent name.
func (d DatadogTracesAgent) GetNameAliases() []string { return []string{"Datadog Traces"} }

// GetDescription returns a description of the agent.
func (d DatadogTracesAgent) GetDescription() string {
	return `Uses Datadog to provide traces based on the given question.`
}

// GetSystemPrompt returns the system prompt for the agent.
// This agent uses a ReAct pattern to first generate a Datadog trace query
// and then execute it.
func (d DatadogTracesAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific trace information they need.",
		"**Generate Datadog Trace Query:** Based on the user's request, formulate a precise Datadog trace query string.",
		"**Execute Query:** Always use the `datadog_traces_execute` tool to execute the generated Datadog trace query.",
		"**Present Results:** Summarize the results returned by the `datadog_traces_execute` tool. **STRICT SECURITY RULE:** You MUST NOT include the Datadog query string, the tool name, or any internal implementation details in your final answer to the user.",
		"**Empty Results Handling:** If the tool returns no data or an empty set, simply state 'No traces were found for the specified criteria' or a similar user-friendly message. Do NOT explain the query steps or show the query used.",
		"**Filters:** Use fields like `service`, `@duration`, `status` (ok, error), `resource_name`, `operation_name`, `@span.kind` (client,server), `type` (sql, web), `@http.host`, `@http.method`, `@http.status_code`, `kube_container_name`, `kube_deployment`, `kube_namespace`, `pod_name`, `host`,   for filtering.",
	}

	constraints := []string{
		"You MUST use the `datadog_traces_execute` tool to execute the Datadog trace query.",
		"You MUST NOT answer the question without using the tool.",
		"Do not ask for any clarification from the user, try to resolve using the available tools.",
		"The input to the `datadog_traces_execute` tool MUST be a valid Datadog trace query string.",
		"**NO QUERY LEAKAGE:** You are strictly forbidden from including any internal Datadog queries or tool names in your final user-facing response.",
	}

	toolUsage := map[string][]string{
		tools.ToolDatadogTracesExecute: { // Reference the execute tool directly
			"Use this tool to execute a Datadog trace query.",
			"Input: A valid Datadog trace query string. Optional: start_time, end_time, or range (e.g. '2d', '1w', '1mo' for months).",
			"Output: JSON response from Datadog containing trace data.",
		},
		core.ToolLlm: { // Allow LLM for initial query generation and final summarization
			"Use this tool for reasoning, generating the initial Datadog trace query string, or summarizing the results.",
			"Input: A clear instruction or question.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me error traces for app my-api of namespace my-namespace in the last hour",
			Answer:      `kube_deployment:my-api kube_namespace:my-namespace @http.status_code:5*`,
			Explanation: "This query filters for traces from 'my-api' service with an HTTP status code 500/502/503 and so on.",
		},
		{
			Question:    "Get traces for resource:my-database-query with latency > 1 second in the last 30 minutes",
			Answer:      `resource_name:"my-database-query" @duration:>1s`,
			Explanation: "This query filters for traces with the resource name 'my-database-query' and a duration greater than 1 second.",
		},
		{
			Question:    "Find traces in the 'prod' environment for the 'checkout-service' that have an error.",
			Answer:      `kube_deployment:checkout-service @http.status_code:5*`,
			Explanation: "This query filters for traces from the 'checkout-service' in the 'prod' environment that have any kind of error.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog trace analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

// GetSupportedTools returns the tools that this agent can use.
func (d DatadogTracesAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// This agent uses the specific Datadog trace execution tool.
	// It might also use an LLM tool for query generation and summarization if not doing it directly.
	toolsList := []string{tools.ToolDatadogTracesExecute, core.ToolLlm} // Include LLM for ReAct steps

	tools := make([]toolcore.NBTool, 0, len(toolsList))
	for _, toolName := range toolsList {
		if tool, ok := toolcore.GetNBTool(d.accountId, toolName); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// GetPlannerType returns the planner type for this agent.
// This is a ReAct agent, not a planner itself.
func (d DatadogTracesAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct // This agent uses ReAct
}

// UpdateToolResponseForPlanner is used by a planner agent (like DatadogDebugAgent)
// to process the output of this agent before feeding it back to the planner's LLM.
// This method summarizes the trace data JSON response.
func (d DatadogTracesAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	// This method is called when this agent's output is used by a planner.
	// We need to summarize the JSON trace data.
	if strings.EqualFold(toolRequest.Tool, DatadogTracesAgentName) { // The planner calls this agent by its agent name
		resultsMap := map[string]any{}
		if err := common.UnmarshalJson([]byte(toolResponse), &resultsMap); err == nil {
			// Assuming the Datadog Traces API response structure has a "data" field
			// containing an array of trace events.
			traceEvents, ok := resultsMap["data"].([]any)
			if !ok || len(traceEvents) == 0 {
				return "No trace data found in the response."
			}

			var summary strings.Builder
			fmt.Fprintf(&summary, "Found %d trace events.\n", len(traceEvents))

			// Summarize key fields from the first few traces
			tracesToSummarize := traceEvents
			if len(traceEvents) > 5 { // Limit summary to the first 5 traces
				tracesToSummarize = traceEvents[:5]
				fmt.Fprintf(&summary, "Summarizing the first %d trace events:\n", len(tracesToSummarize))
			}

			for i, eventAny := range tracesToSummarize {
				event, ok := eventAny.(map[string]any)
				if !ok {
					continue
				}

				fmt.Fprintf(&summary, "\n--- Trace Event %d ---\n", i+1)

				// Extract and summarize relevant fields
				if attributes, ok := event["attributes"].(map[string]any); ok {
					if serviceName, ok := attributes["service.name"].(string); ok {
						fmt.Fprintf(&summary, "  Service: %s\n", serviceName)
					}
					if name, ok := attributes["name"].(string); ok {
						fmt.Fprintf(&summary, "  Operation: %s\n", name)
					}
					if resource, ok := attributes["resource.name"].(string); ok {
						fmt.Fprintf(&summary, "  Resource: %s\n", resource)
					}
					if status, ok := attributes["status"].(string); ok {
						fmt.Fprintf(&summary, "  Status: %s\n", status)
					}
					// Add more relevant fields as needed
				}
				if id, ok := event["id"].(string); ok {
					fmt.Fprintf(&summary, "  ID: %s\n", id)
				}
				if timestamp, ok := event["timestamp"].(string); ok {
					fmt.Fprintf(&summary, "  Timestamp: %s\n", timestamp)
				}
			}

			return summary.String()

		} else {
			// If JSON unmarshalling failed, return the raw response or an error message
			// If JSON unmarshalling failed, return a safe error message
			return "Trace data is unavailable for this request due to a processing error."
		}
	}
	// If the tool name doesn't match, return the original response
	return toolResponse
}
