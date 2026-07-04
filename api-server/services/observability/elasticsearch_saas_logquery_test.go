package observability

import (
	"encoding/json"
	"strings"
	"testing"
)

// The WHERE-built query carried no time bound / size / sort, so Elasticsearch
// defaulted to 10 hits, unbounded time, index order. finalizeESLogQueryBody must
// apply the request's window, limit and sort.
func TestFinalizeESLogQueryBody_AppliesTimeRangeSizeSort(t *testing.T) {
	body, err := finalizeESLogQueryBody(`{"query":{"term":{"x":"y"}}}`, 1000, 2000, 50, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	q, ok := body["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected query map, got %T", body["query"])
	}
	filter, ok := q["bool"].(map[string]any)["filter"].([]any)
	if !ok || len(filter) != 2 {
		t.Fatalf("expected bool filter of len 2 (user query + time range), got %v", q["bool"])
	}
	if body["size"] != 50 {
		t.Fatalf("expected size=50 from limit, got %v", body["size"])
	}
	out, _ := json.Marshal(body)
	if s := string(out); !strings.Contains(s, "epoch_millis") || !strings.Contains(s, `"x":"y"`) || !strings.Contains(s, `"@timestamp":{"order":"desc"}`) {
		t.Fatalf("missing time range / user query / default sort: %s", s)
	}
}

// Empty query + no limit -> bounded by the default size and default sort.
func TestFinalizeESLogQueryBody_DefaultsWhenEmpty(t *testing.T) {
	body, err := finalizeESLogQueryBody("", 0, 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["size"] != defaultESLogQuerySize {
		t.Fatalf("expected default size %d, got %v", defaultESLogQuerySize, body["size"])
	}
	if _, ok := body["sort"].([]any); !ok {
		t.Fatalf("expected a default sort clause, got %v", body["sort"])
	}
}

// A raw DSL body that already sets size/sort must be respected; the time range
// is still AND-merged.
func TestFinalizeESLogQueryBody_RespectsCallerSizeAndSort(t *testing.T) {
	body, err := finalizeESLogQueryBody(`{"size":5,"sort":[{"x":"asc"}],"query":{"match_all":{}}}`, 1000, 2000, 50, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := body["size"].(float64); !ok || got != 5 {
		t.Fatalf("expected caller size 5 preserved, got %v (%T)", body["size"], body["size"])
	}
	out, _ := json.Marshal(body["sort"])
	if string(out) != `[{"x":"asc"}]` {
		t.Fatalf("expected caller sort preserved, got %s", out)
	}
	full, _ := json.Marshal(body)
	if !strings.Contains(string(full), "epoch_millis") {
		t.Fatalf("expected time range still merged: %s", full)
	}
}

// A one-sided window (start only, "from X to now") must still bound the scan and
// must not pin the missing edge to epoch 0.
func TestFinalizeESLogQueryBody_OneSidedRange(t *testing.T) {
	body, err := finalizeESLogQueryBody(`{"query":{"match_all":{}}}`, 1000, 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, _ := json.Marshal(body)
	s := string(out)
	if !strings.Contains(s, `"gte":1000`) {
		t.Fatalf("expected gte bound applied for start-only range: %s", s)
	}
	if strings.Contains(s, `"lte"`) {
		t.Fatalf("did not expect an lte bound when end is unset: %s", s)
	}
}

func TestFinalizeESLogQueryBody_OffsetSetsFrom(t *testing.T) {
	body, err := finalizeESLogQueryBody(`{"query":{"match_all":{}}}`, 0, 0, 10, 20, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["from"] != 20 {
		t.Fatalf("expected from=20 from offset, got %v", body["from"])
	}
}

func TestFinalizeESLogQueryBody_InvalidJSON(t *testing.T) {
	if _, err := finalizeESLogQueryBody("not json", 0, 0, 0, 0, nil); err == nil {
		t.Fatalf("expected parse error for invalid JSON")
	}
}

func TestBuildESLogSort_TranslatesSortFieldsAndDefaults(t *testing.T) {
	got := buildESLogSort([]SortField{{ColumnName: "severity", Order: "ASC"}, {ColumnName: "", Order: "desc"}})
	out, _ := json.Marshal(got)
	if string(out) != `[{"severity":{"order":"asc"}}]` {
		t.Fatalf("expected translated+filtered sort, got %s", out)
	}
	def, _ := json.Marshal(buildESLogSort(nil))
	if string(def) != `[{"@timestamp":{"order":"desc"}}]` {
		t.Fatalf("expected default @timestamp desc, got %s", def)
	}
}
