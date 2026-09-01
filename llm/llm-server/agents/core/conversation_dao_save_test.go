package core

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for GH #37367.

func TestSaveConversation_EmptyUserId_TargetsNullUserPartialIndex(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	id := uuid.New().String()
	rows := sqlmock.NewRows([]string{"id"}).AddRow(id)

	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (session_id, account_id) WHERE user_id IS NULL")).
		WithArgs(id, "event-abc123", "tenant-1", "account-1", nil, "", ConversationStatusInProgress, ConversationSourceInvestigation, "title", nil, nil, nil, nil).
		WillReturnRows(rows)

	_, err := dao.SaveConversation(id, "event-abc123", "tenant-1", "account-1", "", "", "title", ConversationStatusInProgress, ConversationSourceInvestigation, "", "", nil, "")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveConversation_RealUserId_TargetsOriginalConstraint(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	id := uuid.New().String()
	userId := uuid.New().String()
	rows := sqlmock.NewRows([]string{"id"}).AddRow(id)

	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (session_id, user_id, account_id)")).
		WithArgs(id, "manual-session", "tenant-1", "account-1", userId, "", ConversationStatusInProgress, ConversationSourceUserInvestigation, "title", nil, nil, nil, nil).
		WillReturnRows(rows)

	_, err := dao.SaveConversation(id, "manual-session", "tenant-1", "account-1", userId, "", "title", ConversationStatusInProgress, ConversationSourceUserInvestigation, "", "", nil, "")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConversationBySession_OrdersByUpdatedAtDesc(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	id := uuid.New().String()
	accountId := uuid.New().String()
	tenantId := uuid.New().String()

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "session_id", "account_id", "context", "status", "tenant_id",
		"title", "source", "llm_provider", "llm_model", "llm_tier_overrides", "llm_config_source",
	}).AddRow(id, nil, "event-abc123", accountId, nil, ConversationStatusInProgress, tenantId, nil, nil, nil, nil, nil, nil)

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY updated_at DESC LIMIT 1")).
		WithArgs("event-abc123", accountId).
		WillReturnRows(rows)

	conv, err := dao.GetConversationBySession(accountId, "event-abc123")
	require.NoError(t, err)
	assert.Equal(t, id, conv.ID.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}
