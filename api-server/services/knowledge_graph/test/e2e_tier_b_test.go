package test

// End-to-end KG test module — Tier B (DB-backed).
//
// Tier B covers the parts Tier A structurally can't: persistence and the read
// APIs / consumers that query the knowledge_graph_node/_edge tables. It is gated
// by testenv.RequireMetastore — it SKIPS cleanly when no migrated Postgres is
// reachable (DATABASE_URL unset), and runs green wherever one is.
//
// B1 (this file's TestE2E_TierB_ReadAPIs) seeds a deterministic graph via
// SaveNodes/SaveEdges and exercises every read method. B2 (tombstoning) and B3
// (full-orchestrator smoke) live in e2e_tier_b_build_test.go.

import (
	"slices"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
)

// Tier B uses valid UUIDs because knowledge_graph_node/_edge.{tenant_id,
// cloud_account_id} are uuid columns. A dedicated synthetic tenant keeps the
// suite isolated and lets cleanup delete everything by tenant_id.
const (
	e2eTierBTenant  = "e2e00000-0000-4000-8000-0000000000b1"
	e2eTierBAccount = "e2e00000-0000-4000-8000-0000000000a1"
	e2eTierBSync    = int64(1)
)

// tierBSetup builds the basic_k8s graph, rebrands it onto the synthetic Tier-B
// UUID tenant/account, and returns the nodes/edges plus a Service bound to the
// live metastore. It also registers cleanup. Skips if no DB.
func tierBSetup(t *testing.T) (*core.Service, *security.RequestContext, []*core.DbNode, []*core.DbEdge) {
	t.Helper()
	dbm := testenv.RequireMetastore(t)

	req := &core.SourceBuildRequest{TenantID: e2eTierBTenant, CloudAccountID: e2eTierBAccount}
	nodes, edges := buildK8sGraph(t, loadK8sWorkloads(t), loadK8sNodes(t), req)
	nodes, edges = mergeGraph(nodes, edges, mergeOpts{})

	// Rebrand onto UUID identifiers for the uuid columns. Node IDs are uuid5s of
	// (unique_key:account:tenant) and remain valid; edges reference those IDs.
	for _, n := range nodes {
		n.CloudAccountID = e2eTierBAccount
		n.TenantID = e2eTierBTenant
	}
	for _, e := range edges {
		e.CloudAccountID = e2eTierBAccount
		e.TenantID = e2eTierBTenant
	}

	reqCtx := createTestRequestContext(e2eTierBTenant)
	svc := core.NewService(reqCtx, e2eDiscardLogger(), dbm)

	t.Cleanup(func() { tierBCleanup(t, dbm, e2eTierBTenant) })
	return svc, reqCtx, nodes, edges
}

// tierBCleanup removes all KG rows for a tenant so reruns start clean.
func tierBCleanup(t *testing.T, dbm *database.DatabaseManager, tenantID string) {
	t.Helper()
	for _, table := range []string{"knowledge_graph_edge", "knowledge_graph_node"} {
		if _, err := dbm.Exec("DELETE FROM "+table+" WHERE tenant_id = $1::uuid", tenantID); err != nil {
			t.Logf("tier-b cleanup: failed to delete from %s: %v", table, err)
		}
	}
}

func TestE2E_TierB_ReadAPIs(t *testing.T) {
	svc, reqCtx, nodes, edges := tierBSetup(t)

	if err := svc.SaveNodes(nodes, e2eTierBSync); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	if err := svc.SaveEdges(edges, nodes, e2eTierBSync); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}

	t.Run("complete_graph", func(t *testing.T) { tierBAssertCompleteGraph(t, svc, reqCtx, nodes, edges) })
	t.Run("complete_graph_filtered_by_type", func(t *testing.T) { tierBAssertTypeFilter(t, svc, reqCtx) })
	t.Run("search_by_type", func(t *testing.T) { tierBAssertSearch(t, svc) })
	t.Run("traverse_downstream", func(t *testing.T) { tierBAssertTraverse(t, svc, nodes) })
	t.Run("get_node_by_id", func(t *testing.T) { tierBAssertGetNode(t, svc, nodes) })
	t.Run("get_edge_by_id", func(t *testing.T) { tierBAssertGetEdge(t, svc, edges) })
	t.Run("filter_options", func(t *testing.T) { tierBAssertFilterOptions(t, svc) })
}

func tierBAssertCompleteGraph(t *testing.T, svc *core.Service, reqCtx *security.RequestContext, nodes []*core.DbNode, edges []*core.DbEdge) {
	g, err := svc.GetCompleteGraphFromDatabaseWithFilters(reqCtx, e2eTierBTenant, nil)
	if err != nil {
		t.Fatalf("GetCompleteGraphFromDatabaseWithFilters: %v", err)
	}
	if len(g.Nodes) != len(nodes) {
		t.Errorf("complete graph: got %d nodes, want %d", len(g.Nodes), len(nodes))
	}
	if len(g.Edges) != len(edges) {
		t.Errorf("complete graph: got %d edges, want %d", len(g.Edges), len(edges))
	}
}

func tierBAssertTypeFilter(t *testing.T, svc *core.Service, reqCtx *security.RequestContext) {
	filters := &core.GraphFilters{NodeTypes: []core.NodeType{core.NodeTypePod}}
	g, err := svc.GetCompleteGraphFromDatabaseWithFilters(reqCtx, e2eTierBTenant, filters)
	if err != nil {
		t.Fatalf("filtered graph: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Error("type filter returned no Pod nodes")
	}
	for _, n := range g.Nodes {
		if n.NodeType != core.NodeTypePod {
			t.Errorf("type filter leaked non-Pod node: %s", n.NodeType)
		}
	}
}

func tierBAssertSearch(t *testing.T, svc *core.Service) {
	resp, err := svc.SearchNodes(e2eTierBTenant, core.SearchNodesParams{
		NodeTypes: []core.NodeType{core.NodeTypeWorkload},
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(resp.Nodes) == 0 {
		t.Error("search returned no Workload nodes")
	}
	for _, n := range resp.Nodes {
		if n.NodeType != core.NodeTypeWorkload {
			t.Errorf("search leaked non-Workload node: %s", n.NodeType)
		}
	}
}

func tierBAssertTraverse(t *testing.T, svc *core.Service, nodes []*core.DbNode) {
	seedID := firstNodeIDOfType(nodes, core.NodeTypePod)
	if seedID == "" {
		t.Skip("no Pod node to seed traversal")
	}
	resp, err := svc.TraverseDirectional(e2eTierBTenant, core.TraverseParams{
		NodeIDs:   []string{seedID},
		Direction: core.TraverseDirectionDownstream,
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("TraverseDirectional: %v", err)
	}
	if resp.TotalDiscovered < 1 {
		t.Error("downstream traversal discovered nothing")
	}
}

func tierBAssertGetNode(t *testing.T, svc *core.Service, nodes []*core.DbNode) {
	if len(nodes) == 0 {
		t.Skip("no nodes seeded")
	}
	id := nodes[0].ID
	got, err := svc.GetNodeByID(id)
	if err != nil {
		t.Fatalf("GetNodeByID: %v", err)
	}
	if got == nil || got.ID != id {
		t.Errorf("GetNodeByID returned %+v, want id %s", got, id)
	}
}

func tierBAssertGetEdge(t *testing.T, svc *core.Service, edges []*core.DbEdge) {
	if len(edges) == 0 {
		t.Skip("no edges seeded")
	}
	id := edges[0].ID
	got, err := svc.GetEdgeByID(id)
	if err != nil {
		t.Fatalf("GetEdgeByID: %v", err)
	}
	if got == nil || got.ID != id {
		t.Errorf("GetEdgeByID returned %+v, want id %s", got, id)
	}
}

func tierBAssertFilterOptions(t *testing.T, svc *core.Service) {
	opts, err := svc.GetFilterOptions(e2eTierBTenant, nil, nil)
	if err != nil {
		t.Fatalf("GetFilterOptions: %v", err)
	}
	if opts == nil {
		t.Fatal("GetFilterOptions returned nil")
	}
	// The concurrent fetch must populate every field correctly (guards against a
	// goroutine writing the wrong variable). The Tier-B graph is a k8s topology, so
	// it has account IDs, node types, and a non-empty unique_key -> id map.
	if len(opts.AccountIDs) == 0 {
		t.Error("GetFilterOptions: expected non-empty AccountIDs")
	}
	if len(opts.NodeTypes) == 0 {
		t.Error("GetFilterOptions: expected non-empty NodeTypes")
	}
	if !slices.Contains(opts.NodeTypes, string(core.NodeTypePod)) {
		t.Errorf("GetFilterOptions: NodeTypes %v missing Pod", opts.NodeTypes)
	}
	if len(opts.NodeIDMap) == 0 {
		t.Error("GetFilterOptions: expected non-empty NodeIDMap")
	}
	if opts.NodeCount != len(opts.NodeIDMap) {
		t.Errorf("GetFilterOptions: NodeCount %d != len(NodeIDMap) %d", opts.NodeCount, len(opts.NodeIDMap))
	}
}

func firstNodeIDOfType(nodes []*core.DbNode, nt core.NodeType) string {
	for _, n := range nodes {
		if n.NodeType == nt {
			return n.ID
		}
	}
	return ""
}
