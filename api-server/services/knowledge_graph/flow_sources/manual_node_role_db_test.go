package flow_sources_test

import (
	"context"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
)

// A role given when a node is declared is on the node from its first write,
// shows on the declaration, and stays when a collector adopts the node.
func TestManualNodeRole_DeclaredWithRoleKeepsItThroughAdoption(t *testing.T) {
	f := newManualNodeFixture(t)
	t.Cleanup(func() { _, _ = f.db.Exec(`DELETE FROM kg_node_roles WHERE tenant_id = $1`, f.tenantID) })
	ctx := context.Background()

	n, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: f.account, SpecificType: "SQSQueue", Name: "orders", Region: "us-east-1",
		Role: core.NodeRoleMessageQueue, Engine: "SQS",
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if n.Role != core.NodeRoleMessageQueue || n.Engine != "sqs" {
		t.Fatalf("declaration must show its role: %q/%q", n.Role, n.Engine)
	}
	if role := nodeRoleProp(t, f, n.KGNodeID); role != core.NodeRoleMessageQueue {
		t.Fatalf("node role after declare = %q", role)
	}
	list, err := f.repo.List(ctx, f.tenantID)
	if err != nil || len(list) != 1 || list[0].Role != core.NodeRoleMessageQueue {
		t.Fatalf("List must carry the role: %v %+v", err, list)
	}

	// The collector now writes the same resource: same id, its own properties.
	f.collect(t, "SQSQueue", "orders")
	if role := nodeRoleProp(t, f, n.KGNodeID); role != core.NodeRoleMessageQueue {
		t.Fatalf("collector write dropped the role: %q", role)
	}
	if _, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{}); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if role := nodeRoleProp(t, f, n.KGNodeID); role != core.NodeRoleMessageQueue {
		t.Fatalf("adoption dropped the role: %q", role)
	}
}

// Merging a declaration into the collected node it duplicates hands over the
// role, visibly at once: the collected node shows it before any build.
func TestManualNodeRole_MergeShowsRoleOnCollectedNodeAtOnce(t *testing.T) {
	f := newManualNodeFixture(t)
	t.Cleanup(func() { _, _ = f.db.Exec(`DELETE FROM kg_node_roles WHERE tenant_id = $1`, f.tenantID) })
	ctx := context.Background()

	n, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, NodeType: "MessageQueue", Name: "invoices", Role: core.NodeRoleMessageQueue,
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	collected := f.collect(t, "SQSQueue", "invoices")
	if _, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{}); err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if got, _ := f.repo.Get(ctx, f.tenantID, n.ID); got.DuplicateNodeID != collected.ID {
		t.Fatalf("expected a possible duplicate of %s, got %+v", collected.ID, got)
	}
	if role := nodeRoleProp(t, f, collected.ID); role != "" {
		t.Fatalf("collected node has a role before the merge: %q", role)
	}

	if _, _, err := f.repo.Merge(ctx, f.tenantID, n.ID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if role := nodeRoleProp(t, f, collected.ID); role != core.NodeRoleMessageQueue {
		t.Fatalf("merged role not on the collected node: %q", role)
	}
	var rows int
	if err := f.db.Db.Get(&rows, `SELECT count(*) FROM kg_node_roles WHERE tenant_id = $1 AND kg_node_id = $2`, f.tenantID, n.KGNodeID); err != nil || rows != 0 {
		t.Errorf("the declared node's row must move, not stay: rows=%d err=%v", rows, err)
	}
}

func TestManualNodeRole_RefusesBadRoleBeforeWriting(t *testing.T) {
	f := newManualNodeFixture(t)
	_, err := f.repo.Create(context.Background(), f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, NodeType: "Database", Name: "billing-db", Role: "db",
	})
	if err == nil {
		t.Fatal("an unknown role must be refused")
	}
	var n int
	if err := f.db.Db.Get(&n, `SELECT count(*) FROM kg_manual_nodes WHERE tenant_id = $1`, f.tenantID); err != nil || n != 0 {
		t.Fatalf("nothing may be written for a refused declaration: count=%d err=%v", n, err)
	}
}

func nodeRoleProp(t *testing.T, f *manualNodeFixture, id string) string {
	t.Helper()
	var role string
	if err := f.db.Db.Get(&role, `SELECT COALESCE(properties->>'role', '') FROM knowledge_graph_node WHERE tenant_id = $1 AND id = $2`, f.tenantID, id); err != nil {
		t.Fatalf("read node role: %v", err)
	}
	return role
}
