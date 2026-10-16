package api

import (
	"net/http"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Validation paths in handleFollowupCancel must reject bad input BEFORE the
// DAO is consulted — otherwise a malformed client call would hit the DB
// unnecessarily. These tests exercise only the short-circuit branches so
// they can run without a live database. (#27582)
func TestHandleFollowupCancel_Validation(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	cases := []struct {
		name         string
		request      ConversationApiRequest
		expectStatus int
		expectErrSub string
	}{
		{
			name:         "missing conversation_id is rejected",
			request:      ConversationApiRequest{Resolution: "dismiss"},
			expectStatus: http.StatusBadRequest,
			expectErrSub: "conversation_id is required",
		},
		{
			name: "unsupported resolution is rejected",
			request: ConversationApiRequest{
				ConversationId: "00000000-0000-0000-0000-000000000000",
				Resolution:     "approve",
			},
			expectStatus: http.StatusBadRequest,
			expectErrSub: "unsupported resolution",
		},
		{
			name: "missing agent_id is rejected",
			request: ConversationApiRequest{
				ConversationId: "00000000-0000-0000-0000-000000000000",
				Resolution:     "dismiss",
			},
			expectStatus: http.StatusBadRequest,
			expectErrSub: "agent_id is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, status, err := handleFollowupCancel(ctx, tc.request)
			assert.Nil(t, resp)
			assert.Equal(t, tc.expectStatus, status)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.expectErrSub)
			}
		})
	}
}

// cancelConversationDao serves one conversation.
type cancelConversationDao struct {
	core.IConversationDao
	conversation core.Conversation
}

func (d cancelConversationDao) GetConversation(_ string) (core.Conversation, error) {
	return d.conversation, nil
}

// starterOnlyPolicy lets only a governed run's starter act on its conversation.
type starterOnlyPolicy struct{ core.NoRunPolicy }

func (starterOnlyPolicy) MayActOnConversation(conversation core.Conversation, userId string) bool {
	return conversation.UserID.String() == userId
}

// Dismissing a follow-up resumes the run, so the dismiss path asks the policy
// of the conversation's stored owner who may act on it; an ordinary
// conversation (no profile) is anyone's on the account, whatever its session.
func TestHandleFollowupCancel_RowOwnerDecidesWhoMayDismiss(t *testing.T) {
	core.RegisterAgentRunPolicy("followup_test_ext", starterOnlyPolicy{})
	t.Cleanup(func() { core.RegisterAgentRunPolicy("followup_test_ext", nil) })
	starter, account := uuid.New(), uuid.New()
	conversation := core.Conversation{ID: uuid.New(), AccountID: account, UserID: starter,
		SessionID: uuid.NewString(), Status: core.ConversationStatusCompleted,
		RunProfile: &core.RunProfile{Extension: "followup_test_ext", Agent: "governed"}}
	previous := core.GetConversationDao()
	core.SetConversationDao(cancelConversationDao{conversation: conversation})
	t.Cleanup(func() { core.SetConversationDao(previous) })

	request := ConversationApiRequest{ConversationId: conversation.ID.String(), AccountId: account.String(),
		Resolution: "dismiss", AgentId: uuid.NewString(), UserId: uuid.NewString()}
	_, status, err := handleFollowupCancel(security.NewRequestContextForSuperAdmin(), request)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Error(t, err)

	// The starter gets past the check (here to the already-finished reply).
	request.UserId = starter.String()
	resp, status, err := handleFollowupCancel(security.NewRequestContextForSuperAdmin(), request)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, resp["idempotent"])

	// Any user may still dismiss on an ordinary conversation, even on a
	// session id that looks like a governed run's.
	conversation.RunProfile = nil
	conversation.SessionID = "bee-" + uuid.NewString()
	core.SetConversationDao(cancelConversationDao{conversation: conversation})
	request.UserId = uuid.NewString()
	_, status, err = handleFollowupCancel(security.NewRequestContextForSuperAdmin(), request)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
}
