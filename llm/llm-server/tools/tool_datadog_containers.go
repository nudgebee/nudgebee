package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const ToolDatadogContainersExecute = "datadog_containers_execute"

func init() {
	toolcore.RegisterNBToolFactory(ToolDatadogContainersExecute, func(accountId string) (toolcore.NBTool, error) {
		return DatadogContainersExecuteTool{}, nil
	})
}

type DatadogContainersExecuteTool struct{}

func (t DatadogContainersExecuteTool) Name() string {
	return ToolDatadogContainersExecute
}

func (t DatadogContainersExecuteTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (t DatadogContainersExecuteTool) Description() string {
	return `Executes a Datadog container query string to retrieve container-related data. Input is a Datadog container query string.`
}

func (t DatadogContainersExecuteTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"query": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "The Datadog container query string.",
			},
		},
		Required: []string{"query"},
	}
}

func (t DatadogContainersExecuteTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	// The command can be a plain query string or a JSON string with query, group_by, and sort.
	command := input.Command

	var query, groupBy, sort string

	var requestParams map[string]string
	err := common.UnmarshalJson([]byte(command), &requestParams)
	if err == nil {
		// Successfully parsed as JSON
		query = requestParams["query"]
		groupBy = requestParams["group_by"]
		sort = requestParams["sort"]
	} else {
		// Fallback: treat the command as a plain query string
		query = command
	}

	// The input schema says the property is "query", but ReAct agents use "command".
	// Let's check the input.Arguments as well for flexibility.
	if query == "" {
		if q, ok := input.Arguments["query"].(string); ok {
			query = q
		}
	}

	// Call the actual Datadog API via the common execution function
	responseAny, err := t.executeDatadogContainers(nbRequestContext, query, map[string]any{
		"group_by": groupBy,
		"sort":     sort,
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute containers api call", "error", err.Error())
		return toolcore.NBToolResponse{Data: err.Error(), Status: toolcore.NBToolResponseStatusError}, err
	}

	// The response from doDatadogRequest is already a map[string]any, so we just need to marshal it.
	jsonResponse, err := common.MarshalJson(responseAny)
	if err != nil {
		return toolcore.NBToolResponse{}, fmt.Errorf("failed to marshal datadog response: %w", err)
	}

	return toolcore.NBToolResponse{Data: string(jsonResponse), Type: toolcore.NBToolResponseTypeJson}, nil
}

func (t DatadogContainersExecuteTool) ConfigSchema(ctx *security.RequestContext) toolcore.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (t DatadogContainersExecuteTool) executeDatadogContainers(ctx toolcore.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Default limit: Use config or a default value (e.g., 100)
	limit := 100

	// Datadog Containers API v2 endpoint
	// See: https://docs.datadoghq.com/api/latest/containers/#get-a-list-of-containers
	baseURL := fmt.Sprintf("https://%s/api/v2/containers", site)
	queryParams := url.Values{}
	if query != "" {
		queryParams.Set("filter[tags]", query)
	}
	queryParams.Set("page[size]", fmt.Sprintf("%d", limit))

	if configs != nil {
		if groupBy, ok := configs["group_by"].(string); ok && groupBy != "" {
			queryParams.Set("group_by", groupBy)
		}
		if sort, ok := configs["sort"].(string); ok && sort != "" {
			queryParams.Set("sort", sort)
		}
	}
	requestURL := baseURL + "?" + queryParams.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}
