package core

// A self-hosted VM and its cloud instance are one machine joined by SAME_AS
// (sources/selfhosted). Generic traversal shows the twin but does not expand it,
// so a host's neighbourhood is not doubled and the service map stays within its
// caps. DB-gated: skips cleanly via testenv.RequireMetastore.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
)

const (
	twinLeafTenant  = "b1a5e000-0000-4000-8000-0000000000c1"
	twinLeafAccount = "b1a5e000-0000-4000-8000-0000000000c2"
)

type twinLeafFixture struct {
	svc   *Service
	nodes map[string]*DbNode
}

func newTwinLeafFixture(t *testing.T) *twinLeafFixture {
	t.Helper()
	dbm := testenv.RequireMetastore(t)
	svc := NewService(newTestRequestContext(), slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)
	cleanup := func() {
		for _, tbl := range []string{"knowledge_graph_edge", "knowledge_graph_node"} {
			_, _ = dbm.Exec("DELETE FROM "+tbl+" WHERE tenant_id = $1::uuid", twinLeafTenant)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	return &twinLeafFixture{svc: svc, nodes: map[string]*DbNode{}}
}

func (f *twinLeafFixture) node(name string, nt NodeType) *DbNode {
	key := fmt.Sprintf("selfhosted:%s::%s::%s", twinLeafAccount, nt, name)
	n := NewNode(nt, key, map[string]interface{}{"name": name}, twinLeafTenant, twinLeafAccount, "test")
	f.nodes[name] = n
	return n
}

// save persists the nodes and the edges given as "from REL to" triples.
func (f *twinLeafFixture) save(t *testing.T, edges ...[3]string) {
	t.Helper()
	nodes := make([]*DbNode, 0, len(f.nodes))
	for _, n := range f.nodes {
		nodes = append(nodes, n)
	}
	dbEdges := make([]*DbEdge, 0, len(edges))
	for _, e := range edges {
		edge := NewEdge(f.nodes[e[0]].ID, f.nodes[e[2]].ID, RelationshipType(e[1]), map[string]interface{}{}, twinLeafTenant, twinLeafAccount, "test")
		edge.IsActive = true
		edge.LastSyncVersion = 1
		dbEdges = append(dbEdges, edge)
	}
	if err := f.svc.SaveNodes(nodes, 1); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	if err := f.svc.SaveEdges(dbEdges, nodes, 1); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
}

func (f *twinLeafFixture) discover(t *testing.T, seed string, opts traverseOptions) map[string]int {
	t.Helper()
	opts.TenantID = twinLeafTenant
	_, _, depth, err := f.svc.discoverBFS([]string{f.nodes[seed].ID}, opts)
	if err != nil {
		t.Fatalf("discoverBFS: %v", err)
	}
	byName := map[string]int{}
	for name, n := range f.nodes {
		if d, ok := depth[n.ID]; ok {
			byName[name] = d
		}
	}
	return byName
}

// twinLeafRequestContext is scoped to the fixture tenant: the neighbours API
// refuses a context without one.
func twinLeafRequestContext() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForTenantAdmin(twinLeafTenant), slog.Default(), nil, nil)
}

func sameAs(a, b string) [][3]string {
	return [][3]string{{a, string(RelationshipSameAs), b}, {b, string(RelationshipSameAs), a}}
}

func TestDiscoverBFS_HostTwinIsALeaf(t *testing.T) {
	f := newTwinLeafFixture(t)
	f.node("vm", NodeTypeComputeInstance)
	f.node("ec2-a", NodeTypeComputeInstance)
	f.node("ec2-b", NodeTypeComputeInstance)
	f.node("k8s-node", NodeTypeNode)
	edges := append(sameAs("vm", "ec2-a"), sameAs("vm", "ec2-b")...)
	edges = append(edges, [3]string{"k8s-node", string(RelationshipRunsOn), "ec2-a"})
	f.save(t, edges...)

	t.Run("the twins are shown, their neighbourhoods are not", func(t *testing.T) {
		got := f.discover(t, "vm", traverseOptions{Direction: TraverseDirectionBoth, Levels: 3})
		for _, twin := range []string{"ec2-a", "ec2-b"} {
			if got[twin] != 1 {
				t.Errorf("%s depth = %v, want 1", twin, got[twin])
			}
		}
		if _, ok := got["k8s-node"]; ok {
			t.Errorf("k8s-node was reached through the twin ec2-a; a twin must not be expanded")
		}
	})

	t.Run("a twin's own edges still expand when it is the seed", func(t *testing.T) {
		got := f.discover(t, "ec2-a", traverseOptions{Direction: TraverseDirectionBoth, Levels: 3})
		if got["k8s-node"] != 1 || got["vm"] != 1 {
			t.Errorf("from ec2-a: k8s-node=%v vm=%v, want both at depth 1", got["k8s-node"], got["vm"])
		}
		if _, ok := got["ec2-b"]; ok {
			t.Errorf("ec2-b was reached through the twin vm; a twin must not be expanded")
		}
	})

	t.Run("ExpandSameAsTwins walks the whole twin set", func(t *testing.T) {
		got := f.discover(t, "ec2-a", traverseOptions{
			Direction: TraverseDirectionBoth, Levels: 3, ExpandSameAsTwins: true,
			RelationshipTypes: []string{string(RelationshipSameAs)},
			IncludeNodeTypes:  []NodeType{NodeTypeComputeInstance},
		})
		if got["vm"] != 1 || got["ec2-b"] != 2 {
			t.Errorf("closure from ec2-a: vm=%v ec2-b=%v, want 1 and 2", got["vm"], got["ec2-b"])
		}
		if _, ok := got["k8s-node"]; ok {
			t.Errorf("closure must follow SAME_AS only")
		}
	})
}

// A node reachable at one level both over a real edge and over a twin SAME_AS
// must be expanded, whatever order Postgres returns the two rows in.
func TestDiscoverBFS_RealEdgeBeatsTwinLeafAtTheSameLevel(t *testing.T) {
	f := newTwinLeafFixture(t)
	f.node("seed", NodeTypeComputeInstance)
	f.node("twin", NodeTypeComputeInstance)
	f.node("behind", NodeTypeWorkload)
	edges := append(sameAs("seed", "twin"),
		[3]string{"seed", string(RelationshipCalls), "twin"},
		[3]string{"behind", string(RelationshipRunsOn), "twin"})
	f.save(t, edges...)

	got := f.discover(t, "seed", traverseOptions{Direction: TraverseDirectionBoth, Levels: 2})
	if got["behind"] != 2 {
		t.Errorf("behind depth = %v, want 2: twin is also reached over CALLS, so it must be expanded", got["behind"])
	}
}

// Identity SAME_AS joins user nodes, not hosts, and keeps expanding: a GitHub
// user resolves through its NudgebeeUser to what that person owns.
func TestDiscoverBFS_IdentitySameAsStillExpands(t *testing.T) {
	f := newTwinLeafFixture(t)
	f.node("gh-user", NodeTypeUserAccount)
	f.node("nb-user", NodeTypeUserAccount)
	f.node("owned", NodeTypeComputeInstance)
	f.save(t,
		[3]string{"gh-user", string(RelationshipSameAs), "nb-user"},
		[3]string{"nb-user", string(RelationshipOwns), "owned"})

	got := f.discover(t, "gh-user", traverseOptions{Direction: TraverseDirectionBoth, Levels: 2})
	if got["owned"] != 2 {
		t.Errorf("owned depth = %v, want 2: identity SAME_AS must not be a leaf", got["owned"])
	}
}

// A walk that starts from a host's VM node must reach what runs on the machine,
// not stop at its cloud twin: the twins are seeded together at depth 0, and the
// SAME_AS link between them is still drawn.
func TestGetMultipleNodeNeighbors_WalkFromVMReachesTheMachine(t *testing.T) {
	f := newTwinLeafFixture(t)
	f.node("vm", NodeTypeComputeInstance)
	f.node("ec2-a", NodeTypeComputeInstance)
	f.node("ec2-b", NodeTypeComputeInstance)
	f.node("k8s-node", NodeTypeNode)
	edges := append(sameAs("vm", "ec2-a"), sameAs("vm", "ec2-b")...)
	edges = append(edges, [3]string{"k8s-node", string(RelationshipRunsOn), "ec2-a"})
	f.save(t, edges...)

	for _, subgraph := range []bool{false, true} {
		got, err := f.svc.GetMultipleNodeNeighbors(twinLeafRequestContext(), []string{f.nodes["vm"].ID}, 1, nil, subgraph)
		if err != nil {
			t.Fatalf("subgraph=%v: %v", subgraph, err)
		}
		ids := map[string]bool{}
		for _, n := range got.Nodes {
			ids[n.ID] = true
		}
		for _, want := range []string{"vm", "ec2-a", "ec2-b", "k8s-node"} {
			if !ids[f.nodes[want].ID] {
				t.Errorf("subgraph=%v: %s missing from a level-1 walk started at the VM", subgraph, want)
			}
		}
		var sameAsEdge, runsOn bool
		for _, e := range got.Edges {
			if e.RelationshipType == RelationshipSameAs && e.SourceNodeID == f.nodes["vm"].ID && e.DestinationNodeID == f.nodes["ec2-a"].ID {
				sameAsEdge = true
			}
			if e.RelationshipType == RelationshipRunsOn && e.SourceNodeID == f.nodes["k8s-node"].ID {
				runsOn = true
			}
		}
		if !sameAsEdge || !runsOn {
			t.Errorf("subgraph=%v: SAME_AS vm->ec2-a drawn=%v, RUNS_ON k8s-node->ec2-a drawn=%v; want both", subgraph, sameAsEdge, runsOn)
		}
	}
}

func TestTraverseDirectional_WalkFromVMReachesTheMachine(t *testing.T) {
	f := newTwinLeafFixture(t)
	f.node("vm", NodeTypeComputeInstance)
	f.node("ec2-a", NodeTypeComputeInstance)
	f.node("k8s-node", NodeTypeNode)
	edges := append(sameAs("vm", "ec2-a"), [3]string{"k8s-node", string(RelationshipRunsOn), "ec2-a"})
	f.save(t, edges...)

	got, err := f.svc.TraverseDirectional(twinLeafTenant, TraverseParams{
		NodeIDs:   []string{f.nodes["vm"].ID},
		Direction: TraverseDirectionUpstream,
		MaxDepth:  1,
	})
	if err != nil {
		t.Fatalf("TraverseDirectional: %v", err)
	}
	if len(got.SeedNodeIDs) != 1 || got.SeedNodeIDs[0] != f.nodes["vm"].ID {
		t.Errorf("SeedNodeIDs = %v, want only the requested VM", got.SeedNodeIDs)
	}
	found := false
	for _, n := range got.Graph.Nodes {
		if n.ID == f.nodes["k8s-node"].ID {
			found = true
		}
	}
	if !found {
		t.Error("an upstream walk from the VM did not reach the K8s node running on its EC2 twin")
	}
}
