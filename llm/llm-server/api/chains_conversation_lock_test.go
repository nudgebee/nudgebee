package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pins #37486: KILLED must be rejected up front, the same as IN_PROGRESS,
// instead of silently running a full turn and discarding the result — except
// for a followup cancel/resume, which shouldSkipResumeForTerminalConversation
// always allows through regardless of conversation status.
func TestRejectIfConversationLocked(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name                    string
		status                  core.ConversationStatus
		allowFollowupCompletion bool
		wantRejected            bool
		wantMessagePart         string
	}{
		{"in progress is rejected", core.ConversationStatusInProgress, false, true, "conversation is in progress"},
		{"in progress is rejected even for a followup completion", core.ConversationStatusInProgress, true, true, "conversation is in progress"},
		{"killed is rejected", core.ConversationStatusKilled, false, true, "cannot be resumed"},
		{"killed is allowed through for a followup completion", core.ConversationStatusKilled, true, false, ""},
		{"completed is allowed through", core.ConversationStatusCompleted, false, false, ""},
		{"failed is allowed through", core.ConversationStatusFailed, false, false, ""},
		// TERMINATED must stay resumable — see the #30137 note on shouldSkipSaveBack.
		{"terminated is allowed through", core.ConversationStatusTerminated, false, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			conversation := core.Conversation{ID: uuid.New(), AccountID: uuid.New(), Status: tt.status}
			got := rejectIfConversationLocked(c, "/v1/completions/chat", conversation, tt.allowFollowupCompletion)

			assert.Equal(t, tt.wantRejected, got)
			if !tt.wantRejected {
				assert.Equal(t, 0, w.Body.Len(), "must not write a response when the conversation isn't locked")
				return
			}

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp ApiResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.Errors, 1)
			assert.Contains(t, resp.Errors[0].Message, tt.wantMessagePart)
		})
	}
}
