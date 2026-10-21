package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
)

const ToolMetricForecast = "metric_forecast"

func init() {
	core.RegisterNBToolFactory(ToolMetricForecast, func(accountId string) (core.NBTool, error) {
		return MetricForecastTool{}, nil
	})
}

// generateForecastFn is the api-server call; tests swap it.
var generateForecastFn = services_server.GenerateForecast

// forecastMetrics mirrors api-server's forecast.Presets, which is the source of truth:
// api-server rejects a name it does not know and lists the valid ones.
var (
	forecastKinds   = []string{"deployment", "statefulset", "daemonset"}
	forecastMetrics = []string{"cpu_throttling", "cpu_saturation"}
)

// MetricForecastTool forecasts whether and when one workload metric will cross a
// threshold, through api-server's forecast_generate action: the same engine as the
// hourly CPU SLO run and the SLO dialog's Check now. It creates no event. Read-only.
type MetricForecastTool struct {
	core.ReadOnlyTool
}

func (t MetricForecastTool) Name() string             { return ToolMetricForecast }
func (t MetricForecastTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t MetricForecastTool) Description() string {
	return "Forecasts whether and when one Kubernetes workload's metric will reach a threshold, from its trend over " +
		"the last 3 days with the daily cycle taken out. metric: cpu_throttling (% of throttled CFS periods, default " +
		"threshold 25) or cpu_saturation (CPU usage as % of the limit, default 90). Use for 'will <service> hit CPU " +
		"throttling / breach its CPU SLO / run out of CPU?'. Returns an explicit status: projected, not_projected, breached, " +
		"insufficient_history (under 48h of data since the workload's last limit change), no_limit, " +
		"provider_unsupported or error. In the answer quote eta_hours, current_pct, threshold_pct and " +
		"suggested_limit (in limit_unit), with the daily table as evidence. To list services that already have a " +
		"CPU breach projected, use list_events instead."
}

func (t MetricForecastTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"workload_namespace": {Type: core.ToolSchemaTypeString, Description: "Namespace of the workload."},
			"workload_name":      {Type: core.ToolSchemaTypeString, Description: "Name of the Deployment, StatefulSet or DaemonSet (not a pod name)."},
			"kind": {
				Type: core.ToolSchemaTypeString, Enum: []any{"deployment", "statefulset", "daemonset"},
				Description: "Workload kind. Omit it when unknown; it is looked up from the cluster inventory.",
			},
			"metric": {
				Type: core.ToolSchemaTypeString, Enum: []any{"cpu_throttling", "cpu_saturation"},
				Description: "What to forecast: cpu_throttling or cpu_saturation.",
			},
			"threshold": {
				Type:        core.ToolSchemaTypeNumber,
				Description: "Threshold in percent. Default 25 for cpu_throttling, 90 for the saturations.",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Optional end of the evaluated window, unix milliseconds. Default now.",
			},
		},
		Required: []string{"workload_namespace", "workload_name", "metric"},
	}
}

// parseMetricForecastInput builds the RPC request from the call's arguments (or a
// JSON command), validating the enums. account_id is not taken from the model: it is
// the call's target account.
func parseMetricForecastInput(input core.NBToolCallRequest) (services_server.ForecastRequest, error) {
	args := map[string]any{}
	if cmd := strings.TrimSpace(input.Command); strings.HasPrefix(cmd, "{") {
		if err := common.UnmarshalJson([]byte(cmd), &args); err != nil {
			return services_server.ForecastRequest{}, fmt.Errorf("invalid command JSON: %w", err)
		}
	}
	for k, v := range input.Arguments {
		args[k] = v
	}
	str := func(key string) string {
		s, _ := args[key].(string)
		return strings.TrimSpace(s)
	}

	req := services_server.ForecastRequest{
		WorkloadNamespace: str("workload_namespace"),
		WorkloadName:      str("workload_name"),
		Kind:              strings.ToLower(str("kind")),
		Metric:            strings.ToLower(str("metric")),
	}
	if req.WorkloadNamespace == "" || req.WorkloadName == "" {
		return req, errors.New("workload_namespace and workload_name are required")
	}
	// An omitted kind is left empty: api-server resolves it from the inventory, so a
	// StatefulSet asked about by name alone is not looked up as a Deployment.
	if req.Kind != "" && !slices.Contains(forecastKinds, req.Kind) {
		return req, fmt.Errorf("kind must be one of %s, got %q", strings.Join(forecastKinds, ", "), req.Kind)
	}
	if !slices.Contains(forecastMetrics, req.Metric) {
		return req, fmt.Errorf("metric must be one of %s, got %q", strings.Join(forecastMetrics, ", "), req.Metric)
	}
	if v, ok := numberArg(args["threshold"]); ok {
		if v <= 0 || v > 100 {
			return req, fmt.Errorf("threshold must be a percentage in (0, 100], got %v", v)
		}
		req.Threshold = &v
	}
	if v, ok := numberArg(args["end_time"]); ok && v > 0 {
		req.EndTime = int64(v)
	}
	return req, nil
}

// numberArg reads a JSON number, or a numeric string a model sometimes sends.
func numberArg(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		var f float64
		if _, err := fmt.Sscan(strings.TrimSpace(n), &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func (t MetricForecastTool) Call(nbCtx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbCtx = nbCtx.ForTarget()
	req, err := parseMetricForecastInput(input)
	if err != nil {
		return core.NBToolResponse{Status: core.NBToolResponseStatusError, Data: "metric_forecast: " + err.Error()}, nil
	}
	req.AccountId = nbCtx.AccountId
	if nbCtx.Ctx == nil {
		return core.NBToolResponse{Status: core.NBToolResponseStatusError, Data: "metric_forecast: request context is missing"}, nil
	}

	data, err := generateForecastFn(*nbCtx.Ctx, req)
	if errors.Is(err, services_server.ErrForecastForbidden) {
		return core.NBToolResponse{
			Status: core.NBToolResponseStatusError,
			Data: "metric_forecast: not available for this account (" + err.Error() + "): no access to its metrics. " +
				"Do not retry; tell the user.",
		}, nil
	}
	if err != nil {
		nbCtx.Ctx.GetLogger().Error("metric_forecast: forecast request failed", "error", err,
			"namespace", req.WorkloadNamespace, "workload", req.WorkloadName, "metric", req.Metric)
		return core.NBToolResponse{Status: core.NBToolResponseStatusError, Data: "metric_forecast: " + err.Error()}, nil
	}

	// A forecast whose engine failed comes back as status "error"; every other
	// status is an answer, including "no breach projected" and "not enough data".
	status := core.NBToolResponseStatusSuccess
	var result struct {
		Status string `json:"status"`
	}
	if err := common.UnmarshalJson(data, &result); err == nil && result.Status == "error" {
		status = core.NBToolResponseStatusError
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: status}, nil
}
