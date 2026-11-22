package core

import (
	"testing"

	"nudgebee/llm/common"
	llmruntime "nudgebee/llm/llms/runtime"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mutual-exclusivity guarantee: each updater MUST atomically clear the other
// column in the same UPDATE so a concurrent reader can never see both modes
// set at once. These tests assert the actual SQL the DAO emits.

func setupConversationDAOMock(t *testing.T) (*ConversationDao, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgresql")}}, mock
}

func TestUpdateConversationModelBlanket_ClearsTierColumn(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	conversationID := uuid.New().String()

	// Must SET llm_provider, llm_model AND llm_tier_overrides = NULL in one UPDATE.
	mock.ExpectExec(`UPDATE llm_conversations\s+SET llm_provider = \$2, llm_model = \$3,\s+llm_tier_overrides = NULL`).
		WithArgs(conversationID, "openai", "gpt-4").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := dao.UpdateConversationModelBlanket(conversationID, "openai", "gpt-4")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationModelBlanket_RejectsEmptyID(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	err := dao.UpdateConversationModelBlanket("", "openai", "gpt-4")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should be issued for empty id")
}

func TestUpdateConversationTierOverrides_ClearsBlanketColumns(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	conversationID := uuid.New().String()
	overrides := llmruntime.ConversationTierOverrides{
		Picks: map[string]llmruntime.TierModelPick{
			"reasoning": {Provider: "googleai", Model: "gemini-2.5-pro"},
			"retrieval": {Provider: "openai", Model: "gpt-4o-mini"},
		},
	}

	// Must SET llm_provider = NULL, llm_model = NULL AND llm_tier_overrides = <jsonb>
	// in one UPDATE.
	mock.ExpectExec(`UPDATE llm_conversations\s+SET llm_provider = NULL, llm_model = NULL,\s+llm_tier_overrides = \$2`).
		WithArgs(conversationID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := dao.UpdateConversationTierOverrides(conversationID, overrides)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationTierOverrides_RejectsEmptyID(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	err := dao.UpdateConversationTierOverrides("", llmruntime.ConversationTierOverrides{Picks: map[string]llmruntime.TierModelPick{"reasoning": {Provider: "x", Model: "y"}}})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should be issued for empty id")
}
