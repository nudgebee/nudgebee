package core

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"nudgebee/llm/security"
)

func TestApplyUsageLink(t *testing.T) {
	link := UsageLink{ConversationID: "conv-link", MessageID: "msg-link", AgentID: "agent-link", ModelTier: "retrieval", TaskType: "reflection"}
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name   string
		ctx    *security.RequestContext
		record TokenUsageRecord
		want   TokenUsageRecord
	}{
		{
			name:   "no link leaves the record alone",
			ctx:    llmOverrideContext(),
			record: TokenUsageRecord{},
			want:   TokenUsageRecord{},
		},
		{
			name:   "link fills what the caller left empty",
			ctx:    WithUsageLink(llmOverrideContext(), link),
			record: TokenUsageRecord{},
			want: TokenUsageRecord{ConversationID: "conv-link", MessageID: "msg-link", AgentID: strPtr("agent-link"),
				ModelTier: strPtr("retrieval"), TaskType: strPtr("reflection")},
		},
		{
			// A caller that named any part of the run keeps all of it: mixing its
			// conversation with the link's message would describe no real run.
			name:   "caller's run is never mixed with the link's",
			ctx:    WithUsageLink(llmOverrideContext(), link),
			record: TokenUsageRecord{ConversationID: "conv-caller"},
			want: TokenUsageRecord{ConversationID: "conv-caller",
				ModelTier: strPtr("retrieval"), TaskType: strPtr("reflection")},
		},
		{
			name:   "caller's tier and task type win",
			ctx:    WithUsageLink(llmOverrideContext(), link),
			record: TokenUsageRecord{ModelTier: strPtr("reasoning"), TaskType: strPtr("investigation")},
			want: TokenUsageRecord{ConversationID: "conv-link", MessageID: "msg-link", AgentID: strPtr("agent-link"),
				ModelTier: strPtr("reasoning"), TaskType: strPtr("investigation")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := tt.record
			applyUsageLink(tt.ctx, &record)
			assert.Equal(t, tt.want, record)
		})
	}
}

// The link must label the usage row without becoming a model tier on the
// context: that key selects the model, and a workspace's label must not.
func TestWithUsageLinkDoesNotSelectTheModel(t *testing.T) {
	ctx := WithUsageLink(llmOverrideContext(), UsageLink{ModelTier: "retrieval", TaskType: "reflection"})
	assert.Empty(t, modelTierFromContext(ctx))
	assert.Empty(t, taskTypeFromContext(ctx))
}

func waitForUsage(t *testing.T, usage chan *TokenUsageRecord) *TokenUsageRecord {
	t.Helper()
	select {
	case record := <-usage:
		return record
	case <-time.After(3 * time.Second):
		t.Fatal("no usage row was written")
		return nil
	}
}

// A call generated with no conversation id still has its usage row written
// against the linked run, on both the success and the failure path.
func TestUsageLinkReachesTheUsageRow(t *testing.T) {
	link := UsageLink{ConversationID: uuid.NewString(), MessageID: uuid.NewString(), AgentID: uuid.NewString(), ModelTier: "retrieval", TaskType: "reflection"}
	assertLinked := func(t *testing.T, record *TokenUsageRecord) {
		t.Helper()
		assert.Equal(t, link.ConversationID, record.ConversationID)
		assert.Equal(t, link.MessageID, record.MessageID)
		require.NotNil(t, record.AgentID)
		assert.Equal(t, link.AgentID, *record.AgentID)
		assert.Equal(t, "code_analyzer", record.AgentName)
		require.NotNil(t, record.ModelTier)
		assert.Equal(t, "retrieval", *record.ModelTier)
		require.NotNil(t, record.TaskType)
		assert.Equal(t, "reflection", *record.TaskType)
	}

	t.Run("success", func(t *testing.T) {
		dao := &claimCaptureDAO{usage: make(chan *TokenUsageRecord, 1)}
		installClaimCaptureDAO(t, dao)
		withFakeLLMModel(t, &fakeLLMModel{turns: []fakeTurn{{content: "ok"}}})

		_, err := GenerateAndTrackLLMContent(WithUsageLink(llmOverrideContext(), link), "", uuid.NewString(), "", "", "code_analyzer", false,
			[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")}, false)
		require.NoError(t, err)

		record := waitForUsage(t, dao.usage)
		assert.Equal(t, "success", record.RequestStatus)
		assertLinked(t, record)
	})

	t.Run("failure", func(t *testing.T) {
		dao := &claimCaptureDAO{usage: make(chan *TokenUsageRecord, 1)}
		installClaimCaptureDAO(t, dao)

		recordTokenUsageFailure(WithUsageLink(llmOverrideContext(), link), "", "", "code_analyzer", "code_analyzer", "test", "test", "", "", nil, errors.New("provider unavailable"))

		record := waitForUsage(t, dao.usage)
		assert.Equal(t, "failure", record.RequestStatus)
		assertLinked(t, record)
	})
}

// The lookup is what keeps a workspace's usage inside its own account: the run
// must match on the account and the agent name, not on its id alone.
func TestGetAgentRunAttribution(t *testing.T) {
	const lookup = `FROM llm_conversation_agent WHERE id = \$1 AND account_id = \$2 AND agent_name = \$3`

	t.Run("run in the account", func(t *testing.T) {
		dao, mock := setupConversationDAOMock(t)
		mock.ExpectQuery(lookup).WithArgs("run-1", "acct-1", "code_analyzer").
			WillReturnRows(sqlmock.NewRows([]string{"conversation_id", "message_id", "user_id"}).AddRow("conv-1", "msg-1", "user-1"))

		conversationID, messageID, userID, err := dao.GetAgentRunAttribution("run-1", "acct-1", "code_analyzer")

		require.NoError(t, err)
		assert.Equal(t, []string{"conv-1", "msg-1", "user-1"}, []string{conversationID, messageID, userID})
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no such run in the account", func(t *testing.T) {
		dao, mock := setupConversationDAOMock(t)
		mock.ExpectQuery(lookup).WithArgs("run-1", "acct-2", "code_analyzer").
			WillReturnRows(sqlmock.NewRows([]string{"conversation_id", "message_id", "user_id"}))

		_, _, _, err := dao.GetAgentRunAttribution("run-1", "acct-2", "code_analyzer")

		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("run with no conversation cannot be linked", func(t *testing.T) {
		dao, mock := setupConversationDAOMock(t)
		mock.ExpectQuery(lookup).WithArgs("run-1", "acct-1", "code_analyzer").
			WillReturnRows(sqlmock.NewRows([]string{"conversation_id", "message_id", "user_id"}).AddRow(nil, "msg-1", ""))

		_, _, _, err := dao.GetAgentRunAttribution("run-1", "acct-1", "code_analyzer")

		require.Error(t, err)
	})
}

func TestWithUsageLinkOnZeroValueContext(t *testing.T) {
	ctx := WithUsageLink(&security.RequestContext{}, UsageLink{ConversationID: "conv"})
	link, ok := ctx.GetContext().Value(contextKeyUsageLink).(UsageLink)
	require.True(t, ok)
	assert.Equal(t, "conv", link.ConversationID)
	assert.NotPanics(t, func() { applyUsageLink(&security.RequestContext{}, &TokenUsageRecord{}) })
	assert.NotPanics(t, func() { applyUsageLink(nil, &TokenUsageRecord{}) })
	assert.NotPanics(t, func() { applyUsageLink(ctx, nil) })
}
