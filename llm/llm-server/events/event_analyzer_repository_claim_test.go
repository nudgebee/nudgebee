package events

import (
	"regexp"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newClaimTestRepo builds an EventAnalysisRepository backed by a fresh sqlmock
// connection. Each test gets its own mock so expectations don't leak.
func newClaimTestRepo(t *testing.T) (*EventAnalysisRepository, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	repo := &EventAnalysisRepository{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")}}
	return repo, mock
}

// TestClaimEventAnalysis verifies the atomic claim: the same INSERT ... ON
// CONFLICT ... WHERE statement returns claimed=true only when a row was
// affected, force toggles whether COMPLETED is re-claimable, and errors
// propagate as claimed=false.
func TestClaimEventAnalysis(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-1"
		fp      = "fp-1"
		acct    = "acct-1"
		aggKey  = "pod_unschedulable"
	)

	t.Run("won: one row affected → claimed", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectExec("INSERT INTO event_log_analysis").
			WillReturnResult(sqlmock.NewResult(1, 1))
		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.True(t, claimed, "one affected row means this caller won the claim")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("lost: zero rows affected → not claimed", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectExec("INSERT INTO event_log_analysis").
			WillReturnResult(sqlmock.NewResult(0, 0))
		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "zero affected rows means another dispatcher already owns the run")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("force=false excludes COMPLETED in the WHERE clause", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		// The COMPLETED exclusion adds a second `status <> $8` guard bound to COMPLETED.
		mock.ExpectExec(regexp.QuoteMeta("status <> $7 AND event_log_analysis.status <> $8")).
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog, AnalysisStatusInProgress, AnalysisStatusCompleted).
			WillReturnResult(sqlmock.NewResult(1, 1))
		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.True(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("force=true drops the COMPLETED guard (regenerate re-claims)", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		// Only the IN_PROGRESS guard remains; COMPLETED is not passed as an arg.
		mock.ExpectExec("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog, AnalysisStatusInProgress).
			WillReturnResult(sqlmock.NewResult(1, 1))
		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, true)
		require.NoError(t, err)
		assert.True(t, claimed)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error → not claimed, error propagated", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectExec("INSERT INTO event_log_analysis").
			WillReturnError(assert.AnError)
		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.Error(t, err)
		assert.False(t, claimed, "a failed claim must never report success")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
