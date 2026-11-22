package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var applyRules = FailureRules{
	ExpectJSON:      true,
	StatusPath:      "resolution.status",
	FailingStatuses: []string{"Failed"},
	ClosedStatuses:  []string{"InProgress", "Success", "Failed"},
}

// The four cases every opted-in tool must pass, plus the shapes that must not
// be mistaken for a failure.
func TestClassifyResponse(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		rules      FailureRules
		wantFailed bool
		wantReason FailureReason
	}{
		{"clean success", `{"data":[],"resolution":{"status":"Success"},"status":"Open"}`, applyRules, false, ""},
		{"lifecycle status is not a failure", `{"resolution":{"status":"InProgress"},"status":"InProgress"}`, applyRules, false, ""},
		{"plain-text error from a JSON tool", `Internal Server Error`, applyRules, true, FailureReasonMalformedResponse},
		{"html body from a JSON tool", `<html>502 Bad Gateway</html>`, applyRules, true, FailureReasonMalformedResponse},
		{"truncated json", `{"resolution":{"status":"Fai`, applyRules, true, FailureReasonMalformedResponse},
		{"json error field", `{"error":"account not found"}`, applyRules, true, FailureReasonReportedFailed},
		{"top-level failed status", `{"status":"failed"}`, applyRules, true, FailureReasonReportedFailed},
		{"nested failed status", `{"resolution":{"status":"Failed"},"status":"InProgress"}`, applyRules, true, FailureReasonReportedFailed},
		{"nested status is matched case-insensitively", `{"resolution":{"status":"failed"}}`, applyRules, true, FailureReasonReportedFailed},
		{"unknown status fails closed", `{"resolution":{"status":"Cancelled"}}`, applyRules, true, FailureReasonMalformedResponse},
		{"closed set is case-insensitive", `{"resolution":{"status":"success"}}`, applyRules, false, ""},
		{"no closed set accepts any status", `{"resolution":{"status":"Cancelled"}}`, FailureRules{StatusPath: "resolution.status", FailingStatuses: []string{"Failed"}}, false, ""},
		{"empty error field is not a failure", `{"error":"","resolution":{"status":"Success"}}`, applyRules, false, ""},
		{"json array body is valid json", `[{"id":1}]`, applyRules, false, ""},
		{"empty body is left to the executor", ``, applyRules, false, ""},
		{"path missing", `{"data":[]}`, applyRules, false, ""},
		{"path through a non-object", `{"resolution":"Failed"}`, applyRules, false, ""},
		{"path through null", `{"resolution":null}`, applyRules, false, ""},
		{"path through an array", `{"resolution":[{"status":"Failed"}]}`, applyRules, false, ""},
		{"non-string status", `{"resolution":{"status":5}}`, applyRules, false, ""},
		{"single-segment path", `{"phase":"Failed"}`, FailureRules{StatusPath: "phase", FailingStatuses: []string{"Failed"}}, true, FailureReasonReportedFailed},
		{"three-segment path", `{"a":{"b":{"c":"failed"}}}`, FailureRules{StatusPath: "a.b.c", FailingStatuses: []string{"Failed"}}, true, FailureReasonReportedFailed},
		{"plain text from a tool that does not expect JSON", `command failed`, FailureRules{}, false, ""},
		{"a log line quoted in a complete answer", `{"answer":"unable to fetch","steps":[{"error":"boom"}]}`, applyRules, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failed, reason := ClassifyResponse(tt.body, tt.rules)
			assert.Equal(t, tt.wantFailed, failed)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

// Agent answers are judged by the same function, so these must stay exactly as
// the agent path had them.
func TestStructuredResponseFailed(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantStructured bool
		wantFailed     bool
	}{
		{"not an object", `plain`, false, false},
		{"array", `[1]`, false, false},
		{"invalid object", `{"a":`, false, false},
		{"complete object", `{"a":1}`, true, false},
		{"error key", `{"error":"x"}`, true, true},
		{"errors key", `{"errors":["x"]}`, true, true},
		{"empty error", `{"error":null}`, true, false},
		{"status error", `{"status":"error"}`, true, true},
		{"status is case-sensitive here", `{"status":"Failed"}`, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			structured, failed := StructuredResponseFailed(tt.body)
			assert.Equal(t, tt.wantStructured, structured)
			assert.Equal(t, tt.wantFailed, failed)
		})
	}
}
