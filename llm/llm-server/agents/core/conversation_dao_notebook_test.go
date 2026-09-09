package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func expectNotebookParentLock(mock sqlmock.Sqlmock, messageStatus, conversationStatus string) {
	mock.ExpectQuery(`SELECT m\.status, c\.status`).
		WithArgs("agent-id", notebookDummyAgent).
		WillReturnRows(sqlmock.NewRows([]string{"message_status", "conversation_status"}).AddRow(messageStatus, conversationStatus))
}

func TestUpdateConversationNotebook_UpdatesCompletedNotebookRow(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	mock.ExpectBegin()
	expectNotebookParentLock(mock, string(ConversationStatusInProgress), string(ConversationStatusInProgress))
	mock.ExpectExec(`UPDATE llm_conversation_agent\s+SET response`).
		WithArgs("agent-id", "replacement", "breadcrumb", notebookDummyAgent).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	assert.NoError(t, dao.UpdateConversationNotebook("agent-id", "replacement", "breadcrumb"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationNotebook_TruncatesSummaryAtRuneBoundary(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	summary := strings.Repeat("a", 249) + "é"
	truncatedSummary := strings.Repeat("a", 249)
	assert.True(t, utf8.ValidString(truncatedSummary))

	mock.ExpectBegin()
	expectNotebookParentLock(mock, string(ConversationStatusInProgress), string(ConversationStatusInProgress))
	mock.ExpectExec(`UPDATE llm_conversation_agent\s+SET response`).
		WithArgs("agent-id", "replacement", truncatedSummary, notebookDummyAgent).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	assert.NoError(t, dao.UpdateConversationNotebook("agent-id", "replacement", summary))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationNotebook_RejectsMissingNotebookRow(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	mock.ExpectBegin()
	expectNotebookParentLock(mock, string(ConversationStatusInProgress), string(ConversationStatusInProgress))
	mock.ExpectExec(`UPDATE llm_conversation_agent\s+SET response`).
		WithArgs("agent-id", "replacement", "breadcrumb", notebookDummyAgent).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := dao.UpdateConversationNotebook("agent-id", "replacement", "breadcrumb")
	assert.EqualError(t, err, "history: notebook agent update affected 0 rows")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateConversationNotebook_RejectsTerminatedParent(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	mock.ExpectBegin()
	expectNotebookParentLock(mock, string(ConversationStatusTerminated), string(ConversationStatusTerminated))
	mock.ExpectRollback()

	err := dao.UpdateConversationNotebook("agent-id", "replacement", "breadcrumb")
	assert.EqualError(t, err, "history: notebook parent is terminated")
	assert.NoError(t, mock.ExpectationsWereMet())
}
