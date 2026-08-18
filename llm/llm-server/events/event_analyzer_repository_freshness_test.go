package events

import (
	"testing"
	"time"

	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A completed analysis for a fingerprint is reused by later events that share
// it. Without an age bound that reuse is unconditional, so an event arriving
// today can be handed findings produced days ago — and, because the claim also
// writes a mapping row, bound to them permanently.
//
// These tests pin the three states of that bound: disabled (previous
// behaviour), enabled-and-fresh, enabled-and-stale.
func TestClaimEventAnalysisFreshnessBound(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-fresh"
		fp      = "fp-fresh"
		acct    = "acct-fresh"
		aggKey  = "pod_oom"
		oldID   = "analysis-old"
	)

	// expectLookups sets up the two reads every claim performs: this event's
	// mapping (none here, so it is a first-time claim for the event) and the
	// newest analysis for the fingerprint, written at writtenAt.
	expectLookups := func(mock sqlmock.Sqlmock, writtenAt time.Time) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).
				AddRow(oldID, string(AnalysisStatusCompleted), writtenAt))
	}

	t.Run("disabled: an ancient completed analysis is still reused", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 0 // previous behaviour

		expectLookups(mock, time.Now().Add(-30*24*time.Hour))
		// Reuse path: bind this event to the existing analysis, claim nothing.
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, oldID, AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "with the bound disabled, age must not affect reuse")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("enabled and fresh: analysis is reused, no new run", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 24 * time.Hour

		expectLookups(mock, time.Now().Add(-1*time.Hour))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, oldID, AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "an analysis inside the window is still good")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("enabled and stale: falls through and claims a new run", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 24 * time.Hour

		expectLookups(mock, time.Now().Add(-10*24*time.Hour))
		// Claim path: insert a new analysis and map this event to it, so the
		// event gets its own run rather than inheriting the old findings.
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-new"))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-new", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.True(t, claimed, "a stale analysis must not be inherited by a new event")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("stale but IN_PROGRESS: never stolen, regardless of age", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 24 * time.Hour

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).
				AddRow("analysis-running", string(AnalysisStatusInProgress), time.Now().Add(-10*24*time.Hour)))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-running", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "an active run is recovered by ListInProgressAnalysis, not stolen here")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("mapped event fresh: reused, no new run", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 24 * time.Hour

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow(oldID))
		mock.ExpectQuery("SELECT status, COALESCE\\(updated_at, recorded_at\\) FROM event_log_analysis WHERE id = .*").
			WithArgs(oldID).
			WillReturnRows(sqlmock.NewRows([]string{"status", "written_at"}).
				AddRow(string(AnalysisStatusCompleted), time.Now().Add(-1*time.Hour)))

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "a mapped analysis inside the window is kept")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	// A mapped row whose COALESCE(updated_at, recorded_at) scans as the zero time
	// is an unknown timestamp, not an ancient one. Reading it as ~2000 years stale
	// reclaims the row on every event for the fingerprint, and each new run leaves
	// the same unknown timestamp behind — a regeneration loop with no exit. The
	// bound therefore has to reach this row through IsAnalysisStale, which treats
	// an unknown timestamp as fresh, rather than re-deriving the comparison.
	t.Run("mapped event with zero timestamp: treated as fresh, not reclaimed", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 24 * time.Hour

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow(oldID))
		mock.ExpectQuery("SELECT status, COALESCE\\(updated_at, recorded_at\\) FROM event_log_analysis WHERE id = .*").
			WithArgs(oldID).
			WillReturnRows(sqlmock.NewRows([]string{"status", "written_at"}).
				AddRow(string(AnalysisStatusCompleted), time.Time{}))

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.False(t, claimed, "an unknown timestamp must read as fresh, not as ~2000 years stale")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("mapped event stale: repoints mapping and claims new run", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		repo.analysisFreshness = 24 * time.Hour

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
			WithArgs(eventID, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}).AddRow(oldID))
		mock.ExpectQuery("SELECT status, COALESCE\\(updated_at, recorded_at\\) FROM event_log_analysis WHERE id = .*").
			WithArgs(oldID).
			WillReturnRows(sqlmock.NewRows([]string{"status", "written_at"}).
				AddRow(string(AnalysisStatusCompleted), time.Now().Add(-10*24*time.Hour)))
		mock.ExpectQuery("SELECT id, status.* FROM event_log_analysis WHERE id = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status", "written_at"}).
				AddRow(oldID, string(AnalysisStatusCompleted), time.Now().Add(-10*24*time.Hour)))
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusInProgress, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-new"))
		mock.ExpectExec("INSERT INTO event_analysis_mapping .* ON CONFLICT \\(event_id, analysis_type\\) DO UPDATE SET analysis_id = EXCLUDED.analysis_id").
			WithArgs(eventID, "analysis-new", AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		claimed, err := repo.ClaimEventAnalysis(ctx, eventID, fp, acct, aggKey, AnalysisTypeLog, false)
		require.NoError(t, err)
		assert.True(t, claimed, "a mapped stale analysis must be regenerated with a new claim")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
