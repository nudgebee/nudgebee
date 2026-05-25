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

const ToolDatadogEventsExecute = "datadog_events_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogEventsExecute, func(accountId string) (core.NBTool, error) {
		return DatadogEventsExecuteTool{}, nil
	})
}

type DatadogEventsExecuteTool struct{}

func (m DatadogEventsExecuteTool) Name() string { return ToolDatadogEventsExecute }

func (m DatadogEventsExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogEventsExecuteTool) Description() string {
	return `Executes a Datadog event query and retrieves the corresponding event data.`
}

func (m DatadogEventsExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog event query to execute (e.g., 'tags:env:prod,source:kubernetes')."},
		},
		Required: []string{"command"},
	}
}

func (m DatadogEventsExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m DatadogEventsExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing events tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)

	endTime := time.Now()
	startTime := endTime.Add(-2 * time.Hour)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	response, err := m.executeDatadogEvents(nbRequestContext, finalQuery, map[string]any{
		"end_time":   endTime.Unix(),
		"start_time": startTime.Unix(),
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute events api call", "error", err.Error())
		return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
	}

	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize events json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}

	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m DatadogEventsExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (m DatadogEventsExecuteTool) executeDatadogEvents(ctx core.NbToolContext, query string, configs map[string]any) (map[string]any, error) {
	apiKey, appKey, site, err := getDataDogConfigs(ctx)

	if err != nil {
		return nil, err
	}

	// Default time range: last hour
	now := time.Now().UTC()
	from := now.Add(-1 * time.Hour)

	// The query parameter for the v1 events API is a space-separated string of tags.
	// e.g. `tags:env:prod,source:nagios`
	q := url.QueryEscape(query)

	// Datadog Events API v1 endpoint
	requestURL := fmt.Sprintf("https://%s/api/v1/events?start=%d&end=%d&query=%s",
		site, from.Unix(), now.Unix(), q)

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, err
	}

	return doDatadogRequest(req, apiKey, appKey)
}
