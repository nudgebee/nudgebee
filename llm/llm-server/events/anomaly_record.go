package events

import (
	"encoding/json"
	"fmt"
	"math"
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
	// card is the title of the event's metric evidence card for the same metric
	// (anomoly.collectEvidences); its metadata.query is the query the key ran.
	card string
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
	"Latency":   {key: "workload_http_latency_p99", desc: "p99 HTTP request latency", unit: "s", card: "LATENCY Metric"},
	"Memory":    {key: "memory_usage_max_pod", desc: "largest pod memory working set", unit: "bytes", card: "MEMORY Metric"},
	"CPU":       {key: "cpu_usage", desc: "CPU usage", card: "CPU Metric"},
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
	AnomalyType             string   `json:"anomaly_type"`
	Metric                  string   `json:"metric,omitempty"`
	MetricDescription       string   `json:"metric_description,omitempty"`
	MetricCard              string   `json:"metric_card,omitempty"`
	Unit                    string   `json:"unit,omitempty"`
	CurrentValue            *float64 `json:"current_value,omitempty"`
	ReportedValueAt         string   `json:"reported_value_at,omitempty"`
	EvaluatedAt             string   `json:"evaluated_at,omitempty"`
	TriggerThreshold        *float64 `json:"trigger_threshold,omitempty"`
	EvaluationWindowMinutes *float64 `json:"evaluation_window_minutes,omitempty"`
	EvaluationWindow        string   `json:"evaluation_window,omitempty"`
	SeriesStart             string   `json:"series_start,omitempty"`
	SeriesEnd               string   `json:"series_end,omitempty"`
	SeriesEndsBeforeWindow  bool     `json:"series_ends_before_window,omitempty"`
	// WindowCardHasData says whether the metric card of the same metric, which
	// queried the evaluation window when the event was created, holds data. Nil
	// when the event has no such card.
	WindowCardHasData *bool              `json:"window_card_has_data,omitempty"`
	TrainingWindow    string             `json:"training_window,omitempty"`
	Baseline          map[string]float64 `json:"baseline,omitempty"`
	AnomalousPoints   int                `json:"anomalous_points"`
	FirstAnomalousAt  string             `json:"first_anomalous_at,omitempty"`
	LastAnomalousAt   string             `json:"last_anomalous_at,omitempty"`
	PeakValue         *float64           `json:"peak_value,omitempty"`
	PeakAt            string             `json:"peak_at,omitempty"`
	Insights          []string           `json:"insights,omitempty"`
	// LabelValue is the event's anomaly_current label when it does not match the
	// record's reported value; the two can come from different detector runs.
	LabelValue string `json:"label_value,omitempty"`
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
		s.Metric, s.MetricDescription, s.Unit, s.MetricCard = m.key, m.desc, m.unit, m.card
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

	// Lead with it: stated only further down, an investigation still took the
	// stale reported value for the current symptom.
	if s.SeriesEndsBeforeWindow && s.WindowCardHasData != nil && !*s.WindowCardHasData {
		fmt.Fprintf(&b, "Unconfirmed: no data in the evaluation window supports this anomaly. The detector's stored series ends before the window, and the %s card, which queried the window, returned no data.\n", s.MetricCard)
	}
	fmt.Fprintf(&b, "%s anomaly from the anomaly detector", s.AnomalyType)
	if s.Metric != "" {
		fmt.Fprintf(&b, ", measured as %s", s.MetricDescription)
		if s.Unit != "" {
			fmt.Fprintf(&b, " in %s", s.Unit)
		}
		// The key names the metric to the platform's metrics layer, which turns
		// it into the provider's own query; read as a series name it matches
		// nothing, and an agent spent minutes searching for it.
		fmt.Fprintf(&b, " (platform metric key %s, not a series name", s.Metric)
		if s.MetricCard != "" {
			fmt.Fprintf(&b, "; the %s card's metadata.query is the query it runs", s.MetricCard)
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
			b.WriteString(" It ends before the evaluation window, so nothing stored confirms a value in that window: the reported value and the flagged points below are from earlier.")
			if s.WindowCardHasData != nil && *s.WindowCardHasData {
				fmt.Fprintf(&b, " The %s card has data for the window; the current value is there.", s.MetricCard)
			}
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
	if s.LabelValue != "" {
		fmt.Fprintf(&b, "The event's anomaly_current label is %s, which does not match the reported value above.\n", s.LabelValue)
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

// NoteWindowCard records whether the event's metric card for the same metric
// holds any data. That card queried the evaluation window, so with the detector's
// series ending earlier it is the only evidence of the window.
func (s *AnomalySummary) NoteWindowCard(cards []InvestigateDataInsight) {
	if s.MetricCard == "" {
		return
	}
	seen, has := false, false
	for _, c := range cards {
		if title, _ := c.AdditionalInfo["title"].(string); title != s.MetricCard {
			continue
		}
		// A card that cannot be read (an error, a cut payload) says nothing
		// about the window; only a readable result counts, and any one with
		// data is enough.
		if h, readable := cardHasData(c.Data); readable {
			seen, has = true, has || h
		}
	}
	if seen {
		s.WindowCardHasData = &has
	}
}

// cardHasData reports whether a metric card's series hold any finite number, and
// whether the card could be read as a query result at all. Data is the decoded
// card or its JSON text.
func cardHasData(data any) (has, readable bool) {
	m, ok := data.(map[string]any)
	if !ok {
		str, isStr := data.(string)
		if !isStr || json.Unmarshal([]byte(str), &m) != nil {
			return false, false
		}
	}
	raw, present := m["series_list_result"]
	if !present {
		return false, false
	}
	series, _ := raw.([]any)
	for _, sr := range series {
		sm, _ := sr.(map[string]any)
		values, _ := sm["values"].([]any)
		for _, v := range values {
			if f, ok := toFloat(v); ok && !math.IsNaN(f) && !math.IsInf(f, 0) {
				return true, true
			}
		}
	}
	return false, true
}

// SetLabelValue records the event's anomaly_current label when it disagrees with
// the reported value by more than rounding (labels carry two decimals). Memory
// and CPU labels carry units ("820 Mi", "1.5 cores") and are left alone.
func (s *AnomalySummary) SetLabelValue(label string) {
	label = strings.TrimSpace(label)
	v, err := strconv.ParseFloat(label, 64)
	if err != nil || s.CurrentValue == nil {
		return
	}
	if math.Abs(v-*s.CurrentValue) > 0.006 {
		s.LabelValue = label
	}
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
