package events

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// The three analysis write paths — UpsertEventAnalysisInProgress,
// SaveEventRCAAnalysis and UpsertEventAnalysis — share one skeleton: resolve the
// row this event currently owns (mapping first, latest-for-fingerprint as
// fallback), update it if found, otherwise insert and register the mapping.
//
// These tests pin that skeleton rather than the SQL text, so they hold across
// refactors of the shared lookup and mapping helpers. What they assert is the
// sequence of database operations and the arguments each carries: whether the
// mapping is consulted, whether the write became an UPDATE or an INSERT, whether
// a mapping row is registered, and whether the transaction commits.

const (
	wEventID = "evt-w"
	wFp      = "fp-w"
	wAcct    = "acct-w"
	wAggKey  = "pod_crashloop"
	wRowID   = "analysis-existing"
	wNewID   = "analysis-new"
)

// expectMappingLookup expects the FOR UPDATE mapping lookup, returning either the
// analysis id this event already owns or no rows.
func expectMappingLookup(mock sqlmock.Sqlmock, analysisType EventAnalysisType, analysisID string) {
	rows := sqlmock.NewRows([]string{"analysis_id"})
	if analysisID != "" {
		rows = rows.AddRow(analysisID)
	}
	mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .* FOR UPDATE").
		WithArgs(wEventID, analysisType).
		WillReturnRows(rows)
}

// expectFingerprintLookup expects the latest-row-for-fingerprint fallback used
// when no eventId is supplied.
func expectFingerprintLookup(mock sqlmock.Sqlmock, analysisType EventAnalysisType, analysisID string) {
	rows := sqlmock.NewRows([]string{"id"})
	if analysisID != "" {
		rows = rows.AddRow(analysisID)
	}
	mock.ExpectQuery("SELECT id FROM event_log_analysis .* ORDER BY COALESCE\\(updated_at, recorded_at\\) DESC LIMIT 1 FOR UPDATE").
		WithArgs(wFp, wAcct, wAggKey, analysisType).
		WillReturnRows(rows)
}

func TestUpsertEventAnalysisInProgress(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("mapped event updates the row it owns, no insert", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectMappingLookup(mock, AnalysisTypeLog, wRowID)
		mock.ExpectExec("UPDATE event_log_analysis SET status=.*status_reason=NULL.*WHERE id=").
			WithArgs(wRowID, AnalysisStatusInProgress, wEventID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, wEventID, wFp, wAcct, wAggKey, AnalysisTypeLog))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unmapped event inserts a new row and registers the mapping", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectMappingLookup(mock, AnalysisTypeLog, "")
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(wEventID, wFp, "", "", AnalysisStatusInProgress, wAcct, wAggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wNewID))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(wEventID, wNewID, AnalysisTypeLog).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, wEventID, wFp, wAcct, wAggKey, AnalysisTypeLog))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no eventId falls back to the latest row for the fingerprint", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectFingerprintLookup(mock, AnalysisTypeLog, wRowID)
		mock.ExpectExec("UPDATE event_log_analysis SET status=.*status_reason=NULL.*WHERE id=").
			WithArgs(wRowID, AnalysisStatusInProgress).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, "", wFp, wAcct, wAggKey, AnalysisTypeLog))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no eventId and no existing row inserts with NULL event_id and no mapping", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectFingerprintLookup(mock, AnalysisTypeLog, "")
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(nil, wFp, "", "", AnalysisStatusInProgress, wAcct, wAggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wNewID))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, "", wFp, wAcct, wAggKey, AnalysisTypeLog))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSaveEventRCAAnalysis(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const result = "rca-body"

	t.Run("mapped event updates the row it owns", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectMappingLookup(mock, AnalysisTypeRCA, wRowID)
		mock.ExpectExec("UPDATE event_log_analysis SET analysis=.*status_reason=NULL.*WHERE id=").
			WithArgs(wRowID, result, AnalysisStatusCompleted, wEventID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.SaveEventRCAAnalysis(ctx, wEventID, wFp, wAcct, wAggKey, result))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unmapped event inserts and registers the mapping", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectMappingLookup(mock, AnalysisTypeRCA, "")
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(wEventID, result, AnalysisStatusCompleted, wFp, wAcct, wAggKey, AnalysisTypeRCA).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wNewID))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(wEventID, wNewID, AnalysisTypeRCA).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.SaveEventRCAAnalysis(ctx, wEventID, wFp, wAcct, wAggKey, result))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no eventId falls back to the latest row for the fingerprint", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectFingerprintLookup(mock, AnalysisTypeRCA, wRowID)
		mock.ExpectExec("UPDATE event_log_analysis SET analysis=.*status_reason=NULL.*WHERE id=").
			WithArgs(wRowID, result, AnalysisStatusCompleted).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.SaveEventRCAAnalysis(ctx, "", wFp, wAcct, wAggKey, result))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUpsertEventAnalysis(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		analysis = "analysis-body"
		summary  = "summary-body"
		status   = "COMPLETED"
	)

	t.Run("mapped event updates the row it owns", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectMappingLookup(mock, AnalysisTypeSummary, wRowID)
		mock.ExpectExec("UPDATE event_log_analysis SET analysis=.*summary=.*status_reason=NULL.*WHERE id=").
			WithArgs(wRowID, analysis, summary, status, wEventID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysis(ctx, wEventID, analysis, summary, status, wFp, wAcct, wAggKey, AnalysisTypeSummary))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unmapped event inserts and registers the mapping", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectMappingLookup(mock, AnalysisTypeSummary, "")
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(wEventID, wFp, analysis, summary, status, wAcct, wAggKey, AnalysisTypeSummary).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(wNewID))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(wEventID, wNewID, AnalysisTypeSummary).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysis(ctx, wEventID, analysis, summary, status, wFp, wAcct, wAggKey, AnalysisTypeSummary))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no eventId falls back to the latest row for the fingerprint", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectBegin()
		expectFingerprintLookup(mock, AnalysisTypeSummary, wRowID)
		mock.ExpectExec("UPDATE event_log_analysis SET analysis=.*summary=.*status_reason=NULL.*WHERE id=").
			WithArgs(wRowID, analysis, summary, status).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpsertEventAnalysis(ctx, "", analysis, summary, status, wFp, wAcct, wAggKey, AnalysisTypeSummary))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
