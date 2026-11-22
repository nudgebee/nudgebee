package recommendation

import (
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestHealthCheckWorkloadsQuery_DB checks that the health-check query returns
// each workload once. Its rows feed a single multi-row upsert, so a workload
// returned twice aborts the whole batch for the account.
//
// Runs against throwaway tables, never the real ones. DB-gated: skips with no
// database, fails in the job that provisions one.
func TestHealthCheckWorkloadsQuery_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	dbms, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = dbms.Close() })

	const (
		eventsTable = "zz_health_check_events_test"
		wlTable     = "zz_health_check_workload_test"
		account     = "00000000-0000-0000-0000-00000000000a"
		tenant      = "00000000-0000-0000-0000-00000000000b"
		webID       = "00000000-0000-0000-0000-000000000001"
		apiID       = "00000000-0000-0000-0000-000000000002"
	)

	mustExec := func(q string, args ...any) {
		_, e := dbms.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() {
		for _, tbl := range []string{eventsTable, wlTable} {
			_, _ = dbms.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tbl))
		}
	}
	drop()
	t.Cleanup(drop)

	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		cloud_account_id uuid, subject_name text NOT NULL, subject_namespace text NOT NULL,
		cluster text NOT NULL, finding_type text NOT NULL, starts_at timestamptz NOT NULL)`, eventsTable))
	mustExec(fmt.Sprintf(`CREATE TABLE %s (
		tenant_id uuid, cloud_resource_id uuid, cloud_account_id uuid, is_active boolean,
		name text, namespace text, kind text, meta jsonb, labels jsonb)`, wlTable))

	workload := func(id, name string) {
		mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,$2,$3,true,$4,'ns','Deployment','{}','{}')`, wlTable),
			tenant, id, account, name)
	}
	event := func(name, cluster, findingType string) {
		mustExec(fmt.Sprintf(`INSERT INTO %s VALUES ($1,$2,'ns',$3,$4,now())`, eventsTable),
			account, name, cluster, findingType)
	}

	workload(webID, "web")
	workload(apiID, "api")
	// The same workload's events carry several cluster values, such as an empty
	// one next to a set one, or a name from before a rename.
	event("web", "cluster-a", "CrashLoopBackOff")
	event("web", "cluster-a", "Unhealthy")
	event("web", "", "LivenessProbeFailure")
	event("web", "cluster-b", "ReadinessProbeFailure")
	event("web", "cluster-b", "OOMKilled")

	rows, err := dbms.Queryx(healthCheckWorkloadsQuery(eventsTable, wlTable), account)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	seen := map[string]int{}
	failures := map[string]any{}
	for rows.Next() {
		d := map[string]any{}
		require.NoError(t, rows.MapScan(d))
		id := d["cloud_resource_id"].(string)
		seen[id]++
		failures[id] = d["unhealthy_events"]
	}
	require.NoError(t, rows.Err())

	require.Equal(t, map[string]int{webID: 1, apiID: 1}, seen)
	// Counts from every cluster value are summed into the one row.
	require.Equal(t, int64(4), failures[webID])
	require.Nil(t, failures[apiID])
}
