package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// A read-only person cannot approve, deny or dismiss a change on an alert from the API: the tab's write check is the
// client's, so the server checks it too, on its own record of the follow-up the named agent waits on. A writer can;
// other follow-ups and other conversations are as before.
func TestRefuseReadOnlyAlertApproval(t *testing.T) {
	const account = "target-k8s"
	readOnly := func(t *testing.T) *security.RequestContext {
		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal([]byte(`{"RoleNames":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["`+account+`"]}}`), &sc))
		return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	}
	writer := func(*testing.T) *security.RequestContext {
		return security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account})
	}
	answer := ConversationApiRequest{AccountId: account, SessionId: "event-fp1", ConversationId: "conv-1", MessageId: "msg-1", AgentId: "agent-1", Query: "yes"}
	alertApproval := waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "event-fp1", Confirmation: true}
	for _, tc := range []struct {
		name     string
		ctx      func(*testing.T) *security.RequestContext
		request  ConversationApiRequest
		loopOn   bool
		loopErr  error
		approval waitingApproval
		readErr  error
		refused  bool
	}{
		{"read-only approves a change on the alert", readOnly, answer, true, nil, alertApproval, nil, true},
		{"read-only dismisses it", readOnly, ConversationApiRequest{AccountId: account, ConversationId: "conv-1", AgentId: "agent-1", Resolution: "dismiss"}, true, nil, alertApproval, nil, true},
		{"the follow-up cannot be read: refused", readOnly, answer, true, nil, waitingApproval{}, errors.New("db down"), true},
		{"a writer approves", writer, answer, true, nil, alertApproval, nil, false},
		{"read-only answers a clarifying question", readOnly, answer, true, nil, waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "event-fp1"}, nil, false},
		{"loop off", readOnly, answer, false, nil, alertApproval, nil, false},
		{"loop flag not read: refused", readOnly, answer, false, errors.New("tenant not found"), alertApproval, nil, true},
		{"not an alert's conversation", readOnly, ConversationApiRequest{AccountId: account, SessionId: "chat-1", ConversationId: "conv-1", AgentId: "agent-1"}, true, nil,
			waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "chat-1", Confirmation: true}, nil, false},
		{"the RCA's own conversation", readOnly, answer, true, nil,
			waitingApproval{Found: true, ConversationId: "conv-1", SessionId: events.SessionIdPrefixEventRCA + "fp1", Confirmation: true}, nil, false},
		{"a new message, not an answer", readOnly, ConversationApiRequest{AccountId: account, SessionId: "event-fp1", Query: "go with option 1"}, true, nil, alertApproval, nil, false},
		// The bypasses: the request's own message id, session or conversation do not decide.
		{"a made-up message id", readOnly, ConversationApiRequest{AccountId: account, SessionId: "event-fp1", ConversationId: "conv-1", MessageId: "not-the-followup", AgentId: "agent-1"},
			true, nil, alertApproval, nil, true},
		{"no session or message id sent", readOnly, ConversationApiRequest{AccountId: account, ConversationId: "conv-1", AgentId: "agent-1", Query: "yes"}, true, nil, alertApproval, nil, true},
		{"another conversation named than the follow-up's", readOnly, ConversationApiRequest{AccountId: account, SessionId: "chat-1", ConversationId: "conv-2", AgentId: "agent-1"},
			true, nil, alertApproval, nil, true},
		{"no such agent: nothing to answer", readOnly, answer, true, nil, waitingApproval{}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubLoopOn(t, tc.loopOn)
			if tc.loopErr != nil {
				events.RemediationLoopRead = func(string) (bool, error) { return false, tc.loopErr }
			}
			swap(t, &approvalOfAgent, func(_ context.Context, accountId, agentId string) (waitingApproval, error) {
				assert.Equal(t, account, accountId)
				return tc.approval, tc.readErr
			})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			assert.Equal(t, tc.refused, refuseReadOnlyAlertApproval(c, tc.ctx(t), tc.request))
			if tc.refused {
				assert.Equal(t, 403, rec.Code)
			}
		})
	}
}
