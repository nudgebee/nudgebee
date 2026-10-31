package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"time"
)

const ToolOpenObserveLogsExecute = "openobserve_logs_execute"

func init() {
	core.RegisterNBToolFactory(ToolOpenObserveLogsExecute, func(accountId string) (core.NBTool, error) {
		return OpenObserveLogsExecuteTool{}, nil
	})
}

type OpenObserveLogsExecuteTool struct{}

func (OpenObserveLogsExecuteTool) Name() string             { return ToolOpenObserveLogsExecute }
func (OpenObserveLogsExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (OpenObserveLogsExecuteTool) Description() string {
	return `Queries OpenObserve logs with read-only SQL against the configured log stream. Provide one SELECT statement or a WHERE predicate. Example: {"command":"SELECT * FROM default WHERE severity >= 17 ORDER BY _timestamp DESC LIMIT 100","range":"1h"}. Use start_time/end_time as RFC3339 or Unix timestamps, or range such as "1h"; the default window is the last hour. Returns up to 100 rows.`
}

func (OpenObserveLogsExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: `A single read-only SQL SELECT from the configured log stream, or a WHERE predicate for that stream. Example: SELECT * FROM default WHERE severity >= 17 ORDER BY _timestamp DESC LIMIT 100.`,
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
				Description: `Relative query window such as "15m", "1h", or "2d"; defaults to the last hour.`,
			},
		},
		Required: []string{"command"},
	}
}

func (t OpenObserveLogsExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext = nbRequestContext.ForTarget()
	cfg, err := getOpenObserveConfig(nbRequestContext)
	if err != nil {
		return openObserveToolError("logs", "could not load the OpenObserve integration. Configure its URL, organization, username, and password", err)
	}

	sql, err := prepareOpenObserveSQL(input.Command, cfg.LogStream)
	if err != nil {
		return openObserveToolError("logs", "the query is invalid. Use one read-only SELECT or WHERE predicate for the configured log stream", err)
	}

	start, end, err := openObserveTimeRange(nbRequestContext, input.Arguments, time.Hour)
	if err != nil {
		return openObserveToolError("logs", "the time range is invalid. Set RFC3339 or Unix start_time/end_time values, or a range such as 1h", err)
	}

	result, err := newOpenObserveClient(cfg).search(
		nbRequestContext.GoContext(), sql, start, end, 0, "",
	)
	if err != nil {
		return openObserveToolError("logs", "the OpenObserve query failed. Check the SQL, configured stream, credentials, and time window", err)
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return openObserveToolError("logs", "the query succeeded but its response could not be encoded", err)
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (OpenObserveLogsExecuteTool) ConfigSchema(_ *security.RequestContext) core.ToolConfigSchema {
	return getOpenObserveConfigSchema()
}

func openObserveToolError(signal, guidance string, err error) (core.NBToolResponse, error) {
	wrapped := fmt.Errorf("OpenObserve %s query: %w", signal, err)
	return core.NBToolResponse{Data: fmt.Sprintf("%s: %v", guidance, err), Status: core.NBToolResponseStatusError}, wrapped
}
