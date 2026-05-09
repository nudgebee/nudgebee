package tools

import (
	"testing"
	"time"
)

// Verifies marshalToMap round-trips an arbitrary tagged struct (modelling
// what each ticket SDK returns) into a map[string]any preserving field
// names from json tags. This is the foundation for populating Ticket.Raw
// across all per-platform Get methods (Jira / GitHub / GitLab / PD / ZD).
func TestMarshalToMap_RoundTripsTaggedStruct(t *testing.T) {
	type customField struct {
		Value string `json:"value"`
	}
	type sampleIssue struct {
		ID            string       `json:"id"`
		Title         string       `json:"title"`
		Custom        *customField `json:"custom_field"`
		Labels        []string     `json:"labels"`
		Created       time.Time    `json:"created_at"`
		Skipped       string       `json:"-"`
		Untagged      string
		Optional      string `json:"optional,omitempty"`
		FreeForm      map[string]any
		EmptyOptional string `json:"empty_optional,omitempty"`
	}

	issue := sampleIssue{
		ID:       "INC-1",
		Title:    "Disk full",
		Custom:   &customField{Value: "rxtsqldev01"},
		Labels:   []string{"prod", "db"},
		Created:  time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
		Skipped:  "should not appear",
		Untagged: "appears as Untagged",
		Optional: "set",
		FreeForm: map[string]any{"u_payload": "{\"x\":1}"},
	}

	got := marshalToMap(issue)
	if got == nil {
		t.Fatal("marshalToMap returned nil for non-nil struct")
	}

	if got["id"] != "INC-1" {
		t.Errorf("id = %v, want INC-1", got["id"])
	}
	if got["title"] != "Disk full" {
		t.Errorf("title = %v", got["title"])
	}
	if cf, ok := got["custom_field"].(map[string]any); !ok || cf["value"] != "rxtsqldev01" {
		t.Errorf("custom_field round-trip failed: %v", got["custom_field"])
	}
	if labels, ok := got["labels"].([]any); !ok || len(labels) != 2 || labels[0] != "prod" {
		t.Errorf("labels = %v", got["labels"])
	}
	if _, present := got["-"]; present {
		t.Errorf("json:\"-\" tagged field should be skipped, got: %v", got)
	}
	if got["Untagged"] != "appears as Untagged" {
		t.Errorf("untagged field should appear under struct field name, got: %v", got["Untagged"])
	}
	if _, present := got["empty_optional"]; present {
		t.Errorf("omitempty field that's empty should be absent")
	}
	if ff, ok := got["FreeForm"].(map[string]any); !ok || ff["u_payload"] != "{\"x\":1}" {
		t.Errorf("FreeForm map didn't round-trip: %v", got["FreeForm"])
	}
}

// Nil input must return nil (not an empty map) so callers can distinguish
// "no SDK record" from "SDK record with no fields".
func TestMarshalToMap_NilReturnsNil(t *testing.T) {
	if got := marshalToMap(nil); got != nil {
		t.Errorf("marshalToMap(nil) = %v, want nil", got)
	}
}

// Pointer to nil should also be handled gracefully.
func TestMarshalToMap_TypedNilReturnsNilOrEmpty(t *testing.T) {
	type empty struct{}
	var p *empty // typed nil
	got := marshalToMap(p)
	// JSON marshals typed nil pointer to "null" → unmarshal into map[string]any
	// fails. Helper logs at debug and returns nil. Either nil or empty map are
	// acceptable; the contract is "don't panic, don't lie about populated data".
	if len(got) != 0 {
		t.Errorf("marshalToMap(typed nil) = %v, want nil or empty map", got)
	}
}

// Channel can't be marshalled — helper must swallow and return nil.
func TestMarshalToMap_NonMarshalableReturnsNil(t *testing.T) {
	ch := make(chan int)
	if got := marshalToMap(ch); got != nil {
		t.Errorf("marshalToMap(chan) = %v, want nil (marshal must fail gracefully)", got)
	}
}
