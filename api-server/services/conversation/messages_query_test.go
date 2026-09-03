package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Proves the Go-side scan (sqlmock rows -> Message.FollowupWaitSeconds), not
// the CTE's SQL semantics or the real Postgres numeric->float64 conversion —
// both were verified live against dev Postgres; see the PR body for numbers.
func TestFetchMessages_ScansFollowupWaitSeconds(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "postgres")

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "user_id", "user_display_name", "created_at", "updated_at",
		"message", "message_type", "response", "role", "status",
		"parent_agent_id", "message_config", "ack_message", "metadata",
		"attachments", "followup_wait_seconds",
	}).AddRow(
		"msg-waited", nil, nil, now, now,
		"find the pvcs", "generation", "answer", "human", "COMPLETED",
		nil, nil, nil, nil,
		"[]", 1024.29,
	).AddRow(
		"msg-plain", nil, nil, now, now,
		"how many namespaces", "generation", "answer", "human", "COMPLETED",
		nil, nil, nil, nil,
		"[]", 0.0,
	)

	mock.ExpectQuery(`(?s)SELECT.*followup_wait_seconds.*FROM llm_conversation_messages m`).
		WithArgs("tenant-1", "conv-1", sqlmock.AnyArg()).
		WillReturnRows(rows)

	messages, err := fetchMessages(context.Background(), db, "tenant-1", "conv-1", time.Time{})
	require.NoError(t, err)
	require.Len(t, messages, 2)
	assert.Equal(t, 1024.29, messages[0].FollowupWaitSeconds)
	assert.Equal(t, 0.0, messages[1].FollowupWaitSeconds)
	assert.NoError(t, mock.ExpectationsWereMet())
}
