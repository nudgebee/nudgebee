package scan_orchestrator

import (
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestLinkPopeyeResources_DB runs the resource lookup and the row adoption
// against a real Postgres. Both decide which rows move inside a unique key, and
// a wrong predicate either strands a user's dismissed row or aborts the scan on
// a unique violation; sqlmock shows neither.
//
// Runs in a throwaway schema, never the real tables. DB-gated: skips with no
// database, fails in the job that provisions one.
func TestLinkPopeyeResources_DB(t *testing.T) {
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

	const (
		schema   = "zz_popeye_link_test"
		tenant   = "00000000-0000-0000-0000-00000000000a"
		account  = "00000000-0000-0000-0000-0000000000ac"
		deploy   = "00000000-0000-0000-0000-000000000001"
		sts      = "00000000-0000-0000-0000-000000000002"
		node     = "00000000-0000-0000-0000-000000000003"
		oldDep   = "00000000-0000-0000-0000-000000000004"
		podRowA  = "00000000-0000-0000-0000-0000000000f1"
		podRowB  = "00000000-0000-0000-0000-0000000000f2"
		dismissd = "Dismissed"
	)

	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)

	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`CREATE TABLE ` + schema + `.recommendation (
		id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid, cloud_account_id uuid,
		resource_id uuid, category text, rule_name text, account_object_id text, status text,
		updated_at timestamp DEFAULT now())`)
	// Stands in for the UNIQUE NULLS NOT DISTINCT key, which PG14 lacks.
	mustExec(`CREATE UNIQUE INDEX ON ` + schema + `.recommendation (cloud_account_id, rule_name,
		COALESCE(resource_id, '00000000-0000-0000-0000-000000000000'), category, account_object_id)`)
	mustExec(`CREATE TABLE ` + schema + `.cloud_resourses (
		id uuid PRIMARY KEY, account uuid, tenant uuid, service_name text, type text,
		resourse_id text, is_active boolean)`)
	mustExec(`CREATE TABLE ` + schema + `.k8s_workloads (
		tenant_id uuid, cloud_account_id uuid, cloud_resource_id uuid, namespace text, name text, kind text)`)
	mustExec(`CREATE TABLE ` + schema + `.k8s_pods (
		tenant_id uuid, cloud_account_id uuid, cloud_resource_id uuid, external_id text, namespace text,
		name text, workload_name text, workload_type text, is_active boolean, last_seen timestamp)`)

	mustExec(`INSERT INTO `+schema+`.cloud_resourses VALUES
		($1, $4, $5, 'kubernetes', 'Deployment', 'shop/Deployment/web', true),
		($2, $4, $5, 'kubernetes', 'StatefulSet', 'shop/StatefulSet/db', false),
		($3, $4, $5, 'kubernetes', 'node', 'node-1', NULL),
		($6, $4, $5, 'kubernetes', 'Pod', 'shop/Pod/web-abc', true)`,
		deploy, sts, node, account, tenant, podRowA)
	mustExec(`INSERT INTO `+schema+`.k8s_workloads VALUES
		($1, $2, $3, 'shop', 'web', 'Deployment'),
		($1, $2, $4, 'shop', 'db', 'StatefulSet')`, tenant, account, deploy, sts)
	mustExec(`INSERT INTO `+schema+`.k8s_pods VALUES
		($1, $2, $3, 'shop/Pod/web-abc', 'shop', 'web-abc', 'web', 'Deployment', true, now()),
		($1, $2, $4, 'shop/Pod/db-0', 'shop', 'db-0', 'db', 'StatefulSet', false, now() - interval '1 day')`,
		tenant, account, podRowA, podRowB)

	insertRec := func(rule, objectID, resourceID, status string) {
		var rid any
		if resourceID != "" {
			rid = resourceID
		}
		mustExec(`INSERT INTO `+schema+`.recommendation
			(tenant_id, cloud_account_id, resource_id, category, rule_name, account_object_id, status)
			VALUES ($1, $2, $3, 'Configuration', $4, $5, $6)`, tenant, account, rid, rule, objectID, status)
	}
	// The deployment finding was dismissed before resources were linked.
	insertRec("deployments_misconfigurations", "shop/Deployment/web", "", dismissd)
	// The StatefulSet finding was linked while its workload was active.
	insertRec("statefulsets_misconfigurations", "shop/StatefulSet/db", sts, "Open")
	// The node finding has an unlinked dismissed row and a stale linked Open
	// row; only the dismissed one may move, or the move breaks the unique key.
	insertRec("nodes_misconfigurations", "/Node/node-1", "", dismissd)
	insertRec("nodes_misconfigurations", "/Node/node-1", oldDep, "Open")
	// The pod finding already has a row on the target key and a NULL twin; the
	// twin must stay where it is.
	insertRec("pods_misconfigurations", "shop/Pod/web-abc", deploy, "Open")
	insertRec("pods_misconfigurations", "shop/Pod/web-abc", "", dismissd)

	recs := []Recommendation{
		{RuleName: "deployments_misconfigurations", AccountObjectID: "shop/Deployment/web"},
		{RuleName: "statefulsets_misconfigurations", AccountObjectID: "shop/StatefulSet/db"},
		{RuleName: "nodes_misconfigurations", AccountObjectID: "/Node/node-1"},
		{RuleName: "pods_misconfigurations", AccountObjectID: "shop/Pod/web-abc"},
		{RuleName: "pods_misconfigurations", AccountObjectID: "shop/Pod/db-0"},
		{RuleName: "secrets_misconfigurations", AccountObjectID: "shop/Secret/creds"},
	}

	tx, err := db.Beginx()
	require.NoError(t, err)
	_, err = tx.Exec(`SET LOCAL search_path TO ` + schema)
	require.NoError(t, err)
	require.NoError(t, linkPopeyeResources(tx, ScanAccount{AccountID: account, TenantID: tenant}, recs))
	require.NoError(t, tx.Commit())

	resolved := map[string]string{}
	for _, r := range recs {
		resolved[r.AccountObjectID] = r.ResourceID
	}
	require.Equal(t, map[string]string{
		"shop/Deployment/web": deploy,
		"shop/StatefulSet/db": "", // inactive workload
		"/Node/node-1":        node,
		"shop/Pod/web-abc":    deploy, // owning workload, not the pod row
		"shop/Pod/db-0":       "",     // owner is inactive
		"shop/Secret/creds":   "",
	}, resolved)

	type row struct {
		ObjectID   string  `db:"account_object_id"`
		ResourceID *string `db:"resource_id"`
		Status     string  `db:"status"`
	}
	var rows []row
	require.NoError(t, db.Select(&rows, `SELECT account_object_id, resource_id::text AS resource_id, status
		FROM `+schema+`.recommendation ORDER BY account_object_id, status`))
	got := map[string][]string{}
	for _, r := range rows {
		rid := "NULL"
		if r.ResourceID != nil {
			rid = *r.ResourceID
		}
		got[r.ObjectID] = append(got[r.ObjectID], r.Status+"@"+rid)
	}
	require.Equal(t, map[string][]string{
		"shop/Deployment/web": {dismissd + "@" + deploy},
		"shop/StatefulSet/db": {"Open@NULL"},
		"/Node/node-1":        {dismissd + "@" + node, "Open@" + oldDep},
		"shop/Pod/web-abc":    {dismissd + "@NULL", "Open@" + deploy},
	}, got)
}
