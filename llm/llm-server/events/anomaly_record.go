package events

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// anomalyMetric is what one anomaly type measures. The keys mirror METRIC_SPECS in
// ml-k8s-server/server/anomaly/anomaly_algo/abstract.py, which owns them.
type anomalyMetric struct {
	key  string
	desc string
	// unit is left empty where the detector's numbers do not share one scale.
	unit string
}

// anomalyMetrics is keyed by the api-server AnomalyType (anomoly/model.go). Types
// not listed (Network, the spend types) get a summary without a metric or unit.
//
// CPU has no unit: the detector may hold its stats in millicores while the
// series is in cores (see generate_insights in abstract.py), so naming one unit
// would be wrong for half the numbers.
var anomalyMetrics = map[string]anomalyMetric{
	"Latency":   {key: "workload_http_latency_p99", desc: "p99 HTTP request latency", unit: "s"},
	"Memory":    {key: "memory_usage_max_pod", desc: "largest pod memory working set", unit: "bytes"},
	"CPU":       {key: "cpu_usage", desc: "CPU usage"},
	"ErrorRate": {key: "workload_http_error_count / workload_http_request_count", desc: "smoothed HTTP error ratio", unit: "ratio 0-1"},
	"Replicas":  {key: "replica_defined", desc: "desired replicas", unit: "replicas"},
}

const (
	maxAnomalySummaryInsights = 3
	maxAnomalyInsightChars    = 240
)

// AnomalySummary is the anomaly detector's result for an anomaly event, reduced to
// what an investigation needs: what was measured, the reported value, the
// threshold and the baseline it was compared against, and when the flagged
// points are. The raw series it came from (often ~1,600 points) is left out.
type AnomalySummary struct {
	AnomalyType             string             `json:"anomaly_type"`
	Metric                  string             `json:"metric,omitempty"`
	MetricDescription       string             `json:"metric_description,omitempty"`
	Unit                    string             `json:"unit,omitempty"`
	CurrentValue            *float64           `json:"current_value,omitempty"`
	ReportedValueAt         string             `json:"reported_value_at,omitempty"`
	EvaluatedAt             string             `json:"evaluated_at,omitempty"`
	TriggerThreshold        *float64           `json:"trigger_threshold,omitempty"`
	EvaluationWindowMinutes *float64           `json:"evaluation_window_minutes,omitempty"`
	EvaluationWindow        string             `json:"evaluation_window,omitempty"`
	SeriesStart             string             `json:"series_start,omitempty"`
	SeriesEnd               string             `json:"series_end,omitempty"`
	SeriesEndsBeforeWindow  bool               `json:"series_ends_before_window,omitempty"`
	TrainingWindow          string             `json:"training_window,omitempty"`
	Baseline                map[string]float64 `json:"baseline,omitempty"`
	AnomalousPoints         int                `json:"anomalous_points"`
	FirstAnomalousAt        string             `json:"first_anomalous_at,omitempty"`
	LastAnomalousAt         string             `json:"last_anomalous_at,omitempty"`
	PeakValue               *float64           `json:"peak_value,omitempty"`
	PeakAt                  string             `json:"peak_at,omitempty"`
	Insights                []string           `json:"insights,omitempty"`
}

// anomalyReference is the part of the detector's reference_value the summary reads.
type anomalyReference struct {
	Stats               map[string]any `json:"stats"`
	TriggerThresholdMax *float64       `json:"trigger_threshold_max"`
	EvaluationPeriod    *float64       `json:"evaluation_period"`
	StartTime           string         `json:"start_time"`
	EndTime             string         `json:"end_time"`
	TrainingEndTime     string         `json:"training_end_time"`
	Insights            []struct {
		Insight string `json:"insight"`
	} `json:"insights"`
	Data []struct {
		Timestamp string `json:"timestamp"`
		Value     any    `json:"data"`
		Anomaly   bool   `json:"anomaly"`
	} `json:"data"`
}

// IsAnomalyRecord reports whether an evidence entry is the detector's own result.
// api-server stores it without a type (anomoly.collectEvidences); the UI's
// AnomalyCard recognises it by is_anomaly, and so does this.
func IsAnomalyRecord(e Evidence) bool {
	return e.Type == "" && e.IsAnomaly && len(e.ReferenceValue) > 0 && string(e.ReferenceValue) != "null"
}

// SummarizeAnomalyRecord reduces the detector's result to an AnomalySummary. It
// returns false when the reference value cannot be read.
func SummarizeAnomalyRecord(e Evidence) (AnomalySummary, bool) {
	raw := []byte(e.ReferenceValue)
	// Some producers store reference_value as a JSON string.
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		raw = []byte(asString)
	}
	var ref anomalyReference
	if err := json.Unmarshal(raw, &ref); err != nil {
		return AnomalySummary{}, false
	}

	s := AnomalySummary{
		AnomalyType:             e.AnomalyType,
		CurrentValue:            e.CurrentValue,
		EvaluatedAt:             e.EvaluatedAt,
		TriggerThreshold:        ref.TriggerThresholdMax,
		EvaluationWindowMinutes: ref.EvaluationPeriod,
	}
	if m, ok := anomalyMetrics[e.AnomalyType]; ok {
		s.Metric, s.MetricDescription, s.Unit = m.key, m.desc, m.unit
	}
	if ref.StartTime != "" && ref.TrainingEndTime != "" {
		s.TrainingWindow = ref.StartTime + " to " + ref.TrainingEndTime
	}
	for _, k := range []string{"mean", "p99", "p999", "min", "max"} {
		if v, ok := toFloat(ref.Stats[k]); ok {
			if s.Baseline == nil {
				s.Baseline = map[string]float64{}
			}
			s.Baseline[k] = v
		}
	}
	// The series is not stored in time order, so first/last are by timestamp.
	// The timestamps share one layout, so they compare as strings.
	for _, p := range ref.Data {
		if p.Timestamp == "" {
			continue
		}
		if s.SeriesStart == "" || p.Timestamp < s.SeriesStart {
			s.SeriesStart = p.Timestamp
		}
		if p.Timestamp > s.SeriesEnd {
			s.SeriesEnd = p.Timestamp
		}
		if !p.Anomaly {
			continue
		}
		// api-server reports the first flagged point in array order as the
		// event's current value (anomoly.ProcessAnomaly).
		if s.AnomalousPoints == 0 {
			s.ReportedValueAt = p.Timestamp
		}
		s.AnomalousPoints++
		if s.FirstAnomalousAt == "" || p.Timestamp < s.FirstAnomalousAt {
			s.FirstAnomalousAt = p.Timestamp
		}
		if p.Timestamp > s.LastAnomalousAt {
			s.LastAnomalousAt = p.Timestamp
		}
		// A point's value can arrive as a number or a numeric string.
		if v, ok := toFloat(p.Value); ok && (s.PeakValue == nil || v > *s.PeakValue) {
			s.PeakValue, s.PeakAt = &v, p.Timestamp
		}
	}
	if end, ok := parseSeriesTime(ref.EndTime); ok && ref.EvaluationPeriod != nil {
		start := end.Add(-time.Duration(*ref.EvaluationPeriod * float64(time.Minute)))
		s.EvaluationWindow = start.UTC().Format(time.RFC3339) + " to " + end.UTC().Format(time.RFC3339)
		if last, ok := parseSeriesTime(s.SeriesEnd); ok && last.Before(start) {
			s.SeriesEndsBeforeWindow = true
		}
	}
	for _, in := range ref.Insights {
		msg := strings.TrimSpace(in.Insight)
		if msg == "" {
			continue
		}
		if len(msg) > maxAnomalyInsightChars {
			cut := maxAnomalyInsightChars
			for cut > 0 && !utf8.RuneStart(msg[cut]) {
				cut--
			}
			msg = msg[:cut]
		}
		s.Insights = append(s.Insights, msg)
		if len(s.Insights) == maxAnomalySummaryInsights {
			break
		}
	}
	return s, true
}

// Render writes the summary as a few plain lines for a prompt or tool preview.
func (s AnomalySummary) Render() string {
	var b strings.Builder
	num := func(v float64) string {
		out := strconv.FormatFloat(v, 'g', 4, 64)
		if s.Unit != "" {
			out += " " + s.Unit
		}
		return out
	}

	fmt.Fprintf(&b, "%s anomaly from the anomaly detector", s.AnomalyType)
	if s.Metric != "" {
		fmt.Fprintf(&b, ", metric %s (%s", s.Metric, s.MetricDescription)
		if s.Unit != "" {
			fmt.Fprintf(&b, ", unit %s", s.Unit)
		}
		b.WriteString(")")
	}
	if s.EvaluatedAt != "" {
		fmt.Fprintf(&b, ", evaluated at %s", s.EvaluatedAt)
	}
	b.WriteString(".\n")

	var parts []string
	if s.CurrentValue != nil {
		reported := "reported value: " + num(*s.CurrentValue)
		if s.ReportedValueAt != "" {
			reported += " (the first flagged point, at " + s.ReportedValueAt + ")"
		}
		parts = append(parts, reported)
	}
	if s.TriggerThreshold != nil {
		parts = append(parts, "trigger threshold: "+num(*s.TriggerThreshold))
	}
	switch {
	case s.EvaluationWindow != "":
		parts = append(parts, "evaluation window: "+s.EvaluationWindow)
	case s.EvaluationWindowMinutes != nil:
		parts = append(parts, fmt.Sprintf("evaluation window: last %s min", strconv.FormatFloat(*s.EvaluationWindowMinutes, 'f', -1, 64)))
	}
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, "; ") + ".\n")
	}

	if len(s.Baseline) > 0 {
		keys := make([]string, 0, len(s.Baseline))
		for k := range s.Baseline {
			keys = append(keys, k)
		}
		order := map[string]int{"mean": 0, "p99": 1, "p999": 2, "min": 3, "max": 4}
		sort.Slice(keys, func(i, j int) bool { return order[keys[i]] < order[keys[j]] })
		stats := make([]string, 0, len(keys))
		for _, k := range keys {
			stats = append(stats, k+" "+num(s.Baseline[k]))
		}
		b.WriteString("Baseline")
		if s.TrainingWindow != "" {
			fmt.Fprintf(&b, " (training window %s)", s.TrainingWindow)
		}
		b.WriteString(": " + strings.Join(stats, ", ") + ".\n")
	}

	if s.SeriesEnd != "" {
		fmt.Fprintf(&b, "Stored series: %s to %s.", s.SeriesStart, s.SeriesEnd)
		if s.SeriesEndsBeforeWindow {
			b.WriteString(" It ends before the evaluation window, so the points evaluated in that window are not stored and the flagged points below are from earlier.")
		}
		b.WriteString("\n")
	}
	if s.AnomalousPoints > 0 {
		fmt.Fprintf(&b, "Anomalous points: %d, from %s to %s", s.AnomalousPoints, s.FirstAnomalousAt, s.LastAnomalousAt)
		if s.PeakValue != nil {
			fmt.Fprintf(&b, ", peak %s at %s", num(*s.PeakValue), s.PeakAt)
		}
		b.WriteString(".\n")
	}
	for _, in := range s.Insights {
		b.WriteString("Detector insight: " + in + "\n")
	}
	return b.String()
}

// parseSeriesTime reads a series timestamp: the detector writes
// "2006-01-02 15:04:05" in UTC; RFC 3339 is accepted too.
func parseSeriesTime(ts string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}
