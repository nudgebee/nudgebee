package tools

import (
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"time"
)

const ToolGetTracesChronosphere = "traces_execute_chronosphere"

func init() {
	core.RegisterNBToolFactory(ToolGetTracesChronosphere, func(accountId string) (core.NBTool, error) {
		return TracesExecuteChronosphereTool{
			accountId: accountId,
		}, nil
	})
}

type TracesExecuteChronosphereTool struct {
	accountId string
}

func (m TracesExecuteChronosphereTool) Name() string {
	return ToolGetTracesChronosphere
}

func (m TracesExecuteChronosphereTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TracesExecuteChronosphereTool) Description() string {
	return "Executes a Chronosphere query and returns the result."
}

func (m TracesExecuteChronosphereTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Chronosphere JSON query to Execute",
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

func (m TracesExecuteChronosphereTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("traces: executing getTraces tool call")

	queryBuilder, startTime, endTime, err := core.BuildTraceQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, err
	}

	// Use generic time extraction to handle range and explicit start/end times from tool input
	// This overrides the times from BuildTraceQueryBuilder if they are present in input.Arguments
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	config := map[string]any{
		"end_time":   endTime.UnixNano() / int64(time.Millisecond),
		"start_time": startTime.UnixNano() / int64(time.Millisecond),
	}
	queryResponse, err := executeFetchTrace(nbRequestContext, "chronosphere", "user", "", queryBuilder, config)

	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to get traces", "error", err.Error())
		return core.NBToolResponse{
			Data:   "Trace data is unavailable for this request. " + err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	//restrict 100 rows only
	if len(queryResponse.Traces) > 100 {
		queryResponse.Traces = queryResponse.Traces[:100]
	}

	response, err := common.MarshalJson(queryResponse.Traces)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to serialize on json", "error", err.Error())
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
