package relay

import (
	"testing"
	"time"
)

func TestPrometheusActionParamsStep(t *testing.T) {
	start := time.Date(2026, 9, 5, 10, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))
	end := start.Add(time.Hour)
	queries := map[string]string{"A": "up"}

	// Zero keeps the agent on its own default: no key at all, not "0".
	params := prometheusActionParams(start, end, queries, false, 0)
	if _, ok := params["steps"]; ok {
		t.Fatalf("steps should be absent at zero, got %v", params["steps"])
	}

	// A dashboard panel sends the step it can draw; the agent reads `steps`
	// as a string.
	params = prometheusActionParams(start, end, queries, false, 3600)
	if got := params["steps"]; got != "3600" {
		t.Fatalf("steps = %v, want \"3600\"", got)
	}
	if got := params["instant"]; got != false {
		t.Fatalf("instant = %v, want false", got)
	}
	// Timestamps are converted to UTC, not merely labelled UTC.
	duration := params["duration"].(map[string]any)
	if got := duration["starts_at"]; got != "2026-09-05 04:30:00 UTC" {
		t.Fatalf("starts_at = %v, want 2026-09-05 04:30:00 UTC", got)
	}
}
