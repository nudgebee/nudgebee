package tools

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"time"
)

const ToolGetTracesDefault = "traces_execute_default"

func init() {
	core.RegisterNBToolFactory(ToolGetTracesDefault, func(accountId string) (core.NBTool, error) {
		return TracesExecuteDefaultTool{
			accountId: accountId,
		}, nil
	})
}

// TracesExecuteDefaultTool is a provider-agnostic trace query tool. It accepts the
// unified Nudgebee trace JSON `where`-clause schema and routes the query through
// services-server, which translates it for whichever provider the account has
// configured (e.g. Dynatrace). Use this for any provider that does not have a
// dedicated tool (TracesExecuteClickhouseTool, TracesExecuteJaegerTool, ...).
type TracesExecuteDefaultTool struct {
	accountId string
}

func (m TracesExecuteDefaultTool) Name() string {
	return ToolGetTracesDefault
}

func (m TracesExecuteDefaultTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m TracesExecuteDefaultTool) Description() string {
	return "Executes a trace query (unified JSON where-clause schema) against the account's configured trace provider via services-server. Supports providers without a dedicated tool, e.g. Dynatrace."
}

func (m TracesExecuteDefaultTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON query with a 'where' clause to filter traces.",
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

func (m TracesExecuteDefaultTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("traces: executing default traces tool call")

	queryBuilder, startTime, endTime, err := core.BuildTraceQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("traces_default: failed to build trace query: %w", err)
	}

	// Parse time_range from the JSON command (mirrors Jaeger tool behavior)
	if cmdMap := map[string]any{}; json.Unmarshal([]byte(input.Command), &cmdMap) == nil {
		if tr, ok := cmdMap["time_range"].(string); ok && tr != "" {
			if dur, parseErr := ParseDuration(tr); parseErr == nil {
				endTime = time.Now().UTC()
				startTime = endTime.Add(-dur)
			}
		}
	}

	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	provider, err := GetTraceProvider(nbRequestContext.AccountId)
	if err != nil || provider.Provider == "" {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to resolve trace provider for default tool", "error", err)
		return core.NBToolResponse{
			Data:   "Trace provider is not configured for this account.",
			Status: core.NBToolResponseStatusError,
		}, fmt.Errorf("traces_default: unable to resolve trace provider: %w", err)
	}

	traceProviderSource := "agent"
	if provider.IntegrationSource != "" {
		traceProviderSource = provider.IntegrationSource
	}

	config := map[string]any{
		"end_time":   endTime.UnixNano() / int64(time.Millisecond),
		"start_time": startTime.UnixNano() / int64(time.Millisecond),
	}

	queryResponse, err := executeFetchTrace(nbRequestContext, provider.Provider, traceProviderSource, "", queryBuilder, config)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to get traces (default tool)", "error", err.Error(), "provider", provider.Provider)
		return core.NBToolResponse{
			Data:   "Trace data is unavailable for this request. " + err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	if len(queryResponse.Traces) > 100 {
		queryResponse.Traces = queryResponse.Traces[:100]
	}

	response, err := common.MarshalJson(queryResponse.Traces)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to serialize default traces to json", "error", err.Error())
		return core.NBToolResponse{}, fmt.Errorf("traces_default: failed to serialize traces: %w", err)
	}

	resp := core.NBToolResponse{
		Data:   string(response),
		Type:   core.NBToolResponseTypeTable,
		Status: core.NBToolResponseStatusSuccess,
	}

	resp.References = []core.NBToolResponseReference{
		core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "traces"}, "Traces Details", nil, ""),
	}

	return resp, nil
}
