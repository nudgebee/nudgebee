package core

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolcore "nudgebee/llm/tools/core"
)

// Regression tests for GH #37367.

func TestSaveConversation_EmptyUserId_TargetsNullUserPartialIndex(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	id := uuid.New().String()
	rows := sqlmock.NewRows([]string{"id"}).AddRow(id)

	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (session_id, account_id) WHERE user_id IS NULL")).
		WithArgs(id, "event-abc123", "tenant-1", "account-1", nil, "", ConversationStatusInProgress, ConversationSourceInvestigation, "title", nil, nil, nil, nil, nil).
		WillReturnRows(rows)

	_, err := dao.SaveConversation(id, "event-abc123", "tenant-1", "account-1", "", "", "title", ConversationStatusInProgress, ConversationSourceInvestigation, "", "", nil, "", nil)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveConversation_RealUserId_TargetsOriginalConstraint(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	id := uuid.New().String()
	userId := uuid.New().String()
	rows := sqlmock.NewRows([]string{"id"}).AddRow(id)

	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (session_id, user_id, account_id)")).
		WithArgs(id, "manual-session", "tenant-1", "account-1", userId, "", ConversationStatusInProgress, ConversationSourceUserInvestigation, "title", nil, nil, nil, nil, nil).
		WillReturnRows(rows)

	_, err := dao.SaveConversation(id, "manual-session", "tenant-1", "account-1", userId, "", "title", ConversationStatusInProgress, ConversationSourceUserInvestigation, "", "", nil, "", nil)
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
		"title", "source", "llm_provider", "llm_model", "llm_tier_overrides", "llm_config_source", "run_profile",
	}).AddRow(id, nil, "event-abc123", accountId, nil, ConversationStatusInProgress, tenantId, nil, nil, nil, nil, nil, nil, nil)

	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY updated_at DESC LIMIT 1")).
		WithArgs("event-abc123", accountId).
		WillReturnRows(rows)

	conv, err := dao.GetConversationBySession(accountId, "event-abc123")
	require.NoError(t, err)
	assert.Equal(t, id, conv.ID.String())
	assert.Nil(t, conv.RunProfile, "an ordinary conversation has no run profile")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A governed run's profile is written as JSON on the INSERT and read back
// from the row.
func TestRunProfileRoundTripsThroughTheRow(t *testing.T) {
	dao, mock := setupConversationDAOMock(t)
	id := uuid.New().String()
	userId := uuid.New().String()
	profile := &RunProfile{Extension: "test_ext", Ref: "run-1", Agent: "governed"}
	stored := []byte(`{"extension":"test_ext","ref":"run-1","agent":"governed"}`)

	mock.ExpectQuery(regexp.QuoteMeta("ON CONFLICT (session_id, user_id, account_id)")).
		WithArgs(id, "s", "tenant-1", "account-1", userId, "", ConversationStatusInProgress, ConversationSourceUserInvestigation, "title", nil, nil, nil, nil, stored).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
	_, err := dao.SaveConversation(id, "s", "tenant-1", "account-1", userId, "", "title", ConversationStatusInProgress, ConversationSourceUserInvestigation, "", "", nil, "", profile)
	require.NoError(t, err)

	accountId, tenantId := uuid.New().String(), uuid.New().String()
	mock.ExpectQuery(regexp.QuoteMeta("FROM llm_conversations WHERE id = $1")).
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "session_id", "account_id", "context", "status", "tenant_id",
			"title", "source", "llm_provider", "llm_model", "llm_tier_overrides", "llm_config_source", "run_profile",
		}).AddRow(id, nil, "s", accountId, nil, ConversationStatusInProgress, tenantId, nil, nil, nil, nil, nil, nil, stored))
	conv, err := dao.GetConversation(id)
	require.NoError(t, err)
	assert.Equal(t, profile, conv.RunProfile)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// #40734: the follow-up check starts a turn only in an idle conversation, saving its message with its config
// and claiming the conversation in the same transaction.
func TestStartIdleTurn(t *testing.T) {
	for _, idle := range []bool{false, true} {
		dao, mock := setupConversationDAOMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta("FOR UPDATE OF c")).WithArgs("c1", "a1").WillReturnRows(sqlmock.NewRows([]string{"idle"}).AddRow(idle))
		if idle {
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO llm_conversation_messages")).
				WithArgs("m1", "a1", "c1", MessageRoleHuman, "check", MessageTypeGeneration, sqlmock.AnyArg(), "remediation", uuid.Nil, `{"read_only_run":true,"strict_read_only":true,"capabilities":{}}`).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("UPDATE llm_conversations SET status = 'IN_PROGRESS'")).WithArgs("c1").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
		} else {
			mock.ExpectRollback()
		}
		started, err := dao.StartIdleTurn(context.Background(), "c1", "a1", "m1", "check", "remediation", toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true})
		require.NoError(t, err)
		assert.Equal(t, idle, started)
		assert.NoError(t, mock.ExpectationsWereMet())
	}
}
