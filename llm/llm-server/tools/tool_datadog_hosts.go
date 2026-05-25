package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

const ToolDatadogHosts = "datadog_hosts_execute"

func init() {
	toolcore.RegisterNBToolFactory(ToolDatadogHosts, func(accountId string) (toolcore.NBTool, error) {
		return NewDatadogHostsTool(accountId), nil
	})
}

// NewDatadogHostsTool creates a new instance of DatadogHostsTool.
func NewDatadogHostsTool(accountId string) toolcore.NBTool {
	return DatadogHostsTool{
		accountId: accountId,
	}
}

type DatadogHostsTool struct {
	accountId string
}

func (m DatadogHostsTool) Name() string {
	return ToolDatadogHosts
}

func (m DatadogHostsTool) GetType() toolcore.NBToolType {
	return toolcore.NBToolTypeTool
}

func (m DatadogHostsTool) Description() string {
	return `Retrieves a list of hosts from Datadog.
	Usage:
	* Input: Datadog hosts query.
	* Output: A JSON array of host details from Datadog.
	`
}

func (m DatadogHostsTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type:       toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{},
		Required:   []string{},
	}
}

func (m DatadogHostsTool) Call(nbRequestContext toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing datadog_hosts tool call")

	// Assuming there's a generic ExecuteApiCall for Datadog, similar to Elasticsearch
	// This needs to be adapted based on how Datadog API calls are handled in this project.
	// For now, I'll use a placeholder.
	response, err := m.executeDatadogHosts(nbRequestContext, "", nil)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute api call for hosts", "error", err.Error())
		return toolcore.NBToolResponse{
			Data:   "",
			Status: toolcore.NBToolResponseStatusError,
		}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize json for hosts", "error", err.Error())
		return toolcore.NBToolResponse{
			Data:   "",
			Status: toolcore.NBToolResponseStatusError,
		}, err
	}

	return toolcore.NBToolResponse{
		Data:       string(data),
		Type:       toolcore.NBToolResponseTypeJson,
		Status:     toolcore.NBToolResponseStatusSuccess,
		References: []toolcore.NBToolResponseReference{}, // Add relevant references if available
	}, nil
}

func (m DatadogHostsTool) ConfigSchema(ctx *security.RequestContext) toolcore.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (m DatadogHostsTool) executeDatadogHosts(ctx toolcore.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Datadog Hosts API v1 endpoint
	// See: https://docs.datadoghq.com/api/latest/hosts/#get-all-hosts-reported-by-datadog-agent
	baseURL := fmt.Sprintf("https://%s/api/v1/hosts", site)
	queryParams := url.Values{}
	if query != "" {
		queryParams.Set("query", query)
	}

	requestURL := baseURL + "?" + queryParams.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}
