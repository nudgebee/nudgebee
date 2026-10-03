package aws

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
)

// maxMetricDatapoints caps how many datapoints an evidence chart may hold when
// the query period is narrowed to match the alarm's evaluation period.
const maxMetricDatapoints = 1440

// maxInsightSeries caps how many series get an insight line, so a wide query
// cannot flood the evidence card.
const maxInsightSeries = 5

// alarmEvaluation carries the parameters CloudWatch itself used when it decided
// the alarm state: the evaluation period and the threshold it compared against.
// Both come from detail.state.reasonData, with the period falling back to
// detail.configuration.metrics[].metricStat.period.
type alarmEvaluation struct {
	PeriodSeconds int64
	Threshold     float64
	HasThreshold  bool
}

// extractAlarmEvaluation parses the evaluation period and threshold out of a
// CloudWatch Alarm State Change event. It returns a zero value for events that
// are not alarm state changes.
func extractAlarmEvaluation(ebEvent EventBridgeEvent) alarmEvaluation {
	var eval alarmEvaluation

	var detail map[string]any
	if err := common.UnmarshalJson(ebEvent.Detail, &detail); err != nil {
		return eval
	}

	if state, ok := detail["state"].(map[string]any); ok {
		// reasonData is a JSON document embedded as a string.
		if reasonData, ok := state["reasonData"].(string); ok && reasonData != "" {
			var rd map[string]any
			if err := common.UnmarshalJson([]byte(reasonData), &rd); err == nil {
				if period, ok := anyToFloat64(rd["period"]); ok && period > 0 {
					eval.PeriodSeconds = int64(period)
				}
				if threshold, ok := anyToFloat64(rd["threshold"]); ok {
					eval.Threshold = threshold
					eval.HasThreshold = true
				}
			}
		}
	}

	if eval.PeriodSeconds == 0 {
		config, _ := detail["configuration"].(map[string]any)
		metrics, _ := config["metrics"].([]any)
		for _, raw := range metrics {
			metric, _ := raw.(map[string]any)
			if metric == nil {
				continue
			}
			metricStat, _ := metric["metricStat"].(map[string]any)
			if metricStat == nil {
				continue
			}
			if period, ok := anyToFloat64(metricStat["period"]); ok && period > 0 {
				eval.PeriodSeconds = int64(period)
				break
			}
		}
	}

	return eval
}

// buildActionEvidenceInsight returns the insight lines for an action's evidence
// card. An insight has to say something about the data that was fetched — the
// action's name and description are static per rule and already carried in
// additional_info, so action types with nothing to report get no insight rather
// than an echo of their own definition.
func buildActionEvidenceInsight(ebEvent EventBridgeEvent, actionDef ActionDefinition, actionResult any) []string {
	switch actionDef.Type {
	case "aws_get_metric":
		metrics, ok := actionResult.(providers.QueryMetricsResponse)
		if !ok {
			return nil
		}
		return metricSeriesInsight(metrics, extractAlarmEvaluation(ebEvent))
	default:
		return nil
	}
}

// metricSeriesInsight summarises each returned series — peak, when it peaked,
// and the mean — and states the alarm threshold next to the observed range.
// The threshold line is phrased as a comparison rather than a verdict because
// the alarm's comparison operator is not carried on the event, so "breached"
// cannot be asserted for LessThanThreshold alarms.
func metricSeriesInsight(metrics providers.QueryMetricsResponse, eval alarmEvaluation) []string {
	var lines []string

	for i, item := range metrics.Items {
		if i >= maxInsightSeries {
			lines = append(lines, fmt.Sprintf("%d further series omitted.", len(metrics.Items)-maxInsightSeries))
			break
		}

		label := item.Name
		if item.Statistics != "" {
			label = fmt.Sprintf("%s (%s)", item.Name, item.Statistics)
		}

		if len(item.Values) == 0 {
			lines = append(lines, fmt.Sprintf("%s returned no datapoints between %s and %s.",
				label,
				metrics.StartDate.UTC().Format(time.RFC3339),
				metrics.EndDate.UTC().Format(time.RFC3339)))
			continue
		}

		peak, low, sum, peakIdx := item.Values[0], item.Values[0], 0.0, 0
		for j, v := range item.Values {
			if v > peak {
				peak, peakIdx = v, j
			}
			if v < low {
				low = v
			}
			sum += v
		}
		mean := sum / float64(len(item.Values))

		line := fmt.Sprintf("%s peaked at %s", label, formatMetricValue(peak))
		if peakIdx < len(item.Timestamps) {
			line += " at " + item.Timestamps[peakIdx].UTC().Format(time.RFC3339)
		}
		line += fmt.Sprintf("; mean %s over %d datapoints.", formatMetricValue(mean), len(item.Values))
		lines = append(lines, line)

		if eval.HasThreshold {
			lines = append(lines, fmt.Sprintf("Alarm threshold %s; series ranged %s to %s.",
				formatMetricValue(eval.Threshold), formatMetricValue(low), formatMetricValue(peak)))
		}
	}

	return lines
}

// formatMetricValue renders a metric value at a readable precision: two decimals
// for ordinary magnitudes, significant digits for values that would otherwise
// round to zero, and no decimals for large counters.
func formatMetricValue(v float64) string {
	switch av := math.Abs(v); {
	case av == 0:
		return "0"
	case av < 0.01:
		return strconv.FormatFloat(v, 'g', 3, 64)
	case av < 1000:
		return strconv.FormatFloat(v, 'f', 2, 64)
	default:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
}
