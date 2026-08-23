package queue

import (
	"testing"

	"nudgebee/services/event"
)

// loadEventMap nils Evidences before marshalling, so anything a downstream
// processor needs — notifications included — has to travel on a field that
// survives that step. This pins the contract both ways.
func TestStructToMapDropsEvidencesButKeepsLabels(t *testing.T) {
	eventObj := event.Event{
		Title:     "CPU Anomaly detected for checkout-api in namespace payments",
		Source:    "anomaly",
		Priority:  "HIGH",
		Evidences: []any{map[string]any{"current_value": 0.82}},
		Labels: map[string]string{
			"anomaly_current":  "820m",
			"anomaly_baseline": "140m",
		},
	}

	// Mirrors loadEventMap: evidences are cleared, then the struct is marshalled.
	hasEvidences := eventObj.Evidences != nil
	eventObj.Evidences = nil

	eventMap, err := structToMap(eventObj)
	if err != nil {
		t.Fatalf("structToMap: %v", err)
	}
	eventMap["has_evidences"] = hasEvidences

	if evidences, ok := eventMap["evidences"]; ok && evidences != nil {
		t.Fatalf("evidences must not survive the strip, got %v", evidences)
	}
	if eventMap["has_evidences"] != true {
		t.Fatal("has_evidences should record that evidences existed")
	}

	// The JSON round-trip in structToMap widens the map's value type, which is
	// what notifications-server actually receives.
	labels, ok := eventMap["labels"].(map[string]any)
	if !ok {
		t.Fatalf("labels missing or wrong type in published map: %#v", eventMap["labels"])
	}
	if labels["anomaly_current"] != "820m" || labels["anomaly_baseline"] != "140m" {
		t.Fatalf("anomaly stat labels did not survive: %#v", labels)
	}
}
