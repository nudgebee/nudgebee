package signoz

import (
	"errors"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"time"
)

const SignozLogQueryAgentName = "signoz_log_query_generator"

func init() {
	toolDescription := `Generates a Signoz log query filters in JSON format from a natural language question. Input should be a natural language request for logs.`
	toolInput := "Provide log question in natural language"
	toolOutput := "The tool will return the Signoz query filters in JSON format based on users question"

	core.RegisterNBAgentFactoryAsTool(SignozLogQueryAgentName, func(accountId string) (core.NBAgent, error) {
		return SignozLogQueryAgent{}, nil
	}, toolDescription, toolInput, toolOutput)
}

type SignozLogQueryAgent struct{}

func (d SignozLogQueryAgent) GetName() string { return SignozLogQueryAgentName }

func (d SignozLogQueryAgent) GetNameAliases() []string { return []string{"Signoz Log Query"} }

func (d SignozLogQueryAgent) GetDescription() string {
	return `Generate Signoz log query based on natural language question.`
}

func (d SignozLogQueryAgent) GetSupportedLabels(accountId string) (string, error) {
	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "signoz_label_suggest",
		ActionParams: map[string]any{},
	}
	response, err := relay.Execute(actionParam)
	if err != nil {
		return "", err
	}
	data1, ok := response["data"].(map[string]any)
	if !ok || data1 == nil {
		return "", errors.New("unable to execute loki query")
	}
	if data1["success"] != nil && !data1["success"].(bool) {
		return "", errors.New("unable to execute loki query")
	}
	data2, ok := data1["data"].(map[string]any)
	if !ok || data2 == nil {
		return "", errors.New("data2 field not found or is nil from data")
	}
	if !ok || data2 == nil {
		if errStr, ok := data1["data"].(string); ok {
			return "", errors.New(errStr)
		}
		return "", errors.New("data2 field not found or is nil from data")
	}
	data3, ok := data2["data"].(map[string]any)
	if !ok || data3 == nil {
		return "", errors.New("data3 field not found or is nil from data")
	}
	resultFromData, ok := data3["attributes"]
	if !ok {
		return "", errors.New("result field not found or is not a []any from data")
	}

	// Result keys slice
	var keys []string

	// Loop through and filter
	for _, i := range resultFromData.([]any) {
		if item, ok := i.(map[string]any); ok {
			if dataType, ok := item["dataType"].(string); ok && dataType == "string" {
				if key, ok := item["key"].(string); ok {
					keys = append(keys, key)
				}
			}
		}
	}
	return "'" + strings.Join(keys, "','") + "'", nil
}

func (d SignozLogQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific log information they need.",
		"**Generate Signoz Query filters:** Construct a valid Signoz log filters and their respective operators based on the user's request.",
		fmt.Sprintf(
			"**Find startdate and enddate:** Use today's date time is %s UTC as a reference for the time range. If the user does not specify a time range, default to the last hour.",
			time.Now().UTC().Format("2006-01-02 15:04:05"),
		),
		func() string {
			labels, err := d.GetSupportedLabels(query.AccountId)
			if err != nil {
				return "unknown fields"
			}
			return fmt.Sprintf("**Filters:** Use fields like %s for filtering.", labels)
		}(),
		"**Operators:** =,- =, !=, >, >=, <, <=, contains, ncontains, regex, nregex, like, nlike",
		"**Output:** Return only the filters in json format with no additional text or formatting.",
	}
	constraints := []string{
		"Always return a valid Signoz query in json format.",
		"Do not add any formatting or additional information in the response. Return only the query.",
		"always make sure you return filters, startdate and enddate in the response json.",
		"prefer contains operator for query insetad of = operator",
		"prefer 'body' attribute for full search ie 'body contains <search term>'",
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me error logs for service 'my-api' in the last hour.",
			Answer:      `{"filters":[{"key":{"key":"service.name"},"value":"my-api", "op":"="},{"key":{"key":"status"},"value":"error","op":"="}]}, "startdate": "2023-10-01 12:00:00", "enddate": "2023-10-01 13:00:00"}`,
			Explanation: "Filters logs by service 'my-api' and 'error' status. Time range is handled by the tool.",
		},

		{
			Question:    "Get logs for pod 'my-pod-xyz' in namespace 'default' for last 2 days.",
			Answer:      `{"filters":[{"key":{"key":"pod_name"},"value":"my-pod-xyz","op":"="},{"key":{"key":"service.namespace"},"value":"default","op":"="}],"startdate": "2023-09-29 12:00:00", "enddate": "2023-10-01 12:00:00"}`,
			Explanation: "Filters logs by specific pod name and Kubernetes namespace.",
		},
		{
			Question:    "Find all warning logs from source 'kubernetes'.",
			Answer:      `{"filters":[{"key":{"key":"source"},"value":"kubernetes","op":"="},{"key":{"key":"level"},"value":"warn","op":"="}], "startdate": "2023-10-01 12:00:00", "enddate": "2023-10-01 13:00:00"}`,
			Explanation: "Filters logs by source 'kubernetes' and log level 'warn'.",
		},
		{
			Question:    "Show logs containing 'connection refused' from host 'my-web-server'.",
			Answer:      `{"filter":[{"key":{"key":"host"},"value":"my-web-server","op":"="},{"key":{"key":"log"},"value":"connection refused","op":"="}],"startdate": "2023-10-01 12:00:00", "enddate": "2023-10-01 13:00:00"}`,
			Explanation: "Filters logs by host and a specific text string 'connection refused'.",
		},
		{
			Question:    "Show logs containing 'connection refused' from host 'my-web-server'.",
			Answer:      `{"filter":[{"key":{"key":"host"},"value":"my-web-server","op":"="},{"key":{"key":"log"},"value":"connection refused","op":"="}], "startdate": "2023-10-01 12:00:00", "enddate": "2023-10-01 13:00:00"}`,
			Explanation: "Filters logs by host and a specific text string 'connection refused'.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "an SRE expert in Generating Signoz log queries",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
	}
}

func (d SignozLogQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (d SignozLogQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeTool
}

func (d SignozLogQueryAgent) GetModelCategory() core.ModelTier {
	return core.ModelTierRetrieval
}
