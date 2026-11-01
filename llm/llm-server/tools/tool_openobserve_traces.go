package tools

import (
	"errors"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"time"
)

const ToolOpenObserveTracesExecute = "openobserve_traces_execute"

func init() {
	core.RegisterNBToolFactory(ToolOpenObserveTracesExecute, func(accountId string) (core.NBTool, error) {
		return OpenObserveTracesExecuteTool{}, nil
	})
}

type OpenObserveTracesExecuteTool struct{}

func (OpenObserveTracesExecuteTool) Name() string             { return ToolOpenObserveTracesExecute }
func (OpenObserveTracesExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (OpenObserveTracesExecuteTool) Description() string {
	return `Queries OpenObserve traces with read-only SQL against the configured trace stream. Provide one SELECT statement or a WHERE predicate. Example: {"command":"SELECT trace_id, span_id, reference_parent_span_id, operation_name, start_time, duration FROM default WHERE trace_id = 'abc123' ORDER BY start_time ASC LIMIT 100","range":"12h"}. Use start_time/end_time as RFC3339 or Unix timestamps, or range such as "12h"; the default window is the last 12 hours. Returns up to 100 rows.`
}

func (OpenObserveTracesExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: `A single read-only SQL SELECT from the configured trace stream, or a WHERE predicate for that stream. Example: SELECT trace_id, span_id, operation_name, start_time, duration FROM default WHERE trace_id = 'abc123' ORDER BY start_time ASC LIMIT 100.`,
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Inclusive query start time as RFC3339 or Unix timestamp.",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Inclusive query end time as RFC3339 or Unix timestamp.",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: `Relative query window such as "15m", "12h", or "2d"; defaults to the last 12 hours.`,
			},
		},
		Required: []string{"command"},
	}
}

func (OpenObserveTracesExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext = nbRequestContext.ForTarget()
	if nbRequestContext.AccountId == "" {
		return openObserveToolError("traces", "the target account is unavailable; retry with an authenticated account", errors.New("missing target account ID"))
	}
	cfg, err := getOpenObserveConfig(nbRequestContext)
	if err != nil {
		return openObserveToolError("traces", "could not load the OpenObserve integration. Configure its URL, organization, username, and password", err)
	}

	sql, err := prepareOpenObserveSQL(input.Command, cfg.TraceStream)
	if err != nil {
		return openObserveToolError("traces", "the query is invalid. Use one read-only SELECT or WHERE predicate for the configured trace stream", err)
	}

	start, end, err := openObserveTimeRange(nbRequestContext, input.Arguments, 12*time.Hour)
	if err != nil {
		return openObserveToolError("traces", "the time range is invalid. Set RFC3339 or Unix start_time/end_time values, or a range such as 12h", err)
	}

	result, err := newOpenObserveClient(cfg).search(
		nbRequestContext.GoContext(), sql, start, end, 0, "traces",
	)
	if err != nil {
		return openObserveToolError("traces", "the OpenObserve query failed. Check the SQL, configured stream, credentials, and time window", err)
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return openObserveToolError("traces", "the query succeeded but its response could not be encoded", err)
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (OpenObserveTracesExecuteTool) ConfigSchema(_ *security.RequestContext) core.ToolConfigSchema {
	return getOpenObserveConfigSchema()
}
