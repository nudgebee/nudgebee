package events

import (
	"testing"
	"time"

	"nudgebee/llm/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Real Postgres (#40871): a re-run gets its own row for every finished stage, so
// the previous run stays a complete version in the history, both while the
// re-run is in progress and after it finishes.
func TestRerunKeepsPreviousVersionPostgres(t *testing.T) {
	repo := rcaIntegrationRepo(t)
	_, err := repo.dbManager.Db.Exec(`ALTER TABLE event_log_analysis ADD COLUMN trigger text, ADD COLUMN triggered_by text;
		CREATE TABLE users (id uuid PRIMARY KEY, display_name text)`)
	require.NoError(t, err)
	ctx := security.NewRequestContextForSuperAdmin()
	account, event := uuid.NewString(), uuid.NewString()
	others := []EventAnalysisType{AnalysisTypeSummary, AnalysisTypeInvestigation, AnalysisTypeDetailedResponse}
	all := append([]EventAnalysisType{AnalysisTypeLog}, others...)

	versionsOfEvent := func() []EventAnalysisEventVersion {
		versions, err := repo.ListCompletedEventAnalysisVersions(ctx, "fp", account, "agg")
		require.NoError(t, err)
		var own []EventAnalysisEventVersion
		for _, v := range versions {
			if v.EventID == event {
				own = append(own, v)
			}
		}
		return own
	}
	rowCount := func() int {
		var n int
		require.NoError(t, repo.dbManager.Db.Get(&n, `SELECT count(*) FROM event_log_analysis WHERE event_id = $1`, event))
		return n
	}
	finish := func(summary string) {
		for _, stage := range all {
			require.NoError(t, repo.UpsertEventAnalysis(ctx, event, "{}", summary, string(AnalysisStatusCompleted), "fp", account, "agg", stage))
		}
	}

	// First run, as the first-time HTTP path does it.
	claimed, err := repo.ClaimEventAnalysis(ctx, event, "fp", account, "agg", AnalysisTypeLog, false)
	require.NoError(t, err)
	require.True(t, claimed)
	for _, stage := range others {
		require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, event, "fp", account, "agg", stage))
	}
	finish("first")
	require.Len(t, versionsOfEvent(), 1)
	time.Sleep(10 * time.Millisecond)

	// Re-run: the regenerate claim gives the log a new row; the other finished
	// stages get theirs, then the pipeline marks them in progress via the mapping.
	claimed, err = repo.ClaimEventAnalysis(ctx, event, "fp", account, "agg", AnalysisTypeLog, true)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, repo.StartFreshStages(ctx, event, "fp", account, "agg", others...))
	require.Equal(t, 8, rowCount(), "4 rows per run")
	for _, stage := range others {
		require.NoError(t, repo.UpsertEventAnalysisInProgress(ctx, event, "fp", account, "agg", stage))
	}

	during := versionsOfEvent()
	require.Len(t, during, 1, "while the re-run is in progress, the previous run is still a complete version")
	require.Equal(t, "first", during[0].Summary)

	// Starting again while the re-run is in progress must not create orphan rows.
	require.NoError(t, repo.StartFreshStages(ctx, event, "fp", account, "agg", all...))
	require.Equal(t, 8, rowCount(), "in-progress rows are reused, never replaced")

	// Part-way through, a finished new stage must not pair with the old ones.
	require.NoError(t, repo.UpsertEventAnalysis(ctx, event, "{}", "second", string(AnalysisStatusCompleted), "fp", account, "agg", AnalysisTypeSummary))
	partway := versionsOfEvent()
	require.Len(t, partway, 1, "no mixed version while the re-run is part-way")
	require.Equal(t, "first", partway[0].Summary)

	finish("second")
	after := versionsOfEvent()
	require.Len(t, after, 2, "both runs are in the history")
	require.Equal(t, "second", after[0].Summary)
	require.Equal(t, "first", after[1].Summary)

	// A re-run that fails part-way leaves the earlier versions as they were.
	time.Sleep(10 * time.Millisecond)
	claimed, err = repo.ClaimEventAnalysis(ctx, event, "fp", account, "agg", AnalysisTypeLog, true)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, repo.StartFreshStages(ctx, event, "fp", account, "agg", others...))
	require.NoError(t, repo.UpsertEventAnalysis(ctx, event, "{}", "third", string(AnalysisStatusCompleted), "fp", account, "agg", AnalysisTypeSummary))
	for _, stage := range []EventAnalysisType{AnalysisTypeInvestigation, AnalysisTypeLog, AnalysisTypeDetailedResponse} {
		require.NoError(t, repo.UpsertEventAnalysisStatus(ctx, event, "fp", account, "agg", string(AnalysisStatusFailed), "boom", stage, false))
	}
	failed := versionsOfEvent()
	require.Len(t, failed, 2, "the failed re-run is not listed and nothing is mixed")
	require.Equal(t, "second", failed[0].Summary)
	require.Equal(t, "first", failed[1].Summary)

	// A successful re-run after the failed one: every run keeps one row per stage,
	// so both earlier successful runs still line up as versions.
	time.Sleep(10 * time.Millisecond)
	claimed, err = repo.ClaimEventAnalysis(ctx, event, "fp", account, "agg", AnalysisTypeLog, true)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, repo.StartFreshStages(ctx, event, "fp", account, "agg", others...))
	finish("fourth")
	afterFailure := versionsOfEvent()
	require.Len(t, afterFailure, 3, "the failed run stays hidden; every successful run is listed")
	require.Equal(t, "fourth", afterFailure[0].Summary)
	require.Equal(t, "second", afterFailure[1].Summary)
	require.Equal(t, "first", afterFailure[2].Summary)

	// A stage with no mapping is left alone.
	other := uuid.NewString()
	require.NoError(t, repo.StartFreshStages(ctx, other, "fp", account, "agg", all...))
	var n int
	require.NoError(t, repo.dbManager.Db.Get(&n, `SELECT count(*) FROM event_log_analysis WHERE event_id = $1`, other))
	require.Zero(t, n)
}
