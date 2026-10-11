package api

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/llm/events"
)

// The RCA prompt carries the detector's metric, threshold and baseline, so the
// agent does not have to work out what the bare anomaly value means.
func TestBuildInvestigationEvidenceContext_AnomalyDetection(t *testing.T) {
	current, threshold := 0.89, 0.5
	ev := events.InvestigateData{AnomalyDetection: &events.AnomalySummary{
		AnomalyType:      "Latency",
		Metric:           "workload_http_latency_p99",
		Unit:             "s",
		CurrentValue:     &current,
		TriggerThreshold: &threshold,
		Baseline:         map[string]float64{"mean": 0.029},
	}}

	got := buildInvestigationEvidenceContext(ev)

	if !strings.HasPrefix(got, "### Anomaly Detection\nLatency anomaly from the anomaly detector, metric workload_http_latency_p99") {
		t.Fatalf("anomaly section missing or not first:\n%s", got)
	}
	for _, want := range []string{"reported value: 0.89 s", "trigger threshold: 0.5 s", "Baseline: mean 0.029 s"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// getEventData round-trips the evidence through JSON into events.Event; the
// summary has to survive that as the same type.
func TestAnomalyDetectionSurvivesEventRoundTrip(t *testing.T) {
	current := 0.89
	raw, err := json.Marshal(events.Event{Evidences: events.InvestigateData{
		AnomalyDetection: &events.AnomalySummary{AnomalyType: "Latency", CurrentValue: &current},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var back events.Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Evidences.AnomalyDetection == nil || *back.Evidences.AnomalyDetection.CurrentValue != 0.89 {
		t.Fatalf("anomaly_detection lost in round trip: %+v", back.Evidences.AnomalyDetection)
	}
}
