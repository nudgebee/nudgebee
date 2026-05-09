package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"strings"
)

func init() {
	toolDescription := `Retrieves Datadog container data (e.g., pods, nodes, workloads). Input should be a natural language question about containers.`
	toolInput := "Provide container data question in natural language"
	toolOutput := "The tool will return the datadog container data retrieved your query"

	core.RegisterNBAgentFactoryAndToolAndPrioritizeAgentResponseForTool(DatadogContainersAgentName, func(accountId string) (core.NBAgent, error) {
		return DatadogContainersAgent{accountId: accountId}, nil
	}, toolDescription, toolInput, toolOutput)
}

const DatadogContainersAgentName = "datadog_containers"

type DatadogContainersAgent struct {
	accountId string
}

func NewDatadogContainersAgent(accountId string) DatadogContainersAgent {
	return DatadogContainersAgent{accountId: accountId}
}

func (d DatadogContainersAgent) GetName() string { return DatadogContainersAgentName }

func (d DatadogContainersAgent) GetNameAliases() []string {
	return []string{"Datadog Containers", "Datadog Workloads"}
}

func (d DatadogContainersAgent) GetDescription() string {
	return `Uses Datadog to provide container, pod, node, and workload information based on the given question.`
}

func (d DatadogContainersAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific container/workload information they need.",
		"**Generate Datadog Query:** Use the `datadog_containers_query` tool to generate a Datadog container query string.",
		"**Execute Query:** Always use the `datadog_containers_execute` tool to execute the generated query.",
		"**Present Results:** Summarize the results returned by the `datadog_containers_execute` tool.",
		"**Error Handling:** Handle errors gracefully and provide informative error messages.",
	}

	constraints := []string{
		"You MUST use the `datadog_containers_query` tool to generate the Datadog query.",
		"You MUST use the `datadog_containers_execute` tool to execute the Datadog query.",
		"You MUST NOT answer the question without using the tools.",
		"Do not ask for any clarification from the user, try to resolve using the available tools.",
	}

	toolUsage := map[string][]string{
		DatadogContainersQueryAgentName: {
			"Use this tool to generate a Datadog container query string.",
			"Input: Natural language request for container data (e.g., pods, nodes, deployments, containers).",
			"Output: Valid Datadog container query string.",
		},
		tools.ToolDatadogContainersExecute: {
			"Use this tool to execute a Datadog container query string.",
			"Input: A valid Datadog container query string.",
			"Output: JSON response from Datadog containing container data.",
		},
	}

	examples := []core.NBAgentPromptExample{
		{
			Question:    "Get me all pods in namespace 'default'.",
			Answer:      "Use the `datadog_containers_query` tool with the input `Get me all pods in namespace 'default'` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will first generate a JSON object like `{\"query\": \"kube_namespace:default\"}` and then execute it to retrieve container information for pods in that namespace.",
		},
		{
			Question:    "Find all containers that are part of a daemonset in the 'monitoring' namespace.",
			Answer:      "Use the `datadog_containers_query` tool with the input `Find all containers that are part of a daemonset in the 'monitoring' namespace` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will generate a JSON object like `{\"query\": \"kube_namespace:monitoring,kube_daemon_set:*\"}` and then execute it.",
		},
		{
			Question:    "Find containers for 'my-api-service' that are not running.",
			Answer:      "Use the `datadog_containers_query` tool with the input `Find containers for 'my-api-service' that are not running` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will generate a JSON object like `{\"query\": \"kube_deployment:my-api-service,-pod_phase:running\"}` and then execute it.",
		},
		{
			Question:    "List all containers grouped by namespace and sorted by name.",
			Answer:      "Use the `datadog_containers_query` tool with the input `List all containers grouped by namespace and sorted by name` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will generate a JSON object like `{\"query\": \"\", \"group_by\": \"kube_namespace\", \"sort\": \"name\"}` and then execute it.",
		},
		{
			Question:    "Show me all containers, grouped by image name.",
			Answer:      "Use the `datadog_containers_query` tool with the input `Show me all containers, grouped by image name` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will generate a JSON object like `{\"query\": \"\", \"group_by\": \"image_name\"}` and then execute it.",
		},
		{
			Question:    "List all containers, sorted by host.",
			Answer:      "Use the `datadog_containers_query` tool with the input `List all containers, sorted by host` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will generate a JSON object like `{\"query\": \"\", \"sort\": \"host\"}` and then execute it.",
		},
		{
			Question:    "Get all running containers in the 'default' namespace, grouped by deployment and sorted by name.",
			Answer:      "Use the `datadog_containers_query` tool with the input `Get all running containers in the 'default' namespace, grouped by deployment and sorted by name` to get the query JSON. Then use `datadog_containers_execute` with the generated JSON.",
			Explanation: "This will generate a JSON object like `{\"query\": \"kube_namespace:default,pod_phase:running\", \"group_by\": \"kube_deployment\", \"sort\": \"name\"}` and then execute it.",
		},
	}

	return core.NBAgentPrompt{
		Role:         "an SRE expert specializing in Datadog container and workload analysis",
		Instructions: instructions,
		Constraints:  constraints,
		ToolUsage:    toolUsage,
		Examples:     examples,
	}
}

func (d DatadogContainersAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	toolsList := []toolcore.NBTool{}
	if queryTool, ok := toolcore.GetNBTool(d.accountId, DatadogContainersQueryAgentName); ok {
		toolsList = append(toolsList, queryTool)
	}
	if executeTool, ok := toolcore.GetNBTool(d.accountId, tools.ToolDatadogContainersExecute); ok {
		toolsList = append(toolsList, executeTool)
	}
	return toolsList
}

func (d DatadogContainersAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (d DatadogContainersAgent) UpdateToolResponseForPlanner(toolRequest core.NBAgentPlannerToolAction, toolResponse string) string {
	if strings.EqualFold(toolRequest.Tool, tools.ToolDatadogContainersExecute) {
		resultsMap := map[string]any{}
		if err := common.UnmarshalJson([]byte(toolResponse), &resultsMap); err == nil {
			var summary strings.Builder

			// The real API response has a 'data' array.
			containerData, ok := resultsMap["data"].([]any)
			if !ok {
				// It might be an error response from datadog
				if errors, ok := resultsMap["errors"].([]any); ok && len(errors) > 0 {
					return fmt.Sprintf("Datadog API returned an error: %v", errors)
				}
				return "No container data found in the response or response format is unexpected."
			}

			totalCount := len(containerData)
			if meta, ok := resultsMap["meta"].(map[string]any); ok {
				if page, ok := meta["page"].(map[string]any); ok {
					if tc, ok := page["total_filtered_count"].(float64); ok {
						totalCount = int(tc)
					}
				}
			}

			if totalCount == 0 {
				return "No containers found matching the query."
			}

			fmt.Fprintf(&summary, "Found %d containers.\n", totalCount)

			containersToSummarize := containerData
			if len(containerData) > 5 {
				containersToSummarize = containerData[:5]
				fmt.Fprintf(&summary, "Summarizing the first %d containers:\n", len(containersToSummarize))
			}

			for i, containerAny := range containersToSummarize {
				container, ok := containerAny.(map[string]any)
				if !ok {
					continue
				}

				attributes, ok := container["attributes"].(map[string]any)
				if !ok {
					continue
				}

				fmt.Fprintf(&summary, "\n--- Container %d ---\n", i+1)
				if name, ok := attributes["name"].(string); ok {
					fmt.Fprintf(&summary, "  Name: %s\n", name)
				}
				if state, ok := attributes["state"].(string); ok {
					fmt.Fprintf(&summary, "  State: %s\n", state)
				}
				if host, ok := attributes["host"].(string); ok {
					fmt.Fprintf(&summary, "  Host: %s\n", host)
				}
				if imageName, ok := attributes["image_name"].(string); ok {
					tag, _ := attributes["image_tag"].(string)
					fmt.Fprintf(&summary, "  Image: %s:%s\n", imageName, tag)
				}
				if tags, ok := attributes["tags"].([]any); ok {
					// Find key tags to display
					kubeNamespace := "N/A"
					kubeDeployment := "N/A"
					for _, tagAny := range tags {
						tag, ok := tagAny.(string)
						if !ok {
							continue
						}
						if strings.HasPrefix(tag, "kube_namespace:") {
							kubeNamespace = strings.TrimPrefix(tag, "kube_namespace:")
						}
						if strings.HasPrefix(tag, "kube_deployment:") {
							kubeDeployment = strings.TrimPrefix(tag, "kube_deployment:")
						}
					}
					fmt.Fprintf(&summary, "  Namespace: %s\n", kubeNamespace)
					fmt.Fprintf(&summary, "  Deployment: %s\n", kubeDeployment)
				}
			}
			return summary.String()
		}
		return fmt.Sprintf("Failed to parse container data JSON. Raw response: %s", toolResponse)
	}
	return toolResponse
}
