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

// Regression test for GH #37367.
func TestFetchShell_OrdersByUpdatedAtDesc(t *testing.T) {
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "postgres")

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"id", "session_id", "account_id", "tenant_id", "user_id", "user_display_name",
		"created_at", "updated_at", "source", "context", "status", "title",
	}).AddRow("conv-fuller", "event-abc123", "account-1", "tenant-1", nil, nil, now, now, nil, nil, nil, nil)

	mock.ExpectQuery(`(?s)SELECT.*FROM llm_conversations c.*ORDER BY c\.updated_at DESC.*LIMIT 1`).
		WithArgs("tenant-1", "account-1", nil, "event-abc123").
		WillReturnRows(rows)

	shell, err := fetchShell(context.Background(), db, "tenant-1", GetConversationDeltaRequest{
		AccountId: "account-1",
		SessionId: "event-abc123",
	})
	require.NoError(t, err)
	require.NotNil(t, shell)
	assert.Equal(t, "conv-fuller", shell.Id)
	assert.NoError(t, mock.ExpectationsWereMet())
}
