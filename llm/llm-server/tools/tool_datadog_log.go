package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"strings"
	"time"
)

const ToolDatadogLogExecute = "datadog_log_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogLogExecute, func(accountId string) (core.NBTool, error) {
		return DatadogLogExecuteTool{}, nil
	})
}

type DatadogLogExecuteTool struct{}

func (m DatadogLogExecuteTool) Name() string { return ToolDatadogLogExecute }

func (m DatadogLogExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogLogExecuteTool) Description() string {
	return `Executes a Datadog log query and retrieves the corresponding log data.`
}

func (m DatadogLogExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog log query to execute"},
		},
		Required: []string{"command"},
	}
}

func (m DatadogLogExecuteTool) cleanupQuery(query string, args map[string]any) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	finalQuery = unwrapJSONQuery(finalQuery)
	return stripCLITimeFlags(finalQuery, args)
}

func (m DatadogLogExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing logs tool call", "query", input.Command)
	if input.Arguments == nil {
		input.Arguments = map[string]any{}
	}
	finalQuery := m.cleanupQuery(input.Command, input.Arguments)
	// Fallback for executor parser drift: when the LLM emits JSON with a `query` key (instead
	// of `command`), the executor leaves Command as the raw JSON blob and populates Arguments
	// with the parsed fields. unwrapJSONQuery handles the case where Command is itself JSON;
	// this branch handles the case where Command is something else but Arguments has the query.
	if strings.HasPrefix(finalQuery, "{") {
		if q, ok := input.Arguments["query"].(string); ok && q != "" {
			finalQuery = m.cleanupQuery(q, input.Arguments)
		} else if q, ok := input.Arguments["command"].(string); ok && q != "" {
			finalQuery = m.cleanupQuery(q, input.Arguments)
		}
	}
	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Hour)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}
	startTime, endTime = ExpandNarrowTimeWindow(nbRequestContext.Ctx.GetLogger(), startTime, endTime)

	response, err := m.executeDatadogLogs(nbRequestContext, finalQuery, map[string]any{
		"end_time":   endTime.UnixMilli(),
		"start_time": startTime.UnixMilli(),
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute api call", "error", err.Error())
		respData := ""
		return core.NBToolResponse{Data: respData, Status: core.NBToolResponseStatusError}, err
	}
	data, err := common.MarshalJson(response)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to serialize json", "error", err.Error())
		return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, err
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m DatadogLogExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func getDatadogConfigSchema() core.ToolConfigSchema {
	return core.ToolConfigSchema{
		Type:         core.ToolSchemaTypeObject,
		Required:     []string{"api_key", "app_key", "site"},
		ConfigType:   "datadog",
		ConfigSource: core.ToolConfigSourceIntegration,
		Properties: map[string]core.ToolSchemaProperty{
			"api_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Datadog API Key",
				IsEncrypted: true,
			},
			"app_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Datadog App Key",
				IsEncrypted: true,
			},
			"site": {
				Type:        core.ToolSchemaTypeString,
				Description: "Datadog Site",
			},
		},
	}
}

func (m DatadogLogExecuteTool) executeDatadogLogs(ctx core.NbToolContext, query string, configs map[string]any) (core.ObservabilityLogResponse, error) {
	return executeFetchLogs(ctx, services_server.ObservabilityProvider{
		IntegrationSource: "user",
		Provider:          "datadog",
	}, query, configs)
}
