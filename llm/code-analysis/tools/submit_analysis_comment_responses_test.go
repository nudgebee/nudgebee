package tools

import (
	"context"
	"reflect"
	"testing"
)

// TestSubmitAnalysis_CommentResponses covers the shapes models send for
// comment_responses. A JSON-encoded string used to be passed through as-is,
// the PR followup agent then failed to parse the whole payload, and the run
// recorded nothing (#39995).
func TestSubmitAnalysis_CommentResponses(t *testing.T) {
	tool := NewSubmitAnalysisTool()
	want := []any{
		map[string]any{"comment_id": float64(101), "action": "fixed", "reply": "Applied.", "should_reply": true},
		map[string]any{"comment_id": float64(102), "action": "acknowledged", "reply": "", "should_reply": false},
	}
	const encoded = `[{"comment_id": 101, "action": "fixed", "reply": "Applied.", "should_reply": true}, {"comment_id": 102, "action": "acknowledged", "reply": "", "should_reply": false}]`

	cases := []struct {
		name      string
		value     any
		wantError bool
		want      []any
	}{
		{name: "array passes through", value: want, want: want},
		{name: "JSON-encoded string is decoded", value: encoded, want: want},
		{name: "JSON-encoded string with surrounding whitespace is decoded", value: "\n" + encoded + "\n", want: want},
		{name: "empty array stays empty", value: []any{}, want: []any{}},
		{name: "prose string is rejected", value: "All comments addressed.", wantError: true},
		{name: "object is rejected", value: map[string]any{"comment_id": 1}, wantError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := tool.Execute(context.Background(), map[string]any{
				"execution_status":  "success",
				"execution_summary": "Review comments addressed.",
				"comment_responses": tc.value,
			})
			if tc.wantError {
				if resp.Status != "error" {
					t.Fatalf("want error response, got %q", resp.Status)
				}
				return
			}
			if resp.Status == "error" {
				t.Fatalf("unexpected error response: %s", resp.Error)
			}
			got := resp.Data.(map[string]any)["comment_responses"]
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("comment_responses = %#v, want %#v", got, tc.want)
			}
		})
	}
}
