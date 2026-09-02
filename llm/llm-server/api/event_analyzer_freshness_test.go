package api

import (
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRepo(t *testing.T) (*events.EventAnalysisRepository, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	repo := events.NewEventAnalysisRepository(&common.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgres")})
	return repo, mock
}

func TestAllEventAnalysisTypesCompletedFreshness(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	const (
		eventID = "evt-freshness-check"
		fp      = "fp-freshness-check"
		acct    = "acct-freshness-check"
		aggKey  = "pod_crash"
	)

	analysisTypes := []events.EventAnalysisType{
		events.AnalysisTypeSummary,
		events.AnalysisTypeInvestigation,
		events.AnalysisTypeLog,
		events.AnalysisTypeDetailedResponse,
	}

	t.Run("all types completed and fresh -> returns true", func(t *testing.T) {
		repo, mock := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		freshTime := time.Now().Add(-2 * time.Hour)

		for _, aType := range analysisTypes {
			mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
				WithArgs(eventID, aType).
				WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
			mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
				WithArgs(fp, acct, aggKey, aType).
				WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
					AddRow("row-"+string(aType), "{}", string(events.AnalysisStatusCompleted), eventID, "summary", "", freshTime))
		}

		completed := allEventAnalysisTypesCompleted(ctx, repo, eventID, fp, aggKey, acct)
		assert.True(t, completed, "all stages completed and fresh must report completed")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("one type is stale (> 24h) -> returns false", func(t *testing.T) {
		repo, mock := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		staleTime := time.Now().Add(-48 * time.Hour)

		// Summary is stale (48h old)
		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
			WithArgs(eventID, events.AnalysisTypeSummary).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
		mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
			WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
			WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
				AddRow("row-summary", "{}", string(events.AnalysisStatusCompleted), eventID, "summary", "", staleTime))

		completed := allEventAnalysisTypesCompleted(ctx, repo, eventID, fp, aggKey, acct)
		assert.False(t, completed, "a stage that is stale must report not completed so pipeline re-evaluates")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("one type is in progress -> returns false", func(t *testing.T) {
		repo, mock := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		mock.ExpectQuery("SELECT analysis_id FROM event_analysis_mapping .*").
			WithArgs(eventID, events.AnalysisTypeSummary).
			WillReturnRows(sqlmock.NewRows([]string{"analysis_id"}))
		mock.ExpectQuery("SELECT id, analysis, status.* FROM event_log_analysis WHERE event_fingerprint = .*").
			WithArgs(fp, acct, aggKey, events.AnalysisTypeSummary).
			WillReturnRows(sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"}).
				AddRow("row-summary", "{}", string(events.AnalysisStatusInProgress), eventID, "summary", "", time.Now()))

		completed := allEventAnalysisTypesCompleted(ctx, repo, eventID, fp, aggKey, acct)
		assert.False(t, completed, "in-progress stage must report not completed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// A FAILED analysis is served to the caller through executeEventInvestigation's
// `anyFailed` early return. Ungated by freshness that return fires on every
// later request forever, so a failure recorded once pins the event to FAILED and
// only an explicit regenerate can dislodge it — the same never-refreshes defect
// the bound was added to close for COMPLETED, on the other terminal status.
func TestIsLiveFailure(t *testing.T) {
	newAnalysis := func(status string, updatedAt time.Time) *events.EventAnalysis {
		return &events.EventAnalysis{Status: status, UpdatedAt: updatedAt}
	}

	t.Run("fresh failure is live -> caller sees FAILED", func(t *testing.T) {
		repo, _ := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		got := isLiveFailure(repo, newAnalysis(string(events.AnalysisStatusFailed), time.Now().Add(-1*time.Hour)))
		assert.True(t, got, "a failure inside the window is the current verdict and must be reported")
	})

	t.Run("stale failure is not live -> pipeline re-dispatches", func(t *testing.T) {
		repo, _ := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		got := isLiveFailure(repo, newAnalysis(string(events.AnalysisStatusFailed), time.Now().Add(-48*time.Hour)))
		assert.False(t, got, "a failure past the window must not pin the event to FAILED forever")
	})

	t.Run("bound disabled -> every failure stays live", func(t *testing.T) {
		repo, _ := newTestRepo(t)
		repo.SetAnalysisFreshness(0) // previous behaviour

		got := isLiveFailure(repo, newAnalysis(string(events.AnalysisStatusFailed), time.Now().Add(-30*24*time.Hour)))
		assert.True(t, got, "with the bound off, age must not affect the reported status")
	})

	t.Run("unknown timestamp reads as fresh, not as an ancient failure", func(t *testing.T) {
		repo, _ := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		got := isLiveFailure(repo, newAnalysis(string(events.AnalysisStatusFailed), time.Time{}))
		assert.True(t, got, "a zero timestamp is unknown, not ~2000 years old")
	})

	t.Run("non-failed statuses are never live failures", func(t *testing.T) {
		repo, _ := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		stale := time.Now().Add(-48 * time.Hour)
		for _, status := range []events.AnalysisStatus{events.AnalysisStatusCompleted, events.AnalysisStatusInProgress} {
			assert.False(t, isLiveFailure(repo, newAnalysis(string(status), stale)), "status %s must not count as a failure", status)
		}
	})

	t.Run("nil analysis is not a failure", func(t *testing.T) {
		repo, _ := newTestRepo(t)
		repo.SetAnalysisFreshness(24 * time.Hour)

		assert.False(t, isLiveFailure(repo, nil))
	})
}
