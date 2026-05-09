package tools

import (
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

const ToolDatadogAPMEntities = "datadog_apm_entities_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogAPMEntities, func(accountId string) (core.NBTool, error) {
		return DatadogAPMEntitiesExecuteTool{}, nil
	})
}

type DatadogAPMEntitiesExecuteTool struct{}

func (m DatadogAPMEntitiesExecuteTool) Name() string { return ToolDatadogAPMEntities }

func (m DatadogAPMEntitiesExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogAPMEntitiesExecuteTool) Description() string {
	return `Executes a Datadog APM entities query and retrieves APM entity data including services, operations, and resources.`
}

func (m DatadogAPMEntitiesExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog APM entities query to execute"},
		},
		Required: []string{"command"},
	}
}

func (m DatadogAPMEntitiesExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m DatadogAPMEntitiesExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing APM entities tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)

	// ExecuteApiCall will handle the API interaction
	response, err := m.executeDatadogAPMEntities(nbRequestContext, finalQuery, map[string]any{})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute APM entities api call", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize APM entities json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m DatadogAPMEntitiesExecuteTool) executeDatadogAPMEntities(ctx core.NbToolContext, _ string, _ map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Datadog APM Entities API endpoint (unstable API)
	// See: https://p44.datadoghq.com/api/unstable/apm/entities
	baseURL := fmt.Sprintf("https://%s/api/unstable/apm/entities", site)
	queryParams := url.Values{}

	// Time range filters (last 24 hours by default)
	now := time.Now().Unix()
	from := now - (24 * 60 * 60) // 24 hours ago in seconds
	queryParams.Set("filter[from]", fmt.Sprintf("%d", from))
	queryParams.Set("filter[to]", fmt.Sprintf("%d", now))

	// Additional parameters
	queryParams.Set("source", "web-ui")
	queryParams.Set("with_structured_facets", "false")
	queryParams.Set("page[size]", "100") // Limit to 100 results

	// Columns to return
	queryParams.Set("filter[columns]", "OPERATION_NAME,REQUESTS_PER_SECOND,LATENCY_AVG,ERRORS_PERCENTAGE")

	// Include related entities
	queryParams.Set("include", "entity.catalog_definition,entity.service_health,entity.service_health.watchdog_third_party_alerts,inferred_entities")

	requestURL := baseURL + "?" + queryParams.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}

func (m DatadogAPMEntitiesExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}
