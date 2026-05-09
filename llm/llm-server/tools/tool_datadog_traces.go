package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strconv"
	"strings"
	"time"
)

const ToolDatadogTracesExecute = "datadog_traces_execute"

func init() {
	core.RegisterNBToolFactory(ToolDatadogTracesExecute, func(accountId string) (core.NBTool, error) {
		return DatadogTracesExecuteTool{}, nil
	})
}

type DatadogTracesExecuteTool struct{}

func (m DatadogTracesExecuteTool) Name() string { return ToolDatadogTracesExecute }

func (m DatadogTracesExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (m DatadogTracesExecuteTool) Description() string {
	return `Executes a Datadog trace query and retrieves the corresponding trace data.`
}

func (m DatadogTracesExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {Type: core.ToolSchemaTypeString, Description: "The Datadog trace query to execute"},
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

func (m DatadogTracesExecuteTool) cleanupQuery(query string) string {
	finalQuery := strings.TrimSpace(query)
	finalQuery = strings.TrimPrefix(finalQuery, "`")
	finalQuery = strings.TrimSuffix(finalQuery, "`")
	return finalQuery
}

func (m DatadogTracesExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("datadog: executing traces tool call", "query", input.Command)
	finalQuery := m.cleanupQuery(input.Command)

	endTime := time.Now()
	startTime := endTime.Add(-12 * time.Hour)
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	// ExecuteApiCall will handle the API interaction
	response, err := m.executeDatadogTraces(nbRequestContext, finalQuery, map[string]any{
		"start_time": startTime.UnixMilli(),
		"end_time":   endTime.UnixMilli(),
	})
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("datadog: unable to execute traces api call", "error", err.Error())
		return core.NBToolResponse{Data: "Trace data is unavailable for this request.", Status: core.NBToolResponseStatusError}, err
	}

	var jsonStr []byte

	if len(response.Traces) == 0 {
		jsonStr = []byte(`""`) // return empty string instead of "null"
	} else {
		jsonStr, err = common.MarshalJson(response.Traces)
		if err != nil {
			return core.NBToolResponse{Data: "", Status: core.NBToolResponseStatusError}, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
		}
	}
	return core.NBToolResponse{Data: string(jsonStr), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (m DatadogTracesExecuteTool) ConfigSchema(ctx *security.RequestContext) core.ToolConfigSchema {
	return getDatadogConfigSchema()
}

func (m DatadogTracesExecuteTool) executeDatadogTraces(ctx core.NbToolContext, query string, configs map[string]any) (core.ObservabilityTraceResponse, error) {
	return executeFetchTrace(ctx, "datadog", "user", query, core.TraceQueryBuilder{}, configs)
}

type IntOrString int

func (i *IntOrString) UnmarshalJSON(b []byte) error {
	// Try to unmarshal as number first
	var intVal int
	if err := common.UnmarshalJson(b, &intVal); err == nil {
		*i = IntOrString(intVal)
		return nil
	}

	// Try as string
	var strVal string
	if err := common.UnmarshalJson(b, &strVal); err != nil {
		return err
	}
	intVal, err := strconv.Atoi(strVal)
	if err != nil {
		return err
	}

	*i = IntOrString(intVal)
	return nil
}
