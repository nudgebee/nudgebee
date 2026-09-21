package events

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Uses an isolated schema on an explicitly supplied disposable PostgreSQL DB.
// This exercises real transactions, FK locks, SQL ordering, and concurrent claims.
func rcaIntegrationRepo(t *testing.T) *EventAnalysisRepository {
	t.Helper()
	dsn := os.Getenv("RCA_HISTORY_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set RCA_HISTORY_TEST_DB_URL to a disposable PostgreSQL database")
	}
	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := "rca_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	scopedURL, err := url.Parse(dsn)
	require.NoError(t, err)
	params := scopedURL.Query()
	params.Set("search_path", schema)
	scopedURL.RawQuery = params.Encode()
	scoped, err := sqlx.Connect("postgres", scopedURL.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = scoped.Close(); _, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = db.Close() })
	_, err = scoped.Exec(`CREATE TABLE event_log_analysis (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid, event_fingerprint text, cloud_account_id uuid, event_aggregation_key text,analysis_type text,analysis text,summary text,status text,status_reason text,recorded_at timestamp NOT NULL DEFAULT now(),updated_at timestamp);
 CREATE TABLE event_analysis_mapping (event_id uuid,analysis_type text,analysis_id uuid REFERENCES event_log_analysis(id) ON DELETE CASCADE,PRIMARY KEY(event_id,analysis_type));`)
	require.NoError(t, err)
	return NewEventAnalysisRepository(&common.DatabaseManager{Db: scoped})
}

func TestRCAHistoryPostgres(t *testing.T) {
	repo := rcaIntegrationRepo(t)
	ctx := security.NewRequestContextForSuperAdmin()
	event, account := uuid.NewString(), uuid.NewString()
	claim := func(expected string) string {
		id, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", expected)
		require.NoError(t, err)
		require.True(t, won)
		return id
	}
	read := func() []RCAReportVersion {
		rows, err := repo.ListRCAReports(ctx, event, "fp", account, "agg")
		require.NoError(t, err)
		return rows
	}
	type claimResult struct {
		id  string
		won bool
		err error
	}
	claims := make(chan claimResult, 8)
	for i := 0; i < 8; i++ {
		go func() {
			id, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", "")
			claims <- claimResult{id, won, err}
		}()
	}
	first := ""
	winners := 0
	for i := 0; i < 8; i++ {
		got := <-claims
		require.NoError(t, got.err)
		if got.won {
			first = got.id
			winners++
		}
	}
	require.Equal(t, 1, winners)
	require.NotEmpty(t, first)
	require.Empty(t, read())
	// Concurrent duplicates do not dispatch new attempts.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", "")
			require.NoError(t, err)
			require.False(t, won)
			require.Equal(t, first, id)
		}()
	}
	wg.Wait()
	require.NoError(t, repo.FinishRCAAttempt(ctx, first, event, account, "first"))
	rows := read()
	require.Len(t, rows, 1)
	firstTime := *rows[0].GeneratedAt
	second := claim(first)
	require.Equal(t, "first", read()[0].Analysis)
	require.NoError(t, repo.UpdateRCAAttemptStatus(ctx, second, event, account, "FAILED", "failed"))
	require.Equal(t, firstTime, *read()[0].GeneratedAt)
	attempt, err := repo.GetRCAAttempt(ctx, event, account)
	require.NoError(t, err)
	require.Equal(t, "FAILED", attempt.Status)
	third := claim(first)
	require.Error(t, repo.FinishRCAAttempt(ctx, second, event, account, "late"))
	require.NoError(t, repo.FinishRCAAttempt(ctx, third, event, account, "third"))
	require.NoError(t, repo.FinishRCAAttempt(ctx, first, event, account, "duplicate must not replace first"))
	require.NoError(t, repo.UpdateRCAAttemptStatus(ctx, third, event, account, "FAILED", "late failure"))
	require.Equal(t, "third", read()[0].Analysis)
	_, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", first)
	require.NoError(t, err)
	require.False(t, won)
	require.NotEqual(t, RCAAttemptSessionID(first), RCAAttemptSessionID(third))
	// A different event shares the first report. Pruning must keep its mapping.
	other := uuid.NewString()
	_, err = repo.dbManager.Db.Exec(`INSERT INTO event_analysis_mapping VALUES ($1,'rca_analysis',$2)`, other, first)
	require.NoError(t, err)
	latest := third
	for i := 0; i < 6; i++ {
		id := claim(latest)
		require.NoError(t, repo.FinishRCAAttempt(ctx, id, event, account, fmt.Sprint("report-", i)))
		latest = id
	}
	require.Len(t, read(), RCAHistoryLimit)
	var shared string
	require.NoError(t, repo.dbManager.Db.Get(&shared, `SELECT analysis_id FROM event_analysis_mapping WHERE event_id=$1`, other))
	require.Equal(t, first, shared)
	sharedRows, err := repo.ListRCAReports(ctx, other, "fp", account, "agg")
	require.NoError(t, err)
	require.Len(t, sharedRows, 1)
	sharedAttempt, won, err := repo.ClaimRCAAttempt(ctx, other, "fp", account, "agg", first)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, repo.FinishRCAAttempt(ctx, sharedAttempt, other, account, "other report"))
	sharedRows, err = repo.ListRCAReports(ctx, other, "fp", account, "agg")
	require.NoError(t, err)
	require.Len(t, sharedRows, 2)
	require.Equal(t, "first", sharedRows[1].Analysis)
	require.Equal(t, firstTime, *sharedRows[1].GeneratedAt)
	for _, test := range []struct{ event, account, fp string }{{event, uuid.NewString(), "fp"}, {uuid.NewString(), account, "fp"}, {event, account, "wrong"}} {
		rows, err := repo.ListRCAReports(ctx, test.event, test.fp, test.account, "agg")
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	require.Error(t, repo.FinishRCAAttempt(ctx, latest, event, uuid.NewString(), "unauthorized"))
}

func TestRCAHistoryLegacyPostgres(t *testing.T) {
	repo := rcaIntegrationRepo(t)
	ctx := security.NewRequestContextForSuperAdmin()
	event, account := uuid.NewString(), uuid.NewString()
	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var legacy string
	require.NoError(t, repo.dbManager.Db.QueryRowx(`INSERT INTO event_log_analysis(event_id,cloud_account_id,event_fingerprint,event_aggregation_key,analysis_type,analysis,status,recorded_at) VALUES ($1,$2,'fp','agg','rca_analysis','legacy','COMPLETED',$3) RETURNING id`, event, account, oldTime).Scan(&legacy))
	rows, err := repo.ListRCAReports(ctx, event, "fp", account, "agg")
	require.NoError(t, err)
	require.True(t, oldTime.Equal(*rows[0].GeneratedAt))
	id, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", legacy)
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, repo.FinishRCAAttempt(ctx, id, event, account, "new"))
	rows, err = repo.ListRCAReports(ctx, event, "fp", account, "agg")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// Legacy failure time is not a successful generation time.
	_, err = repo.dbManager.Db.Exec(`UPDATE event_log_analysis SET status='FAILED' WHERE id=$1`, legacy)
	require.NoError(t, err)
	rows, err = repo.ListRCAReports(ctx, event, "fp", account, "agg")
	require.NoError(t, err)
	require.Nil(t, rows[1].GeneratedAt)
	_, err = repo.dbManager.Db.Exec(`UPDATE event_log_analysis SET status='IN_PROGRESS' WHERE id=$1`, legacy)
	require.NoError(t, err)
	require.NoError(t, repo.SaveEventRCAAnalysis(ctx, legacy, event, "fp", account, "agg", "legacy recovery"))
	require.NoError(t, repo.SaveEventRCAAnalysis(ctx, legacy, event, "fp", account, "agg", "retry"))
	rows, err = repo.ListRCAReports(ctx, event, "fp", account, "agg")
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "new", rows[1].Analysis)
}

func TestRCAHistoryPublicationSnapshotPostgres(t *testing.T) {
	repo := rcaIntegrationRepo(t)
	ctx := security.NewRequestContextForSuperAdmin()
	event, account := uuid.NewString(), uuid.NewString()
	first, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", "")
	require.NoError(t, err)
	require.True(t, won)
	require.NoError(t, repo.FinishRCAAttempt(ctx, first, event, account, "first"))
	next, won, err := repo.ClaimRCAAttempt(ctx, event, "fp", account, "agg", first)
	require.NoError(t, err)
	require.True(t, won)
	tx, err := repo.dbManager.Db.BeginTxx(ctx.GetContext(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	reports, err := listRCAReports(tx, event, "fp", account, "agg")
	require.NoError(t, err)
	require.Equal(t, "first", reports[0].Analysis)
	require.NoError(t, repo.FinishRCAAttempt(ctx, next, event, account, "second"))
	attempt, err := getRCAAttempt(tx, event, account)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	require.Equal(t, "IN_PROGRESS", attempt.Status)
	require.NoError(t, tx.Commit())
	reports, attempt, err = repo.GetRCAHistory(ctx, event, "fp", account, "agg")
	require.NoError(t, err)
	require.Nil(t, attempt)
	require.Equal(t, "second", reports[0].Analysis)
}
