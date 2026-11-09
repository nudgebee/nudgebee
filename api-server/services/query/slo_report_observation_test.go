package query

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sloObservationSQL is the SQL for the request the SLO cards send
// (app/src/api1/kubernetes1/index.ts getSLOObservation): one account, a
// timestamp lower bound, optional workload filters, and the per-workload sums.
// The tenant filter is the one ExecuteQuery adds for every caller.
func sloObservationSQL(t *testing.T, tenant, account, since string) string {
	t.Helper()
	td, ok := GetTableMetadata("slo_report_observation_v2")
	require.True(t, ok)
	generated, err := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
		Table:   "slo_report_observation_v2",
		Columns: cols("config_name", "total_bad_events", "total_events", "total_good_events", "workload_name", "workload_namespace", "status"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"account_id": {Eq: account},
				"timestamp":  {Gte: since},
			},
			And: []QueryWhereClause{{Binary: BinaryWhereClause{"tenant_id": {Eq: tenant}}}},
		},
	}, td)
	require.NoError(t, err)
	return generated
}

// The view's timestamp has to be the grouped column itself, not MAX() of it.
// Both give the same value (timestamp is in the GROUP BY), but a filter on an
// aggregate output can only run after the aggregate, so with MAX() every SLO
// report the account ever wrote was grouped and sorted before the screen's
// "last 24 hours" filter threw almost all of it away (#39743).
func TestSloReportObservationTimestampIsGroupedColumn(t *testing.T) {
	td, ok := GetTableMetadata("slo_report_observation_v2")
	require.True(t, ok)
	assert.NotContains(t, strings.ToUpper(td.Def), "MAX(SR.TIMESTAMP)")
	assert.Contains(t, td.Def, "sr.timestamp,")

	generated := sloObservationSQL(t, "00000000-0000-0000-0000-0000000000aa", "00000000-0000-0000-0000-0000000000a1", "2026-01-05T00:00:00.000Z")
	assert.Contains(t, generated, "(timestamp >= '2026-01-05T00:00:00.000Z')")
	assert.Contains(t, generated, "(cloud_account_id = '00000000-0000-0000-0000-0000000000a1')")
}

// TestSloReportObservationWindow_DB runs the generated SQL against a real
// Postgres: the sums cover exactly the reports inside the window, and the
// window is applied to slo_report rows before they are grouped, not as a
// filter on the aggregate.
//
// Builds its own tables in a throwaway schema. DB-gated: skips when no
// database is reachable.
func TestSloReportObservationWindow_DB(t *testing.T) {
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

	const schema = "zz_slo_report_observation_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE slo_config (id uuid PRIMARY KEY, name text NOT NULL)`)
	mustExec(`CREATE TABLE slo_report (
		config_id uuid NOT NULL, tenant_id uuid NOT NULL, cloud_account_id uuid NOT NULL,
		workload_name text NOT NULL, workload_namespace text NOT NULL,
		timestamp timestamp NOT NULL, status text NOT NULL,
		good_events_count numeric, bad_events_count numeric, events_count numeric)`)

	const (
		tenant  = "00000000-0000-0000-0000-0000000000aa"
		account = "00000000-0000-0000-0000-0000000000a1"
		other   = "00000000-0000-0000-0000-0000000000a2"
		config  = "00000000-0000-0000-0000-000000000c01"
	)
	mustExec(`INSERT INTO slo_config VALUES ($1, 'availability')`, config)
	report := func(acct, workload, at, status string, good, bad int) {
		mustExec(`INSERT INTO slo_report VALUES ($1, $2, $3, $4, 'shop', $5, $6, $7::int, $8::int, $7::int + $8::int)`,
			config, tenant, acct, workload, at, status, good, bad)
	}
	// Outside the window: must not count.
	report(account, "cart", "2026-01-01 10:00:00", "BREACHED", 1000, 1000)
	report(account, "cart", "2026-01-04 23:00:00", "OK", 1000, 0)
	// Inside the window.
	report(account, "cart", "2026-01-05 00:00:00", "OK", 90, 10)
	report(account, "cart", "2026-01-06 00:00:00", "OK", 80, 20)
	report(account, "cart", "2026-01-07 00:00:00", "BREACHED", 50, 50)
	report(account, "checkout", "2026-01-06 00:00:00", "OK", 7, 3)
	// Another account, inside the window: must not count.
	report(other, "cart", "2026-01-06 00:00:00", "OK", 500, 500)

	query := sloObservationSQL(t, tenant, account, "2026-01-05T00:00:00.000Z")

	t.Run("sums cover the window only", func(t *testing.T) {
		type row struct {
			Config    string `db:"config_name"`
			Workload  string `db:"workload_name"`
			Namespace string `db:"workload_namespace"`
			Status    string `db:"status"`
			Good      string `db:"total_good_events"`
			Bad       string `db:"total_bad_events"`
			Total     string `db:"total_events"`
		}
		var rows []row
		require.NoError(t, db.Select(&rows, query))
		got := map[string]row{}
		for _, r := range rows {
			got[r.Workload+"/"+r.Status] = r
		}
		require.Len(t, got, 3)
		assert.Equal(t, row{"availability", "cart", "shop", "OK", "170", "30", "200"}, got["cart/OK"])
		assert.Equal(t, row{"availability", "cart", "shop", "BREACHED", "50", "50", "100"}, got["cart/BREACHED"])
		assert.Equal(t, row{"availability", "checkout", "shop", "OK", "7", "3", "10"}, got["checkout/OK"])
	})

	t.Run("the window is applied before grouping", func(t *testing.T) {
		var raw string
		require.NoError(t, db.Get(&raw, `EXPLAIN (FORMAT JSON) `+query))
		var plans []struct {
			Plan map[string]any `json:"Plan"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &plans))
		require.Len(t, plans, 1)

		var scanConditions []string
		var walk func(node map[string]any)
		walk = func(node map[string]any) {
			if relation, _ := node["Relation Name"].(string); relation == "slo_report" {
				for _, key := range []string{"Filter", "Index Cond", "Recheck Cond"} {
					if cond, ok := node[key].(string); ok {
						scanConditions = append(scanConditions, cond)
					}
				}
			}
			if filter, ok := node["Filter"].(string); ok {
				assert.NotContains(t, strings.ToLower(filter), "max(", "the timestamp window is a filter on an aggregate: %s", filter)
			}
			children, _ := node["Plans"].([]any)
			for _, child := range children {
				walk(child.(map[string]any))
			}
		}
		walk(plans[0].Plan)
		assert.Contains(t, strings.Join(scanConditions, " "), `"timestamp" >=`,
			"slo_report is read without the timestamp window; it is applied after grouping")
	})
}
