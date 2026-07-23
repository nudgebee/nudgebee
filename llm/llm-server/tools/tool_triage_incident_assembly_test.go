package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// assemblyResponse builds an event_get_impact-shaped response with n items in
// each tier (and one config change / upstream entry each when n > 0).
func assemblyResponse(n int, backendTruncated bool) string {
	item := func(i int) string {
		return fmt.Sprintf(`{"event_id":"e%d","title":"t%d","dt_seconds":%d}`, i, i, i)
	}
	items := make([]string, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, item(i))
	}
	tier := "[" + strings.Join(items, ",") + "]"
	return fmt.Sprintf(`{
		"event_id": "seed-1",
		"coverage_confidence": "high",
		"impacted": [{"legacy":"surface"}],
		"assembly": {
			"root_identity": "nudgebee|services-server",
			"same_incident": %s,
			"cause": {"config_changes": %s, "upstream": %s},
			"impact": %s,
			"chronic": %s,
			"window": {"lead_in_s": 7200, "core_s": 7200, "impact_s": 7200},
			"truncated": %v
		}
	}`, tier, tier, tier, tier, tier, backendTruncated)
}

func TestBoundIncidentAssemblyCapsTiers(t *testing.T) {
	out := boundIncidentAssembly(assemblyResponse(15, false))

	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	for _, tier := range []string{"same_incident", "impact", "chronic"} {
		if got := len(m[tier].([]any)); got != maxAssemblyTierItems {
			t.Errorf("%s: got %d items, want %d", tier, got, maxAssemblyTierItems)
		}
	}
	cause := m["cause"].(map[string]any)
	for _, tier := range []string{"config_changes", "upstream"} {
		if got := len(cause[tier].([]any)); got != maxAssemblyTierItems {
			t.Errorf("cause.%s: got %d items, want %d", tier, got, maxAssemblyTierItems)
		}
	}

	trunc, ok := m["truncated"].(map[string]any)
	if !ok {
		t.Fatal("truncated accounting missing")
	}
	si := trunc["same_incident"].(map[string]any)
	if si["shown"].(float64) != float64(maxAssemblyTierItems) || si["total"].(float64) != 15 {
		t.Errorf("same_incident truncation accounting wrong: %v", si)
	}

	// The legacy impacted[] surface must not leak into the tool payload.
	if _, present := m["impacted"]; present {
		t.Error("legacy impacted[] leaked into tool output")
	}
	if m["topology_coverage"] != "high" {
		t.Errorf("topology_coverage: got %v", m["topology_coverage"])
	}
}

func TestBoundIncidentAssemblyCaveatLeads(t *testing.T) {
	out := boundIncidentAssembly(assemblyResponse(2, false))
	if !strings.HasPrefix(out, `{"note":"Alerts grouped around this event`) {
		t.Errorf("caveat is not the leading field: %s", out[:80])
	}
	if !strings.Contains(out, "CANDIDATE") || !strings.Contains(out, "chronic") {
		t.Error("caveat missing candidates-only / chronic wording")
	}
}

func TestBoundIncidentAssemblyEmptyWindow(t *testing.T) {
	out := boundIncidentAssembly(assemblyResponse(0, false))
	if !strings.HasPrefix(out, `{"note":"No other alerts were recorded`) {
		t.Errorf("empty window should lead with the empty caveat: %s", out[:80])
	}
	if !strings.Contains(out, "not proof") {
		t.Error("empty caveat missing the low-coverage warning")
	}
}

func TestBoundIncidentAssemblyBackendTruncation(t *testing.T) {
	out := boundIncidentAssembly(assemblyResponse(2, true))
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	trunc, ok := m["truncated"].(map[string]any)
	if !ok || trunc["window_rows"] == nil {
		t.Errorf("backend window truncation not surfaced: %v", m["truncated"])
	}
}

func TestBoundIncidentAssemblyPassthroughOnUnexpectedShape(t *testing.T) {
	for _, data := range []string{
		"not json at all",
		`{"event_id":"x","impacted":[]}`, // no assembly block
	} {
		if got := boundIncidentAssembly(data); got != data {
			t.Errorf("unexpected shape should pass through unchanged: in=%q out=%q", data, got)
		}
	}
}
