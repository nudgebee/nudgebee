package tools

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
	"time"
)

const ToolTracesExecuteV2 = "traces_execute_v2"

func init() {
	core.RegisterNBToolFactory(ToolTracesExecuteV2, func(accountId string) (core.NBTool, error) {
		return NBTraceToolV2{accountId: accountId}, nil
	})
}

// NBTraceToolV2 is the integration-agnostic (v2) trace query tool. It accepts the
// canonical Nudgebee trace JSON `where`-clause schema and sends it to services-server
// with an EMPTY provider — services-server resolves the account's default trace provider
// and translates the canonical query per that provider's label mapping. This is the
// traces analog of logs_execute_v2; use it via TracesDefaultAgentV2. It is NOT added to
// any generic planner's tool list.
type NBTraceToolV2 struct {
	accountId string
}

func (m NBTraceToolV2) Name() string {
	return ToolTracesExecuteV2
}

func (m NBTraceToolV2) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m NBTraceToolV2) Description() string {
	return "Executes a trace query using the canonical, provider-independent JSON where-clause schema. Services-server resolves the account's trace provider and translates the query, so the same schema works for every backend."
}

func (m NBTraceToolV2) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Canonical JSON query with a 'where' clause to filter traces.",
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

func (m NBTraceToolV2) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("traces: executing canonical (v2) traces tool call")

	queryBuilder, startTime, endTime, err := core.BuildTraceQueryBuilder(nbRequestContext, input.Command)
	if err != nil {
		return core.NBToolResponse{}, fmt.Errorf("traces_v2: failed to build trace query: %w", err)
	}

	// Parse time_range from the JSON command (mirrors the default/Jaeger tools).
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

	configs := map[string]any{
		"end_time":   endTime.UnixMilli(),
		"start_time": startTime.UnixMilli(),
	}

	queryResponse, err := executeFetchTraceCanonical(nbRequestContext, queryBuilder, configs)
	if err != nil {
		// Surface the failure as a real error. Do NOT synthesize a "no traces" /
		// "unable to retrieve" string here — those are the fallbackTracesAgent
		// soft-completion sentinels and would mask a translation failure as clean
		// no-data (there is no kubectl fallback for traces).
		nbRequestContext.Ctx.GetLogger().Error("traces: canonical (v2) query failed", "error", err.Error())
		return core.NBToolResponse{
			Data:   "Trace query failed: " + err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// A genuine empty result set is a normal success (the LLM will report no matches).
	if len(queryResponse.Traces) > maxTracesInResponse {
		queryResponse.Traces = queryResponse.Traces[:maxTracesInResponse]
	}

	response, err := common.MarshalJson(queryResponse.Traces)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("traces: unable to serialize canonical (v2) traces to json", "error", err.Error())
		return core.NBToolResponse{}, fmt.Errorf("traces_v2: failed to serialize traces: %w", err)
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
