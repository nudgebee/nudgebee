package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"
)

// DatadogEventsAgentName is the name of the Datadog Events agent.
const DatadogEventsAgentName = "datadog_events"

func init() {
	// Register the agent factory with the core agent registry.
	toolDescription := `Retrieves Datadog events. Input should be a natural language question about events.`
	toolInput := "Provide a question about Datadog events in natural language."
	toolOutput := "The tool will return the Datadog event data retrieved for your query."
	core.RegisterNBAgentFactoryAndTool(DatadogEventsAgentName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogEventsAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

// DatadogEventsAgent is an agent specialized in querying Datadog events.
type DatadogEventsAgent struct {
	accountId string
}

// NewDatadogEventsAgent creates a new instance of the DatadogEventsAgent.
func NewDatadogEventsAgent(accountId string) core.NBAgent {
	return &DatadogEventsAgent{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (d DatadogEventsAgent) GetName() string { return DatadogEventsAgentName }

// GetNameAliases returns aliases for the agent name.
func (d DatadogEventsAgent) GetNameAliases() []string { return []string{"Datadog Events"} }

// GetDescription returns a description of the agent.
func (d DatadogEventsAgent) GetDescription() string {
	return `Uses Datadog to provide event information based on the given question.`
}

// GetSystemPrompt returns the system prompt for the agent.
func (d DatadogEventsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific event information they need.",
		"**Generate Datadog Event Query:** Based on the user's request, formulate a precise Datadog event query string. The query should be a comma-separated list of key:value tags (e.g., 'source:kubernetes,priority:normal').",
		"**Execute Query:** Always use the `datadog_events_execute` tool to execute the generated Datadog event query.",
		"**Present Results:** Summarize the results returned by the `datadog_events_execute` tool.",
		"**Filters:** Use fields like `source` (kubernetes, datadog, watchdog, alert), `host`, `status` (error, warn, info, ok), `@aggregation_key`, `pod_name`, `kube_namespace`, `kube_node`, `kube_kind`, `kube_name`, `@priority`, `@title` for filtering.",
	}

	constraints := []string{
		"You MUST use the `datadog_events_execute` tool to execute the Datadog event query.",
		"The input to the `datadog_events_execute` tool MUST be a valid Datadog event query string (tags).",
	}
	toolUsage := map[string][]string{
		tools.ToolDatadogEventsExecute: {
			"Use this tool to execute a Datadog event query.",
			"Input: A valid Datadog event query string, which is a comma-separated list of tags (e.g., 'priority:normal,source:kubernetes').",
			"Output: JSON response from Datadog containing event data.",
		},
		core.ToolLlm: {
			"Use this tool for reasoning, generating the initial Datadog event query string, or summarizing the results.",
			"Input: A clear instruction or question.",
		},
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me all kubernetes events with normal priority in the last hour.",
			Answer:      `source:kubernetes @priority:normal`,
			Explanation: "This query filters for events originating from Kubernetes with a normal priority. The time range is handled by the tool.",
		},
		{
			Question:    "Find all events related to deployment failures in the production environment.",
			Answer:      `tags:deployment status:failed`,
			Explanation: "This query searches for events tagged with 'deployment' and 'failed status' in the 'prod' environment.",
		},
		{
			Question:    "List events for host 'my-web-server-01' with a high priority.",
			Answer:      `host:my-web-server-01 @priority:high`,
			Explanation: "This query retrieves high-priority events specifically from the host 'my-web-server-01'.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog event analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

// GetSupportedTools returns the tools that this agent can use.
func (d DatadogEventsAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []string{tools.ToolDatadogEventsExecute, core.ToolLlm}

	tools := make([]toolcore.NBTool, 0, len(toolsList))
	for _, toolName := range toolsList {
		if tool, ok := toolcore.GetNBTool(d.accountId, toolName); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// GetPlannerType returns the planner type for this agent.
func (d DatadogEventsAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// UpdateToolResponseForPlanner summarizes the event data for the planner.
func (d DatadogEventsAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, DatadogEventsAgentName) {
		resultsMap := map[string]any{}
		if err := common.UnmarshalJson([]byte(toolResponse), &resultsMap); err == nil {
			eventList, ok := resultsMap["events"].([]any)
			if !ok || len(eventList) == 0 {
				return "No events found in the response."
			}

			var summary strings.Builder
			fmt.Fprintf(&summary, "Found %d events.\n", len(eventList))

			eventsToSummarize := eventList
			if len(eventList) > 5 {
				eventsToSummarize = eventList[:5]
				fmt.Fprintf(&summary, "Summarizing the first %d events:\n", len(eventsToSummarize))
			}

			for i, eventAny := range eventsToSummarize {
				event, ok := eventAny.(map[string]any)
				if !ok {
					continue
				}

				fmt.Fprintf(&summary, "\n--- Event %d ---\n", i+1)

				if title, ok := event["title"].(string); ok {
					fmt.Fprintf(&summary, "  Title: %s\n", title)
				}
				if dateHappened, ok := event["date_happened"].(float64); ok {
					fmt.Fprintf(&summary, "  Time: %s\n", time.Unix(int64(dateHappened), 0).Format(time.RFC3339))
				}
			}
			return summary.String()
		}
		return fmt.Sprintf("Failed to parse event data JSON. Raw response: %s", toolResponse)
	}
	return toolResponse
}
