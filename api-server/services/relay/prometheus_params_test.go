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
	for _, key := range []string{"step", "steps"} {
		if _, ok := params[key]; ok {
			t.Fatalf("%s should be absent at zero, got %v", key, params[key])
		}
	}

	// A dashboard panel sends the step it can draw, as a string. `step` must be
	// set too: relay-server only checks `step` and otherwise overwrites it with
	// its own default, which the agent reads ahead of `steps`.
	params = prometheusActionParams(start, end, queries, false, 3600)
	for _, key := range []string{"step", "steps"} {
		if got := params[key]; got != "3600" {
			t.Fatalf("%s = %v, want \"3600\"", key, got)
		}
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
