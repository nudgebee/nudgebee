package events

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anomalyRecordJSON is the detector's result as api-server stores it in an anomaly
// event's evidences: the Anomaly struct, no type, reference_value as an object.
// Shape taken from dev latency anomalies; names and ids are synthetic.
func anomalyRecordJSON(t *testing.T, anomalyType string, reference map[string]any) map[string]any {
	t.Helper()
	return map[string]any{
		"id":              "00000000-0000-0000-0000-000000000001",
		"account_id":      "00000000-0000-0000-0000-000000000002",
		"name":            "checkout",
		"namespace":       "shop",
		"anomaly_type":    anomalyType,
		"is_anomaly":      true,
		"current_value":   0.89,
		"evaluated_at":    "2026-10-10T05:01:47Z",
		"reference_value": reference,
	}
}

func latencyReference() map[string]any {
	data := []any{}
	// 1,000 normal points, then three anomalous ones, out of time order as the
	// detector stores them.
	for range 1000 {
		data = append(data, map[string]any{"timestamp": "2026-10-09 00:00:00", "data": 0.03, "anomaly": false, "anomaly_score": 0})
	}
	data = append(data,
		map[string]any{"timestamp": "2026-10-10 04:58:00", "data": 0.89, "anomaly": true, "anomaly_score": 0.9},
		map[string]any{"timestamp": "2026-10-10 04:56:00", "data": 0.61, "anomaly": true, "anomaly_score": 0.7},
		map[string]any{"timestamp": "2026-10-10 05:00:00", "data": 0.72, "anomaly": true, "anomaly_score": 0.8},
		map[string]any{"timestamp": "2026-10-10 05:02:00", "data": nil, "anomaly": false, "anomaly_score": 0},
	)
	return map[string]any{
		"stats":                 map[string]any{"mean": 0.029, "p99": 0.21, "p999": 2.4, "min": 0.005, "max": 4.8, "count": 3265},
		"trigger_threshold_max": 0.5,
		"evaluation_period":     60,
		"start_time":            "2026-10-03T05:00:00Z",
		"end_time":              "2026-10-10T05:00:00Z",
		"training_end_time":     "2026-10-10 04:00:00",
		"insights":              []any{},
		"has_anomaly":           true,
		"data":                  data,
	}
}

func decodeEvidence(t *testing.T, v any) Evidence {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var e Evidence
	require.NoError(t, json.Unmarshal(raw, &e))
	return e
}

func TestSummarizeAnomalyRecord_Latency(t *testing.T) {
	e := decodeEvidence(t, anomalyRecordJSON(t, "Latency", latencyReference()))
	require.True(t, IsAnomalyRecord(e))

	s, ok := SummarizeAnomalyRecord(e)
	require.True(t, ok)
	assert.Equal(t, "workload_http_latency_p99", s.Metric)
	assert.Equal(t, "s", s.Unit)
	assert.InDelta(t, 0.89, *s.CurrentValue, 1e-9)
	assert.InDelta(t, 0.5, *s.TriggerThreshold, 1e-9)
	assert.InDelta(t, 60, *s.EvaluationWindowMinutes, 1e-9)
	assert.Equal(t, map[string]float64{"mean": 0.029, "p99": 0.21, "p999": 2.4, "min": 0.005, "max": 4.8}, s.Baseline)
	assert.Equal(t, "2026-10-03T05:00:00Z to 2026-10-10 04:00:00", s.TrainingWindow)
	assert.Equal(t, 3, s.AnomalousPoints)
	assert.Equal(t, "2026-10-10 04:58:00", s.ReportedValueAt)
	assert.Equal(t, "2026-10-10T04:00:00Z to 2026-10-10T05:00:00Z", s.EvaluationWindow)
	assert.False(t, s.SeriesEndsBeforeWindow)
	assert.Equal(t, "2026-10-10 04:56:00", s.FirstAnomalousAt)
	assert.Equal(t, "2026-10-10 05:00:00", s.LastAnomalousAt)
	assert.InDelta(t, 0.89, *s.PeakValue, 1e-9)
	assert.Equal(t, "2026-10-10 04:58:00", s.PeakAt)

	// The raw series stays out: the summary is a few hundred bytes, not ~70 KB.
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Less(t, len(raw), 1500)

	text := s.Render()
	for _, want := range []string{
		"metric workload_http_latency_p99 (p99 HTTP request latency, unit s)",
		"reported value: 0.89 s (the first flagged point, at 2026-10-10 04:58:00)",
		"trigger threshold: 0.5 s",
		"evaluation window: 2026-10-10T04:00:00Z to 2026-10-10T05:00:00Z",
		"Stored series: 2026-10-09 00:00:00 to 2026-10-10 05:02:00.\n",
		"mean 0.029 s, p99 0.21 s",
		"Anomalous points: 3, from 2026-10-10 04:56:00 to 2026-10-10 05:00:00, peak 0.89 s at 2026-10-10 04:58:00",
	} {
		assert.Contains(t, text, want)
	}
}

func TestSummarizeAnomalyRecord_ReferenceAsString(t *testing.T) {
	ref, err := json.Marshal(latencyReference())
	require.NoError(t, err)
	e := decodeEvidence(t, anomalyRecordJSON(t, "Latency", nil))
	e.ReferenceValue, err = json.Marshal(string(ref))
	require.NoError(t, err)

	s, ok := SummarizeAnomalyRecord(e)
	require.True(t, ok)
	assert.Equal(t, 3, s.AnomalousPoints)
}

func TestSummarizeAnomalyRecord_UnitsByType(t *testing.T) {
	cases := []struct {
		anomalyType, metric, unit string
	}{
		// CPU stats may be millicores while the series is cores, so no unit.
		{"CPU", "cpu_usage", ""},
		{"Memory", "memory_usage_max_pod", "bytes"},
		// Not in the table: numbers only, nothing asserted about them.
		{"Network", "", ""},
	}
	for _, c := range cases {
		t.Run(c.anomalyType, func(t *testing.T) {
			s, ok := SummarizeAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, c.anomalyType, latencyReference())))
			require.True(t, ok)
			assert.Equal(t, c.metric, s.Metric)
			assert.Equal(t, c.unit, s.Unit)
			if c.unit == "" {
				assert.Contains(t, s.Render(), "reported value: 0.89 (")
			}
		})
	}
}

func TestSummarizeAnomalyRecord_DetectorInsights(t *testing.T) {
	ref := latencyReference()
	ref["insights"] = []any{
		map[string]any{"insight": "p99 latency 0.89s is 30x the 7-day average of 0.03s", "severity": "critical"},
		map[string]any{"insight": strings.Repeat("x", 500)},
	}
	s, ok := SummarizeAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, "Latency", ref)))
	require.True(t, ok)
	require.Len(t, s.Insights, 2)
	assert.Equal(t, "p99 latency 0.89s is 30x the 7-day average of 0.03s", s.Insights[0])
	assert.Len(t, s.Insights[1], maxAnomalyInsightChars)
}

func TestIsAnomalyRecord_OnlyTheBareDetectorResult(t *testing.T) {
	assert.False(t, IsAnomalyRecord(decodeEvidence(t, map[string]any{"type": "prometheus", "data": map[string]any{}})))
	// A typed card that happens to carry is_anomaly is not the bare record.
	typed := anomalyRecordJSON(t, "Latency", latencyReference())
	typed["type"] = "metric-anomaly"
	assert.False(t, IsAnomalyRecord(decodeEvidence(t, typed)))
	notFired := anomalyRecordJSON(t, "Latency", latencyReference())
	notFired["is_anomaly"] = false
	assert.False(t, IsAnomalyRecord(decodeEvidence(t, notFired)))
}

func TestSummarizeAnomalyRecord_UnreadableReference(t *testing.T) {
	e := decodeEvidence(t, anomalyRecordJSON(t, "Latency", nil))
	e.ReferenceValue = json.RawMessage(`[1,2,3]`)
	_, ok := SummarizeAnomalyRecord(e)
	assert.False(t, ok)
}

func TestIsAnomalyRecord_NullReference(t *testing.T) {
	assert.False(t, IsAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, "Latency", nil))))
}

// Seen on most dev latency anomalies: the stored series stops hours before the
// evaluation window, and the flagged points (and so the reported value) are
// days old. The summary says so instead of presenting them as what just fired.
func TestSummarizeAnomalyRecord_SeriesEndsBeforeEvaluationWindow(t *testing.T) {
	ref := latencyReference()
	ref["end_time"] = "2026-10-11T03:00:00Z"
	ref["data"] = []any{
		map[string]any{"timestamp": "2026-10-08 09:06:00", "data": 4.75, "anomaly": true},
		map[string]any{"timestamp": "2026-10-10 01:08:00", "data": 0.09, "anomaly": false},
	}
	s, ok := SummarizeAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, "Latency", ref)))
	require.True(t, ok)
	assert.True(t, s.SeriesEndsBeforeWindow)
	assert.Equal(t, "2026-10-08 09:06:00", s.ReportedValueAt)
	assert.Contains(t, s.Render(), "Stored series: 2026-10-08 09:06:00 to 2026-10-10 01:08:00. It ends before the evaluation window")
}

func TestSummarizeAnomalyRecord_StringValuesAndRuneSafeInsights(t *testing.T) {
	ref := latencyReference()
	ref["data"] = []any{map[string]any{"timestamp": "2026-10-10 04:58:00", "data": "0.89", "anomaly": true}}
	ref["insights"] = []any{map[string]any{"insight": strings.Repeat("a", maxAnomalyInsightChars-1) + "é and more"}}
	s, ok := SummarizeAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, "Latency", ref)))
	require.True(t, ok)
	require.NotNil(t, s.PeakValue)
	assert.InDelta(t, 0.89, *s.PeakValue, 1e-9)
	require.Len(t, s.Insights, 1)
	assert.True(t, utf8.ValidString(s.Insights[0]))
	assert.Len(t, s.Insights[0], maxAnomalyInsightChars-1)
}

func TestSummarizeAnomalyRecord_EndTimeInSeriesLayout(t *testing.T) {
	ref := latencyReference()
	ref["end_time"] = "2026-10-10 05:00:00"
	s, ok := SummarizeAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, "Latency", ref)))
	require.True(t, ok)
	assert.Equal(t, "2026-10-10T04:00:00Z to 2026-10-10T05:00:00Z", s.EvaluationWindow)
}

func TestSummarizeAnomalyRecord_SkipsPointsWithoutTimestamp(t *testing.T) {
	ref := latencyReference()
	ref["data"] = []any{
		map[string]any{"timestamp": "", "data": 9.9, "anomaly": true},
		map[string]any{"timestamp": "2026-10-10 04:58:00", "data": 0.89, "anomaly": true},
	}
	s, ok := SummarizeAnomalyRecord(decodeEvidence(t, anomalyRecordJSON(t, "Latency", ref)))
	require.True(t, ok)
	assert.Equal(t, "2026-10-10 04:58:00", s.SeriesStart)
	assert.Equal(t, 1, s.AnomalousPoints)
	assert.InDelta(t, 0.89, *s.PeakValue, 1e-9)
}
