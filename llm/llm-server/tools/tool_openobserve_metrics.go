package tools

import (
	"errors"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
)

const ToolOpenObserveMetricsExecute = "openobserve_metrics_execute"

func init() {
	core.RegisterNBToolFactory(ToolOpenObserveMetricsExecute, func(accountId string) (core.NBTool, error) {
		return OpenObserveMetricsExecuteTool{}, nil
	})
}

type OpenObserveMetricsExecuteTool struct{}

func (OpenObserveMetricsExecuteTool) Name() string { return ToolOpenObserveMetricsExecute }

func (OpenObserveMetricsExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (OpenObserveMetricsExecuteTool) Description() string {
	return `Queries OpenObserve metrics with a PromQL expression. Example: {"command":"sum(rate(http_requests_total[5m]))","range":"1h"}. Set start_time/end_time as RFC3339 or Unix timestamps, or use a relative range such as "1h"; the default is the last hour. OpenObserve chooses the range resolution.`
}

func (OpenObserveMetricsExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: `PromQL expression to execute, e.g. sum(rate(http_requests_total[5m])).`,
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

func (OpenObserveMetricsExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext = nbRequestContext.ForTarget()
	if nbRequestContext.Ctx == nil || nbRequestContext.Ctx.GetSecurityContext() == nil {
		return openObserveToolError("metrics", "the request context is unavailable; retry from an authenticated account", errors.New("missing request or security context"))
	}

	query, arguments := extractPromQLFromCommand(input.Command, input.Arguments)
	query = strings.TrimSpace(query)
	if syntaxError := validatePromQLSyntax(query); syntaxError != "" {
		return openObserveToolError("metrics", "the query is invalid. Provide a valid PromQL expression", errors.New(syntaxError))
	}

	start, end, err := openObserveTimeRange(nbRequestContext, arguments, time.Hour)
	if err != nil {
		return openObserveToolError("metrics", "the time range is invalid. Set RFC3339 or Unix start_time/end_time values, or a range such as 1h", err)
	}

	response, err := services_server.QueryMetrics(*nbRequestContext.Ctx, core.ObservabilityMetricsQueryRequest{
		AccountId:      nbRequestContext.AccountId,
		MetricProvider: "openobserve",
		Queries:        map[string]string{"query": query},
		StartTime:      start.UnixMilli(),
		EndTime:        end.UnixMilli(),
	})
	if err != nil {
		return openObserveToolError("metrics", "the OpenObserve query failed. Check the integration, PromQL syntax, and time window", err)
	}
	normalizeOpenObserveMetricTimestamps(&response)
	result, err := (PrometheusExecuteTool{}).seriesFromMetricsResponse(response)
	if err != nil {
		return openObserveToolError("metrics", "the OpenObserve query failed. Check the metric name, labels, and time window", err)
	}

	data, err := common.MarshalJson(result)
	if err != nil {
		return openObserveToolError("metrics", "the query succeeded but its response could not be encoded", err)
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

func (OpenObserveMetricsExecuteTool) ConfigSchema(_ *security.RequestContext) core.ToolConfigSchema {
	return getOpenObserveConfigSchema()
}

// OpenObserve's API returns timestamps in milliseconds; the metrics tool contract uses seconds.
func normalizeOpenObserveMetricTimestamps(response *core.ObservabilityMetricsQueryResponse) {
	for resultIndex := range response.Results {
		for seriesIndex := range response.Results[resultIndex].Payload {
			timestamps := response.Results[resultIndex].Payload[seriesIndex].Timestamps
			for i := range timestamps {
				timestamps[i] /= 1000
			}
		}
	}
}
