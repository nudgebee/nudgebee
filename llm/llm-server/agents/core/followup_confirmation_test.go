package core

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	toolcore "nudgebee/llm/tools/core"
)

// TestFollowupRequestForToolOperationConfirmation_QuerySubstring guards the
// confirmation-followup builder against tool inputs that contain the literal
// "query" substring without a top-level string "query" key. The presence check
// is only a substring match, so the literal can appear inside the command text
// while the top-level key is something else (or null). Before the comma-ok
// guard, commandMap["query"].(string) panicked
// ("interface conversion: interface {} is nil, not string") on those inputs.
func TestFollowupRequestForToolOperationConfirmation_QuerySubstring(t *testing.T) {
	query := NBAgentRequest{AgentId: uuid.New().String()}
	agent := LLMAgent{}

	cases := []struct {
		name         string
		toolInput    string
		wantContains string
	}{
		{
			name:         "top-level string query is unwrapped",
			toolInput:    `{"query": "SELECT 1"}`,
			wantContains: "SELECT 1",
		},
		{
			name:         "query substring inside command value does not panic",
			toolInput:    `{"command": "SELECT * FROM t WHERE note = 'query'"}`,
			wantContains: `{"command": "SELECT * FROM t WHERE note = 'query'"}`,
		},
		{
			name:         "null query value does not panic",
			toolInput:    `{"query": null}`,
			wantContains: `{"query": null}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := NBAgentPlannerToolAction{Tool: "mysql_query_execute", ToolInput: tc.toolInput}
			var fr FollowupRequest
			var err error
			assert.NotPanics(t, func() {
				fr, err = FollowupRequestForToolOperationConfirmation(
					llmOverrideContext(), query, agent, action, toolcore.ToolRequestTypeUpdate, action.Tool)
			})
			assert.NoError(t, err)
			assert.Contains(t, fr.Question, tc.wantContains)
		})
	}
}
