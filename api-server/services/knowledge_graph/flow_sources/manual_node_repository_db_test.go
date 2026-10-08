package flow_sources_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
	_ "nudgebee/services/knowledge_graph/sources/aws" // registers the AWS manual node builder
	_ "nudgebee/services/knowledge_graph/sources/k8s" // registers the K8s manual node builder
	"nudgebee/services/security"
)

// manualNodeFixture is one tenant with one AWS account, isolated per test.
type manualNodeFixture struct {
	db       *database.DatabaseManager
	repo     *flow_sources.ManualNodeRepository
	kg       *core.Service
	tenantID string
	account  string
}

func newManualNodeFixture(t *testing.T) *manualNodeFixture {
	t.Helper()
	db := testenv.RequireMetastore(t)
	f := &manualNodeFixture{db: db, tenantID: uuid.NewString(), account: uuid.NewString()}
	userID := uuid.NewString()
	mustExec(t, db, `INSERT INTO users (id, username, display_name) VALUES ($1, $2, 'manual node test')`, userID, "mn-"+userID)
	mustExec(t, db, `INSERT INTO tenant (id, name, created_by, updated_by) VALUES ($1, $2, $3, $3)`, f.tenantID, "mn-"+f.tenantID, userID)
	mustExec(t, db, `INSERT INTO cloud_accounts (id, account_name, created_by, updated_by, tenant, account_type, cloud_provider, account_number)
		VALUES ($1, 'manual node test account', $2, $2, $3, 'aws', 'AWS', '123456789012')`, f.account, userID, f.tenantID)

	ctx := security.NewRequestContextForTenantAdmin(f.tenantID, nil, nil, nil)
	f.repo = flow_sources.NewManualNodeRepository(db, nil)
	f.kg = core.NewService(ctx, ctx.GetLogger(), db)
	return f
}

func mustExec(t *testing.T, db *database.DatabaseManager, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// graphNode reads a node's source and liveness.
func (f *manualNodeFixture) graphNode(t *testing.T, id string) (source string, active, found bool) {
	t.Helper()
	row, err := f.db.QueryRow(`SELECT COALESCE(source, ''), is_active FROM knowledge_graph_node WHERE tenant_id = $1 AND id = $2`, f.tenantID, id)
	if err != nil {
		t.Fatalf("query node: %v", err)
	}
	if err := row.Scan(&source, &active); err != nil {
		return "", false, false
	}
	return source, active, true
}

func (f *manualNodeFixture) declareSQS(t *testing.T, name string) *flow_sources.ManualNode {
	t.Helper()
	n, err := f.repo.Create(context.Background(), f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: f.account, SpecificType: "SQSQueue", Name: name, Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("declare %s: %v", name, err)
	}
	return n
}

// The collector writing the same resource is simulated with the builder's own
// output: it is, by TestManualNode_AWSKeyMatchesCollector, the collector's node.
func (f *manualNodeFixture) collect(t *testing.T, specificType, name string) *core.DbNode {
	t.Helper()
	b, _ := core.ManualNodeBuilderFor("AWS")
	node, err := b.Build(core.ManualNodeInput{
		TenantID: f.tenantID, CloudAccountID: f.account, AccountNumber: "123456789012", CloudProvider: "AWS",
		SpecificType: specificType, Name: name, Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("build collector node: %v", err)
	}
	node.Level = "Tenant"
	if err := f.kg.SaveNodes([]*core.DbNode{node}, 1); err != nil {
		t.Fatalf("save collector node: %v", err)
	}
	return node
}

func TestManualNodeLifecycle_DeclareAndAdopt(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()

	n := f.declareSQS(t, "orders")
	if source, active, found := f.graphNode(t, n.KGNodeID); !found || !active || source != core.ManualNodeSource {
		t.Fatalf("declared node must be in the graph at once: found=%v active=%v source=%q", found, active, source)
	}
	if _, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: f.account, SpecificType: "SQSQueue", Name: "orders", Region: "us-east-1",
	}); !errors.Is(err, flow_sources.ErrManualNodeDuplicate) {
		t.Fatalf("second declaration of the same resource: got %v, want ErrManualNodeDuplicate", err)
	}

	// A build with nothing collected re-emits it.
	cycle, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{})
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if len(cycle.Nodes) != 1 || cycle.Nodes[0].ID != n.KGNodeID {
		t.Fatalf("expected the declared node re-emitted, got %d nodes", len(cycle.Nodes))
	}

	// The collector ingests the real queue: same id, so it lands on this node.
	collected := f.collect(t, "SQSQueue", "orders")
	if collected.ID != n.KGNodeID {
		t.Fatalf("collector node id %s differs from the declaration's %s", collected.ID, n.KGNodeID)
	}
	cycle, err = f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{collected.ID: collected})
	if err != nil {
		t.Fatalf("RunCycle after collect: %v", err)
	}
	if cycle.Adopted != 1 || len(cycle.Nodes) != 0 {
		t.Fatalf("expected adoption and no re-emit, got adopted=%d emitted=%d", cycle.Adopted, len(cycle.Nodes))
	}
	got, _ := f.repo.Get(ctx, f.tenantID, n.ID)
	if got.Status != flow_sources.ManualNodeStatusAdopted {
		t.Errorf("status = %s, want adopted", got.Status)
	}
	var rows int
	if err := f.db.Db.Get(&rows, `SELECT count(*) FROM knowledge_graph_node WHERE tenant_id = $1 AND unique_key = $2`, f.tenantID, n.UniqueKey); err != nil || rows != 1 {
		t.Fatalf("expected exactly one node for the resource, got %d (%v)", rows, err)
	}

	// Withdrawing an adopted declaration leaves the collector's node alone.
	if _, err := f.repo.SoftDelete(ctx, f.tenantID, n.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if source, active, _ := f.graphNode(t, n.KGNodeID); !active || source != "aws" {
		t.Errorf("collector node was touched by the delete: active=%v source=%q", active, source)
	}
}

func TestManualNodeLifecycle_RefusesCollectedResource(t *testing.T) {
	f := newManualNodeFixture(t)
	collected := f.collect(t, "SQSQueue", "payments")
	_, err := f.repo.Create(context.Background(), f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: f.account, SpecificType: "SQSQueue", Name: "payments", Region: "us-east-1",
	})
	var exists *flow_sources.ManualNodeExistsError
	if !errors.As(err, &exists) || exists.NodeID != collected.ID {
		t.Fatalf("expected ManualNodeExistsError for %s, got %v", collected.ID, err)
	}
}

// A delete that lands while a build is running is undone by that build's save;
// the next cycle must retire the node again.
func TestManualNodeLifecycle_ReconcilesResurrectedNode(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()
	n := f.declareSQS(t, "audit-log")
	node, _ := n.Materialize()

	if _, err := f.repo.SoftDelete(ctx, f.tenantID, n.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, active, _ := f.graphNode(t, n.KGNodeID); active {
		t.Fatal("delete did not retire the node")
	}
	// The in-flight build saves its earlier view of the node.
	if err := f.kg.SaveNodes([]*core.DbNode{node}, 1); err != nil {
		t.Fatalf("resurrect: %v", err)
	}

	if _, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{}); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if _, active, _ := f.graphNode(t, n.KGNodeID); active {
		t.Fatal("resurrected node with no declaration is still active after the next cycle")
	}
}

func TestManualNodeLifecycle_DuplicateAndMerge(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()

	// Declared outside any account: its key can never match a collector's.
	n, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, NodeType: "MessageQueue", Name: "invoices",
	})
	if err != nil {
		t.Fatalf("declare tenant-scoped: %v", err)
	}
	if source, _, _ := f.graphNode(t, n.KGNodeID); source != core.ManualNodeSource {
		t.Fatalf("tenant-scoped node not written: source=%q", source)
	}

	// A dependency pinned to the declared node.
	var depID int64
	row, err := f.db.QueryRow(`INSERT INTO kg_manual_dependencies (tenant_id, source_node_type, source_name, dest_node_type, dest_name,
			resolved_dest_node_id, resolution_status)
		VALUES ($1, 'Workload', 'billing', 'MessageQueue', 'invoices', $2, 'resolved') RETURNING id`, f.tenantID, n.KGNodeID)
	if err != nil || row.Scan(&depID) != nil {
		t.Fatalf("insert dependency: %v", err)
	}

	// The collector later finds a queue of the same name in the account.
	collected := f.collect(t, "SQSQueue", "invoices")
	if _, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{}); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	got, _ := f.repo.Get(ctx, f.tenantID, n.ID)
	if got.Status != flow_sources.ManualNodeStatusPossibleDuplicate || got.DuplicateNodeID != collected.ID {
		t.Fatalf("expected possible_duplicate of %s, got %s / %s", collected.ID, got.Status, got.DuplicateNodeID)
	}

	if _, repointed, err := f.repo.Merge(ctx, f.tenantID, n.ID); err != nil || repointed != 1 {
		t.Fatalf("Merge: repointed=%d err=%v", repointed, err)
	}
	var dest string
	if err := f.db.Db.Get(&dest, `SELECT resolved_dest_node_id::text FROM kg_manual_dependencies WHERE id = $1`, depID); err != nil || dest != collected.ID {
		t.Errorf("dependency not repointed: %s (%v)", dest, err)
	}
	if _, active, _ := f.graphNode(t, n.KGNodeID); active {
		t.Error("merged declaration's node is still active")
	}
	if _, active, _ := f.graphNode(t, collected.ID); !active {
		t.Error("merge retired the collected node")
	}
}

// The point of the feature: a dependency on a resource no collector found
// resolves to the declared node, and the build emits both the node and the edge.
func TestManualNodeLifecycle_DependencyResolvesToDeclaredNode(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()

	k8sAccount := uuid.NewString()
	workload := core.NewNode(core.NodeTypeWorkload,
		core.BuildUniqueKey(core.CloudProviderK8s, k8sAccount, "", core.NodeTypeWorkload, "billing", "billing-api"),
		map[string]interface{}{"name": "billing-api", "namespace": "billing"}, f.tenantID, k8sAccount, "k8s")
	workload.Level = "Tenant"
	if err := f.kg.SaveNodes([]*core.DbNode{workload}, 1); err != nil {
		t.Fatalf("save workload: %v", err)
	}

	declared, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, NodeType: "Database", SpecificType: "PostgreSQL", Name: "billing-db",
		Properties: map[string]interface{}{"engine": "postgres"},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}

	deps := flow_sources.NewManualDependencyRepository(f.db, nil)
	dep, err := deps.Create(ctx, flow_sources.ManualDependency{
		TenantID: f.tenantID, RelationshipType: "CALLS",
		SourceNodeType: "Workload", SourceName: "billing-api", SourceNamespace: "billing",
		DestNodeType: "Database", DestName: "billing-db",
	})
	if err != nil {
		t.Fatalf("declare dependency: %v", err)
	}
	if dep.ResolutionStatus != flow_sources.ManualResolutionResolved || dep.ResolvedDestNodeID != declared.KGNodeID {
		t.Fatalf("dependency should resolve to the declared node at once: %s → %s", dep.ResolutionStatus, dep.ResolvedDestNodeID)
	}

	reqCtx := security.NewRequestContextForTenantAdmin(f.tenantID, nil, nil, nil)
	edges, nodes, err := flow_sources.NewManualFlowSource(nil).BuildFlowRelationships(reqCtx, &core.FlowSourceBuildRequest{
		TenantID: f.tenantID, ExistingNodes: []*core.DbNode{workload},
	})
	if err != nil {
		t.Fatalf("BuildFlowRelationships: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != declared.KGNodeID || nodes[0].SpecificType != "PostgreSQL" {
		t.Fatalf("expected the declared node emitted, got %d nodes", len(nodes))
	}
	if len(edges) != 1 || edges[0].SourceNodeID != workload.ID || edges[0].DestinationNodeID != declared.KGNodeID {
		t.Fatalf("expected one CALLS edge workload → declared node, got %d", len(edges))
	}
	// The edge rides on the caller's account, as every manual edge does.
	if edges[0].CloudAccountID != k8sAccount {
		t.Errorf("edge account = %s, want the caller's %s", edges[0].CloudAccountID, k8sAccount)
	}
}

// A Kubernetes declaration: keyed like the k8s source keys the workload,
// resolvable by namespace + cluster, refused when the cluster already reports
// it, and not flagged as a duplicate of a same-named workload elsewhere.
func TestManualNodeLifecycle_KubernetesWorkload(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()
	cluster := uuid.NewString()
	mustExec(t, f.db, `INSERT INTO cloud_accounts (id, account_name, created_by, updated_by, tenant, account_type, cloud_provider)
		SELECT $1, 'prod-cluster', created_by, updated_by, tenant, 'k8s', 'K8s' FROM cloud_accounts WHERE id = $2`, cluster, f.account)

	declared, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: cluster, SpecificType: "KubernetesDeployment", Name: "checkout", Namespace: "payments",
		Properties: map[string]interface{}{
			"annotations":      map[string]interface{}{"team": "payments"},
			"container_images": []interface{}{"checkout:1.4"},
		},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if want := "k8s:" + cluster + "::Workload:payments:checkout"; declared.UniqueKey != want || declared.Namespace != "payments" {
		t.Fatalf("declared %s (ns %q), want %s", declared.UniqueKey, declared.Namespace, want)
	}
	// Annotations and images land in the graph in the collector's shape.
	row, err := f.db.QueryRow(`SELECT jsonb_typeof(properties->'annotations'), properties->'annotations'->>'team',
		jsonb_typeof(properties->'container_images'), properties->'container_images'->>0
		FROM knowledge_graph_node WHERE tenant_id = $1 AND id = $2`, f.tenantID, declared.KGNodeID)
	if err != nil {
		t.Fatalf("query node: %v", err)
	}
	var annType, team, imgType, img string
	if err := row.Scan(&annType, &team, &imgType, &img); err != nil {
		t.Fatalf("scan node: %v", err)
	}
	if annType != "object" || team != "payments" || imgType != "array" || img != "checkout:1.4" {
		t.Errorf("graph node has annotations %s/%q and images %s/%q, want an object and an array", annType, team, imgType, img)
	}

	deps := flow_sources.NewManualDependencyRepository(f.db, nil)
	dep, err := deps.Create(ctx, flow_sources.ManualDependency{
		TenantID: f.tenantID, RelationshipType: "CALLS",
		SourceNodeType: "Workload", SourceName: "checkout", SourceNamespace: "payments", SourceCluster: "prod-cluster",
		DestNodeType: "MessageQueue", DestName: "orders",
	})
	if err != nil {
		t.Fatalf("dependency: %v", err)
	}
	if dep.ResolvedSourceNodeID != declared.KGNodeID {
		t.Fatalf("namespace + cluster qualifiers should resolve to the declared workload, got %q (%s)", dep.ResolvedSourceNodeID, dep.ResolutionStatus)
	}

	// The cluster already reports a different workload: declaring it is refused.
	b, _ := core.ManualNodeBuilderFor("K8s")
	collected, err := b.Build(core.ManualNodeInput{TenantID: f.tenantID, CloudAccountID: cluster, CloudProvider: "K8s",
		AccountName: "prod-cluster", SpecificType: "KubernetesDeployment", Name: "billing", Namespace: "payments"})
	if err != nil {
		t.Fatalf("build collected: %v", err)
	}
	collected.Level = "Tenant"
	if err := f.kg.SaveNodes([]*core.DbNode{collected}, 1); err != nil {
		t.Fatalf("save collected: %v", err)
	}
	_, err = f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: cluster, SpecificType: "KubernetesDeployment", Name: "billing", Namespace: "payments",
	})
	var exists *flow_sources.ManualNodeExistsError
	if !errors.As(err, &exists) || exists.NodeID != collected.ID {
		t.Fatalf("expected already-in-graph for %s, got %v", collected.ID, err)
	}

	// A collected "checkout" in another namespace is not this workload.
	other, _ := b.Build(core.ManualNodeInput{TenantID: f.tenantID, CloudAccountID: cluster, CloudProvider: "K8s",
		AccountName: "prod-cluster", SpecificType: "KubernetesDeployment", Name: "checkout", Namespace: "staging"})
	other.Level = "Tenant"
	if err := f.kg.SaveNodes([]*core.DbNode{other}, 1); err != nil {
		t.Fatalf("save other: %v", err)
	}
	if _, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{}); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	got, _ := f.repo.Get(ctx, f.tenantID, declared.ID)
	if got.Status != flow_sources.ManualNodeStatusActive {
		t.Errorf("a same-named workload in another namespace flagged the declaration %s", got.Status)
	}
}

// An adopted declaration takes its node back when the collector stops writing
// it, so a dependency on the resource does not silently fall to unmatched.
func TestManualNodeLifecycle_AdoptedDeclarationRevives(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()

	n := f.declareSQS(t, "payments")
	collected := f.collect(t, "SQSQueue", "payments")
	if _, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{collected.ID: collected}); err != nil {
		t.Fatalf("RunCycle after collect: %v", err)
	}
	if got, _ := f.repo.Get(ctx, f.tenantID, n.ID); got.Status != flow_sources.ManualNodeStatusAdopted {
		t.Fatalf("status = %s, want adopted", got.Status)
	}

	// The queue is gone: the sweep retires the collector's node.
	mustExec(t, f.db, `UPDATE knowledge_graph_node SET is_active = false WHERE tenant_id = $1 AND id = $2`, f.tenantID, n.KGNodeID)

	cycle, err := f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{})
	if err != nil {
		t.Fatalf("RunCycle after the collector dropped it: %v", err)
	}
	if cycle.Revived != 1 || len(cycle.Nodes) != 1 || cycle.Nodes[0].ID != n.KGNodeID {
		t.Fatalf("expected the declaration revived and re-emitted, got revived=%d emitted=%d", cycle.Revived, len(cycle.Nodes))
	}
	if got, _ := f.repo.Get(ctx, f.tenantID, n.ID); got.Status != flow_sources.ManualNodeStatusActive {
		t.Errorf("status = %s, want active", got.Status)
	}

	// Collected again: adopted again, not emitted.
	cycle, err = f.repo.RunCycle(ctx, f.tenantID, map[string]*core.DbNode{collected.ID: collected})
	if err != nil {
		t.Fatalf("RunCycle after it came back: %v", err)
	}
	if cycle.Adopted != 1 || len(cycle.Nodes) != 0 {
		t.Errorf("expected re-adoption, got adopted=%d emitted=%d", cycle.Adopted, len(cycle.Nodes))
	}
}

// A caller refused for the account writes nothing and learns nothing about
// whether the resource is collected.
func TestManualNodeLifecycle_RefusesUnauthorizedAccount(t *testing.T) {
	f := newManualNodeFixture(t)
	ctx := context.Background()
	onlyOutside := func(accountID string) bool { return accountID == "" }

	collected := f.collect(t, "SQSQueue", "ledger")
	_, err := f.repo.Create(ctx, f.kg, flow_sources.ManualNodeCreate{
		TenantID: f.tenantID, CloudAccountID: f.account, SpecificType: "SQSQueue", Name: "ledger", Region: "us-east-1",
		Authorize: onlyOutside,
	})
	if !errors.Is(err, flow_sources.ErrManualNodeForbidden) {
		t.Fatalf("got %v, want ErrManualNodeForbidden (not the already-in-graph answer for %s)", err, collected.ID)
	}
	rows, _ := f.repo.List(ctx, f.tenantID)
	if len(rows) != 0 {
		t.Fatalf("a refused create left %d declarations", len(rows))
	}
}
