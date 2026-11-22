package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anomalyEventEvidences is an anomaly event's evidences as api-server stores
// them: the detector's result first, with no type (anomoly.collectEvidences),
// then a typed metric card. Names and ids are synthetic.
func anomalyEventEvidences(t *testing.T) map[string]any {
	t.Helper()
	series := []any{}
	for range 1500 {
		series = append(series, map[string]any{"timestamp": "2026-10-09 00:00:00", "data": 0.03, "anomaly": false})
	}
	series = append(series, map[string]any{"timestamp": "2026-10-10 04:58:00", "data": 0.89, "anomaly": true})
	return buildEvidencesRow(t, []map[string]any{
		{
			"id":            "00000000-0000-0000-0000-000000000001",
			"anomaly_type":  "Latency",
			"is_anomaly":    true,
			"current_value": 0.89,
			"evaluated_at":  "2026-10-10T05:01:47Z",
			"reference_value": map[string]any{
				"stats":                 map[string]any{"mean": 0.029, "p99": 0.21},
				"trigger_threshold_max": 0.5,
				"evaluation_period":     60,
				"data":                  series,
			},
		},
		{
			"type":            "prometheus",
			"data":            map[string]any{"result_type": "matrix", "series_list_result": []any{}},
			"additional_info": map[string]any{"title": "LATENCY Metric"},
		},
	})
}

// The detector's result used to match no branch and was dropped, so an anomaly
// investigation saw only the bare `anomaly_current` label.
func TestProcessRowWithMessages_AnomalyRecordBecomesAnomalyDetection(t *testing.T) {
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(anomalyEventEvidences(t), 0, 1, nil))

	require.NotNil(t, ev.AnomalyDetection)
	assert.Equal(t, "workload_http_latency_p99", ev.AnomalyDetection.Metric)
	assert.Equal(t, 1, ev.AnomalyDetection.AnomalousPoints)
	assert.Empty(t, ev.Others, "the record must not also land in Others")
	assert.Len(t, ev.MetricsData, 1, "the typed card is still mapped")

	raw, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"anomaly":false`, "the raw series stays out of the evidence")
}

func TestBuildEventEvidencePreview_AnomalyMetricCardsAndTraces(t *testing.T) {
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(anomalyEventEvidences(t), 0, 1, nil))
	ev.MetricsData = []events.InvestigateDataInsight{
		{Title: "prometheus", AdditionalInfo: map[string]any{"title": "CPU Metric"}, Unit: "cores", Data: map[string]any{
			"series_list_result": []any{map[string]any{"values": []any{"NaN", 0.002}}},
		}},
		{Title: "prometheus", AdditionalInfo: map[string]any{"title": "LATENCY Metric"}, Data: map[string]any{
			"series_list_result": []any{map[string]any{"values": []any{"NaN", "NaN"}}},
		}},
	}
	// Durations arrive as strings of nanoseconds.
	ev.Traces = events.InvestigateDataInsight{Data: map[string]any{"data": []any{
		map[string]any{"Duration": "398286", "SpanName": "query", "SpanAttributes": map[string]any{"db.statement": "UPDATE   workflows\n SET status = $1"}},
		map[string]any{"Duration": "5103754267", "SpanName": "request", "SpanAttributes": map[string]any{
			"destination.workload_name": "relay-server", "http.method": "POST", "http.path": "/request",
		}},
	}}}

	preview := buildEventEvidencePreview(ev)

	assert.True(t, strings.HasPrefix(preview, "Key facts:\nanomaly_detection:\n  Latency anomaly from the anomaly detector"), preview)
	assert.Contains(t, preview, "  [0] CPU Metric: 1 of 1 series with data, unit cores\n")
	assert.Contains(t, preview, "  [1] LATENCY Metric: no data\n")
	assert.Contains(t, preview, "traces: 2 spans in .traces.data.data[]; Duration is in nanoseconds, stored as strings (convert before sorting); slowest: 5103.8 ms request to relay-server POST /request; 0.4 ms query UPDATE workflows SET status = $1;\n")
}

// The shape replaces exploring the file: which keys hold data and in what form,
// which are empty, the metric cards by name, and the traces' unit.
func TestEventEvidenceShape(t *testing.T) {
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(anomalyEventEvidences(t), 0, 1, nil))
	ev.PodData = events.InvestigateDataInsight{Data: `{"status":{"phase":"Running"}}`}
	ev.Traces = events.InvestigateDataInsight{Data: map[string]any{"data": []any{
		map[string]any{"Duration": "5103754267", "SpanName": "request"},
	}}}

	shape := EventEvidenceShape(ev)

	assert.True(t, strings.HasPrefix(shape, "Top-level keys with data: anomaly_detection (the anomaly detector's result, already decoded); "), shape)
	assert.Contains(t, shape, "metrics_queries_data (")
	assert.Contains(t, shape, "pod_data (30 bytes, each entry's .data is a JSON document stored as a string: decode it a second time)")
	assert.Contains(t, shape, "; traces (57 bytes).") // a decoded object: no second decode
	assert.Contains(t, shape, "Empty, do not read: log_data, pod_metrics,")
	assert.Contains(t, shape, "  [0] LATENCY Metric: no data\n")
	assert.Contains(t, shape, "traces: 1 spans in .traces.data.data[]; Duration is in nanoseconds, stored as strings")
}

// Every others entry's own title is just its format, so the shape names each
// one: tables by title with row count and columns, change objects by kind.
func TestEventEvidenceShape_OthersAndAlertSections(t *testing.T) {
	ev := events.InvestigateData{
		AlertLabels: events.InvestigateDataInsight{Data: []any{map[string]any{"name": "severity", "value": "warning"}}},
		Others: []events.InvestigateDataInsight{
			{Title: "json", Data: map[string]any{"kind": "Node", "description": "A `Node` in namespace `` has been `Updated`:\n`node-a`", "obj": map[string]any{}}},
			{Title: "table", AdditionalInfo: map[string]any{"title": "Recent Node Events"}, Data: map[string]any{
				"table_name": "*Node events:*",
				"headers":    []any{"Last Seen", "Type", "Reason", "Source", "Count", "Message", "Object"},
				"rows":       []any{[]any{"1m", "Warning", "Pressure"}, []any{"2m", "Normal", "Ok"}},
			}},
			{Title: "json", Data: `{"b":1,"a":2}`},
		},
	}

	shape := EventEvidenceShape(ev)

	assert.Contains(t, shape, "alert_labels (")
	assert.Contains(t, shape, "alert_data")
	assert.Contains(t, shape, "others entries (.others[i].data):\n")
	assert.Contains(t, shape, "  [0] Node change: A `Node` in namespace `` has been `Updated`: `node-a`\n")
	assert.Contains(t, shape, "  [1] Recent Node Events: table, 2 rows, columns Last Seen, Type, Reason, Source, Count, Message, …\n")
	assert.Contains(t, shape, "  [2] object with keys a, b\n")
}

func TestAnomalyCurrentLabel(t *testing.T) {
	assert.Equal(t, "1.62", anomalyCurrentLabel(`{"anomaly_current":"1.62"}`))
	assert.Equal(t, "1.62", anomalyCurrentLabel(map[string]any{"anomaly_current": "1.62"}))
	assert.Equal(t, "", anomalyCurrentLabel(nil))
	assert.Equal(t, "", anomalyCurrentLabel("not json"))
}

// The run that motivated this: the detector's series ended before the window and
// the LATENCY card, which queried the window, was empty. The summary now opens
// by saying the anomaly is unconfirmed instead of leaving the agent to connect them.
func TestProcessRowWithMessages_AnomalyUnconfirmedInWindow(t *testing.T) {
	row := buildEvidencesRow(t, []map[string]any{
		{
			"anomaly_type":  "Latency",
			"is_anomaly":    true,
			"current_value": 7.24,
			"reference_value": map[string]any{
				"trigger_threshold_max": 0.5,
				"evaluation_period":     60,
				"end_time":              "2026-10-11T17:00:00Z",
				"data":                  []any{map[string]any{"timestamp": "2026-10-09 01:32:00", "data": 7.24, "anomaly": true}},
			},
		},
		{
			"type":            "prometheus",
			"data":            map[string]any{"series_list_result": []any{}},
			"additional_info": map[string]any{"title": "LATENCY Metric"},
		},
	})
	ev := processedEvidence(t, EventsExecuteTool{}.processRowWithMessages(row, 0, 1, nil))

	require.NotNil(t, ev.AnomalyDetection)
	require.NotNil(t, ev.AnomalyDetection.WindowCardHasData)
	assert.False(t, *ev.AnomalyDetection.WindowCardHasData)
	assert.True(t, strings.HasPrefix(ev.AnomalyDetection.Render(), "Unconfirmed: no data in the evaluation window supports this anomaly."), ev.AnomalyDetection.Render())
}
