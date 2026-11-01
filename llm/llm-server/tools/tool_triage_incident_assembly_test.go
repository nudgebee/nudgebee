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

func TestBoundIncidentAssemblyKeepsCoFiring(t *testing.T) {
	data := `{"event_id":"seed-1","coverage_confidence":"none","assembly":{"root_identity":"|order-service",
		"same_incident":[],"cause":{"config_changes":[],"upstream":[]},"impact":[],"chronic":[],
		"co_firing":[{"event_id":"ec2-term","title":"EC2 Instance State-change Notification","relation":"co_firing","dt_seconds":-2}]}}`

	out := boundIncidentAssembly(data)

	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	coFiring, ok := m["co_firing"].([]any)
	if !ok || len(coFiring) != 1 {
		t.Fatalf("co_firing not passed through: %s", out)
	}
	if note, _ := m["note"].(string); !strings.Contains(note, "co_firing") {
		t.Errorf("an assembly holding only co-firing alerts must not get the empty-window caveat: %q", note)
	}
}

// The same_node lane reaches the model, with one line saying what it is, only when
// it holds something. An output without it must not carry the key or mention it:
// most investigations have no such entries, and text about an absent lane is noise.
func TestBoundIncidentAssemblySameNodeOnlyWhenPresent(t *testing.T) {
	decode := func(data string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(boundIncidentAssembly(data)), &m); err != nil {
			t.Fatalf("output is not JSON: %v", err)
		}
		return m
	}

	with := decode(`{"event_id":"seed-1","coverage_confidence":"none","assembly":{"root_identity":"agents|log-agent",
		"same_incident":[],"cause":{"config_changes":[],"upstream":[]},"impact":[],"chronic":[],"co_firing":[],
		"same_node":[{"event_id":"db-1","title":"metrics-db alert","relation":"same_node","dt_seconds":-10}]}}`)
	if got, ok := with["same_node"].([]any); !ok || len(got) != 1 {
		t.Fatalf("same_node not passed through: %v", with["same_node"])
	}
	note, _ := with["note"].(string)
	if !strings.HasPrefix(note, assemblyCaveat) || !strings.HasSuffix(note, assemblySameNodeNote) {
		t.Errorf("an output holding only same_node entries needs the caveat plus the lane note, got %q", note)
	}

	for name, data := range map[string]string{
		"empty lane":   `{"event_id":"seed-1","assembly":{"same_incident":[{"event_id":"a"}],"same_node":[]}}`,
		"older server": `{"event_id":"seed-1","assembly":{"same_incident":[{"event_id":"a"}]}}`,
	} {
		without := decode(data)
		if _, present := without["same_node"]; present {
			t.Errorf("%s: same_node key must be omitted when there is nothing in it", name)
		}
		if note, _ := without["note"].(string); strings.Contains(note, "same_node") {
			t.Errorf("%s: note mentions a lane the output does not have: %q", name, note)
		}
	}
}

// Text every investigation reads stays as it was: the lane is not advertised in the
// standing caveat or the tool description. The one line that does describe it names
// no particular resource, so it needs no edit when another kind of shortage is added.
func TestIncidentAssemblyTextStaysGeneric(t *testing.T) {
	for name, text := range map[string]string{"caveat": assemblyCaveat, "description": IncidentAssemblyTool{}.Description()} {
		if strings.Contains(text, "same_node") {
			t.Errorf("%s mentions same_node; it belongs only in the note added when the lane is present", name)
		}
	}
	lower := strings.ToLower(assemblySameNodeNote)
	for _, word := range []string{"memory", "oom", "cpu", "disk", "kill"} {
		if strings.Contains(lower, word) {
			t.Errorf("the same_node note names a specific scenario (%q)", word)
		}
	}
}
