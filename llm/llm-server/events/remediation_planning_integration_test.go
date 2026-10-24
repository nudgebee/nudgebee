package events

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// planningDB is an isolated schema with the columns the planning statements use, on the disposable database
// REMEDIATION_RUN_TEST_DB_URL names (no migrations needed).
func planningDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("REMEDIATION_RUN_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_RUN_TEST_DB_URL to a disposable PostgreSQL database")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := "planning_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	_, err = db.Exec(`CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, tenant uuid, fingerprint text, aggregation_key text);
		CREATE TABLE feature_flag (feature_id text, tenant_id uuid, account_id uuid, status text);
		CREATE TABLE event_log_analysis (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid NOT NULL, event_fingerprint text,
			analysis text, summary text, status text NOT NULL DEFAULT 'COMPLETED', status_reason text, cloud_account_id uuid,
			event_aggregation_key text, analysis_type varchar NOT NULL, recorded_at timestamp NOT NULL DEFAULT now(), updated_at timestamp DEFAULT now())`)
	require.NoError(t, err)
	return db
}

// A request's life: queued once per event, its turn claimed by one worker, one proposal, then ended once. Other
// rows' analysis is text, and no statement casts it.
func TestPlanningRequestLifecycle(t *testing.T) {
	db := planningDB(t)
	ctx := context.Background()
	account, tenant, event := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := db.Exec(`INSERT INTO events VALUES ($1, $2, $3, 'fp-1', 'agg')`, event, account, tenant)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO feature_flag VALUES ($1, $2, NULL, 'enabled')`, RemediationLoopFeature, tenant)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, cloud_account_id, analysis_type)
		VALUES ($1, 'fp-1', '## Summary: not json', $2, 'summary')`, event, account)
	require.NoError(t, err)

	row, queued, err := EnqueuePlanning(ctx, db, account, event, PlanningRequest{Kind: "auto"})
	require.NoError(t, err)
	require.True(t, queued)
	assert.Equal(t, PlanningQueued, row.State)
	assert.Equal(t, "fp-1", row.Alert)
	again, queued, err := EnqueuePlanning(ctx, db, account, event, PlanningRequest{Kind: "ask"})
	require.NoError(t, err)
	assert.False(t, queued, "one open request per event")
	assert.Equal(t, row.Id, again.Id)

	open, err := OpenPlannings(ctx, db, 10)
	require.NoError(t, err)
	require.Len(t, open, 1)

	set, err := SetPlanningTurn(ctx, db, row.Id, "", "m1")
	require.NoError(t, err)
	require.True(t, set)
	set, err = SetPlanningTurn(ctx, db, row.Id, "", "m2")
	require.NoError(t, err)
	assert.False(t, set, "a second worker does not start it again")

	turn, err := PlanningOfTurn(ctx, db, account, "m1")
	require.NoError(t, err)
	assert.Equal(t, PlanningRunning, turn.State)
	assert.Equal(t, 1, turn.Attempts)

	claimed, err := ClaimPlanningProposal(ctx, db, row.Id)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = ClaimPlanningProposal(ctx, db, row.Id)
	require.NoError(t, err)
	assert.False(t, claimed, "one plan per turn")
	require.NoError(t, ReleasePlanningProposal(ctx, db, row.Id))
	claimed, err = ClaimPlanningProposal(ctx, db, row.Id)
	require.NoError(t, err)
	assert.True(t, claimed, "free again after a plan that was not stored")

	ended, err := FinishPlanning(ctx, db, row.Id, PlanningDone, "pv-1", "")
	require.NoError(t, err)
	require.True(t, ended)
	ended, err = FinishPlanning(ctx, db, row.Id, PlanningNone, "", "late")
	require.NoError(t, err)
	assert.False(t, ended, "ended once")

	latest, err := LatestPlanning(ctx, db, account, event)
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, PlanningDone, latest.State)
	assert.Equal(t, "pv-1", latest.PlanVersion)
	assert.False(t, latest.Open())

	// A new request after the first ended; it ends with no plan and its reason.
	next, queued, err := EnqueuePlanning(ctx, db, account, event, PlanningRequest{Kind: "replan", RequestedBy: "u1", Reasons: []string{"too risky"}, Note: "no restarts"})
	require.NoError(t, err)
	require.True(t, queued)
	ended, err = FinishPlanning(ctx, db, next.Id, PlanningNone, "", "Nubi could not start")
	require.NoError(t, err)
	require.True(t, ended)
	latest, err = LatestPlanning(ctx, db, account, event)
	require.NoError(t, err)
	assert.Equal(t, PlanningNone, latest.State)
	assert.Equal(t, "Nubi could not start", latest.Reason)
	assert.Equal(t, []string{"too risky"}, latest.Reasons)
	var status string
	require.NoError(t, db.Get(&status, `SELECT status FROM event_log_analysis WHERE id = $1`, next.Id))
	assert.Equal(t, "FAILED", status, "never IN_PROGRESS: recovery would restart it as an investigation")

	open, err = OpenPlannings(ctx, db, 10)
	require.NoError(t, err)
	assert.Empty(t, open)

	_, _, err = EnqueuePlanning(ctx, db, uuid.NewString(), event, PlanningRequest{Kind: "ask"})
	assert.Error(t, err, "another account's event is not found")
}
