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

// TestGetCritiqueSummary_ByAgentAcceptedCount is a regression test for #35817:
// the Agents table's by-agent breakdown now also carries the accepted count
// (previously only judged/refined, forcing the UI to derive it).
func TestGetCritiqueSummary_ByAgentAcceptedCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}
	now := time.Now()

	mock.ExpectQuery("judged").WillReturnRows(sqlmock.NewRows([]string{"judged", "refined"}).AddRow(10, 4))
	mock.ExpectQuery("GROUP BY agent_name").WillReturnRows(
		sqlmock.NewRows([]string{"agent_name", "judged", "refined", "accepted"}).
			AddRow("k8s_orchestrator", 10, 4, 6))
	mock.ExpectQuery("root_cause_not_verified").WillReturnRows(
		sqlmock.NewRows([]string{
			"root_cause_not_verified", "incomplete", "manual_action", "evidence",
			"verify", "guessing", "hallucination", "symptom_not_cause", "schema_validation",
		}).AddRow(0, 0, 0, 0, 0, 0, 0, 0, 0))

	filter := CritiqueFilter{StartDate: now.Add(-24 * time.Hour), EndDate: now}
	summary, err := dao.GetCritiqueSummary(filter)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	require.Len(t, summary.ByAgent, 1)
	assert.Equal(t, int64(6), summary.ByAgent[0].Accepted)
	assert.Equal(t, int64(10), summary.ByAgent[0].Judged)
	assert.Equal(t, int64(4), summary.ByAgent[0].Refined)
}
