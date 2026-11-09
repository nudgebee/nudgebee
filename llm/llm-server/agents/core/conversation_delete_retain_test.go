package core

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
)

func newRetainTestDao(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	prev := conversationDao
	conversationDao = &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	t.Cleanup(func() {
		conversationDao = prev
		_ = db.Close()
	})
	return mock
}

var retainTokenUsageSQL = regexp.QuoteMeta("UPDATE llm_conversation_token_usage t") +
	`[\s\S]*tenant_id = COALESCE\(t\.tenant_id, c\.tenant_id\)` +
	`[\s\S]*session_id = COALESCE\(t\.session_id, c\.session_id\)` +
	`[\s\S]*prompt_messages = NULL[\s\S]*response_content = NULL[\s\S]*error_message = NULL` +
	`[\s\S]*conversation_id = NULL[\s\S]*message_id = NULL`

// Deleting a conversation stamps its token usage with who it belongs to and
// clears the prompt/response/error text immediately before the conversation
// row goes, so a usage row written by a call still finishing is cleared too.
// The token rows themselves are unlinked, not deleted.
func TestDeleteConversation_RetainsTokenUsageBeforeDeleting(t *testing.T) {
	mock := newRetainTestDao(t)
	id := uuid.New()

	mock.ExpectExec("DELETE FROM llm_conversation_tool_calls").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM llm_conversation_agent").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM llm_conversation_messages").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM llm_conversation_saved").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM llm_conversation_references").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id::text, account_id::text FROM llm_conversation_memory").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id"}))
	mock.ExpectExec("DELETE FROM llm_conversation_memory").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(retainTokenUsageSQL).WithArgs(id.String()).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("DELETE FROM llm_conversations").WithArgs(id.String()).WillReturnResult(sqlmock.NewResult(0, 1))
	// A late row written while the delete ran is cleared and unlinked too.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE llm_conversation_token_usage") +
		`[\s\S]*prompt_messages = NULL[\s\S]*conversation_id = NULL[\s\S]*WHERE conversation_id = \$1`).
		WithArgs(id.String()).WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, DeleteConversation(id))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// If the text cannot be cleared, the conversation must not be deleted: the
// unlinked rows would otherwise keep the prompts the user asked to remove.
func TestDeleteConversation_StopsWhenRetainFails(t *testing.T) {
	mock := newRetainTestDao(t)
	id := uuid.New()
	mock.MatchExpectationsInOrder(false)

	for _, table := range []string{"tool_calls", "agent", "messages", "saved", "references", "memory"} {
		mock.ExpectExec("DELETE FROM llm_conversation_" + table).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectQuery("SELECT id::text, account_id::text FROM llm_conversation_memory").
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_id"}))
	mock.ExpectExec(retainTokenUsageSQL).WithArgs(id.String()).WillReturnError(errors.New("db down"))

	err := DeleteConversation(id)
	require.Error(t, err)
	// The conversation delete was never expected: sqlmock would fail the
	// test with an unexpected Exec if it ran.
	assert.NoError(t, mock.ExpectationsWereMet())
}
