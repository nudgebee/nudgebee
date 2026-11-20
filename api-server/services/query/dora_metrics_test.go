package query

import (
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLGen_RealTable_EventGroupings_DoraColumns(t *testing.T) {
	td, ok := GetTableMetadata("event_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "event_groupings_v2",
		Columns: cols("event_count", "count_failed_changes", "minutes_to_resolve", "count_resolved_issues"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id":  {Eq: "t1"},
				"is_rollout": {Eq: true},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	assert.Contains(t, sql, "AS count_failed_changes")
	assert.Contains(t, sql, "AS minutes_to_resolve")
	assert.Contains(t, sql, "AS count_resolved_issues")
	// A deployment is a pod-template change, filtered in WHERE so that a replica
	// bump is not counted as a deploy.
	assert.Contains(t, sql, "LIKE 'spec.template.%'")
	// Every inner read is bound to the outer row's tenant and account: the
	// engine scoped the outer rows, and the subqueries must not look past it.
	assert.Contains(t, sql, "dora_issue.tenant = events.tenant")
	assert.Contains(t, sql, "dora_issue.cloud_account_id = events.cloud_account_id")
	assert.Contains(t, sql, "dora_prior.tenant = dora_issue.tenant")
	assert.Contains(t, sql, "dora_prior.cloud_account_id = dora_issue.cloud_account_id")
	// Point-in-time events close with ends_at == starts_at; they are not recoveries.
	assert.Contains(t, sql, "events.ends_at > events.starts_at")
}

func TestSQLGen_RealTable_EventGroupings_NoDoraSubqueriesWhenUnreferenced(t *testing.T) {
	td, ok := GetTableMetadata("event_groupings_v2")
	require.True(t, ok)

	req := QueryRequest{
		Table:   "event_groupings_v2",
		Columns: cols("account_id", "status", "event_count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"tenant_id": {Eq: "t1"},
			},
		},
		Limit: 10,
	}
	sql, err := GenerateSqlQuery(superAdminCtx(), "", req, td)
	require.NoError(t, err)

	// Existing callers keep the plan they had: the per-row subqueries only run
	// for a query that asks for a DORA column.
	assert.NotContains(t, sql, "dora_issue")
	assert.NotContains(t, sql, "dora_ev")
}

// TestDoraMetrics_DB runs the DORA columns of event_groupings_v2 against a real
// Postgres. What has to be right is which rows count, which the generated text
// cannot show. DB-gated like TestEventGroupingsCountByLatestRank_DB: skips when
// no database is reachable.
func TestDoraMetrics_DB(t *testing.T) {
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
	db.SetMaxOpenConns(1)

	const schema = "zz_dora_metrics_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE events (
		id serial PRIMARY KEY, tenant uuid NOT NULL, cloud_account_id uuid NOT NULL,
		created_at timestamp NOT NULL, starts_at timestamp, ends_at timestamp,
		finding_type text, aggregation_key text, subject_type text, service_key text,
		fingerprint text, priority text, status text, nb_status text, evidences jsonb)`)

	const (
		tenant   = "00000000-0000-0000-0000-0000000000aa"
		accountA = "00000000-0000-0000-0000-0000000000a1"
		accountB = "00000000-0000-0000-0000-0000000000b1"
		day      = "2026-01-10 "
	)
	diff := func(path string) string {
		return `[{"type":"diff","data":{"updated_values":[{"path":"` + path + `","old":"a","new":"b"}]}}]`
	}
	const image = "spec.template.spec.containers[0].image"
	change := func(account, service, at, path string) {
		mustExec(`INSERT INTO events (tenant, cloud_account_id, created_at, starts_at, finding_type, subject_type, service_key, status, evidences)
			VALUES ($1, $2, $3, $3, 'configuration_change', 'deployment', $4, 'CLOSED', $5)`,
			tenant, account, day+at, service, diff(path))
	}
	// An issue still firing: it can fail a change but is not a recovery.
	issue := func(account, service, fingerprint, at, priority string) {
		mustExec(`INSERT INTO events (tenant, cloud_account_id, created_at, starts_at, finding_type, service_key, fingerprint, priority, status)
			VALUES ($1, $2, $3, $3, 'issue', $4, $5, $6, 'FIRING')`,
			tenant, account, day+at, service, fingerprint, priority)
	}

	// Failed: a new HIGH issue on the same workload 20 minutes later.
	change(accountA, "shop/checkout", "10:00:00", image)
	issue(accountA, "shop/checkout", "fp-new", "10:20:00", "HIGH")
	// Not failed: the alert was already firing in the hour before the deploy.
	change(accountA, "shop/cart", "12:00:00", image)
	issue(accountA, "shop/cart", "fp-flap", "11:30:00", "HIGH")
	issue(accountA, "shop/cart", "fp-flap", "12:30:00", "HIGH")
	// Not failed: the issue that followed is LOW.
	change(accountA, "shop/api", "14:00:00", image)
	issue(accountA, "shop/api", "fp-low", "14:10:00", "LOW")
	// Not failed: the issue came after the window.
	change(accountA, "shop/web", "16:00:00", image)
	issue(accountA, "shop/web", "fp-late", "17:30:00", "CRITICAL")
	// Not failed: same workload name, but another account's issue.
	change(accountA, "shop/pay", "18:00:00", image)
	issue(accountB, "shop/pay", "fp-other", "18:10:00", "HIGH")
	// A replica bump followed by an issue: a change, but not a rollout, so it
	// neither counts as a deploy nor as a failed one.
	change(accountA, "shop/search", "20:00:00", "spec.replicas")
	issue(accountA, "shop/search", "fp-scale", "20:05:00", "HIGH")

	// Recoveries: 10, 30 and 50 minutes. Lower-case status still counts.
	resolved := func(at, endsAt, status, nbStatus string) {
		mustExec(`INSERT INTO events (tenant, cloud_account_id, created_at, starts_at, ends_at, finding_type, priority, status, nb_status)
			VALUES ($1, $2, $3, $3, $4, 'issue', 'HIGH', $5, NULLIF($6, ''))`,
			tenant, accountA, day+at, day+endsAt, status, nbStatus)
	}
	resolved("01:00:00", "01:10:00", "RESOLVED", "")
	resolved("02:00:00", "02:30:00", "CLOSED", "OPEN")
	resolved("03:00:00", "03:50:00", "resolved", "")
	// Excluded: a point-in-time event (ends_at == starts_at)...
	resolved("04:00:00", "04:00:00", "CLOSED", "")
	// ...and a triage-marked duplicate of a long outage already counted.
	resolved("05:00:00", "21:40:00", "RESOLVED", "DUPLICATE")

	td, ok := GetTableMetadata("event_groupings_v2")
	require.True(t, ok)
	type row struct {
		EventCount     int      `db:"event_count"`
		FailedChanges  *int     `db:"count_failed_changes"`
		MinutesResolve *float64 `db:"minutes_to_resolve"`
		Resolved       *int     `db:"count_resolved_issues"`
	}
	run := func(columns []string, where BinaryWhereClause) row {
		where["tenant_id"] = map[BinaryWhereClauseType]any{Eq: tenant}
		where["account_id"] = map[BinaryWhereClauseType]any{Eq: accountA}
		generated, genErr := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
			Table: "event_groupings_v2", Columns: cols(columns...), Where: QueryWhereClause{Binary: where},
		}, td)
		require.NoError(t, genErr)
		var rows []row
		require.NoError(t, db.Select(&rows, generated), generated)
		require.Len(t, rows, 1, strings.TrimSpace(generated))
		return rows[0]
	}

	t.Run("deploys are pod-template changes, and only caused issues fail them", func(t *testing.T) {
		got := run([]string{"event_count", "count_failed_changes"}, BinaryWhereClause{
			"finding_type": {Eq: "configuration_change"},
			"is_rollout":   {Eq: true},
		})
		assert.Equal(t, 5, got.EventCount, "the replica bump is not a deploy")
		require.NotNil(t, got.FailedChanges)
		assert.Equal(t, 1, *got.FailedChanges, "only shop/checkout failed")
	})

	t.Run("a non-rollout change never counts as a failed one, even unfiltered", func(t *testing.T) {
		got := run([]string{"event_count", "count_failed_changes"}, BinaryWhereClause{
			"finding_type": {Eq: "configuration_change"},
		})
		assert.Equal(t, 6, got.EventCount)
		require.NotNil(t, got.FailedChanges)
		assert.Equal(t, 1, *got.FailedChanges)
	})

	t.Run("recovery is the median of real, closed, non-duplicate intervals", func(t *testing.T) {
		got := run([]string{"minutes_to_resolve", "count_resolved_issues"}, BinaryWhereClause{
			"finding_type": {Eq: "issue"},
		})
		require.NotNil(t, got.Resolved)
		assert.Equal(t, 3, *got.Resolved)
		require.NotNil(t, got.MinutesResolve)
		assert.InDelta(t, 30.0, *got.MinutesResolve, 0.001)
	})
}
