package core

import (
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCritiqueList_ConversationLink is a regression test for #35815: the
// Browse view's row now needs account_id/conversation_id/message_id (already
// columns on llm_conversation_agent_critiques, just not selected) plus
// session_id (looked up from llm_conversations) to build a "Go to
// conversation" deep-link.
func TestGetCritiqueList_ConversationLink(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT session_id FROM llm_conversations`).WillReturnRows(
		sqlmock.NewRows([]string{
			"id", "agent_name", "decision", "input", "critiqued_content", "feedback", "created_at",
			"account_id", "conversation_id", "message_id", "session_id",
		}).
			AddRow("crit-1", "k8s_debug", "refine", "why is pod crashing", "### Node Status\n- Ready", "use the logs tool",
				now, "acc-1", "conv-1", "msg-1", "sess-abc"))

	filter := CritiqueFilter{StartDate: now.Add(-24 * time.Hour), EndDate: now}
	out, err := dao.GetCritiqueList(filter, 50, 0)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, out.Rows, 1)
	row := out.Rows[0]
	assert.Equal(t, "acc-1", row.AccountID)
	assert.Equal(t, "conv-1", row.ConversationID)
	assert.Equal(t, "msg-1", row.MessageID)
	assert.Equal(t, "sess-abc", row.SessionID)
}
