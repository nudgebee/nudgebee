package core

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

type fakeWaitingExpirer struct {
	notes   map[string]string // conversation id -> note it was ended with
	answer  map[string]bool   // conversation ids that are no longer WAITING
	failIDs map[string]bool
}

func (f *fakeWaitingExpirer) ExpireWaitingConversation(_ *security.RequestContext, _, conversationId, note string) (bool, error) {
	if f.failIDs[conversationId] {
		return false, errors.New("boom")
	}
	if f.answer[conversationId] {
		return false, nil
	}
	if f.notes == nil {
		f.notes = map[string]string{}
	}
	f.notes[conversationId] = note
	return true, nil
}

var unattendedSources = []string{"Investigation", "InstantNotification", "Automation"}

func expectUnattendedSelect(mock sqlmock.Sqlmock, hours int, ids ...string) {
	rows := sqlmock.NewRows([]string{"id", "account_id"})
	for _, id := range ids {
		rows.AddRow(id, "account-1")
	}
	mock.ExpectQuery(`SELECT id, account_id FROM llm_conversations\s+WHERE status = \$1 AND source = ANY\(\$2\) AND updated_at < NOW\(\) - make_interval\(hours => \$3\)`).
		WithArgs("WAITING", pq.Array(unattendedSources), hours, waitingExpiryBatchSize).
		WillReturnRows(rows)
}

func expectChatSelect(mock sqlmock.Sqlmock, excluded []string, days int, ids ...string) {
	rows := sqlmock.NewRows([]string{"id", "account_id"})
	for _, id := range ids {
		rows.AddRow(id, "account-1")
	}
	mock.ExpectQuery(`SELECT id, account_id FROM llm_conversations\s+WHERE status = \$1 AND COALESCE\(source, ''\) <> ALL\(\$2\) AND updated_at < NOW\(\) - make_interval\(days => \$3\)`).
		WithArgs("WAITING", pq.Array(excluded), days, waitingExpiryBatchSize).
		WillReturnRows(rows)
}

func newSelecter(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "postgres"), mock
}

func TestExpireWaitingConversations_EndsEachWithItsRulesNote(t *testing.T) {
	db, mock := newSelecter(t)
	expectUnattendedSelect(mock, 12, "alert-1", "answered-1")
	expectChatSelect(mock, unattendedSources, 14, "chat-1")
	dao := &fakeWaitingExpirer{answer: map[string]bool{"answered-1": true}}

	expireWaitingConversations(context.Background(), db, dao, 14, 12)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, map[string]string{
		"alert-1": unattendedExpiryNote(12),
		"chat-1":  chatExpiryNote(14),
	}, dao.notes, "an answered conversation is left alone; each rule writes its own note")
	assert.Contains(t, dao.notes["alert-1"], "12 hours")
	assert.Contains(t, dao.notes["chat-1"], "14 days")
}

func TestExpireWaitingConversations_UnattendedOffLeavesAllSourcesToDaysRule(t *testing.T) {
	db, mock := newSelecter(t)
	expectChatSelect(mock, []string{}, 14, "alert-1")
	dao := &fakeWaitingExpirer{}

	expireWaitingConversations(context.Background(), db, dao, 14, 0)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, chatExpiryNote(14), dao.notes["alert-1"])
}

func TestExpireWaitingConversations_DaysOffRunsOnlyUnattendedRule(t *testing.T) {
	db, mock := newSelecter(t)
	expectUnattendedSelect(mock, 12)

	expireWaitingConversations(context.Background(), db, &fakeWaitingExpirer{}, 0, 12)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExpireWaitingConversations_OneFailureDoesNotStopTheBatch(t *testing.T) {
	db, mock := newSelecter(t)
	expectUnattendedSelect(mock, 12, "broken", "alert-2")
	dao := &fakeWaitingExpirer{failIDs: map[string]bool{"broken": true}}

	expireWaitingConversations(context.Background(), db, dao, 0, 12)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, map[string]string{"alert-2": unattendedExpiryNote(12)}, dao.notes)
}

func TestExpireWaitingConversations_SelectErrorIsLogged(t *testing.T) {
	db, mock := newSelecter(t)
	mock.ExpectQuery(`SELECT id, account_id FROM llm_conversations`).WillReturnError(errors.New("boom"))
	dao := &fakeWaitingExpirer{}

	expireWaitingConversations(context.Background(), db, dao, 0, 12)

	require.NoError(t, mock.ExpectationsWereMet())
	assert.Empty(t, dao.notes)
}

func TestExpireWaitingConversation_WritesNoteLikeStop(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	note := unattendedExpiryNote(12)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM llm_conversations").WithArgs("conversation-1", "account-1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(ConversationStatusWaiting))
	mock.ExpectQuery(regexp.QuoteMeta(`UPDATE llm_conversation_messages SET updated_at = now(), status = $2, response = $4`)).
		WithArgs("conversation-1", string(ConversationStatusTerminated), "account-1", note).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("UPDATE llm_conversations SET status").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE llm_conversation_agent SET status").
		WithArgs("conversation-1", "terminated", "in_progress", "waiting", note, "account-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE llm_conversation_tool_calls SET status").
		WithArgs("conversation-1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), note, "account-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ended, err := dao.ExpireWaitingConversation(security.NewRequestContextForSuperAdmin(), "account-1", "conversation-1", note)
	require.NoError(t, err)
	assert.True(t, ended)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Someone answered between the job's select and the row lock: the run that
// answer resumed must not be cut off.
func TestExpireWaitingConversation_LeavesAnsweredConversationAlone(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM llm_conversations").WithArgs("conversation-1", "account-1").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(ConversationStatusInProgress))
	mock.ExpectRollback()

	ended, err := dao.ExpireWaitingConversation(security.NewRequestContextForSuperAdmin(), "account-1", "conversation-1", "note")
	require.NoError(t, err)
	assert.False(t, ended)
	require.NoError(t, mock.ExpectationsWereMet())
}
