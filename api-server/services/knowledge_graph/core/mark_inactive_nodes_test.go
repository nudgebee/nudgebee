package core

// DB-gated test for the markInactiveNodes account-scoping fix (#35519): a stale
// tenant-scoped node (ownership-sourced, cloud_account_id = tenant ID) must still
// be tombstoned when the tenant's build is scoped to specific cloud accounts,
// while a stale per-cloud-account node (aws-sourced) outside that scope must
// remain protected — the fix only widens eligibility for known tenant/integration
// sources, never narrows the existing per-account guard. Skips cleanly when no
// Postgres is reachable.

import (
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/internal/testenv"
)

const markInactiveNodesTenant = "b2ca6e00-0000-4000-8000-000000000002"

func TestMarkInactiveNodes_TenantScopedSourceSweptDespiteAccountFilter(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ctx := newTestRequestContext()
	svc := NewService(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_node WHERE tenant_id = $1::uuid`, markInactiveNodesTenant)
	})

	// staleGroup mimics ownership_enricher.go: a NudgebeeGroup node whose
	// cloud_account_id is the tenant ID itself — never a member of any real
	// account_ids filter.
	staleGroupKey := BuildUniqueKey(CloudProviderExternal, markInactiveNodesTenant, "", NodeTypeUserGroup, "", "stale-group")
	staleGroup := NewNode(NodeTypeUserGroup, staleGroupKey,
		map[string]interface{}{"specific_type": "NudgebeeGroup", "name": "stale-group"},
		markInactiveNodesTenant, markInactiveNodesTenant, "ownership")

	// protectedInstance mimics a real per-cloud-account resource whose account
	// simply isn't part of this build's scope — must NOT be tombstoned.
	const outOfScopeAccountID = "b2ca6e00-0000-4000-8000-0000000000aa"
	protectedKey := BuildUniqueKey(CloudProviderAWS, outOfScopeAccountID, "us-east-1", NodeTypeComputeInstance, "", "protected-instance")
	protectedInstance := NewNode(NodeTypeComputeInstance, protectedKey,
		map[string]interface{}{"specific_type": "EC2Instance", "name": "protected-instance"},
		markInactiveNodesTenant, outOfScopeAccountID, "aws")

	const oldSyncVersion = 1
	if err := svc.SaveNodes([]*DbNode{staleGroup, protectedInstance}, oldSyncVersion); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}

	// Build scoped to an account neither node belongs to (in-scope accountIDs is
	// disjoint from both the tenant ID and outOfScopeAccountID), sources=nil so
	// neither the sources-match nor flow-source-exclusion clause applies — isolates
	// the account-scoping predicate under test.
	const inScopeAccountID = "b2ca6e00-0000-4000-8000-0000000000bb"
	const newSyncVersion = 2
	if _, err := svc.markInactiveNodes(ctx, markInactiveNodesTenant, []string{inScopeAccountID}, newSyncVersion, nil, nil); err != nil {
		t.Fatalf("markInactiveNodes: %v", err)
	}

	rows, err := dbm.Query(`SELECT unique_key, is_active FROM knowledge_graph_node WHERE tenant_id = $1::uuid`, markInactiveNodesTenant)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	defer func() { _ = rows.Close() }()

	active := make(map[string]bool)
	for rows.Next() {
		var uniqueKey string
		var isActive bool
		if err := rows.Scan(&uniqueKey, &isActive); err != nil {
			t.Fatalf("scan: %v", err)
		}
		active[uniqueKey] = isActive
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	if isActive, ok := active[staleGroupKey]; !ok {
		t.Errorf("stale ownership-sourced node was deleted from database (want it to exist with is_active=false)")
	} else if isActive {
		t.Errorf("stale ownership-sourced node was not tombstoned: is_active=true (want false)")
	}
	if isActive, ok := active[protectedKey]; !ok || !isActive {
		t.Errorf("out-of-scope aws-sourced node was incorrectly tombstoned: is_active=%v ok=%v (want true)", isActive, ok)
	}
}
