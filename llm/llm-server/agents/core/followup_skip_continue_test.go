package core

import (
	"errors"
	"testing"

	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// siblingFakeDao is a minimal IConversationDao exercising only
// ListAgentsByFollowupMessageId, the one method hasBlockingSibling calls.
type siblingFakeDao struct {
	IConversationDao

	siblings []ConversationAgent
	err      error
}

func (f *siblingFakeDao) ListAgentsByFollowupMessageId(accountId, followupMessageId string) ([]ConversationAgent, error) {
	return f.siblings, f.err
}

// TestHasBlockingSibling locks in the fail-safe contract for the v1
// skip-and-continue sibling gate: resume only when we can positively confirm
// no true sibling is still waiting on the same follow-up message. Anything
// short of that (a real sibling still waiting, or a DAO error that leaves
// the answer unknown) must block skip-and-continue and let the caller fall
// back to today's terminate path — never silently strand a sibling.
func TestHasBlockingSibling(t *testing.T) {
	parentID := uuid.New()
	primary := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID, FollowupMessageID: uuid.New(), AccountID: uuid.New()}

	t.Run("no other agents bound to the followup → not blocking", func(t *testing.T) {
		dao := &siblingFakeDao{siblings: []ConversationAgent{primary}}
		assert.False(t, hasBlockingSibling(dao, primary))
	})

	t.Run("true sibling still waiting → blocking", func(t *testing.T) {
		sib := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID, Status: AgentExecutionStatusWaiting}
		dao := &siblingFakeDao{siblings: []ConversationAgent{primary, sib}}
		assert.True(t, hasBlockingSibling(dao, primary))
	})

	t.Run("true sibling still waiting_for_client_tool → blocking", func(t *testing.T) {
		sib := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID, Status: AgentExecutionStatusWaitingForClientTool}
		dao := &siblingFakeDao{siblings: []ConversationAgent{primary, sib}}
		assert.True(t, hasBlockingSibling(dao, primary))
	})

	t.Run("true sibling already resolved (success) → not blocking", func(t *testing.T) {
		sib := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID, Status: AgentExecutionStatusSuccess}
		dao := &siblingFakeDao{siblings: []ConversationAgent{primary, sib}}
		assert.False(t, hasBlockingSibling(dao, primary))
	})

	t.Run("bound row is the parent, not a true sibling, even if waiting → not blocking", func(t *testing.T) {
		parentRow := ConversationAgent{ID: parentID, ParentAgentID: uuid.Nil, Status: AgentExecutionStatusWaiting}
		dao := &siblingFakeDao{siblings: []ConversationAgent{primary, parentRow}}
		assert.False(t, hasBlockingSibling(dao, primary),
			"the parent bound to a child's followup must not itself be treated as a blocking sibling")
	})

	t.Run("DAO error → fail closed (treated as blocking)", func(t *testing.T) {
		dao := &siblingFakeDao{err: errors.New("db unavailable")}
		assert.True(t, hasBlockingSibling(dao, primary),
			"an error means we can't rule out a stranded sibling, so treat it as blocking")
	})

	t.Run("agent has no followup_message_id → not blocking (nothing to check)", func(t *testing.T) {
		noFollowup := ConversationAgent{ID: uuid.New(), ParentAgentID: parentID, FollowupMessageID: uuid.Nil}
		dao := &siblingFakeDao{siblings: []ConversationAgent{}}
		assert.False(t, hasBlockingSibling(dao, noFollowup))
	})
}

// skipContinueFakeDao is a minimal IConversationDao for exercising
// TrySkipAndContinueFollowup's out-of-scope-type short-circuit — the routing
// decision that must happen before any lock is taken or resume attempted.
type skipContinueFakeDao struct {
	IConversationDao

	agent          ConversationAgent
	followupConfig string
}

func (f *skipContinueFakeDao) ListConversationAgents(messageId, agentId string) ([]ConversationAgent, error) {
	return []ConversationAgent{f.agent}, nil
}

func (f *skipContinueFakeDao) GetConversationMessage(id, accountId, conversationId string) (ConversationMessage, error) {
	cfg := f.followupConfig
	return ConversationMessage{ID: f.agent.FollowupMessageID, MessageConfig: &cfg}, nil
}

// TestTrySkipAndContinueFollowup_OutOfScopeTypesNeverAttemptResume locks in
// the v1 scope boundary: only text/single_select/multi_select/user_input are
// eligible for skip-and-continue. tool_confirmation, tool_config, and
// account_select must return handled=false immediately (before ever taking
// the conversation lock or touching resume) so the caller's existing
// terminate path runs unchanged — their answer field is interpreted as
// structured data downstream, not free text, so extending skip semantics to
// those types is explicitly out of scope for v1.
func TestTrySkipAndContinueFollowup_OutOfScopeTypesNeverAttemptResume(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	ctx := security.NewRequestContextForSuperAdmin()
	agentID := uuid.New()
	followupMsgID := uuid.New()
	accountID := uuid.New() // must match the account passed to TrySkipAndContinueFollowup below,
	// or the cross-account guard short-circuits before ever reaching the
	// type-scope check this test exists to prove.
	agent := ConversationAgent{
		ID:                agentID,
		AccountID:         accountID,
		FollowupMessageID: followupMsgID,
		Status:            AgentExecutionStatusWaiting,
	}

	outOfScope := []string{
		`{"followupType":"tool_confirmation"}`,
		`{"followupType":"tool_config"}`,
		`{"followupType":"account_select"}`,
	}
	for _, cfg := range outOfScope {
		t.Run(cfg, func(t *testing.T) {
			SetConversationDao(&skipContinueFakeDao{agent: agent, followupConfig: cfg})
			handled, resp, err := TrySkipAndContinueFollowup(ctx, accountID.String(), uuid.New().String(), agentID.String(), "")
			assert.False(t, handled, "out-of-scope FollowupType must not be handled by skip-and-continue")
			assert.Equal(t, NBAgentResponse{}, resp)
			assert.NoError(t, err)
		})
	}
}

// TestTrySkipAndContinueFollowup_MalformedConfigFailsClosed is a security
// regression test: an unparsable message_config must never let followupType
// silently keep its FollowupTypeText default and slip through as in-scope —
// the real type could be tool_confirmation. Declines cleanly (handled=false,
// no error) rather than surfacing a 500 that would block the user's skip
// entirely over what's likely just corrupted data.
func TestTrySkipAndContinueFollowup_MalformedConfigFailsClosed(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	ctx := security.NewRequestContextForSuperAdmin()
	agentID := uuid.New()
	accountID := uuid.New()
	agent := ConversationAgent{
		ID:                agentID,
		AccountID:         accountID,
		FollowupMessageID: uuid.New(),
		Status:            AgentExecutionStatusWaiting,
	}
	SetConversationDao(&skipContinueFakeDao{agent: agent, followupConfig: `{not valid json`})

	handled, resp, err := TrySkipAndContinueFollowup(ctx, accountID.String(), uuid.New().String(), agentID.String(), "")
	assert.False(t, handled, "malformed config must decline, never default to an in-scope type")
	assert.Equal(t, NBAgentResponse{}, resp)
	assert.NoError(t, err, "declining is not itself an error — the caller should fall through to terminate")
}

// TestTrySkipAndContinueFollowup_NoActiveFollowup covers the guard for an
// agent with no bound follow-up message (already resolved, or never had
// one) — must decline cleanly rather than erroring, letting the caller fall
// back to terminate exactly as it would for any other non-applicable case.
func TestTrySkipAndContinueFollowup_NoActiveFollowup(t *testing.T) {
	original := GetConversationDao()
	defer SetConversationDao(original)

	ctx := security.NewRequestContextForSuperAdmin()
	agentID := uuid.New()
	SetConversationDao(&skipContinueFakeDao{
		agent: ConversationAgent{ID: agentID, FollowupMessageID: uuid.Nil, Status: AgentExecutionStatusWaiting},
	})

	handled, resp, err := TrySkipAndContinueFollowup(ctx, uuid.New().String(), uuid.New().String(), agentID.String(), "")
	assert.False(t, handled)
	assert.Equal(t, NBAgentResponse{}, resp)
	assert.NoError(t, err)
}

// TestTrySkipAndContinueFollowup_CrossAccountAgentRejected is a security
// regression test — the same guard proven for the other two resolution
// paths. A cross-account agentId must decline (handled=false, no error) so
// the caller falls through to its own terminate path, which independently
// and authoritatively rejects it — not silently read the followup message
// or run the sibling check for another account's agent.
func TestTrySkipAndContinueFollowup_CrossAccountAgentRejected(t *testing.T) {
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
	handled, resp, err := TrySkipAndContinueFollowup(ctx, uuid.New().String(), uuid.New().String(), uuid.New().String(), "")
	assert.False(t, handled, "a cross-account agent must not be handled by skip-and-continue")
	assert.Equal(t, NBAgentResponse{}, resp)
	assert.NoError(t, err)
}
