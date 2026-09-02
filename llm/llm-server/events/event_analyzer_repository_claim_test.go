package events

import (
	"testing"
	"time"

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

// TestClaimEventAnalysis verifies the claim logic with event_analysis_mapping.
func TestClaimEventAnalysis(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-1"
		fp      = "fp-1"
		acct    = "acct-1"
		aggKey  = "pod_unschedulable"
	)

	t.Run("won: new mapping registered → claimed", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		// 1. Initial FOR UPDATE mapping check returns no rows
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))

		// 2. Transactional status check for fingerprint returns no rows
		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}))

		// 3. Insert into event_log_analysis RETURNING id
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-1"))

		// 4. Insert into event_analysis_mapping
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-1", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.True(t, claimed, "one affected row means this caller won the claim")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("won: empty eventID → claimed without mapping insert, passes NULL event_id", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		// 1. Fingerprint check returns no rows
		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}))

		// 2. Insert into event_log_analysis with nil event_id RETURNING id
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(nil, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-2"))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, "", fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.True(t, claimed, "empty eventID should be claimed without mapping insert")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("existing IN_PROGRESS fingerprint row → inserts mapping, returns claimed=false", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))

		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).AddRow("analysis-inprogress", string(AnalysisStatusInProgress), time.Now()))

		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-inprogress", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "IN_PROGRESS fingerprint row must bind mapping and return false")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("existing COMPLETED fingerprint row, force=false → inserts mapping, returns claimed=false", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))

		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).AddRow("analysis-completed", string(AnalysisStatusCompleted), time.Now()))

		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-completed", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "force=false on COMPLETED row must return false")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("existing COMPLETED fingerprint row, force=true → claims new analysis run", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))

		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).AddRow("analysis-completed", string(AnalysisStatusCompleted), time.Now()))

		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-new"))

		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-new", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, true)
		require.NoError(t, err)
		assert.True(t, claimed, "force=true on COMPLETED row must claim a new analysis run")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	// Regression: a forced regenerate for an event that already owns a mapping row
	// must repoint that mapping at the newly inserted historical run. With a
	// DO NOTHING conflict clause the insert affected zero rows and the whole claim
	// was rolled back, so every regenerate after the first silently did nothing.
	t.Run("existing mapping, force=true → repoints mapping and claims new run", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow("analysis-old"))

		mock.ExpectQuery("SELECT status, COALESCE\\(updated_at, recorded_at\\) FROM event_log_analysis").
			WithArgs("analysis-old").
			WillReturnRows(sqlmock.NewRows([]string{"status", "written_at"}).AddRow(string(AnalysisStatusCompleted), time.Now()))

		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).AddRow("analysis-old", string(AnalysisStatusCompleted), time.Now()))

		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-new"))

		// DO UPDATE, not DO NOTHING: the mapping must move to the new run.
		mock.ExpectExec("INSERT INTO event_analysis_mapping .* DO UPDATE SET analysis_id").
			WithArgs(eventID, "analysis-new", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, true)
		require.NoError(t, err)
		assert.True(t, claimed, "force=true must re-claim even when a mapping already exists")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	// Regression: a forced regenerate that declines to steal an active run must
	// still repoint this event's mapping at that run, otherwise the regenerate
	// keeps serving the event's previous, superseded analysis.
	t.Run("existing mapping, force=true, fingerprint run IN_PROGRESS → repoints mapping, not claimed", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow("analysis-old"))

		mock.ExpectQuery("SELECT status, COALESCE\\(updated_at, recorded_at\\) FROM event_log_analysis").
			WithArgs("analysis-old").
			WillReturnRows(sqlmock.NewRows([]string{"status", "written_at"}).AddRow(string(AnalysisStatusCompleted), time.Now()))

		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).AddRow("analysis-active", string(AnalysisStatusInProgress), time.Now()))

		mock.ExpectExec("INSERT INTO event_analysis_mapping .* DO UPDATE SET analysis_id").
			WithArgs(eventID, "analysis-active", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, true)
		require.NoError(t, err)
		assert.False(t, claimed, "an active run must never be stolen, even on force")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty accountID → returns error immediately", func(t *testing.T) {
		repo, _ := newClaimTestRepo(t)

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, "", aggKey, AnalysisTypeLog, false)
		require.Error(t, err)
		assert.False(t, claimed)
		assert.Contains(t, err.Error(), "accountId cannot be empty")
	})

	t.Run("lost: mapping exists and in progress → not claimed", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow("analysis-1"))

		mock.ExpectQuery("SELECT status, COALESCE\\(updated_at, recorded_at\\) FROM event_log_analysis").
			WithArgs("analysis-1").
			WillReturnRows(sqlmock.NewRows([]string{"status", "written_at"}).AddRow(string(AnalysisStatusInProgress), time.Now()))

		mock.ExpectRollback()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "in progress status means another dispatcher owns the run")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error → not claimed, error propagated", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnError(assert.AnError)

		mock.ExpectRollback()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.Error(t, err)
		assert.False(t, claimed, "a failed claim must never report success")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
