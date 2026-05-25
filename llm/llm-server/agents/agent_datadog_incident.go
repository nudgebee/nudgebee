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

func init() {
	toolDescription := `Get list of datadog incidents using list using natural language question. Input should be a natural language request for incidents data.`
	toolInput := "Provide incidents data question in natural language"
	toolOutput := "The tool will return incidents data from your question"

	core.RegisterNBAgentFactoryAndTool(DatadogIncidentAgentName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogIncidentAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

// DatadogIncidentAgentName is the name of the Datadog Incident agent.
const DatadogIncidentAgentName = "datadog_incident"

func init() {
	// Register the agent factory with the core agent registry.
	toolDescription := `Retrieves Datadog incidents. Input should be a natural language question about incidents.`
	toolInput := "Provide a question about Datadog incidents in natural language."
	toolOutput := "The tool will return the Datadog incident data retrieved for your query."
	core.RegisterNBAgentFactoryAndTool(DatadogIncidentAgentName, func(accountId string) (core.NBAgent, error) {
		return NewDatadogIncidentAgent(accountId), nil
	}, toolDescription, toolInput, toolOutput)
}

// DatadogIncidentAgent is an agent specialized in querying Datadog incidents.
type DatadogIncidentAgent struct {
	accountId string
}

// NewDatadogIncidentAgent creates a new instance of the DatadogIncidentAgent.
func NewDatadogIncidentAgent(accountId string) core.NBAgent {
	return &DatadogIncidentAgent{
		accountId: accountId,
	}
}

// GetName returns the name of the agent.
func (d DatadogIncidentAgent) GetName() string { return DatadogIncidentAgentName }

// GetNameAliases returns aliases for the agent name.
func (d DatadogIncidentAgent) GetNameAliases() []string { return []string{"Datadog Incidents"} }

// GetDescription returns a description of the agent.
func (d DatadogIncidentAgent) GetDescription() string {
	return `Uses Datadog to provide incident information based on the given question.`
}

// GetSystemPrompt returns the system prompt for the agent.
func (d DatadogIncidentAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific incident information they need.",
		"**Generate Datadog Incident Query:** Based on the user's request, formulate a precise Datadog incident query string.",
		"**Execute Query:** Always use the `datadog_incident_execute` tool to execute the generated Datadog incident query.",
		"**Present Results:** Summarize the results returned by the `datadog_incident_execute` tool.",
	}

	constraints := []string{
		"You MUST use the `datadog_incident_execute` tool to execute the Datadog incident query.",
		"The input to the `datadog_incident_execute` tool MUST be a valid Datadog incident query string.",
	}
	toolUsage := map[string][]string{
		tools.ToolDatadogIncidentExecute: {
			"Use this tool to execute a Datadog incident query.",
			"Input: A valid Datadog incident query string.",
			"Output: JSON response from Datadog containing incident data.",
		},
		core.ToolLlm: {
			"Use this tool for reasoning, generating the initial Datadog incident query string, or summarizing the results.",
			"Input: A clear instruction or question.",
		},
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me all incidents with high severity.",
			Answer:      `severity:high`,
			Explanation: "This query filters for incidents with a high severity.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog incident analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

// GetSupportedTools returns the tools that this agent can use.
func (d DatadogIncidentAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []string{tools.ToolDatadogIncidentExecute, core.ToolLlm}

	tools := make([]toolcore.NBTool, 0, len(toolsList))
	for _, toolName := range toolsList {
		if tool, ok := toolcore.GetNBTool(d.accountId, toolName); ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// GetPlannerType returns the planner type for this agent.
func (d DatadogIncidentAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// UpdateToolResponseForPlanner summarizes the incident data for the planner.
func (d DatadogIncidentAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, DatadogIncidentAgentName) {
		resultsMap := map[string]any{}
		if err := common.UnmarshalJson([]byte(toolResponse), &resultsMap); err == nil {
			incidentList, ok := resultsMap["incidents"].([]any)
			if !ok || len(incidentList) == 0 {
				return "No incidents found in the response."
			}

			var summary strings.Builder
			fmt.Fprintf(&summary, "Found %d incidents.\n", len(incidentList))

			incidentsToSummarize := incidentList
			if len(incidentList) > 5 {
				incidentsToSummarize = incidentList[:5]
				fmt.Fprintf(&summary, "Summarizing the first %d incidents:\n", len(incidentsToSummarize))
			}

			for i, incidentAny := range incidentsToSummarize {
				incident, ok := incidentAny.(map[string]any)
				if !ok {
					continue
				}

				fmt.Fprintf(&summary, "\n--- Incident %d ---\n", i+1)

				if title, ok := incident["title"].(string); ok {
					fmt.Fprintf(&summary, "  Title: %s\n", title)
				}
				if detected, ok := incident["detected"].(float64); ok {
					fmt.Fprintf(&summary, "  Time: %s\n", time.Unix(int64(detected), 0).Format(time.RFC3339))
				}
			}
			return summary.String()
		}
		return fmt.Sprintf("Failed to parse incident data JSON. Raw response: %s", toolResponse)
	}
	return toolResponse
}
