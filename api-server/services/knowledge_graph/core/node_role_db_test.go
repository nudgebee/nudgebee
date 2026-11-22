package core

// DB-gated tests for user-set node roles: the row survives a rebuild, a VM's
// cloud twin takes the same role, refusals, and role search. Skips cleanly
// when no Postgres is reachable.

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
)

type nodeRoleFixture struct {
	db       *database.DatabaseManager
	svc      *Service
	tenantID string
	vmAcct   string
	awsAcct  string
}

func newNodeRoleFixture(t *testing.T) *nodeRoleFixture {
	t.Helper()
	db := testenv.RequireMetastore(t)
	f := &nodeRoleFixture{db: db, tenantID: uuid.NewString(), vmAcct: uuid.NewString(), awsAcct: uuid.NewString()}
	// GetNodeByID is scoped to the caller's tenant, so the service needs one.
	ctx := security.NewRequestContextForTenantAdmin(f.tenantID, nil, nil, nil)
	f.svc = NewService(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), db)
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM kg_node_roles WHERE tenant_id = $1`,
			`DELETE FROM knowledge_graph_edge WHERE tenant_id = $1`,
			`DELETE FROM knowledge_graph_node WHERE tenant_id = $1`,
			`DELETE FROM knowledge_graph_filter_options WHERE tenant_id = $1`,
		} {
			_, _ = db.Exec(q, f.tenantID)
		}
	})
	return f
}

// node builds and saves a node the way a source would.
func (f *nodeRoleFixture) node(t *testing.T, nt NodeType, account, name string, props map[string]interface{}) *DbNode {
	t.Helper()
	p := map[string]interface{}{"name": name}
	for k, v := range props {
		p[k] = v
	}
	n := NewNode(nt, "test:"+account+":"+string(nt)+":"+name, p, f.tenantID, account, "test")
	if err := f.svc.SaveNodes([]*DbNode{n}, 1); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
	return n
}

func (f *nodeRoleFixture) props(t *testing.T, id string) (map[string]interface{}, map[string]interface{}) {
	t.Helper()
	n, err := f.svc.GetNodeByID(id)
	if err != nil || n == nil {
		t.Fatalf("get node %s: %v", id, err)
	}
	var row struct {
		QA []byte `db:"query_attributes"`
	}
	if err := f.db.Db.Get(&row, `SELECT query_attributes FROM knowledge_graph_node WHERE id = $1`, id); err != nil {
		t.Fatalf("read query_attributes: %v", err)
	}
	qa := map[string]interface{}{}
	if err := json.Unmarshal(row.QA, &qa); err != nil {
		t.Fatalf("decode query_attributes: %v", err)
	}
	return n.Properties, qa
}

func allowAll(string) bool { return true }

func TestNodeRole_UserRoleSurvivesRebuildAndClearRestoresDetected(t *testing.T) {
	f := newNodeRoleFixture(t)
	// The classifier read this workload as a postgres database.
	wl := f.node(t, NodeTypeWorkload, f.vmAcct, "redis-master", map[string]interface{}{"role": "database", "engine": "postgres", "kind": "StatefulSet"})

	res, err := f.svc.SetNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: wl.ID, Role: NodeRoleCache, Engine: "Redis", Authorize: allowAll})
	if err != nil {
		t.Fatalf("set role: %v", err)
	}
	if got := getNodeProp(res.Node.Properties, PropRole); got != "cache" {
		t.Fatalf("response node role = %q, want cache", got)
	}
	if res.Node.LogoID != "redis" {
		t.Errorf("logo follows the engine: got %q, want redis", res.Node.LogoID)
	}

	// A rebuild writes the node again with the classifier's values.
	f.node(t, NodeTypeWorkload, f.vmAcct, "redis-master", map[string]interface{}{"role": "database", "engine": "postgres", "kind": "StatefulSet"})
	props, qa := f.props(t, wl.ID)
	if getNodeProp(props, PropRole) != "cache" || getNodeProp(props, PropRoleSource) != RoleSourceUser || getNodeProp(props, PropDetectedRole) != "database" {
		t.Fatalf("user role lost on rebuild: %v", props)
	}
	if qa[PropRole] != "cache" || qa[PropEngine] != "redis" {
		t.Errorf("query_attributes not mirrored after rebuild: %v", qa)
	}

	res, err = f.svc.ClearNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: wl.ID, Authorize: allowAll})
	if err != nil {
		t.Fatalf("clear role: %v", err)
	}
	if getNodeProp(res.Node.Properties, PropRole) != "database" || getNodeProp(res.Node.Properties, PropRoleSource) != "" {
		t.Fatalf("clear must restore the detected role: %v", res.Node.Properties)
	}
	// And the next rebuild must not bring the user role back.
	f.node(t, NodeTypeWorkload, f.vmAcct, "redis-master", map[string]interface{}{"role": "database", "kind": "StatefulSet"})
	if props, _ := f.props(t, wl.ID); getNodeProp(props, PropRole) != "database" {
		t.Errorf("cleared role came back: %v", props)
	}
}

func TestNodeRole_TwinTakesRoleUnlessCallerCannotWriteIt(t *testing.T) {
	f := newNodeRoleFixture(t)
	vm := f.node(t, NodeTypeComputeInstance, f.vmAcct, "db-01", nil)
	ec2 := f.node(t, NodeTypeComputeInstance, f.awsAcct, "i-0abc", nil)
	for _, pair := range [][2]string{{vm.ID, ec2.ID}, {ec2.ID, vm.ID}} {
		if _, err := f.db.Exec(`INSERT INTO knowledge_graph_edge (source_node_id, destination_node_id, relationship_type, properties, cloud_account_id, tenant_id, level, is_active)
			VALUES ($1, $2, 'SAME_AS', '{}', $3, $4, 'Tenant', true)`, pair[0], pair[1], f.vmAcct, f.tenantID); err != nil {
			t.Fatalf("insert SAME_AS: %v", err)
		}
	}

	onlyVM := func(acct string) bool { return acct == f.vmAcct }
	res, err := f.svc.SetNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: vm.ID, Role: NodeRoleDatabase, Authorize: onlyVM})
	if err != nil {
		t.Fatalf("set role: %v", err)
	}
	if len(res.Applied) != 0 || len(res.Skipped) != 1 || res.Skipped[0] != ec2.ID {
		t.Fatalf("twin in an unwritable account must be skipped: applied=%v skipped=%v", res.Applied, res.Skipped)
	}
	if props, _ := f.props(t, ec2.ID); getNodeProp(props, PropRole) != "" {
		t.Errorf("skipped twin was changed: %v", props)
	}

	res, err = f.svc.SetNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: vm.ID, Role: NodeRoleDatabase, Authorize: allowAll})
	if err != nil {
		t.Fatalf("set role: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != ec2.ID {
		t.Fatalf("twin must take the role: applied=%v", res.Applied)
	}
	if props, _ := f.props(t, ec2.ID); getNodeProp(props, PropRole) != "database" {
		t.Errorf("twin role = %v", props)
	}
	if len(res.Changes) != 2 {
		t.Errorf("one audit change per node, got %d", len(res.Changes))
	}

	if _, err := f.svc.ClearNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: ec2.ID, Authorize: allowAll}); err != nil {
		t.Fatalf("clear from the twin's side: %v", err)
	}
	for _, id := range []string{vm.ID, ec2.ID} {
		if props, _ := f.props(t, id); getNodeProp(props, PropRole) != "" {
			t.Errorf("clear must remove the role from the whole pair, %s still has %v", id, props)
		}
	}
}

func TestNodeRole_Refusals(t *testing.T) {
	f := newNodeRoleFixture(t)
	wl := f.node(t, NodeTypeWorkload, f.vmAcct, "api", nil)
	ns := f.node(t, NodeTypeNamespace, f.vmAcct, "default", nil)
	tenantScoped := f.node(t, NodeTypeDatabase, f.tenantID, "billing-db", nil)

	cases := []struct {
		name string
		req  NodeRoleWrite
		want error
	}{
		{"unknown role", NodeRoleWrite{NodeID: wl.ID, Role: "db", Authorize: allowAll}, ErrNodeRoleInvalid},
		{"namespace", NodeRoleWrite{NodeID: ns.ID, Role: NodeRoleDatabase, Authorize: allowAll}, ErrNodeRoleNotTarget},
		{"no access", NodeRoleWrite{NodeID: wl.ID, Role: NodeRoleDatabase, Authorize: func(string) bool { return false }}, ErrNodeRoleForbidden},
		{"missing node", NodeRoleWrite{NodeID: uuid.NewString(), Role: NodeRoleDatabase, Authorize: allowAll}, ErrNodeRoleNotFound},
		// A node outside any account is asked about as "": tenant-wide access.
		{"tenant-scoped needs tenant access", NodeRoleWrite{NodeID: tenantScoped.ID, Role: NodeRoleDatabase, Authorize: func(acct string) bool { return acct != "" }}, ErrNodeRoleForbidden},
	}
	for _, c := range cases {
		c.req.TenantID = f.tenantID
		if _, err := f.svc.SetNodeRole(c.req); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	// A node of another tenant is not found, whatever its id.
	other := NodeRoleWrite{TenantID: uuid.NewString(), NodeID: wl.ID, Role: NodeRoleCache, Authorize: allowAll}
	if _, err := f.svc.SetNodeRole(other); !errors.Is(err, ErrNodeRoleNotFound) {
		t.Errorf("cross-tenant write: err = %v, want not found", err)
	}
}

func TestSearchNodes_RolesIncludeTypedNodes(t *testing.T) {
	f := newNodeRoleFixture(t)
	wl := f.node(t, NodeTypeWorkload, f.vmAcct, "postgresql", nil)
	rds := f.node(t, NodeTypeDatabase, f.awsAcct, "billing-prod", nil)
	f.node(t, NodeTypeWorkload, f.vmAcct, "frontend", nil)
	if _, err := f.svc.SetNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: wl.ID, Role: NodeRoleDatabase, Authorize: allowAll}); err != nil {
		t.Fatalf("set role: %v", err)
	}

	res, err := f.svc.SearchNodes(f.tenantID, SearchNodesParams{Roles: []string{NodeRoleDatabase}})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	got := map[string]bool{}
	for _, n := range res.Nodes {
		got[n.ID] = true
	}
	if len(res.Nodes) != 2 || !got[wl.ID] || !got[rds.ID] {
		t.Fatalf("roles:[database] must return the tagged workload and the RDS node only, got %d: %v", len(res.Nodes), got)
	}
}

func TestNodeRole_InvalidatesFilterOptionsCache(t *testing.T) {
	f := newNodeRoleFixture(t)
	wl := f.node(t, NodeTypeWorkload, f.vmAcct, "api", nil)
	if _, err := f.db.Exec(`INSERT INTO knowledge_graph_filter_options (tenant_id, payload) VALUES ($1, '{}')`, f.tenantID); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if _, err := f.svc.SetNodeRole(NodeRoleWrite{TenantID: f.tenantID, NodeID: wl.ID, Role: NodeRoleProxy, Authorize: allowAll}); err != nil {
		t.Fatalf("set role: %v", err)
	}
	var n int
	if err := f.db.Db.Get(&n, `SELECT count(*) FROM knowledge_graph_filter_options WHERE tenant_id = $1`, f.tenantID); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("filter options cache must be dropped so role shows in the dropdowns at once")
	}
}
