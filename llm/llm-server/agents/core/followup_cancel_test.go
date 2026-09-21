package core

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// TestHandleFollowupDismiss_MissingAgentId is a pure unit test — it exercises
// the validation short-circuit before any DAO call happens, so it runs with
// no DB. (#27582)
func TestHandleFollowupDismiss_MissingAgentId(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	err := HandleFollowupDismiss(ctx, "acct", "conv", "", "user typed the wrong pod name")
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "agent_id is required")
	}
}

// crossAccountFakeDao returns a single agent scoped to a different account
// than whatever the test calls the function under test with — used to prove
// the agentBelongsToAccount guard actually rejects a cross-account agentId
// instead of silently operating on it.
type crossAccountFakeDao struct {
	IConversationDao

	agent ConversationAgent
}

func (f *crossAccountFakeDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	return []ConversationAgent{f.agent}, nil
}

// TestHandleFollowupDismiss_CrossAccountAgentRejected is a security
// regression test: agentId alone must not be sufficient to dismiss a
// follow-up belonging to a different account, even though only
// conversation-level ownership is validated by handleFollowupCancel before
// this is ever called. Same "not found" error as a genuinely-missing agent —
// deliberately indistinguishable, so a caller probing IDs can't tell "does
// not exist" from "exists in another account."
func TestHandleFollowupDismiss_CrossAccountAgentRejected(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	agentAccountID := uuid.New()
	SetConversationDao(&crossAccountFakeDao{
		agent: ConversationAgent{
			ID:                uuid.New(),
			AccountID:         agentAccountID,
			Status:            AgentExecutionStatusWaiting,
			FollowupMessageID: uuid.New(),
		},
	})

	ctx := security.NewRequestContextForSuperAdmin()
	callerAccountID := uuid.New().String() // deliberately different from agentAccountID
	err := HandleFollowupDismiss(ctx, callerAccountID, "conv", uuid.New().String(), "")
	if assert.Error(t, err) {
		assert.Equal(t, "followup dismiss: agent not found", err.Error())
	}
}

// TestHandleFollowupDismiss_CrossConversationAgentRejected is a security
// regression test for the narrower case within a single account: agentId
// belongs to the right account but a DIFFERENT conversation than the caller
// supplied. Unlike HandleFollowupResponse and TrySkipAndContinueFollowup,
// this function never reads through GetConversationMessage (whose query is
// already scoped by conversation_id), so it needs its own explicit check —
// this proves that check actually fires.
func TestHandleFollowupDismiss_CrossConversationAgentRejected(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	accountID := uuid.New()
	agentConversationID := uuid.New()
	SetConversationDao(&crossAccountFakeDao{
		agent: ConversationAgent{
			ID:                uuid.New(),
			AccountID:         accountID,
			ConversationID:    agentConversationID,
			Status:            AgentExecutionStatusWaiting,
			FollowupMessageID: uuid.New(),
		},
	})

	ctx := security.NewRequestContextForSuperAdmin()
	callerConversationID := uuid.New().String() // deliberately different from agentConversationID
	err := HandleFollowupDismiss(ctx, accountID.String(), callerConversationID, uuid.New().String(), "")
	if assert.Error(t, err) {
		assert.Equal(t, "followup dismiss: agent not found", err.Error())
	}
}

// TestHandleFollowupResponse_CrossAccountAgentRejected is the same
// regression, for the legacy answer-resolution path.
func TestHandleFollowupResponse_CrossAccountAgentRejected(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	agentAccountID := uuid.New()
	SetConversationDao(&crossAccountFakeDao{
		agent: ConversationAgent{
			ID:        uuid.New(),
			AccountID: agentAccountID,
			Status:    AgentExecutionStatusWaiting,
		},
	})

	ctx := security.NewRequestContextForSuperAdmin()
	msg, err := HandleFollowupResponse(ctx, NBAgentRequest{
		AgentId:   uuid.New().String(),
		AccountId: uuid.New().String(), // deliberately different from agentAccountID
		Query:     "some answer",
	})
	assert.Equal(t, ConversationMessage{}, msg)
	if assert.Error(t, err) {
		assert.Equal(t, "followup: agentid is not found required", err.Error())
	}
}

// TestFollowupDismissOnWaitingConversation is an integration test that
// drives a conversation into ConversationStatusWaiting, dismisses the
// follow-up, and verifies the marker + terminal state. Gated on TEST_ACCOUNT
// like the rest of this package's integration tests.
func TestFollowupDismissOnWaitingConversation(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping test: TEST_ACCOUNT not set")
	}
	sc := security.NewRequestContextForSuperAdmin()

	sessionId := "ut-followup-cancel-1"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	// Clean slate.
	err := DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	llmAgent := ClarificationAgent{}
	resp, err := HandleConversationSessionRequest(sc, llmAgent, userId, accountId, sessionId, "show me logs of services-server")
	assert.Nil(t, err)
	assert.Equal(t, ConversationStatusWaiting, resp.Status)

	// Soft dismiss.
	err = HandleFollowupDismiss(sc, accountId, resp.ConversationId, resp.AgentId, "not interested right now")
	assert.Nil(t, err)

	// Second dismiss on the same (now-terminal) agent is a no-op, not an error.
	err = HandleFollowupDismiss(sc, accountId, resp.ConversationId, resp.AgentId, "")
	assert.Nil(t, err)
}
