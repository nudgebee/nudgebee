package flow_sources_test

import (
	"context"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/flow_sources"
)

// importDeclarations parses and imports doc as a caller with the given
// repository (and so its authorizer).
func importDeclarations(t *testing.T, f *depAuthzFixture, deps *flow_sources.ManualDependencyRepository, authorize flow_sources.ManualAccountAuthorizer, doc string) *flow_sources.ManualDeclarationsResult {
	t.Helper()
	parsed, err := flow_sources.ParseManualDeclarations([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := flow_sources.NewManualDeclarationsImporter(f.repo, deps, f.kg).Import(context.Background(), f.tenantID, "", authorize, parsed)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res
}

func nodeCounts(r *flow_sources.ManualDeclarationsResult) [3]int {
	return [3]int{len(r.Nodes.Created), len(r.Nodes.Existing), len(r.Nodes.Rejected)}
}

func depCounts(r *flow_sources.ManualDeclarationsResult) [3]int {
	return [3]int{len(r.Dependencies.Created), len(r.Dependencies.Existing), len(r.Dependencies.Rejected)}
}

const refDocument = `{
  "nodes": [
    {"ref": "cache", "node_type": "Cache", "name": "ledger-cache"},
    {"cloud_account": "123456789012", "specific_type": "SQSQueue", "name": "refunds", "region": "us-east-1"}
  ],
  "dependencies": [
    {"source": "cache", "dest": "refunds"},
    {"source": {"node_type": "MessageQueue", "name": "orders"}, "dest": "refunds", "relationship_type": "PUBLISHES_TO"}
  ]}`

// Nodes land first, and a dependency side that refs one is linked to that
// exact node, whether the other side is a ref or resolves on its own.
func TestManualDeclarations_NodesThenDependenciesByRef(t *testing.T) {
	f := newDepAuthzFixture(t)
	res := importDeclarations(t, f, f.deps, nil, refDocument)
	if nodeCounts(res) != [3]int{2, 0, 0} || depCounts(res) != [3]int{2, 0, 0} {
		t.Fatalf("got nodes %v deps %v: %+v / %+v", nodeCounts(res), depCounts(res), res.Nodes.Rejected, res.Dependencies.Rejected)
	}
	cache, refunds := res.Nodes.Created[0], res.Nodes.Created[1]
	if cache.Ref != "cache" || refunds.Ref != "refunds" || cache.NodeID == "" || refunds.NodeID == "" || cache.Node == nil {
		t.Fatalf("created nodes: %+v / %+v", cache, refunds)
	}
	if _, active, found := f.graphNode(t, refunds.NodeID); !found || !active {
		t.Fatalf("declared node %s is not in the graph", refunds.NodeID)
	}

	both := res.Dependencies.Created[0].Dependency
	if both.ResolutionStatus != flow_sources.ManualResolutionResolved ||
		both.ResolvedSourceNodeID != cache.NodeID || both.ResolvedDestNodeID != refunds.NodeID {
		t.Errorf("ref -> ref: %s, %s -> %s", both.ResolutionStatus, both.ResolvedSourceNodeID, both.ResolvedDestNodeID)
	}
	// The stored text names the node the way the resolver would find it.
	if both.DestNodeType != "MessageQueue" || both.DestName != "refunds" || both.DestAccountID != f.account || both.SourceAccountID != "" {
		t.Errorf("stored endpoints: %+v", both)
	}
	orders := f.collect(t, "SQSQueue", "orders")
	mixed := res.Dependencies.Created[1].Dependency
	if mixed.ResolutionStatus != flow_sources.ManualResolutionResolved ||
		mixed.ResolvedSourceNodeID != orders.ID || mixed.ResolvedDestNodeID != refunds.NodeID {
		t.Errorf("object -> ref: %s, %s -> %s", mixed.ResolutionStatus, mixed.ResolvedSourceNodeID, mixed.ResolvedDestNodeID)
	}
}

// Importing a document again writes nothing new: its nodes and dependencies
// come back as existing, with the same ids, and the nodes still bind a
// dependency that refs them.
func TestManualDeclarations_ReimportIsTolerant(t *testing.T) {
	f := newDepAuthzFixture(t)
	first := importDeclarations(t, f, f.deps, nil, refDocument)
	again := importDeclarations(t, f, f.deps, nil, refDocument)
	if nodeCounts(again) != [3]int{0, 2, 0} || depCounts(again) != [3]int{0, 2, 0} {
		t.Fatalf("re-import: nodes %v deps %v", nodeCounts(again), depCounts(again))
	}
	for i, n := range again.Nodes.Existing {
		if n.NodeID != first.Nodes.Created[i].NodeID || n.ID != first.Nodes.Created[i].ID || n.Node == nil {
			t.Errorf("existing[%d] = %+v, want the declaration made the first time", i, n)
		}
	}
	for _, d := range again.Dependencies.Existing {
		if !strings.Contains(d.Error, flow_sources.ErrManualDependencyDuplicate.Error()) {
			t.Errorf("re-imported dependency: %q, want the already-declared answer", d.Error)
		}
	}
	if n := f.activeDeps(t); n != 2 {
		t.Errorf("re-import left %d dependencies, want the 2 from the first import", n)
	}
	if rows, _ := f.repo.List(context.Background(), f.tenantID); len(rows) != 3 { // the fixture's ledger-db + the two above
		t.Errorf("re-import left %d declarations, want 3", len(rows))
	}

	more := importDeclarations(t, f, f.deps, nil, `{
	  "nodes": [{"ref": "cache", "node_type": "Cache", "name": "ledger-cache"}],
	  "dependencies": [{"source": {"node_type": "MessageQueue", "name": "payments"}, "dest": "cache"}]}`)
	if nodeCounts(more) != [3]int{0, 1, 0} || depCounts(more) != [3]int{1, 0, 0} {
		t.Fatalf("new dependency on an existing node: nodes %v deps %v %+v", nodeCounts(more), depCounts(more), more.Dependencies.Rejected)
	}
	if got := more.Dependencies.Created[0].Dependency.ResolvedDestNodeID; got != first.Nodes.Created[0].NodeID {
		t.Errorf("dependency bound to %s, want the existing node %s", got, first.Nodes.Created[0].NodeID)
	}
}

// A node the collector already provides is reported as existing, and a
// dependency that refs it links to the collected node.
func TestManualDeclarations_CollectedNodeStillBinds(t *testing.T) {
	f := newDepAuthzFixture(t)
	orders := f.collect(t, "SQSQueue", "orders")
	res := importDeclarations(t, f, f.deps, nil, `{
	  "nodes": [{"cloud_account": "123456789012", "specific_type": "SQSQueue", "name": "orders", "region": "us-east-1"}],
	  "dependencies": [{"source": {"node_type": "MessageQueue", "name": "payments"}, "dest": "orders"}]}`)
	if nodeCounts(res) != [3]int{0, 1, 0} || depCounts(res) != [3]int{1, 0, 0} {
		t.Fatalf("nodes %v deps %v: %+v", nodeCounts(res), depCounts(res), res.Dependencies.Rejected)
	}
	existing := res.Nodes.Existing[0]
	if existing.NodeID != orders.ID || existing.ID != 0 || !strings.Contains(existing.Error, "already in the graph") {
		t.Errorf("existing = %+v, want the collected node %s", existing, orders.ID)
	}
	if got := res.Dependencies.Created[0].Dependency; got.ResolvedDestNodeID != orders.ID || got.ResolutionStatus != flow_sources.ManualResolutionResolved {
		t.Errorf("dependency: %s -> %s", got.ResolutionStatus, got.ResolvedDestNodeID)
	}
	if rows, _ := f.repo.List(context.Background(), f.tenantID); len(rows) != 1 { // only the fixture's ledger-db
		t.Errorf("a collected resource was declared anyway: %d declarations", len(rows))
	}
}

// An account admin's document lands only what they may write: a refused node
// is rejected without saying whether it is collected, and so is every
// dependency that would reach it.
func TestManualDeclarations_RefusesWhatTheCallerMayNotWrite(t *testing.T) {
	f := newDepAuthzFixture(t)
	onlyA := func(accountID string) bool { return accountID == f.account }
	depsBefore, nodesBefore := f.activeDeps(t), 1 // the fixture's ledger-db

	res := importDeclarations(t, f, f.asAdminA, onlyA, `{
	  "nodes": [
	    {"ref": "b-new", "cloud_account": "210987654321", "specific_type": "SQSQueue", "name": "audit", "region": "us-east-1"},
	    {"ref": "b-collected", "cloud_account": "210987654321", "specific_type": "SQSQueue", "name": "billing", "region": "us-east-1"},
	    {"ref": "onprem", "node_type": "Database", "name": "warehouse-db"},
	    {"ref": "mine", "cloud_account": "123456789012", "specific_type": "SQSQueue", "name": "refunds", "region": "us-east-1"}
	  ],
	  "dependencies": [
	    {"source": "mine", "dest": "b-new"},
	    {"source": "mine", "dest": "onprem"},
	    {"source": "mine", "dest": {"node_type": "MessageQueue", "name": "billing"}},
	    {"source": "mine", "dest": {"node_type": "MessageQueue", "name": "payments"}}
	  ]}`)
	if nodeCounts(res) != [3]int{1, 0, 3} || depCounts(res) != [3]int{1, 0, 3} {
		t.Fatalf("nodes %v deps %v: %+v / %+v", nodeCounts(res), depCounts(res), res.Nodes, res.Dependencies)
	}
	for _, n := range res.Nodes.Rejected {
		if n.Error != flow_sources.ErrManualNodeForbidden.Error() || n.NodeID != "" || n.Node != nil {
			t.Errorf("refused node %q leaked or mis-reported: %+v", n.Ref, n)
		}
	}
	rejected := res.Dependencies.Rejected
	if !strings.Contains(rejected[0].Error, `node "b-new" was not declared`) || !strings.Contains(rejected[1].Error, `node "onprem" was not declared`) {
		t.Errorf("dependencies on refused nodes: %q / %q", rejected[0].Error, rejected[1].Error)
	}
	if rejected[2].Error != flow_sources.ErrManualDependencyForbidden.Error() {
		t.Errorf("dependency into another account: %q", rejected[2].Error)
	}
	if got := res.Dependencies.Created[0].Dependency; got.DestName != "payments" || got.ResolutionStatus != flow_sources.ManualResolutionResolved {
		t.Errorf("the allowed dependency: %+v", got)
	}
	if after := f.activeDeps(t); after != depsBefore+1 {
		t.Errorf("dependencies %d -> %d, want exactly one more", depsBefore, after)
	}
	if rows, _ := f.repo.List(context.Background(), f.tenantID); len(rows) != nodesBefore+1 {
		t.Errorf("declarations %d -> %d, want exactly one more", nodesBefore, len(rows))
	}
}

// A ref'd node outside any account is linked by id, so a collected node with
// the same type and name does not make the dependency ambiguous.
func TestManualDeclarations_RefIsExactDespiteLookAlike(t *testing.T) {
	f := newDepAuthzFixture(t)
	lookAlike := f.collect(t, "SQSQueue", "events")
	res := importDeclarations(t, f, f.deps, nil, `{
	  "nodes": [{"ref": "ext", "node_type": "MessageQueue", "name": "events"}],
	  "dependencies": [{"source": {"node_type": "MessageQueue", "name": "orders"}, "dest": "ext"}]}`)
	if nodeCounts(res) != [3]int{1, 0, 0} || depCounts(res) != [3]int{1, 0, 0} {
		t.Fatalf("nodes %v deps %v: %+v / %+v", nodeCounts(res), depCounts(res), res.Nodes.Rejected, res.Dependencies.Rejected)
	}
	declared := res.Nodes.Created[0].NodeID
	got := res.Dependencies.Created[0].Dependency
	if got.ResolutionStatus != flow_sources.ManualResolutionResolved || got.ResolvedDestNodeID != declared || declared == lookAlike.ID {
		t.Errorf("dependency %s -> %s, want the declared node %s (not the collected %s)", got.ResolutionStatus, got.ResolvedDestNodeID, declared, lookAlike.ID)
	}
}

// Two nodes of a document with the same type and name in different accounts
// are different nodes, so a dependency on each is declared, not refused as a
// duplicate of the other.
func TestManualDeclarations_SameNameInTwoAccounts(t *testing.T) {
	f := newDepAuthzFixture(t)
	res := importDeclarations(t, f, f.deps, nil, `{
	  "nodes": [
	    {"ref": "cache", "node_type": "Cache", "name": "jobs-cache"},
	    {"ref": "jobs-a", "cloud_account": "123456789012", "specific_type": "SQSQueue", "name": "jobs", "region": "us-east-1"},
	    {"ref": "jobs-b", "cloud_account": "210987654321", "specific_type": "SQSQueue", "name": "jobs", "region": "us-east-1"}
	  ],
	  "dependencies": [
	    {"source": "cache", "dest": "jobs-a"},
	    {"source": "cache", "dest": "jobs-b"}
	  ]}`)
	if nodeCounts(res) != [3]int{3, 0, 0} || depCounts(res) != [3]int{2, 0, 0} {
		t.Fatalf("nodes %v deps %v: %+v / %+v", nodeCounts(res), depCounts(res), res.Nodes.Rejected, res.Dependencies)
	}
	a, b := res.Dependencies.Created[0].Dependency, res.Dependencies.Created[1].Dependency
	if a.ResolvedDestNodeID != res.Nodes.Created[1].NodeID || b.ResolvedDestNodeID != res.Nodes.Created[2].NodeID {
		t.Errorf("dependencies pinned to %s / %s, want %s / %s", a.ResolvedDestNodeID, b.ResolvedDestNodeID,
			res.Nodes.Created[1].NodeID, res.Nodes.Created[2].NodeID)
	}
}

// A node outside any account keeps its namespace in properties only, and an
// endpoint that names that namespace still finds it, so a dependency whose
// pin is dropped re-resolves from its stored text.
func TestManualDeclarations_NamespacedNodeOutsideAccountsResolves(t *testing.T) {
	f := newDepAuthzFixture(t)
	res := importDeclarations(t, f, f.deps, nil, `{
	  "nodes": [{"ref": "db", "node_type": "Database", "name": "billing-db", "namespace": "finance"}],
	  "dependencies": [{"source": {"node_type": "MessageQueue", "name": "orders"}, "dest": "db"}]}`)
	if nodeCounts(res) != [3]int{1, 0, 0} || depCounts(res) != [3]int{1, 0, 0} {
		t.Fatalf("nodes %v deps %v: %+v / %+v", nodeCounts(res), depCounts(res), res.Nodes.Rejected, res.Dependencies.Rejected)
	}
	dep := res.Dependencies.Created[0].Dependency
	if dep.DestNamespace != "finance" {
		t.Fatalf("stored dest namespace %q, want finance", dep.DestNamespace)
	}
	got, err := flow_sources.NewManualEndpointResolver(f.db, nil).Resolve(context.Background(), f.tenantID,
		flow_sources.ManualEndpoint{NodeType: dep.DestNodeType, Name: dep.DestName, Namespace: dep.DestNamespace})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Status != flow_sources.EndpointStatusResolved || got.NodeID != res.Nodes.Created[0].NodeID {
		t.Errorf("re-resolved from text: %s %s, want node %s", got.Status, got.NodeID, res.Nodes.Created[0].NodeID)
	}
}
