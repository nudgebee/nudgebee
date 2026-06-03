package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const DatadogContainersQueryAgentName = "datadog_containers_query"

func init() {
	toolDescription := `Generates a Datadog container query string from a natural language question. Input should be a natural language request for container data.`
	toolInput := "Provide container data question in natural language"
	toolOutput := "The tool will return the Datadog container query retrieved from your question"

	core.RegisterNBAgentFactoryAsTool(DatadogContainersQueryAgentName, func(accountId string) (core.NBAgent, error) {
		return DatadogContainersQueryAgent{}, nil
	}, toolDescription, toolInput, toolOutput)
}

type DatadogContainersQueryAgent struct{}

func (d DatadogContainersQueryAgent) GetName() string { return DatadogContainersQueryAgentName }

func (d DatadogContainersQueryAgent) GetNameAliases() []string {
	return []string{"Datadog Container Query"}
}

func (d DatadogContainersQueryAgent) GetDescription() string {
	return `Generate Datadog container query based on natural language question.`
}

func (d DatadogContainersQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific container information they need.",
		"**Generate Datadog Query JSON:** Construct a valid JSON object based on the user's request containing 'query', 'group_by', and 'sort' keys.",
		"**'query' key:** This is the filter string. Use tags and attributes for filtering. Common attributes include container_id,container_name,host,image_id,image_name,image_tag,kube_app_instance,kube_app_name,kube_container_name,kube_deployment,kube_namespace,kube_node,kube_ownerref_kind,kube_ownerref_name,kube_qos,kube_replica_set,kube_service,orchestrator,pod_name,pod_phase,runtime,service,short_image.",
		"**'group_by' key:** If the user asks to group results (e.g., 'group by namespace'), populate this field with a comma-separated list of tags (e.g., 'kube_namespace').",
		"**'sort' key:** If the user asks to sort results (e.g., 'sort by name'), populate this field with the attribute name. Prepend with `-` for descending order (e.g., '-name').",
		"**Time Range:** The API shows currently-live containers. Time ranges are not applicable for this API, so ignore any time range in the user's request.",
		"**Output:** Return only the JSON object with no additional text or formatting.",
	}
	constraints := []string{
		"Always return a valid JSON object.",
		"Do not add any formatting or additional information in the response. Return only the JSON object.",
		"Do not ask for any clarification from the user, try to resolve using the available tools.",
		"If no grouping or sorting is requested, the `group_by` and `sort` fields can be omitted or be empty strings.",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Get me all pods in namespace 'n1'.",
			Answer:      `{"query": "kube_namespace:n1"}`,
			Explanation: "Filters containers by Kubernetes namespace 'n1'. Returns a JSON object.",
		},
		{
			Question:    "Show me all running containers for deployment 'my-app-deployment'.",
			Answer:      `{"query": "kube_deployment:my-app-deployment,pod_phase:running"}`,
			Explanation: "Filters containers by deployment name and running state. Returns a JSON object.",
		},
		{
			Question:    "List all exited containers on host 'ip-10-0-0-1.ec2.internal'.",
			Answer:      `{"query": "host:ip-10-0-0-1.ec2.internal,pod_phase:exited"}`,
			Explanation: "Filters containers by host and exited state. Returns a JSON object.",
		},
		{
			Question:    "Find all containers using image 'nginx:latest'.",
			Answer:      `{"query": "image_name:nginx:latest"}`,
			Explanation: "Filters containers by image name and tag. Returns a JSON object.",
		},
		{
			Question:    "Find all containers that are part of a daemonset in the 'monitoring' namespace.",
			Answer:      `{"query": "kube_namespace:monitoring,kube_daemon_set:*"}`,
			Explanation: "Filters by namespace and for containers that have the 'kube_daemon_set' tag present. Returns a JSON object.",
		},
		{
			Question:    "Show me all containers that are not in a running state.",
			Answer:      `{"query": "-pod_phase:running"}`,
			Explanation: "Excludes containers that are in a running state. Returns a JSON object.",
		},
		{
			Question:    "Show me all containers, grouped by image name.",
			Answer:      `{"query": "", "group_by": "image_name"}`,
			Explanation: "Groups all containers by their image name. Returns a JSON object.",
		},
		{
			Question:    "List all containers, sorted by host.",
			Answer:      `{"query": "", "sort": "host"}`,
			Explanation: "Sorts all containers by their host name in ascending order. Returns a JSON object.",
		},
		{
			Question:    "Get all running containers in the 'default' namespace, grouped by deployment and sorted by name.",
			Answer:      `{"query": "kube_namespace:default,pod_phase:running", "group_by": "kube_deployment", "sort": "name"}`,
			Explanation: "Filters for running containers in 'default' namespace, then groups by deployment and sorts by container name. Returns a JSON object.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "an SRE expert in Datadog container queries",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
	}
}

func (d DatadogContainersQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (d DatadogContainersQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeTool
}

func (d DatadogContainersQueryAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}
