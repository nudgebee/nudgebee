package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/services/triage"
)

func changeRow(id string, at time.Time) windowRow {
	return windowRow{id: triage.AlertIdentity{ID: id, IsConfigChange: true}, startsAt: at}
}

// Rows arrive newest-first. The net change runs from the earliest edit to the
// latest one at or before the alert; an edit after the alert (a fix, say) did
// not cause it and must not be what the item reports.
func TestChangeSpanBoundsTheEditsBeforeTheAlert(t *testing.T) {
	root := time.Date(2026, 10, 21, 7, 29, 0, 0, time.UTC)
	var s changeSpan
	for _, r := range []windowRow{
		changeRow("after", root.Add(10*time.Minute)),
		changeRow("latest", root.Add(-4*time.Minute)),
		changeRow("earliest", root.Add(-40*time.Minute)),
	} {
		s.add(r, root)
	}
	if got, want := s.bounds("after"), (changeBounds{"earliest", "latest", 2}); got != want {
		t.Fatalf("bounds = %+v, want %+v", got, want)
	}

	var late changeSpan
	late.add(changeRow("b", root.Add(5*time.Minute)), root)
	late.add(changeRow("a", root.Add(2*time.Minute)), root)
	if got, want := late.bounds("b"), (changeBounds{"a", "b", 2}); got != want {
		t.Fatalf("all edits after the alert: bounds = %+v, want %+v", got, want)
	}
}

func recordedConfigMap(t *testing.T, before, after string) triage.RecordedChange {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"kind":   "ConfigMap",
		"oldObj": map[string]any{"data": map[string]any{"VALUE": before}},
		"obj":    map[string]any{"data": map[string]any{"VALUE": after}},
	})
	ev, _ := json.Marshal([]map[string]any{{"type": "json", "data": string(payload)}})
	rc, ok := triage.ParseRecordedChange(ev)
	if !ok {
		t.Fatal("fixture did not parse")
	}
	return rc
}

// The change lines ride in the investigation prompt ahead of the impact tier, so
// together they stay within maxChangesBytes; items past the budget keep only
// their title.
func TestApplyChangeLinesStopsAtTheBudget(t *testing.T) {
	recorded := map[string]triage.RecordedChange{}
	var items []tierItem
	var bounds []changeBounds
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("e%d", i)
		recorded[id] = recordedConfigMap(t, strings.Repeat("a", 60), strings.Repeat("b", 60))
		items = append(items, tierItem{EventID: id})
		bounds = append(bounds, changeBounds{id, id, 1})
	}
	items = append(items, tierItem{EventID: "unrecorded"})
	bounds = append(bounds, changeBounds{"unrecorded", "unrecorded", 1})

	applyChangeLines(items, bounds, recorded)

	total := 0
	for _, it := range items {
		for _, l := range it.Changes {
			total += len(l)
		}
	}
	if total == 0 || total > maxChangesBytes {
		t.Fatalf("change lines total %d bytes, want 1..%d", total, maxChangesBytes)
	}
	if len(items[0].Changes) == 0 || len(items[19].Changes) != 0 || len(items[20].Changes) != 0 {
		t.Errorf("the nearest changes get lines first; past the budget and unrecorded ones get none")
	}
}

// An earliest edit whose event is gone is not a reason to claim "no net
// change": the latest edit alone is described.
func TestApplyChangeLinesWithoutTheEarliestEdit(t *testing.T) {
	recorded := map[string]triage.RecordedChange{"latest": recordedConfigMap(t, "5", "5")}
	items := []tierItem{{EventID: "latest"}}
	applyChangeLines(items, []changeBounds{{"gone", "latest", 3}}, recorded)
	if len(items[0].Changes) != 0 {
		t.Fatalf("got %q; an unchanged single edit has nothing to say", items[0].Changes)
	}
}
