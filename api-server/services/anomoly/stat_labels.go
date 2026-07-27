package anomoly

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Alerts show a couple of numbers ("current vs baseline"), but notifications
// never see event evidences: the post-process consumer nils Evidences before
// building the map it publishes, so anything an alert needs must ride on
// Labels, which survive that step. These keys are the contract with
// notifications-server (message_templates/slack/grouped_anomaly_notification.py).
const (
	LabelAnomalyCurrent  = "anomaly_current"
	LabelAnomalyBaseline = "anomaly_baseline"
	LabelAnomalyZScore   = "anomaly_zscore"
	LabelAnomalyChange   = "anomaly_change"
)

// metricStatLabels renders the observed value and, when the reference data
// carries one, the baseline it deviated from.
func metricStatLabels(anomaly *Anomaly) map[string]string {
	if anomaly == nil {
		return nil
	}
	labels := map[string]string{
		LabelAnomalyCurrent: formatMetricValue(anomaly.AnomalyType, anomaly.CurrentValue),
	}
	if baseline, ok := metricBaseline(anomaly.OldValue); ok {
		labels[LabelAnomalyBaseline] = formatMetricValue(anomaly.AnomalyType, baseline)
	}
	return labels
}

// spendStatLabels renders the money figures behind a spend anomaly. The
// observed-vs-baseline sentence already reaches the channel through the event
// description, so this adds what that sentence drops.
func spendStatLabels(anomaly *Anomaly, money func(float64) string) map[string]string {
	if anomaly == nil || money == nil {
		return nil
	}
	labels := map[string]string{
		LabelAnomalyCurrent: money(anomaly.CurrentValue),
	}
	if zScore, ok := toFloat(anomaly.OldValue["z_score"]); ok {
		labels[LabelAnomalyZScore] = strconv.FormatFloat(zScore, 'f', 2, 64)
	}
	mean, hasMean := toFloat(anomaly.OldValue["mean"])
	if hasMean {
		labels[LabelAnomalyBaseline] = money(mean)
	}
	if pctChange, ok := toFloat(anomaly.OldValue["pct_change"]); ok && hasMean {
		change := anomaly.CurrentValue - mean
		sign := "+"
		if change < 0 {
			sign = "-"
			change = -change
			pctChange = math.Abs(pctChange)
		}
		labels[LabelAnomalyChange] = fmt.Sprintf("%s%s (%s%.1f%%)", sign, money(change), sign, pctChange)
	}
	return labels
}

// metricBaseline digs the comparison value out of the reference map. The two
// detectors shape it differently: the ML server nests it under insights, while
// the Prometheus path writes a historical_<N>_days_value key.
func metricBaseline(reference map[string]any) (float64, bool) {
	if reference == nil {
		return 0, false
	}

	for _, insight := range asMaps(reference["insights"]) {
		if value, ok := toFloat(insight["baseline_value"]); ok {
			return value, true
		}
	}

	for key, value := range reference {
		if strings.HasPrefix(key, "historical_") && strings.HasSuffix(key, "_days_value") {
			if parsed, ok := toFloat(value); ok {
				return parsed, true
			}
		}
	}

	return 0, false
}

// asMaps normalizes a slice of structs that may have been marshalled as
// []any or kept as []map[string]any.
func asMaps(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		maps := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				maps = append(maps, m)
			}
		}
		return maps
	}
	return nil
}

// toFloat accepts every shape a reference value arrives in: native numbers from
// the in-memory struct, json.Number from a decoder configured with UseNumber,
// and the strings some producers persist. json.Number is a named string type,
// so it needs its own case — the string case below does not match it.
func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	}
	return 0, false
}

func formatMetricValue(anomalyType AnomalyType, value float64) string {
	switch anomalyType {
	case MetricAnomolyTypeMemory:
		return formatBytes(value)
	case MetricAnomolyTypeCPU:
		return formatCores(value)
	default:
		// Units for the remaining metrics aren't consistent across detectors,
		// so show the bare number rather than assert a wrong one.
		return trimFloat(value)
	}
}

func formatBytes(value float64) string {
	const mib = 1024 * 1024
	switch {
	case math.Abs(value) >= 1024*mib:
		return trimFloat(value/(1024*mib)) + " Gi"
	case math.Abs(value) >= mib:
		return trimFloat(value/mib) + " Mi"
	default:
		return trimFloat(value) + " B"
	}
}

func formatCores(value float64) string {
	if math.Abs(value) < 1 {
		return fmt.Sprintf("%dm", int(math.Round(value*1000)))
	}
	trimmed := trimFloat(value)
	if trimmed == "1" {
		return "1 core"
	}
	return trimmed + " cores"
}

func trimFloat(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', 2, 64)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(formatted, "0")
		formatted = strings.TrimSuffix(formatted, ".")
	}
	if formatted == "" || formatted == "-" {
		return "0"
	}
	return formatted
}
