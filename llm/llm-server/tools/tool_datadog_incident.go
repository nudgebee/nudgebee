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

const ToolDatadogIncidentExecute = "datadog_incident_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogIncidentExecute, func(accountId string) (core.NBTool, error) {
		return DatadogIncidentExecuteTool{}, nil
	})
}

type DatadogIncidentExecuteTool struct{}

func (m DatadogIncidentExecuteTool) Name() string { return ToolDatadogIncidentExecute }

func (m DatadogIncidentExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogIncidentExecuteTool) Description() string {
	return `Executes a Datadog incident query and retrieves the corresponding incident data.`
}

func (m DatadogIncidentExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog incident query to execute (e.g., 'severity:critical')."},
		},
		Required: []string{"command"},
	}
}

func (m DatadogIncidentExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m DatadogIncidentExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing incidents tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)

	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Hour)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	response, err := m.executeDatadogIncidents(nbRequestContext, finalQuery, map[string]any{
		"end_time":   endTime.Unix(),
		"start_time": startTime.Unix(),
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute incidents api call", "error", err.Error())
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize incidents json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m DatadogIncidentExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (m DatadogIncidentExecuteTool) executeDatadogIncidents(ctx core.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)
	if err != nil {
		return nil, err
	}

	// Datadog Incidents API v2 endpoint
	// See: https://docs.datadoghq.com/api/latest/incidents/#get-a-list-of-incidents
	baseURL := fmt.Sprintf("https://%s/api/v2/incidents", site)
	queryParams := url.Values{}
	if query != "" {
		queryParams.Set("filter[query]", query)
	}

	requestURL := baseURL + "?" + queryParams.Encode()

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}
	return doDatadogRequest(req, apiKey, appKey)
}
