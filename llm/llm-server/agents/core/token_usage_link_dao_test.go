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

	llmruntime "nudgebee/llm/llms/runtime"
)

func waitForUsage(t *testing.T, usage chan *llmruntime.TokenUsageRecord) *llmruntime.TokenUsageRecord {
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
	link := llmruntime.UsageLink{ConversationID: uuid.NewString(), MessageID: uuid.NewString(), AgentID: uuid.NewString(), ModelTier: "retrieval", TaskType: "reflection"}
	assertLinked := func(t *testing.T, record *llmruntime.TokenUsageRecord) {
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
		dao := &claimCaptureDAO{usage: make(chan *llmruntime.TokenUsageRecord, 1)}
		installClaimCaptureDAO(t, dao)
		withFakeLLMModel(t, &fakeLLMModel{turns: []fakeTurn{{content: "ok"}}})

		_, err := llmruntime.GenerateAndTrackLLMContent(llmruntime.WithUsageLink(llmOverrideContext(), link), "", uuid.NewString(), "", "", "code_analyzer", false,
			[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")}, false)
		require.NoError(t, err)

		record := waitForUsage(t, dao.usage)
		assert.Equal(t, "success", record.RequestStatus)
		assertLinked(t, record)
	})

	t.Run("failure", func(t *testing.T) {
		dao := &claimCaptureDAO{usage: make(chan *llmruntime.TokenUsageRecord, 1)}
		installClaimCaptureDAO(t, dao)

		llmruntime.RecordTokenUsageFailure(llmruntime.WithUsageLink(llmOverrideContext(), link), "", "", "code_analyzer", "code_analyzer", "test", "test", "", "", nil, errors.New("provider unavailable"))

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
