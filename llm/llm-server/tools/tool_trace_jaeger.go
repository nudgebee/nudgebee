package tools

import (
	"encoding/json"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"time"
)

const ToolGetTracesJaeger = "traces_execute_jaeger"

func init() {
	core.RegisterNBToolFactory(ToolGetTracesJaeger, func(accountId string) (core.NBTool, error) {
		return TracesExecuteJaegerTool{
			accountId: accountId,
		}, nil
	})
}

type TracesExecuteJaegerTool struct {
	accountId string
}

func (m TracesExecuteJaegerTool) Name() string {
	return ToolGetTracesJaeger
}

func (m TracesExecuteJaegerTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TracesExecuteJaegerTool) Description() string {
	return "Executes a Jaeger trace query and returns the result."
}

func (m TracesExecuteJaegerTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Jaeger JSON query to execute",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range for the query (e.g., '2d', '1w', '1h'). If provided, start_time is calculated relative to end_time.",
			},
		},
		Required: []string{"command"},
	}
}

func (m TracesExecuteJaegerTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("traces: executing getTraces jaeger tool call")

	queryBuilder, startTime, endTime, err := core.BuildTraceQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	// Parse time_range from the JSON command (e.g., "time_range": "2h")
	if cmdMap := map[string]any{}; json.Unmarshal([]byte(input.Command), &cmdMap) == nil {
		if tr, ok := cmdMap["time_range"].(string); ok && tr != "" {
			if dur, parseErr := ParseDuration(tr); parseErr == nil {
				endTime = time.Now().UTC()
				startTime = endTime.Add(-dur)
			}
		}
	}

	// Use generic time extraction to handle range and explicit start/end times from tool input
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	// Resolve the integration source for this account's Jaeger config
	traceProviderSource := "agent"
	provider, err := GetTraceProvider(nbRequestContext.AccountId)
	if err == nil && provider.IntegrationSource != "" {
		traceProviderSource = provider.IntegrationSource
	}

	config := map[string]any{
		"end_time":   endTime.UnixNano() / int64(time.Millisecond),
		"start_time": startTime.UnixNano() / int64(time.Millisecond),
	}
	queryResponse, err := executeFetchTrace(nbRequestContext, "jaeger", traceProviderSource, "", queryBuilder, config)

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to get jaeger traces", "error", err.Error())
		return core.NBToolResponse{
			Data:   "Trace data is unavailable for this request. " + err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Restrict to 100 rows
	if len(queryResponse.Traces) > 100 {
		queryResponse.Traces = queryResponse.Traces[:100]
	}

	response, err := common.MarshalJson(queryResponse.Traces)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to serialize jaeger traces to json", "error", err.Error())
		return core.NBToolResponse{}, err
	}

	resp := core.NBToolResponse{
		Data:   string(response),
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}

	resp.References = []core.NBToolResponseReference{
		core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "traces"}, "Traces Details", nil, ""),
	}

	return resp, err
}
