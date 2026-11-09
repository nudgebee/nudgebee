package events

import (
	"testing"
	"time"

	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSetAnalysisTrigger(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("stamps the claimed log row, NULL when no user", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		mock.ExpectExec(`UPDATE event_log_analysis a SET trigger = \$3, triggered_by = \$4`).
			WithArgs("evt-1", AnalysisTypeLog, "automatic", nil).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.SetAnalysisTrigger(ctx, "evt-1", AnalysisTriggerAutomatic, ""))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no event or no trigger writes nothing", func(t *testing.T) {
		repo, mock := newClaimTestRepo(t)
		require.NoError(t, repo.SetAnalysisTrigger(ctx, "", AnalysisTriggerRerun, "user-1"))
		require.NoError(t, repo.SetAnalysisTrigger(ctx, "evt-1", "", "user-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// Real Postgres: each analysis version keeps the trigger of the run that
// produced it. A re-run claims a new log row and is stamped on its own; the
// previous run's row is never changed. A run redone on the same row is stamped
// with whoever started it this time. Names come from users.display_name.
func TestAnalysisTriggerVersionsPostgres(t *testing.T) {
	repo := rcaIntegrationRepo(t)
	person := uuid.NewString()
	_, err := repo.dbManager.Db.Exec(`ALTER TABLE event_log_analysis ADD COLUMN trigger text, ADD COLUMN triggered_by text;
		CREATE TABLE users (id uuid PRIMARY KEY, display_name text);
		INSERT INTO users (id, display_name) VALUES ('` + person + `', 'Pat Example')`)
	require.NoError(t, err)
	ctx := security.NewRequestContextForSuperAdmin()
	account, event := uuid.NewString(), uuid.NewString()
	stages := []EventAnalysisType{AnalysisTypeSummary, AnalysisTypeInvestigation, AnalysisTypeDetailedResponse}

	run := func(force bool, trigger AnalysisTrigger, by, summary string) string {
		claimed, err := repo.ClaimEventAnalysis(ctx, event, "fp", account, "agg", AnalysisTypeLog, force)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, repo.SetAnalysisTrigger(ctx, event, trigger, by))
		for _, stage := range stages {
			require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, event, "fp", account, "agg", stage))
		}
		for _, stage := range append(stages, AnalysisTypeLog) {
			require.NoError(t, repo.UpsertEventAnalysis(ctx, event, "{}", summary, string(AnalysisStatusCompleted), "fp", account, "agg", stage))
		}
		var logRow string
		require.NoError(t, repo.dbManager.Db.Get(&logRow, `SELECT analysis_id FROM event_analysis_mapping WHERE event_id=$1 AND analysis_type=$2`, event, AnalysisTypeLog))
		return logRow
	}
	triggerOf := func(id string) (string, *string) {
		var row struct {
			Trigger     string  `db:"trigger"`
			TriggeredBy *string `db:"triggered_by"`
		}
		require.NoError(t, repo.dbManager.Db.Get(&row, `SELECT trigger, triggered_by FROM event_log_analysis WHERE id=$1`, id))
		return row.Trigger, row.TriggeredBy
	}

	first := run(false, AnalysisTriggerAutomatic, "", "first")
	time.Sleep(10 * time.Millisecond)
	second := run(true, AnalysisTriggerRerun, person, "second")
	require.NotEqual(t, first, second, "a re-run claims a new log row")

	trigger, by := triggerOf(first)
	require.Equal(t, "automatic", trigger, "the earlier version keeps its own trigger")
	require.Nil(t, by)
	trigger, by = triggerOf(second)
	require.Equal(t, "rerun", trigger)
	require.Equal(t, person, *by)

	// The current run's trigger, as the card reads it while the run is in progress.
	liveTrigger, liveName, err := repo.GetAnalysisTrigger(ctx, event)
	require.NoError(t, err)
	require.Equal(t, "rerun", liveTrigger)
	require.Equal(t, "Pat Example", liveName)
	noTrigger, noName, err := repo.GetAnalysisTrigger(ctx, uuid.NewString())
	require.NoError(t, err, "an event with no analysis is not an error")
	require.Empty(t, noTrigger)
	require.Empty(t, noName)

	versions, err := repo.ListCompletedEventAnalysisVersions(ctx, "fp", account, "agg")
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	require.Equal(t, "second", versions[0].Summary)
	require.Equal(t, "rerun", versions[0].Trigger)
	require.Equal(t, person, versions[0].TriggeredBy)
	require.Equal(t, "Pat Example", versions[0].TriggeredByName)

	// A run redone on the same row (e.g. "Generate RCA" refreshing it) is
	// labelled with whoever started it this time.
	require.NoError(t, repo.SetAnalysisTrigger(ctx, event, AnalysisTriggerOnView, ""))
	trigger, by = triggerOf(second)
	require.Equal(t, "on_view", trigger)
	require.Nil(t, by)
	trigger, _ = triggerOf(first)
	require.Equal(t, "automatic", trigger, "the earlier version is still untouched")
}
