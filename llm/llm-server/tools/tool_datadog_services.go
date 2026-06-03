package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
)

const ToolDatadogServices = "datadog_services_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogServices, func(accountId string) (core.NBTool, error) {
		return DatadogServicesExecuteTool{}, nil
	})
}

type DatadogServicesExecuteTool struct{}

func (m DatadogServicesExecuteTool) Name() string { return ToolDatadogServices }

func (m DatadogServicesExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogServicesExecuteTool) Description() string {
	return `Executes a Datadog services query and retrieves the corresponding service data.`
}

func (m DatadogServicesExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog service query to execute"},
		},
		Required: []string{"command"},
	}
}

func (m DatadogServicesExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m DatadogServicesExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing traces tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)

	// ExecuteApiCall will handle the API interaction
	response, err := m.executeDatadogServices(nbRequestContext, finalQuery, map[string]any{})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute traces api call", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize traces json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m DatadogServicesExecuteTool) executeDatadogServices(ctx core.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Datadog Software Catalog API v2 endpoint
	// See: https://docs.datadoghq.com/api/latest/software-catalog/#get-all-software-catalog-entities
	baseURL := fmt.Sprintf("https://%s/api/v2/apm/services", site)
	queryParams := url.Values{}
	if query == "" {
		query = "env:*"
	}

	queryParams.Set("filter[query]", query)
	queryParams.Set("filter[env]", "*")

	requestURL := baseURL + "?" + queryParams.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}

func (m DatadogServicesExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}
