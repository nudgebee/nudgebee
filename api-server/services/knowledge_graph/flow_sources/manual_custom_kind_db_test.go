package flow_sources_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/security"
)

func declareSwitch(f *depAuthzFixture, name string, admin bool, props map[string]interface{}) (*flow_sources.ManualNode, error) {
	return f.repo.Create(context.Background(), f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, NodeType: "CloudResource", SpecificType: "NetworkSwitch", Name: name,
		Properties: props, AuthorizeNewKind: func() bool { return admin },
	})
}

// A tenant admin introduces a kind; after that anyone may declare it. The kind
// keeps one category, its properties are free but may not be identity or
// endpoint keys, and it lands in the graph as specific_type under its category.
func TestCustomKind_DeclareAndGate(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	if _, err := declareSwitch(f, "sw-core-01", false, nil); !errors.Is(err, flow_sources.ErrManualNodeKindForbidden) {
		t.Fatalf("non-admin introducing a kind: err = %v, want ErrManualNodeKindForbidden", err)
	}
	sw, err := declareSwitch(f, "sw-core-01", true, map[string]interface{}{"site": "blr-1", "vlans": []interface{}{"10", "20"}})
	if err != nil {
		t.Fatalf("admin declares the first switch: %v", err)
	}
	var nodeType, specificType string
	row, err := f.db.QueryRow(`SELECT node_type, specific_type FROM knowledge_graph_node WHERE id = $1 AND is_active`, sw.KGNodeID)
	if err != nil || row.Scan(&nodeType, &specificType) != nil || nodeType != "CloudResource" || specificType != "NetworkSwitch" {
		t.Fatalf("graph node: %s / %s (err %v)", nodeType, specificType, err)
	}
	if _, err := declareSwitch(f, "sw-core-02", false, nil); err != nil {
		t.Fatalf("non-admin declaring an existing kind: %v", err)
	}
	// Named without its category, a known kind takes the one it has.
	if n, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{TenantID: f.tenantID, SpecificType: "NetworkSwitch", Name: "sw-core-03"}); err != nil || n.NodeType != "CloudResource" {
		t.Fatalf("kind without category: %v / %+v", err, n)
	}

	for name, req := range map[string]flow_sources.ManualNodeCreate{
		"second category":   {NodeType: "ComputeInstance", SpecificType: "NetworkSwitch", Name: "sw-x"},
		"endpoint property": {NodeType: "CloudResource", SpecificType: "NetworkSwitch", Name: "sw-y", Properties: map[string]interface{}{"dns_name": "sw-y.corp"}},
		"collector's type":  {NodeType: "Database", SpecificType: "RDSInstance", Name: "db-x"},
		"lower-case kind":   {NodeType: "CloudResource", SpecificType: "networkSwitch", Name: "sw-z"},
	} {
		req.TenantID = f.tenantID
		if _, err := f.repo.Create(ctx, f.kg, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Another kind under the same category and name collides on the key.
	_, err = f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{TenantID: f.tenantID, NodeType: "CloudResource", SpecificType: "NetworkRouter", Name: "sw-core-01"})
	if !errors.Is(err, flow_sources.ErrManualNodeDuplicate) || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("same name, other kind: err = %v, want a duplicate that suggests a namespace", err)
	}

	schema, err := f.repo.Schema(ctx, f.tenantID)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	found := false
	for _, place := range schema {
		for _, k := range place.Kinds {
			if place.CloudProvider == flow_sources.TenantScopedProvider && k.SpecificType == "NetworkSwitch" {
				found = k.NodeType == "CloudResource" && k.FreeProperties
			}
		}
	}
	if !found {
		t.Error("schema does not offer NetworkSwitch as a free-property kind outside any account")
	}
}

const customKindDocument = `{
  "nodes": [
    {"ref": "a", "node_type": "CloudResource", "specific_type": "NetworkSwitch", "name": "sw-a", "properties": {"site": "blr-1"}},
    {"ref": "b", "specific_type": "NetworkSwitch", "name": "sw-b"}
  ],
  "dependencies": [
    {"source": "a", "dest": "b", "relationship_type": "CONNECTED_TO"},
    {"source": "b", "dest": {"node_type": "MessageQueue", "name": "orders"}, "relationship_type": "CONNECTED_TO"},
    {"source": {"node_type": "NetworkSwitch", "name": "sw-a"}, "dest": {"node_type": "MessageQueue", "name": "payments"}, "relationship_type": "DEPENDS_ON"}
  ]}`

// One import declares the kind's nodes and links them to each other and to
// collected nodes; a link may name its end by kind. The link rules then hold
// at write time, and a build emits the edges and retires the ones no
// declaration backs any more.
func TestCustomKind_ImportLinkRulesAndBuild(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	res := importDeclarations(t, f, f.deps, nil, customKindDocument)
	if nodeCounts(res) != [3]int{2, 0, 0} || depCounts(res) != [3]int{3, 0, 0} {
		t.Fatalf("got nodes %v deps %v: %+v / %+v", nodeCounts(res), depCounts(res), res.Nodes.Rejected, res.Dependencies.Rejected)
	}
	swA, swB := res.Nodes.Created[0].NodeID, res.Nodes.Created[1].NodeID
	for _, d := range res.Dependencies.Created {
		if d.Status != flow_sources.ManualResolutionResolved {
			t.Errorf("dependency %d: %s (%s)", d.Index, d.Status, d.Dependency.ResolutionError)
		}
	}
	byKind := res.Dependencies.Created[2].Dependency
	if byKind.ResolvedSourceNodeID != swA || byKind.SourceNodeType != "CloudResource" || byKind.SourceAccountID != f.tenantID {
		t.Errorf("an end named by kind is stored as its category and the tenant: %+v", byKind)
	}

	// Two collected nodes: CALLS as before, a declared-only type refused.
	if _, err := f.deps.Create(ctx, flow_sources.ManualDependency{TenantID: f.tenantID, RelationshipType: "CONNECTED_TO",
		SourceNodeType: "MessageQueue", SourceName: "orders", DestNodeType: "MessageQueue", DestName: "payments"}); !errors.Is(err, flow_sources.ErrManualDependencyNeedsDeclaredNode) {
		t.Errorf("CONNECTED_TO between collected nodes: err = %v", err)
	}
	if _, err := f.deps.Create(ctx, flow_sources.ManualDependency{TenantID: f.tenantID, RelationshipType: "CALLS",
		SourceNodeType: "MessageQueue", SourceName: "orders", DestNodeType: "MessageQueue", DestName: "payments"}); err != nil {
		t.Errorf("CALLS between collected nodes: %v", err)
	}
	// A pair already linked by a declared-only type takes no second link.
	if _, err := f.deps.Create(ctx, flow_sources.ManualDependency{TenantID: f.tenantID, RelationshipType: "CALLS",
		SourceNodeType: "NetworkSwitch", SourceName: "sw-a", DestNodeType: "NetworkSwitch", DestName: "sw-b"}); !errors.Is(err, flow_sources.ErrManualDependencyPairLinked) {
		t.Errorf("second link on a CONNECTED_TO pair: err = %v", err)
	}

	edges := runManualBuild(t, f)
	if !hasEdge(edges, swA, swB, "CONNECTED_TO") {
		t.Fatalf("build did not emit sw-a CONNECTED_TO sw-b: %d edges", len(edges))
	}
	if err := f.kg.SaveEdges(edges, loadTenantNodes(t, f), 1); err != nil {
		t.Fatalf("save edges: %v", err)
	}
	ordersEdge := res.Dependencies.Created[1].Dependency
	if !edgeActive(t, f, ordersEdge.ResolvedSourceNodeID, ordersEdge.ResolvedDestNodeID, "CONNECTED_TO") {
		t.Fatal("sw-b CONNECTED_TO orders is not in the graph after the build")
	}

	// CONNECTED_TO is undirected: a downstream walk from orders reaches sw-b
	// across sw-b → orders, with the edge in the result, and the tenant and
	// relationship filters after the extra query parameter still apply.
	walk, err := f.kg.TraverseDirectional(f.tenantID, core.TraverseParams{TenantID: f.tenantID,
		NodeIDs: []string{ordersEdge.ResolvedDestNodeID}, Direction: core.TraverseDirectionDownstream,
		MaxDepth: 1, RelationshipTypes: []string{"CONNECTED_TO"}})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	reached, walked := false, false
	for _, n := range walk.Graph.Nodes {
		reached = reached || n.ID == swB
	}
	for _, e := range walk.Graph.Edges {
		walked = walked || (e.SourceNodeID == swB && e.DestinationNodeID == ordersEdge.ResolvedDestNodeID)
	}
	if !reached || !walked {
		t.Errorf("downstream from orders: reached sw-b %v, edge returned %v", reached, walked)
	}
	// A directed type is still one-way.
	walk, err = f.kg.TraverseDirectional(f.tenantID, core.TraverseParams{TenantID: f.tenantID,
		NodeIDs: []string{byKind.ResolvedDestNodeID}, Direction: core.TraverseDirectionDownstream,
		MaxDepth: 1, RelationshipTypes: []string{"DEPENDS_ON"}})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	for _, n := range walk.Graph.Nodes {
		if n.ID == swA {
			t.Error("downstream from payments walked sw-a DEPENDS_ON payments backwards")
		}
	}

	// Re-point that link; the next build retires the old edge.
	if _, err := f.deps.Update(ctx, f.tenantID, ordersEdge.ID, flow_sources.ManualDependency{RelationshipType: "CONNECTED_TO",
		SourceNodeType: "NetworkSwitch", SourceName: "sw-b", DestNodeType: "MessageQueue", DestName: "payments"}); err != nil {
		t.Fatalf("re-point: %v", err)
	}
	runManualBuild(t, f)
	if edgeActive(t, f, ordersEdge.ResolvedSourceNodeID, ordersEdge.ResolvedDestNodeID, "CONNECTED_TO") {
		t.Error("the re-pointed link's old edge is still active")
	}
	if !edgeActive(t, f, swA, swB, "CONNECTED_TO") {
		t.Error("an untouched link's edge was retired")
	}
}

// Two servers cabled to one switch. A failing switch impacts both servers; a
// failing server impacts neither the switch nor the other server — the walk
// must not run server A → switch → server B just because they share a switch.
func TestCustomKind_ConnectedToBlastRadius(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	server := func(name string) *core.DbNode {
		n := core.NewNode(core.NodeTypeComputeInstance, "aws:"+f.account+":us-east-1:ComputeInstance::"+name,
			map[string]interface{}{"name": name, "specific_type": "EC2Instance"}, f.tenantID, f.account, "aws")
		n.Level = "Tenant"
		if err := f.kg.SaveNodes([]*core.DbNode{n}, 1); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
		return n
	}
	a, b := server("server-a"), server("server-b")
	swNode, err := declareSwitch(f, "sw-rack-1", true, nil)
	if err != nil {
		t.Fatalf("declare switch: %v", err)
	}
	for _, s := range []*core.DbNode{a, b} {
		// Drawn both ways round on purpose: the result must not depend on it.
		src, dst := flow_sources.ManualEndpoint{NodeType: "NetworkSwitch", Name: "sw-rack-1"}, flow_sources.ManualEndpoint{NodeType: "ComputeInstance", Name: s.Properties["name"].(string), AccountID: f.account}
		if s == b {
			src, dst = dst, src
		}
		d, err := f.deps.Create(ctx, flow_sources.ManualDependency{TenantID: f.tenantID, RelationshipType: "CONNECTED_TO",
			SourceNodeType: src.NodeType, SourceName: src.Name, SourceAccountID: src.AccountID,
			DestNodeType: dst.NodeType, DestName: dst.Name, DestAccountID: dst.AccountID})
		if err != nil || d.ResolutionStatus != flow_sources.ManualResolutionResolved {
			t.Fatalf("link switch and %s: %v / %+v", s.Properties["name"], err, d)
		}
	}
	if err := f.kg.SaveEdges(runManualBuild(t, f), loadTenantNodes(t, f), 1); err != nil {
		t.Fatalf("save edges: %v", err)
	}

	impacted := func(seed string) map[string]bool {
		s, err := f.kg.GetImpactedServices(f.tenantID, seed, nil, 0)
		if err != nil {
			t.Fatalf("impact of %s: %v", seed, err)
		}
		out := map[string]bool{}
		for _, list := range [][]core.ImpactedService{s.Dependents, s.InfrastructureDependents, s.HostedWorkloads} {
			for _, d := range list {
				out[d.NodeID] = true
			}
		}
		return out
	}
	// One hop of CONNECTED_TO: server-a sees its switch, not the switch's
	// other servers.
	if got := impacted(a.ID); got[b.ID] || !got[swNode.KGNodeID] {
		t.Errorf("server-a's blast radius: want the switch and not server-b, got %v", got)
	}
	if got := impacted(swNode.KGNodeID); !got[a.ID] || !got[b.ID] {
		t.Errorf("the switch's blast radius must list both servers: %v", got)
	}
}

func runManualBuild(t *testing.T, f *depAuthzFixture) []*core.DbEdge {
	t.Helper()
	ctx := security.NewRequestContextForTenantAdmin(f.tenantID, nil, nil, nil)
	edges, _, err := flow_sources.NewManualFlowSource(nil).BuildFlowRelationships(ctx, &core.FlowSourceBuildRequest{
		TenantID: f.tenantID, ExistingNodes: loadTenantNodes(t, f),
	})
	if err != nil {
		t.Fatalf("manual build: %v", err)
	}
	return edges
}

func loadTenantNodes(t *testing.T, f *depAuthzFixture) []*core.DbNode {
	t.Helper()
	rows, err := f.db.Query(`SELECT id::text, cloud_account_id::text, COALESCE(source, ''), node_type FROM knowledge_graph_node
		WHERE tenant_id = $1 AND is_active AND level = 'Tenant'`, f.tenantID)
	if err != nil {
		t.Fatalf("load nodes: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*core.DbNode
	for rows.Next() {
		n := &core.DbNode{TenantID: f.tenantID, Level: "Tenant"}
		var nodeType string
		if err := rows.Scan(&n.ID, &n.CloudAccountID, &n.Source, &nodeType); err != nil {
			t.Fatalf("scan node: %v", err)
		}
		n.NodeType = core.NodeType(nodeType)
		out = append(out, n)
	}
	return out
}

func hasEdge(edges []*core.DbEdge, src, dst, rt string) bool {
	for _, e := range edges {
		if e.SourceNodeID == src && e.DestinationNodeID == dst && string(e.RelationshipType) == rt {
			return true
		}
	}
	return false
}

func edgeActive(t *testing.T, f *depAuthzFixture, src, dst, rt string) bool {
	t.Helper()
	row, err := f.db.QueryRow(`SELECT COALESCE(bool_or(is_active), false) FROM knowledge_graph_edge
		WHERE tenant_id = $1 AND source_node_id = $2 AND destination_node_id = $3 AND relationship_type = $4`, f.tenantID, src, dst, rt)
	if err != nil {
		t.Fatalf("query edge: %v", err)
	}
	var active bool
	if err := row.Scan(&active); err != nil {
		t.Fatalf("scan edge: %v", err)
	}
	return active
}
