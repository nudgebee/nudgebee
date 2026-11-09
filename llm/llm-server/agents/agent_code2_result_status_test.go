package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
)

func TestCodeAnalysisResultStatus(t *testing.T) {
	cases := map[string]struct {
		result map[string]any
		want   core.ConversationStatus
	}{
		"execution failed":       {map[string]any{"execution_status": "failed", "failure_summary": "x"}, core.ConversationStatusFailed},
		"pr creation failed":     {map[string]any{"execution_status": "success", "pr_creation_status": "failed"}, core.ConversationStatusFailed},
		"mixed case and spaces":  {map[string]any{"execution_status": " Failed "}, core.ConversationStatusFailed},
		"success":                {map[string]any{"execution_status": "success", "pr_creation_status": "success"}, ""},
		"no-op is not a failure": {map[string]any{"execution_status": codeAnalysisNoOpStatus}, ""},
		"answer mode":            {map[string]any{"answer": "the source logs failed"}, ""},
		"pr skipped":             {map[string]any{"execution_status": "success", "pr_creation_status": "skipped"}, ""},
		"empty":                  {map[string]any{}, ""},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, codeAnalysisResultStatus(c.result), name)
	}
}
