package adapter

import (
	"fmt"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Real-Postgres coverage for the value-refresh cadence.
//
// The claim and the failure hand-back are two statements whose INTERACTION is
// the thing that matters, and no amount of asserting on SQL text can show it:
// the claim stamps the cooldown, and if the failure path clears that stamp then
// a refresh that cannot run is retried on every scheduled run — which, now that
// the optimizer reselects a recommendation whose pull request is open, means a
// code agent dispatched every hour for the life of that pull request.
//
// Named _DB so the services-server DB job's `-run '_DB$'` filter matches, like
// every other database-backed test in this package. Everything runs inside a
// throwaway schema on a single pinned connection, so the unqualified table name
// in the production SQL can never resolve to a real table even if the DSN points
// somewhere it should not.
func cadenceTestManager(t *testing.T) *database.DatabaseManager {
	t.Helper()

	// APP_DATABASE_URL first: that is what the DB job in
	// services-server-dev-gke.yaml exports. TEST_POSTGRES_DSN is accepted too so
	// the same scratch database can drive both services locally.
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but neither APP_DATABASE_URL nor TEST_POSTGRES_DSN is: " +
				"this suite must not be skipped in the job that exists to run it")
		}
		t.Skip("no database configured; export APP_DATABASE_URL to run this suite")
	}

	db, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	// One connection, so SET search_path below holds for every later statement.
	db.SetMaxOpenConns(1)

	schema := fmt.Sprintf("refresh_cadence_test_%d", os.Getpid())
	_, err = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s; SET search_path TO %s`,
		schema, schema, schema))
	require.NoError(t, err)

	_, err = db.Exec(`CREATE TABLE recommendation_resolution (
		id text PRIMARY KEY,
		status_message text,
		updated_at timestamp,
		pr_lifecycle_state text,
		value_refresh_count integer NOT NULL DEFAULT 0,
		last_value_refresh_at timestamp
	)`)
	require.NoError(t, err)

	var current string
	require.NoError(t, db.QueryRow(`SHOW search_path`).Scan(&current))
	require.Contains(t, current, schema, "search_path must point at the throwaway schema")

	t.Cleanup(func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
		_ = db.Close()
	})
	return &database.DatabaseManager{Db: db}
}

func cadenceState(t *testing.T, dbms *database.DatabaseManager, id string) (count int, stamped bool) {
	t.Helper()
	require.NoError(t, dbms.Db.QueryRow(
		`SELECT value_refresh_count, last_value_refresh_at IS NOT NULL
		 FROM recommendation_resolution WHERE id=$1`, id).Scan(&count, &stamped))
	return
}

// TestValueRefreshFailureIsBoundedByTheCadence_DB is the loop that matters: a
// refresh that keeps failing must not re-claim on every run.
//
// It must also not consume the rewrite budget. Charging failures would mean five
// expired tokens or gateway 5xxs retire a pull request for its whole life, and
// the guard would then report it was "already updated 5 times" when it was
// updated zero times.
func TestValueRefreshFailureIsBoundedByTheCadence_DB(t *testing.T) {
	dbms := cadenceTestManager(t)

	_, err := dbms.Db.Exec(`INSERT INTO recommendation_resolution (id, pr_lifecycle_state) VALUES ('res-1', 'created')`)
	require.NoError(t, err)

	claimed, err := claimValueRefreshCadence(dbms, "res-1", 5, 6*time.Hour)
	require.NoError(t, err)
	require.True(t, claimed, "the first attempt should be allowed")

	recordValueRefreshFailure(dbms, "res-1", "code agent failed")

	count, stamped := cadenceState(t, dbms, "res-1")
	require.True(t, stamped,
		"the failure must keep the cadence stamp, or the next run claims again immediately")
	require.Equal(t, 0, count,
		"an infra failure must not spend the rewrite budget — that budget is for rewrites that landed")

	again, err := claimValueRefreshCadence(dbms, "res-1", 5, 6*time.Hour)
	require.NoError(t, err)
	require.False(t, again,
		"a second attempt inside the cooldown must be refused; this is what stops an hourly "+
			"code-agent dispatch for the life of the pull request")

	// Past the cooldown it is due again — a failure delays a retry, it does not
	// abandon one.
	_, err = dbms.Db.Exec(`UPDATE recommendation_resolution
		SET last_value_refresh_at = (now() AT TIME ZONE 'UTC') - interval '7 hours' WHERE id='res-1'`)
	require.NoError(t, err)

	retried, err := claimValueRefreshCadence(dbms, "res-1", 5, 6*time.Hour)
	require.NoError(t, err)
	require.True(t, retried, "once the cooldown elapses the refresh must be retried")
}

// TestValueRefreshBudgetCountsLandedRewrites_DB pins the cap against the budget
// it is named for: rewrites that actually landed.
func TestValueRefreshBudgetCountsLandedRewrites_DB(t *testing.T) {
	dbms := cadenceTestManager(t)

	_, err := dbms.Db.Exec(`INSERT INTO recommendation_resolution (id, pr_lifecycle_state, value_refresh_count)
		VALUES ('res-2', 'created', 5)`)
	require.NoError(t, err)

	claimed, err := claimValueRefreshCadence(dbms, "res-2", 5, 6*time.Hour)
	require.NoError(t, err)
	require.False(t, claimed, "a pull request that has used its rewrite budget must not be claimed again")

	count, stamped := cadenceState(t, dbms, "res-2")
	require.Equal(t, 5, count, "a refused claim must not charge anything")
	require.False(t, stamped,
		"a refused claim must not stamp either — and because nothing re-stamps an exhausted "+
			"resolution, the optimizer's selection predicate has to mirror this cap or it "+
			"reselects the workload forever")
}

// TestValueRefreshClaimStampsNaiveUTC_DB pins what the claim WRITES, not just
// what it decides.
//
// last_value_refresh_at is a timestamp without time zone holding naive UTC —
// recordValueRefresh writes time.Now().UTC() into it, and the claim compares it
// against a UTC bound. A bare now() in the claim stores session-local time
// instead, so on a session ahead of UTC the row it just wrote reads as being in
// the future and the cooldown stretches by the offset: 11.5h after a claim under
// Asia/Kolkata versus 6h after a successful record. Two different cooldowns on
// one column, depending which writer touched it last.
//
// Only bites when the session is not UTC, so on a UTC CI runner this passes
// either way; it earns its keep on developer machines, which is where it was
// caught.
func TestValueRefreshClaimStampsNaiveUTC_DB(t *testing.T) {
	dbms := cadenceTestManager(t)

	_, err := dbms.Db.Exec(`INSERT INTO recommendation_resolution (id, pr_lifecycle_state) VALUES ('res-3', 'created')`)
	require.NoError(t, err)

	claimed, err := claimValueRefreshCadence(dbms, "res-3", 5, 6*time.Hour)
	require.NoError(t, err)
	require.True(t, claimed)

	var skewSeconds float64
	require.NoError(t, dbms.Db.QueryRow(
		`SELECT abs(extract(epoch FROM ((now() AT TIME ZONE 'UTC') - last_value_refresh_at)))
		 FROM recommendation_resolution WHERE id='res-3'`).Scan(&skewSeconds))

	require.Lessf(t, skewSeconds, 60.0,
		"the claim must stamp naive UTC; it is %.0fs off, which is a session-timezone offset, "+
			"and it silently lengthens the cooldown this same statement checks", skewSeconds)
}
