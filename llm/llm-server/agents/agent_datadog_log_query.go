package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const DatadogLogQueryAgentName = "datadog_log_query"

func init() {
	toolDescription := `Generates a Datadog log query string from a natural language question. Input should be a natural language request for logs.`
	toolInput := "Provide log question in natural language"
	toolOutput := "The tool will return the Datadog query retrieved from your question"

	core.RegisterNBAgentFactoryAsTool(DatadogLogQueryAgentName, func(accountId string) (core.NBAgent, error) {
		return DatadogLogQueryAgent{}, nil
	}, toolDescription, toolInput, toolOutput)
}

// DatadogQueryAgentName is the name used for the Datadog query generator agent.

type DatadogLogQueryAgent struct{}

func (d DatadogLogQueryAgent) GetName() string { return DatadogLogQueryAgentName }

func (d DatadogLogQueryAgent) GetNameAliases() []string { return []string{"Datadog Log Query"} }

func (d DatadogLogQueryAgent) GetDescription() string {
	return `Generate Datadog log query based on natural language question.`
}

func (d DatadogLogQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific log information they need.",
		"**Generate Datadog Query:** Construct a valid Datadog log query based on the user's request.",
		"**Filters:** Use fields like `service`, `source`, `status`, `host`, `@level`, `container_id`, `container_name`, `image_name`, `image_tag`, `kube_container_name`, `kube_daemon_set`, `kube_namespace`,`kube_node`, `kube_ownerref_kind`, `kube_ownerref_name`, `kube_qos`, `kube_service`,`pod_name`, `pod_phase`, `short_image` for filtering.",
		"**Field rules:** Only use fields listed above — match field names exactly to user intent (e.g. `pod_name` for pods, `kube_ownerref_name` for deployments, `source` for log origin); do not invent fields; use plain text search for patterns like IPs or keywords.",
		"**Time Range:** Unless specified, limit the query to the last 1 hour.",
		"**Output:** Return only the Datadog query with no additional text or formatting.",
	}
	constraints := []string{
		"Always return a valid Datadog query.",
		"Do not add any formatting or additional information in the response. Return only the query.",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me error logs for service 'my-api' in the last hour.",
			Answer:      "service:my-api status:error",
			Explanation: "Filters logs by service 'my-api' and 'error' status. Time range is handled by the tool.",
		},
		{
			Question:    "Get logs for pod 'my-pod-xyz' in namespace 'default'.",
			Answer:      "pod_name:my-pod-xyz kube_namespace:default",
			Explanation: "Filters logs by specific pod name and Kubernetes namespace.",
		},
		{
			Question:    "Find all warning logs from source 'kubernetes'.",
			Answer:      "source:kubernetes @level:warn",
			Explanation: "Filters logs by source 'kubernetes' and log level 'warn'.",
		},
		{
			Question:    "Show logs containing 'connection refused' from host 'my-web-server'.",
			Answer:      "host:my-web-server \"connection refused\"",
			Explanation: "Filters logs by host and a specific text string 'connection refused'.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "an SRE expert in Datadog log queries",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
	}
}

func (d DatadogLogQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (d DatadogLogQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeTool
}

func (d DatadogLogQueryAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}
