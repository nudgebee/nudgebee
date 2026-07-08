package test

// End-to-end KG test module — Tier B, freshness (B2) and orchestrator smoke (B3).
//
// B2 verifies the sync-version RE-STAMP mechanism that tombstoning keys off of,
// using only exported methods (SaveNodes) plus raw reads. The actual is_active
// tombstone flip is performed by BuildGraphs' unexported markInactive* sweeps;
// exercising it end to end is B3.
//
// B3 (the full real-BuildGraphs smoke) requires seeding the orchestrator's
// gates — cloud_accounts (status='active') and knowledge_graph_tenant_filters —
// plus a registered mock source. Those schemas are environment-specific, so B3
// is scaffolded and skipped by default with the seeding requirements documented;
// complete it where a migrated Postgres with those tables is available.

import (
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
)

// tierBNodeCount returns the number of knowledge_graph_node rows for the Tier-B
// tenant matching an additional condition.
func tierBNodeCount(t *testing.T, dbm *database.DatabaseManager, cond string) int {
	t.Helper()
	row, err := dbm.QueryRow(
		"SELECT count(*) FROM knowledge_graph_node WHERE tenant_id = $1::uuid AND ("+cond+")",
		e2eTierBTenant,
	)
	if err != nil {
		t.Fatalf("tier-b count query: %v", err)
	}
	var c int
	if err := row.Scan(&c); err != nil {
		t.Fatalf("tier-b count scan: %v", err)
	}
	return c
}

func TestE2E_TierB_SyncVersionRestamp(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	svc, _, nodes, _ := tierBSetup(t)

	total := len(nodes)
	if total < 2 {
		t.Fatalf("need >=2 nodes to test re-stamp, got %d", total)
	}

	// Stamp the whole graph at sync version 1: every node active at v1.
	if err := svc.SaveNodes(nodes, 1); err != nil {
		t.Fatalf("SaveNodes v1: %v", err)
	}
	if got := tierBNodeCount(t, dbm, "last_sync_version = 1 AND is_active = true"); got != total {
		t.Errorf("after v1 save: %d nodes at sync=1/active, want %d", got, total)
	}

	// Re-stamp half the nodes at sync version 2 (simulating the next sync seeing
	// only those resources). Upsert by ID bumps their last_sync_version.
	subset := nodes[:total/2]
	if err := svc.SaveNodes(subset, 2); err != nil {
		t.Fatalf("SaveNodes v2: %v", err)
	}
	if got := tierBNodeCount(t, dbm, "last_sync_version = 2"); got != len(subset) {
		t.Errorf("after v2 re-stamp: %d nodes at sync=2, want %d", got, len(subset))
	}
	// The un-re-stamped nodes stay at v1 — these are exactly the set the
	// markInactive sweep would tombstone as absent from the newer sync.
	if got := tierBNodeCount(t, dbm, "last_sync_version = 1"); got != total-len(subset) {
		t.Errorf("stale nodes: %d at sync=1, want %d", got, total-len(subset))
	}
}

// TestE2E_TierB_OrchestratorSmoke is the B3 scaffold: run the REAL BuildGraphs
// on seeded source data and assert its output equals the Tier-A pipeline output
// for the same input (pinning that Tier A's hand-ordered phases match the
// orchestrator, and covering the account/filter gates + tombstone sweep).
//
// To complete it, seed before calling BuildGraphs:
//   - cloud_accounts: one row with tenant = e2eTierBTenant, status = 'active'
//     (getActiveAccountsForTenant filters on ca.tenant + ca.status = 'active').
//   - knowledge_graph_tenant_filters: a row for the tenant (GetFilterForTenant),
//     or confirm it tolerates absence and runs at sync version 0.
//   - a source implementing core.SourceInterface returning the basic_k8s graph,
//     registered via svc.RegisterSource, then BuildGraphs(SaveToDB:true).
//
// Then read the graph back and diff against mergeGraph(...) output via
// normalizeGraph/diffGraphs.
func TestE2E_TierB_OrchestratorSmoke(t *testing.T) {
	testenv.RequireMetastore(t)
	t.Skip("B3 scaffold: requires seeding cloud_accounts + knowledge_graph_tenant_filters and a mock source — see doc comment")
}
