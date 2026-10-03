package recommendation

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestRecomputeFinOpsScores_DB runs the recompute walk against a real Postgres
// planner. What it protects is decided in SQL, not Go: which Open rows the
// walk still visits once their recency boost has decayed, and whether the
// unchanged-row guard lets a row whose score has not moved through untouched.
// Both failure modes are silent — the first as a daily rewrite of the whole
// Open set, the second as a row whose stale score is never corrected.
//
// Runs inside a throwaway schema on the connection's search_path, so the real
// tables of whatever database it is pointed at are never touched. DB-gated:
// skips with no database, fails in the job that provisions one.
func TestRecomputeFinOpsScores_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// One connection, so the SET search_path below holds for every statement
	// the walk issues. The walk closes each page before it scores it, so it
	// never needs a second connection.
	db.SetMaxOpenConns(1)

	const schema = "zz_finops_recompute_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)

	// The columns the walk reads and writes, typed as the real table types them
	// where the SQL depends on it (id is compared against a ::uuid cursor).
	mustExec(`CREATE TABLE recommendation (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
		tenant_id text NOT NULL, cloud_account_id text, category text, rule_name text,
		severity text, estimated_savings float8,
		created_at timestamp NOT NULL DEFAULT now(),
		status text NOT NULL DEFAULT 'Open', resource_id text, recommendation jsonb,
		finops_score int, finops_band text, finops_score_breakdown jsonb)`)
	mustExec(`CREATE TABLE cloud_resourses (id text PRIMARY KEY, name text, meta jsonb)`)

	ctx := security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil)
	dbms := &database.DatabaseManager{Db: db}
	run := func() recomputeStats {
		stats, e := recomputeFinOpsScoresWith(ctx, dbms, 0)
		require.NoError(t, e)
		require.Zero(t, stats.errors, "walk reported errors")
		return stats
	}
	ageAll := func(where string, days int, args ...any) {
		mustExec(`UPDATE recommendation SET created_at = now() - make_interval(days => $1) WHERE `+where,
			append([]any{days}, args...)...)
	}
	type scored struct {
		Score int    `db:"finops_score"`
		Band  string `db:"finops_band"`
	}
	scoreOf := func(where string, args ...any) scored {
		var s scored
		require.NoError(t, db.Get(&s, `SELECT finops_score, finops_band FROM recommendation WHERE `+where, args...))
		return s
	}

	// Three pages' worth of findings with no resource and no savings, the shape
	// that dominates the Open set: scored from severity and age alone.
	const findings = 4500
	mustExec(`INSERT INTO recommendation (tenant_id, cloud_account_id, category, rule_name, severity,
	            created_at, resource_id, finops_score, finops_band, finops_score_breakdown)
	          SELECT 't1', 'a1', 'Security', 'image_scan', 'Medium',
	                 now() - interval '2 days', NULL, 0, '', '{}'
	          FROM generate_series(1, $1)`, findings)

	// First pass scores every row, across pages.
	stats := run()
	require.Equal(t, findings, stats.scanned)
	require.Equal(t, findings, stats.updated)
	require.Equal(t, 3, stats.pages)
	// Security Medium: 50 severity + 5 for being under a week old.
	require.Equal(t, scored{55, "High"}, scoreOf(`rule_name = 'image_scan' LIMIT 1`))

	// A day later nothing has moved but the rows' age in days. The guard must
	// treat that as unchanged, or the daily pass rewrites the whole Open set.
	ageAll(`rule_name = 'image_scan'`, 3)
	stats = run()
	require.Equal(t, findings, stats.scanned, "rows inside the recency window stay in the walk")
	require.Zero(t, stats.updated, "a row whose score did not move must not be rewritten")
	require.Equal(t, findings, stats.unchanged)

	// Crossing a recency step is a real change and is written exactly once.
	ageAll(`rule_name = 'image_scan'`, 8)
	stats = run()
	require.Equal(t, findings, stats.updated)
	require.Equal(t, scored{52, "Medium"}, scoreOf(`rule_name = 'image_scan' LIMIT 1`))
	require.Zero(t, run().updated)

	// Past the last step the boost is zero for good. The row is written once
	// more, stays in the walk through the grace period, then drops out of it.
	ageAll(`rule_name = 'image_scan'`, recencyBoostLastStepDays+1)
	stats = run()
	require.Equal(t, findings, stats.updated)
	require.Equal(t, scored{50, "Medium"}, scoreOf(`rule_name = 'image_scan' LIMIT 1`))
	ageAll(`rule_name = 'image_scan'`, recencyBoostLastStepDays+recencyRescoreGraceDays+1)
	stats = run()
	require.Zero(t, stats.scanned, "a scored, resource-less row past the grace period leaves the walk")

	// A re-scan that re-rates a finding's severity is a scoring input moving
	// under a row the walk would otherwise never revisit.
	mustExec(`UPDATE recommendation SET severity = 'Critical'
	          WHERE id = (SELECT id FROM recommendation WHERE rule_name = 'image_scan' ORDER BY id LIMIT 1)`)
	stats = run()
	require.Equal(t, 1, stats.scanned)
	require.Equal(t, 1, stats.updated)
	require.Equal(t, scored{100, "Act Now"}, scoreOf(`severity = 'Critical'`))
	require.Zero(t, run().scanned)

	// Likewise a savings refresh on an account-level cost row.
	mustExec(`INSERT INTO recommendation (tenant_id, cloud_account_id, category, rule_name, severity,
	            estimated_savings, created_at, resource_id)
	          VALUES ('t1', 'a1', 'RightSizing', 'reserved_instance', 'Medium', 100, now() - interval '90 days', NULL)`)
	stats = run()
	require.Equal(t, 1, stats.scanned, "a never-scored row is visited whatever its age")
	require.Equal(t, 1, stats.updated)
	before := scoreOf(`rule_name = 'reserved_instance'`)
	require.Zero(t, run().scanned)
	mustExec(`UPDATE recommendation SET estimated_savings = 2000 WHERE rule_name = 'reserved_instance'`)
	stats = run()
	require.Equal(t, 1, stats.scanned)
	require.Equal(t, 1, stats.updated)
	require.Greater(t, scoreOf(`rule_name = 'reserved_instance'`).Score, before.Score)

	// A row with a resource is revisited on every pass: its blast radius can
	// change without any column on the row moving. Here the graph is absent, so
	// it is scored and left unannotated; what matters is that it is scanned.
	mustExec(`INSERT INTO cloud_resourses (id, name, meta) VALUES ('res-1', 'api', '{"namespace":"default"}')`)
	mustExec(`INSERT INTO recommendation (tenant_id, cloud_account_id, category, rule_name, severity,
	            estimated_savings, created_at, resource_id)
	          VALUES ('t1', 'a1', 'RightSizing', 'pod_right_sizing', 'High', 40, now() - interval '90 days', 'res-1')`)
	require.Equal(t, 1, run().updated)
	stats = run()
	require.Equal(t, 1, stats.scanned, "a row with a resource never leaves the walk")
	require.Zero(t, stats.updated)

	// Rows outside the requested window are out of scope for the hourly pass.
	stats, err = recomputeFinOpsScoresWith(ctx, dbms, 24*time.Hour)
	require.NoError(t, err)
	require.Zero(t, stats.scanned)
}
