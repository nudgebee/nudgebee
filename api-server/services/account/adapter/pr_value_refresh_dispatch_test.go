package adapter

import (
	"strings"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureExec runs fn against a throwaway sqlmock and returns the SQL it issued.
// The matcher accepts anything so the statement can be asserted on directly,
// which is what these tests need: the difference between the two hand-back paths
// is a column one of them must NOT write.
func captureExec(t *testing.T, fn func(dbms *database.DatabaseManager)) string {
	t.Helper()

	var captured string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(
		sqlmock.QueryMatcherFunc(func(_, actualSQL string) error {
			captured = actualSQL
			return nil
		})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 1))
	fn(&database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")})
	require.NoError(t, mock.ExpectationsWereMet())

	return captured
}

// TestReleaseValueRefreshClaimHandsBackTheCooldown covers the failure path: a
// refresh that genuinely could not run should be retried by the next scheduled
// run rather than waiting out a cooldown no landed update ever earned.
func TestReleaseValueRefreshClaimHandsBackTheCooldown(t *testing.T) {
	sql := captureExec(t, func(dbms *database.DatabaseManager) {
		releaseValueRefreshClaim(dbms, "res-1", "code agent crashed")
	})

	assert.Contains(t, sql, "last_value_refresh_at = NULL",
		"a failed refresh must not consume the cooldown")
	assert.Contains(t, sql, "pr_lifecycle_state = 'addressing'",
		"only a row this run claimed may be handed back")
}

// TestRestoreValueRefreshStateKeepsTheCooldown covers the no_op path, which is
// the one that produced hourly agent runs on dev.
//
// The agent read the branch and found nothing to change. The next run recomputes
// the same drift from the same values and would reach the same conclusion, so
// handing the cooldown back buys an identical agent run every hour for as long
// as the pull request stays open. Keeping the stamp bounds that to the cooldown.
func TestRestoreValueRefreshStateKeepsTheCooldown(t *testing.T) {
	sql := captureExec(t, func(dbms *database.DatabaseManager) {
		restoreValueRefreshState(dbms, "res-1", "no_op")
	})

	assert.False(t, strings.Contains(sql, "last_value_refresh_at"),
		"a no_op must consume the cooldown it claimed, or it re-runs every schedule:\n"+sql)
	assert.Contains(t, sql, "pr_lifecycle_state = 'created'",
		"the row is still handed back, just without refunding the cooldown")
	assert.Contains(t, sql, "pr_lifecycle_state = 'addressing'",
		"only a row this run claimed may be handed back")
}
