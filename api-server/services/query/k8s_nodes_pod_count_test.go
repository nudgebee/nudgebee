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

// nodeListSQL is the SQL for the node list page's request
// (app/src/api1/kubernetes/index.ts LIST_k8_NODES): one account, newest nodes
// first, one page. The tenant filter is the one ExecuteQuery adds.
func nodeListSQL(t *testing.T, tenant, account string, limit int) string {
	t.Helper()
	td, ok := GetTableMetadata("k8s_nodes_v2")
	require.True(t, ok)
	generated, err := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
		Table:   "k8s_nodes_v2",
		Columns: cols("name", "cloud_resource_id", "is_active", "pod_count"),
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{"cloud_account_id": {Eq: account}},
			And:    []QueryWhereClause{{Binary: BinaryWhereClause{"tenant_id": {Eq: tenant}}}},
		},
		OrderBy: []QueryOrderBy{{Column: "node_creation_time", Order: "desc"}},
		Limit:   limit,
	}, td)
	require.NoError(t, err)
	return generated
}

// pod_count must not be a per-row subquery: k8s_pods has no node_name index,
// so each row re-read every active pod of the account (#39743). And a request
// that never reads pod_count must not pay for counting pods.
func TestK8sNodesPodCountIsJoinedNotPerRow(t *testing.T) {
	td, ok := GetTableMetadata("k8s_nodes_v2")
	require.True(t, ok)
	assert.Empty(t, td.Columns["pod_count"].Def, "pod_count is a column of the grouped join, not an expression per row")

	generated := nodeListSQL(t, "00000000-0000-0000-0000-0000000000aa", "00000000-0000-0000-0000-0000000000a1", 10)
	assert.Contains(t, generated, "pod_count AS pod_count")
	assert.Contains(t, generated, "GROUP BY cloud_account_id, tenant_id, node_name")
	assert.NotContains(t, generated, "k8s_nodes.name)", "no correlated reference to the outer node row")

	withoutPodCount, err := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
		Table:   "k8s_nodes_v2",
		Columns: cols("meta", "node_type", "node_flavor"),
		Where:   QueryWhereClause{Binary: BinaryWhereClause{"cloud_account_id": {Eq: "00000000-0000-0000-0000-0000000000a1"}, "is_active": {Eq: true}}},
	}, td)
	require.NoError(t, err)
	assert.NotContains(t, withoutPodCount, "k8s_pods", "pods are counted for a request that does not read pod_count")

	sortedByPodCount, err := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
		Table:   "k8s_nodes_v2",
		Columns: cols("name"),
		Where:   QueryWhereClause{Binary: BinaryWhereClause{"pod_count": {Gt: 5}}},
		OrderBy: []QueryOrderBy{{Column: "pod_count", Order: "desc"}},
	}, td)
	require.NoError(t, err)
	assert.Contains(t, sortedByPodCount, "k8s_pods", "filtering or sorting by pod_count needs the count")
}

// TestK8sNodesPodCount_DB runs the node list SQL against a real Postgres:
// each node gets the number of active pods on it in its own account and
// tenant, and the pods are read once for the page, restricted to the account,
// rather than once per node.
//
// Builds its own tables in a throwaway schema. DB-gated: skips when no
// database is reachable.
func TestK8sNodesPodCount_DB(t *testing.T) {
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

	const schema = "zz_k8s_nodes_pod_count_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE k8s_nodes (
		tenant_id uuid NOT NULL, cloud_account_id uuid NOT NULL, cloud_resource_id uuid NOT NULL,
		name text NOT NULL, is_active boolean NOT NULL, node_creation_time timestamp)`)
	mustExec(`CREATE TABLE k8s_pods (
		tenant_id uuid NOT NULL, cloud_account_id uuid NOT NULL, cloud_resource_id uuid NOT NULL DEFAULT gen_random_uuid(),
		node_name text, is_active boolean)`)

	const (
		tenant      = "00000000-0000-0000-0000-0000000000aa"
		otherTenant = "00000000-0000-0000-0000-0000000000bb"
		account     = "00000000-0000-0000-0000-0000000000a1"
		other       = "00000000-0000-0000-0000-0000000000a2"
		node1       = "00000000-0000-0000-0000-000000000101"
		node1Old    = "00000000-0000-0000-0000-000000000102" // replaced node that had the same name
		node2       = "00000000-0000-0000-0000-000000000103"
		node3       = "00000000-0000-0000-0000-000000000104" // no pods
		otherNode1  = "00000000-0000-0000-0000-000000000201"
	)
	node := func(acct, id, name string, active bool, created string) {
		mustExec(`INSERT INTO k8s_nodes VALUES ($1, $2, $3, $4, $5, $6)`, tenant, acct, id, name, active, created)
	}
	pods := func(tenantID, acct, nodeName string, active any, n int) {
		for range n {
			mustExec(`INSERT INTO k8s_pods (tenant_id, cloud_account_id, node_name, is_active) VALUES ($1, $2, $3, $4)`,
				tenantID, acct, nodeName, active)
		}
	}
	node(account, node1, "node-1", true, "2026-01-04 00:00:00")
	node(account, node1Old, "node-1", false, "2026-01-01 00:00:00")
	node(account, node2, "node-2", true, "2026-01-03 00:00:00")
	node(account, node3, "node-3", true, "2026-01-02 00:00:00")
	node(other, otherNode1, "node-1", true, "2026-01-05 00:00:00")

	pods(tenant, account, "node-1", true, 3)
	pods(tenant, account, "node-1", false, 2)
	pods(tenant, account, "node-2", true, 1)
	pods(tenant, account, "node-2", nil, 1)
	pods(tenant, other, "node-1", true, 4)
	pods(otherTenant, account, "node-1", true, 5)
	pods(tenant, account, "", true, 1)
	mustExec(`ANALYZE k8s_nodes`)
	mustExec(`ANALYZE k8s_pods`)

	t.Run("counts active pods per node in its own account and tenant", func(t *testing.T) {
		type row struct {
			Name     string `db:"name"`
			ID       string `db:"cloud_resource_id"`
			IsActive bool   `db:"is_active"`
			PodCount int    `db:"pod_count"`
		}
		var rows []row
		require.NoError(t, db.Select(&rows, nodeListSQL(t, tenant, account, 10)))
		got := map[string]int{}
		for _, r := range rows {
			got[r.ID] = r.PodCount
		}
		assert.Equal(t, map[string]int{
			node1:    3,
			node1Old: 3, // a node row is counted by name, like the subquery it replaces
			node2:    1,
			node3:    0,
		}, got)
	})

	t.Run("pods are read once for the page, for the account only", func(t *testing.T) {
		var raw string
		require.NoError(t, db.Get(&raw, `EXPLAIN (FORMAT JSON) `+nodeListSQL(t, tenant, account, 2)))
		var plans []struct {
			Plan map[string]any `json:"Plan"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &plans))
		require.Len(t, plans, 1)

		scans := 0
		var walk func(node map[string]any, inSubPlan bool)
		walk = func(node map[string]any, inSubPlan bool) {
			if relationship, _ := node["Parent Relationship"].(string); relationship == "SubPlan" {
				inSubPlan = true
			}
			if relation, _ := node["Relation Name"].(string); relation == "k8s_pods" {
				scans++
				assert.False(t, inSubPlan, "k8s_pods is read in a subplan, once per node row")
				var conditions []string
				for _, key := range []string{"Filter", "Index Cond", "Recheck Cond"} {
					if cond, ok := node[key].(string); ok {
						conditions = append(conditions, cond)
					}
				}
				assert.Contains(t, strings.Join(conditions, " "), account,
					"the account filter does not reach the pods; every account's pods would be grouped")
			}
			children, _ := node["Plans"].([]any)
			for _, child := range children {
				walk(child.(map[string]any), inSubPlan)
			}
		}
		walk(plans[0].Plan, false)
		assert.Equal(t, 1, scans)
	})
}
