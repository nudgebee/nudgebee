package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
)

func TestCodeAnalysisResultStatus(t *testing.T) {
	cases := map[string]struct {
		result map[string]any
		want   core.ConversationStatus
	}{
		"execution failed":                 {map[string]any{"execution_status": "failed", "failure_summary": "x"}, core.ConversationStatusFailed},
		"pr creation failed":               {map[string]any{"execution_status": "success", "pr_creation_status": "failed"}, core.ConversationStatusFailed},
		"mixed case and spaces":            {map[string]any{"execution_status": " Failed "}, core.ConversationStatusFailed},
		"success":                          {map[string]any{"execution_status": "success", "pr_creation_status": "success"}, ""},
		"partial success is a failure":     {map[string]any{"execution_status": "partial_success"}, core.ConversationStatusFailed},
		"skipped is a failure":             {map[string]any{"execution_status": "skipped", "pr_creation_status": "skipped"}, core.ConversationStatusFailed},
		"an unknown status fails closed":   {map[string]any{"execution_status": "cancelled"}, core.ConversationStatusFailed},
		"success in any case":              {map[string]any{"execution_status": " SUCCESS "}, ""},
		"blank status is left to executor": {map[string]any{"execution_status": "  "}, ""},
		"non-string status is ignored":     {map[string]any{"execution_status": 5}, ""},
		"no-op is not a failure":           {map[string]any{"execution_status": codeAnalysisNoOpStatus}, ""},
		"answer mode":                      {map[string]any{"answer": "the source logs failed"}, ""},
		"pr skipped":                       {map[string]any{"execution_status": "success", "pr_creation_status": "skipped"}, ""},
		"empty":                            {map[string]any{}, ""},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, codeAnalysisResultStatus(c.result), name)
	}
}

func TestMalformedAnalysisResult(t *testing.T) {
	got := malformedAnalysisResult("Error: clone failed: authentication required\n")
	assert.Equal(t, core.ConversationStatusFailed, got.Status)
	if assert.Len(t, got.Response, 1) {
		assert.Contains(t, got.Response[0], "not JSON")
		assert.Contains(t, got.Response[0], "Error: clone failed: authentication required")
	}

	long := malformedAnalysisResult(strings.Repeat("x", 5*maxMalformedResultChars))
	assert.Less(t, len(long.Response[0]), 2*maxMalformedResultChars, "the echoed text is bounded")

	assert.Equal(t, core.ConversationStatusFailed, malformedAnalysisResult("").Status, "an empty result is malformed too")
}
