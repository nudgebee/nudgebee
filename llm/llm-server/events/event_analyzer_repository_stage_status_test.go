package events

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stage that is skipped before it ever runs still has to leave a COMPLETED row
// behind. allEventAnalysisTypesCompleted reads every stage back and treats a
// missing row as "not complete", so a skip that writes nothing leaves the
// pipeline permanently unfinished: every later event sharing the fingerprint
// re-enters it and writes another detailed_response row, unbounded since V850
// dropped the one-row-per-identity constraint.
//
// UpdateEventAnalysisStatus cannot satisfy that -- it is a bare UPDATE, matches
// nothing, and reports success. These tests pin the upsert behaviour that can.
func TestUpsertEventAnalysisStatus(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-skip"
		fp      = "fp-skip"
		acct    = "acct-skip"
		aggKey  = "pod_restart"
		reason  = "skipped - event missing 'service' label"
	)

	t.Run("no existing row: inserts the stage and maps the event to it", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()
		// Fingerprint-scoped lookup, not the per-event mapping: a skip applies to
		// every event sharing the fingerprint.
		mock.ExpectQuery("SELECT id FROM event_log_analysis WHERE event_fingerprint = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeInvestigation).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(eventID, fp, AnalysisStatusCompleted, reason, acct, aggKey, AnalysisTypeInvestigation).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-skipped"))
		mock.ExpectExec("INSERT INTO event_analysis_mapping").
			WithArgs(eventID, "analysis-skipped", AnalysisTypeInvestigation).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		err := repo.UpsertEventAnalysisStatus(ctx, eventID, fp, acct, aggKey,
			string(AnalysisStatusCompleted), reason, AnalysisTypeInvestigation)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet(),
			"a skipped stage must leave a row behind, or the pipeline never completes")
	})

	t.Run("existing row: updates in place, writes no mapping", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM event_log_analysis WHERE event_fingerprint = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-existing"))
		mock.ExpectExec("UPDATE event_log_analysis SET status=.* WHERE id=").
			WithArgs("analysis-existing", AnalysisStatusCompleted, "skipped - no logs").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err := repo.UpsertEventAnalysisStatus(ctx, eventID, fp, acct, aggKey,
			string(AnalysisStatusCompleted), "skipped - no logs", AnalysisTypeLog)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet(),
			"an existing row keeps the previous update-in-place behaviour")
	})

	t.Run("no event id: inserts with a null event and no mapping", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM event_log_analysis WHERE event_fingerprint = .* FOR UPDATE").
			WithArgs(fp, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectQuery("INSERT INTO event_log_analysis").
			WithArgs(nil, fp, AnalysisStatusCompleted, reason, acct, aggKey, AnalysisTypeLog).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("analysis-orphan"))
		mock.ExpectCommit()

		err := repo.UpsertEventAnalysisStatus(ctx, "", fp, acct, aggKey,
			string(AnalysisStatusCompleted), reason, AnalysisTypeLog)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
