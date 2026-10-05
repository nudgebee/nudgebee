package query

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cloudResourceMetricsSQL is the SQL for the request the cloud resource
// metrics tab sends (app/src/api1/cloud-account/index.ts
// getCloudResourceMetricsDirect): one account, a service, a time window,
// oldest first. The tenant filter is the one ExecuteQuery adds.
func cloudResourceMetricsSQL(t *testing.T, tenant, account, service, from, to string) string {
	t.Helper()
	td, ok := GetTableMetadata("cloud_resource_metrics_v2")
	require.True(t, ok)
	generated, err := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
		Table:   "cloud_resource_metrics_v2",
		Columns: cols("metric", "value", "timestamp", "cloud_resource_id", "resource_name", "resource_id"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"cloud_account_id": {Eq: account},
				"service_name":     {Eq: service},
				"timestamp":        {Gte: from, Lte: to},
			},
			And: []QueryWhereClause{{Binary: BinaryWhereClause{"tenant_id": {Eq: tenant}}}},
		},
		OrderBy: []QueryOrderBy{{Column: "timestamp", Order: "asc"}},
		Limit:   5000,
	}, td)
	require.NoError(t, err)
	return generated
}

// The view must expose cloud_account_id and tenant_id as the uuid columns.
// Every caller filters on both plus a time window; cast to text, neither can
// use the (tenant_id, cloud_account_id, timestamp) index, and the table is
// several GB (#39743).
func TestCloudResourceMetricsAccountColumnsNotCast(t *testing.T) {
	td, ok := GetTableMetadata("cloud_resource_metrics_v2")
	require.True(t, ok)
	assert.NotContains(t, td.Def, "crm.cloud_account_id::text")
	assert.NotContains(t, td.Def, "crm.tenant_id::text")

	generated := cloudResourceMetricsSQL(t, "00000000-0000-0000-0000-0000000000aa", "00000000-0000-0000-0000-0000000000a1", "ECS",
		"2026-01-05T00:00:00.000Z", "2026-01-06T00:00:00.000Z")
	assert.Contains(t, generated, "(cloud_account_id = '00000000-0000-0000-0000-0000000000a1')")
	assert.Contains(t, generated, "(tenant_id = '00000000-0000-0000-0000-0000000000aa')")
}

// TestCloudResourceMetricsAccountIndex_DB runs the metrics tab's SQL against a
// real Postgres: it returns the account's points in the window, with the ids
// as the same text the view returned before, and the account, tenant and
// window can be served by the (tenant_id, cloud_account_id, timestamp) index.
//
// Builds its own tables in a throwaway schema. DB-gated: skips when no
// database is reachable.
func TestCloudResourceMetricsAccountIndex_DB(t *testing.T) {
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

	const schema = "zz_cloud_resource_metrics_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE cloud_resourses (id uuid PRIMARY KEY, name text, resourse_id text, service_name text)`)
	mustExec(`CREATE TABLE cloud_resource_metrics (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(), timestamp timestamp NOT NULL, metric text NOT NULL,
		value double precision NOT NULL, cloud_resource_id uuid NOT NULL, cloud_account_id uuid, tenant_id uuid)`)
	mustExec(`CREATE INDEX cloud_resource_metrics_tenantaccounttimestamp ON cloud_resource_metrics (tenant_id, cloud_account_id, timestamp)`)

	const (
		tenant   = "00000000-0000-0000-0000-0000000000aa"
		account  = "00000000-0000-0000-0000-0000000000a1"
		other    = "00000000-0000-0000-0000-0000000000a2"
		service  = "00000000-0000-0000-0000-000000000c01"
		database = "00000000-0000-0000-0000-000000000c02"
	)
	mustExec(`INSERT INTO cloud_resourses VALUES ($1, 'shop-svc', 'arn:shop-svc', 'ECS'), ($2, 'shop-db', 'arn:shop-db', 'RDS')`, service, database)
	point := func(acct, resource, at string, value float64) {
		mustExec(`INSERT INTO cloud_resource_metrics (timestamp, metric, value, cloud_resource_id, cloud_account_id, tenant_id)
			VALUES ($1, 'CPUUtilization', $2, $3, $4, $5)`, at, value, resource, acct, tenant)
	}
	point(account, service, "2026-01-04 23:59:00", 1)  // before the window
	point(account, service, "2026-01-05 00:00:00", 10) // window start, inclusive
	point(account, service, "2026-01-05 12:00:00", 20) // inside
	point(account, service, "2026-01-06 00:00:00", 30) // window end, inclusive
	point(account, service, "2026-01-06 00:01:00", 2)  // after the window
	point(account, database, "2026-01-05 12:00:00", 3) // another service
	point(other, service, "2026-01-05 12:00:00", 4)    // another account
	// Enough of another account's rows that the planner has a reason to use
	// the index rather than read the whole table.
	mustExec(`INSERT INTO cloud_resource_metrics (timestamp, metric, value, cloud_resource_id, cloud_account_id, tenant_id)
		SELECT '2026-01-05'::timestamp + make_interval(secs => g), 'CPUUtilization', 0, $1, $2, $3 FROM generate_series(1, 5000) g`,
		service, other, tenant)
	mustExec(`ANALYZE cloud_resource_metrics`)
	mustExec(`ANALYZE cloud_resourses`)

	query := cloudResourceMetricsSQL(t, tenant, account, "ECS", "2026-01-05T00:00:00.000Z", "2026-01-06T00:00:00.000Z")

	t.Run("returns the account's points in the window", func(t *testing.T) {
		type row struct {
			Metric       string    `db:"metric"`
			Value        float64   `db:"value"`
			Timestamp    time.Time `db:"timestamp"`
			ResourceID   string    `db:"cloud_resource_id"`
			ResourceName string    `db:"resource_name"`
			ProviderID   string    `db:"resource_id"`
		}
		var rows []row
		require.NoError(t, db.Select(&rows, query))
		values := make([]float64, 0, len(rows))
		for _, r := range rows {
			values = append(values, r.Value)
			assert.Equal(t, service, r.ResourceID)
			assert.Equal(t, "shop-svc", r.ResourceName)
			assert.Equal(t, "arn:shop-svc", r.ProviderID)
		}
		assert.Equal(t, []float64{10, 20, 30}, values)
	})

	t.Run("ids come back as the same text", func(t *testing.T) {
		td, ok := GetTableMetadata("cloud_resource_metrics_v2")
		require.True(t, ok)
		generated, genErr := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
			Table:   "cloud_resource_metrics_v2",
			Columns: cols("cloud_account_id", "tenant_id", "cloud_resource_id"),
			Where: QueryWhereClause{Binary: BinaryWhereClause{
				"cloud_account_id": {In: []any{account}},
				"tenant_id":        {Eq: tenant},
				"service_name":     {Eq: "ECS"},
			}},
		}, td)
		require.NoError(t, genErr)
		type row struct {
			Account  string `db:"cloud_account_id"`
			Tenant   string `db:"tenant_id"`
			Resource string `db:"cloud_resource_id"`
		}
		var rows []row
		require.NoError(t, db.Select(&rows, generated))
		require.Len(t, rows, 1, "grouped on the selected columns")
		assert.Equal(t, row{account, tenant, service}, rows[0])
	})

	t.Run("the account, tenant and window are index conditions", func(t *testing.T) {
		// Whether the planner picks the index for a few thousand rows is a
		// cost question; whether it can is the property. With the ids cast
		// to text it cannot, whatever the costs.
		mustExec(`SET enable_seqscan = off`)
		t.Cleanup(func() { _, _ = db.Exec(`RESET enable_seqscan`) })
		var raw string
		require.NoError(t, db.Get(&raw, `EXPLAIN (FORMAT JSON) `+query))
		var plans []struct {
			Plan map[string]any `json:"Plan"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &plans))
		require.Len(t, plans, 1)

		var indexConditions []string
		var walk func(node map[string]any)
		walk = func(node map[string]any) {
			relation, _ := node["Relation Name"].(string)
			index, _ := node["Index Name"].(string)
			if relation == "cloud_resource_metrics" || index == "cloud_resource_metrics_tenantaccounttimestamp" {
				if cond, ok := node["Index Cond"].(string); ok {
					indexConditions = append(indexConditions, cond)
				}
			}
			children, _ := node["Plans"].([]any)
			for _, child := range children {
				walk(child.(map[string]any))
			}
		}
		walk(plans[0].Plan)
		conditions := strings.Join(indexConditions, " ")
		for _, column := range []string{"tenant_id", "cloud_account_id", "timestamp"} {
			assert.Contains(t, conditions, column, "%s is not an index condition on cloud_resource_metrics; plan: %s", column, raw)
		}
	})
}
