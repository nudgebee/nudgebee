package tools

import (
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const ToolSplunkMetricsQuery = "splunk_metrics_query"

// splunkEnterpriseMetricProvider is the provider key the api-server registers its
// Splunk Enterprise metric source under. Sending it explicitly pins the query to that
// source, the same way es_metrics_query pins "ES".
const splunkEnterpriseMetricProvider = "splunk_enterprise"

// splunkMetricsValueColumn is the column the api-server reads each data point from.
// mstats otherwise names the value after the aggregation (`avg(k8s.pod.cpu.usage)`),
// and a row without this column is dropped as "not a data point".
const splunkMetricsValueColumn = "nb_value"

var splunkMetricsValueAlias = regexp.MustCompile(`(?i)\bAS\s+` + splunkMetricsValueColumn + `\b`)

func init() {
	core.RegisterNBToolFactory(ToolSplunkMetricsQuery, func(accountId string) (core.NBTool, error) {
		return SplunkMetricsQueryTool{}, nil
	})
}

// SplunkMetricsQueryTool runs a raw SPL `| mstats` search against the account's Splunk
// Enterprise metrics index. It exists because prometheus_execute validates PromQL before
// sending anything, while the Splunk metric source only accepts SPL — so no query could
// satisfy both.
type SplunkMetricsQueryTool struct{}

func (m SplunkMetricsQueryTool) Name() string {
	return ToolSplunkMetricsQuery
}

func (m SplunkMetricsQueryTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m SplunkMetricsQueryTool) Description() string {
	return `Executes a Splunk SPL mstats search against the Splunk Enterprise metrics index.

	Usage:
	* Input: "command" is the SPL, starting with "| mstats". It MUST alias its value "AS nb_value" and name the metrics index as index="<index>". Pass the time window via "range" (e.g. "15m", "1h") or "start_time"/"end_time" — not earliest/latest inside the SPL.
	* Output: Metric time-series (labels, timestamps, values) with per-series min/max/avg.
	* Not for discovery: which metrics, dimensions or values exist comes from ` + ToolMetricsSeriesMatch + `, ` + ToolMetricsList + `, ` + ToolMetricsLabelsList + ` and ` + ToolMetricsLabelValues + `.`
}

func (m SplunkMetricsQueryTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: `SPL metrics search, e.g. | mstats avg(k8s.pod.cpu.usage) AS nb_value WHERE index="otel_metrics" AND k8s.namespace.name="default" span=1m BY k8s.pod.name`,
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range ending now (e.g. '15m', '1h', '1d'). Default 1h.",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
		},
		Required: []string{"command"},
	}
}

// normaliseSplunkMetricsCommand strips the wrappers models put around SPL: markdown
// code fences, inline backticks, and a whole tool input nested inside `command`.
func normaliseSplunkMetricsCommand(input string) string {
	s := strings.TrimSpace(input)
	if strings.HasPrefix(s, "{") {
		var nested map[string]any
		if err := common.UnmarshalJson([]byte(s), &nested); err == nil {
			if inner, ok := nested["command"].(string); ok {
				s = strings.TrimSpace(inner)
			}
		}
	}
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(strings.Trim(s, "`"))
}

// validateSplunkMetricsSPL catches the query shapes the api-server would reject or
// silently return empty for, with a message that says how to fix them. The api-server
// still enforces its own guard (read-only commands, index scoping); this only moves the
// common mistakes ahead of the network call.
//
// `| mcatalog` is rejected even though the api-server accepts it: a catalog search
// returns one multivalue row with no value column, which the metric decoder drops, so it
// always came back empty and the agent kept rewriting it. The discovery tools answer the
// same questions from the same catalog and return the values.
func validateSplunkMetricsSPL(spl string) error {
	trimmed := strings.TrimSpace(spl)
	if trimmed == "" {
		return errors.New("empty SPL: provide an `| mstats` search")
	}
	if !strings.HasPrefix(trimmed, "|") {
		return fmt.Errorf("SPL metrics search must start with `| mstats` (got %q). This tool takes SPL, not PromQL", firstWord(trimmed))
	}
	command := strings.ToLower(firstWord(strings.TrimSpace(strings.TrimPrefix(trimmed, "|"))))
	switch command {
	case "mcatalog":
		return fmt.Errorf("`| mcatalog` is a catalog search, not a time series, and this tool cannot return its rows. Use the discovery tools instead: `%s` (metric families that have series for a named workload), `%s` (metric names by keyword), `%s` (a metric's dimensions), `%s` (a dimension's values)",
			ToolMetricsSeriesMatch, ToolMetricsList, ToolMetricsLabelsList, ToolMetricsLabelValues)
	case "mstats":
		if !splunkMetricsValueAlias.MatchString(trimmed) {
			return fmt.Errorf("mstats must alias its value `AS %s`, e.g. `| mstats avg(k8s.pod.cpu.usage) AS %s WHERE ...` — rows without that column are dropped", splunkMetricsValueColumn, splunkMetricsValueColumn)
		}
		return nil
	default:
		return fmt.Errorf("SPL metrics search may only begin with `| mstats`, got `| %s`", command)
	}
}

func firstWord(s string) string {
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

func (m SplunkMetricsQueryTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext = nbRequestContext.ForTarget()
	logger := nbRequestContext.Ctx.GetLogger()

	// A nested {"command": ..., "range": ...} carries its time window inside the
	// command; lift it so it is not lost with the wrapper.
	args := map[string]any{}
	for k, v := range input.Arguments {
		args[k] = v
	}
	if raw := strings.TrimSpace(input.Command); strings.HasPrefix(raw, "{") {
		var nested map[string]any
		if err := common.UnmarshalJson([]byte(raw), &nested); err == nil {
			for k, v := range nested {
				if _, set := args[k]; !set && k != "command" {
					args[k] = v
				}
			}
		}
	}

	spl := normaliseSplunkMetricsCommand(input.Command)
	if err := validateSplunkMetricsSPL(spl); err != nil {
		logger.Warn("splunk_metrics_query: rejected SPL before query", "spl", spl, "error", err)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Invalid SPL metrics search: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Hour)
	if t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, args); err == nil {
		startTime, endTime = t1, t2
	}
	startTime, endTime = ExpandNarrowTimeWindow(logger, startTime, endTime)

	logger.Info("splunk_metrics_query: executing", "spl", spl, "start", startTime, "end", endTime)

	response, err := services_server.QueryMetrics(*nbRequestContext.Ctx, core.ObservabilityMetricsQueryRequest{
		AccountId:      nbRequestContext.AccountId,
		MetricProvider: splunkEnterpriseMetricProvider,
		Queries:        map[string]string{uuid.New().String(): spl},
		StartTime:      startTime.UnixMilli(),
		EndTime:        endTime.UnixMilli(),
	})
	if err != nil {
		logger.Error("splunk_metrics_query: API call failed", "error", err.Error())
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Metrics query failed: %s", err.Error()),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Per-query failures (bad SPL, wrong index, Splunk errors) arrive in results[].Error
	// with a 200, so they must be surfaced here or they read as "no data".
	if errText := collectESMetricsErrors(response); errText != "" {
		logger.Warn("splunk_metrics_query: query failed", "error", errText)
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Metrics query failed: %s", errText),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	data, err := common.MarshalJson(summarizeESMetricsResponse(response))
	if err != nil {
		return core.NBToolResponse{Status: core.NBToolResponseStatusError}, fmt.Errorf("splunk_metrics_query: marshal response: %w", err)
	}
	return core.NBToolResponse{
		Data:   string(data),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
