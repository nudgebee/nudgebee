package flow_sources_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/flow_sources"
)

// depAuthzFixture is the manual node fixture (account A) plus a second AWS
// account B and a node outside any account, with a caller who may write only
// in A.
type depAuthzFixture struct {
	*manualNodeFixture
	accountB string
	deps     *flow_sources.ManualDependencyRepository
	asAdminA *flow_sources.ManualDependencyRepository
	outside  string // node id of a declaration outside any account
}

func newDepAuthzFixture(t *testing.T) *depAuthzFixture {
	t.Helper()
	f := &depAuthzFixture{manualNodeFixture: newManualNodeFixture(t), accountB: uuid.NewString()}
	mustExec(t, f.db, `INSERT INTO cloud_accounts (id, account_name, created_by, updated_by, tenant, account_type, cloud_provider, account_number)
		SELECT $1, 'second account', created_by, updated_by, tenant, 'aws', 'AWS', '210987654321' FROM cloud_accounts WHERE id = $2`, f.accountB, f.account)

	f.collect(t, "SQSQueue", "orders")
	f.collect(t, "SQSQueue", "payments")
	f.collectIn(t, f.accountB, "210987654321", "billing")
	outside, err := f.repo.Create(context.Background(), f.kg, flow_sources.ManualNodeCreate{TenantID: f.tenantID, NodeType: "Database", Name: "ledger-db"})
	if err != nil {
		t.Fatalf("declare outside node: %v", err)
	}
	f.outside = outside.KGNodeID

	f.deps = flow_sources.NewManualDependencyRepository(f.db, nil)
	onlyA := func(accountID string) bool { return accountID == f.account }
	f.asAdminA = f.deps.WithAccountAuthorizer(onlyA)
	return f
}

func (f *depAuthzFixture) collectIn(t *testing.T, account, number, name string) *core.DbNode {
	t.Helper()
	b, _ := core.ManualNodeBuilderFor("AWS")
	node, err := b.Build(core.ManualNodeInput{
		TenantID: f.tenantID, CloudAccountID: account, AccountNumber: number, CloudProvider: "AWS",
		SpecificType: "SQSQueue", Name: name, Region: "us-east-1",
	})
	if err != nil {
		t.Fatalf("build node: %v", err)
	}
	node.Level = "Tenant"
	if err := f.kg.SaveNodes([]*core.DbNode{node}, 1); err != nil {
		t.Fatalf("save node: %v", err)
	}
	return node
}

func queueDep(f *depAuthzFixture, src, dst string) flow_sources.ManualDependency {
	return flow_sources.ManualDependency{
		TenantID: f.tenantID, RelationshipType: "CALLS",
		SourceNodeType: "MessageQueue", SourceName: src, DestNodeType: "MessageQueue", DestName: dst,
	}
}

func (f *depAuthzFixture) activeDeps(t *testing.T) int {
	t.Helper()
	rows, err := f.deps.List(context.Background(), f.tenantID, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return len(rows)
}

// An account admin creates dependencies only between nodes in accounts they
// manage, and a refused create writes nothing.
func TestManualDependencyAuthz_Create(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	if _, err := f.asAdminA.Create(ctx, queueDep(f, "orders", "payments")); err != nil {
		t.Fatalf("A -> A must be allowed: %v", err)
	}
	unmatched := queueDep(f, "orders", "not-collected-yet")
	if _, err := f.asAdminA.Create(ctx, unmatched); err != nil {
		t.Fatalf("an unmatched endpoint with no account qualifier constrains nothing: %v", err)
	}

	toB := queueDep(f, "orders", "billing")
	toOutside := flow_sources.ManualDependency{TenantID: f.tenantID, RelationshipType: "CALLS",
		SourceNodeType: "MessageQueue", SourceName: "orders", DestNodeType: "Database", DestName: "ledger-db"}
	qualifiedB := queueDep(f, "orders", "future-queue")
	qualifiedB.DestAccountID = "210987654321"
	for name, dep := range map[string]flow_sources.ManualDependency{
		"to a node in another account":   toB,
		"to a node outside any account":  toOutside,
		"qualified with another account": qualifiedB,
	} {
		before := f.activeDeps(t)
		if _, err := f.asAdminA.Create(ctx, dep); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
			t.Errorf("%s: got %v, want ErrManualDependencyForbidden", name, err)
		}
		if after := f.activeDeps(t); after != before {
			t.Errorf("%s: a refused create left a row (%d -> %d)", name, before, after)
		}
	}

	// Internal callers (no authorizer) are unaffected.
	if _, err := f.deps.Create(ctx, toB); err != nil {
		t.Fatalf("an unchecked repository must still create: %v", err)
	}
}

// Update, delete and re-resolve are checked against what the row touches now,
// and an update also against what it would touch.
func TestManualDependencyAuthz_UpdateDeleteReresolve(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	own, err := f.asAdminA.Create(ctx, queueDep(f, "orders", "payments"))
	if err != nil {
		t.Fatalf("create own: %v", err)
	}
	foreign, err := f.deps.Create(ctx, queueDep(f, "orders", "billing"))
	if err != nil {
		t.Fatalf("create foreign: %v", err)
	}

	if _, err := f.asAdminA.Update(ctx, f.tenantID, own.ID, queueDep(f, "orders", "billing")); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
		t.Errorf("moving a row into another account: got %v, want forbidden", err)
	}
	if got, _ := f.deps.Get(ctx, f.tenantID, own.ID); got.DestName != "payments" {
		t.Errorf("a refused update changed the row: dest = %s", got.DestName)
	}
	if _, err := f.asAdminA.Update(ctx, f.tenantID, foreign.ID, queueDep(f, "orders", "payments")); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
		t.Errorf("taking over a row that touches another account: got %v, want forbidden", err)
	}
	if _, err := f.asAdminA.Update(ctx, f.tenantID, own.ID, queueDep(f, "payments", "orders")); err != nil {
		t.Errorf("editing within the caller's account must be allowed: %v", err)
	}

	if err := f.asAdminA.SoftDelete(ctx, f.tenantID, foreign.ID); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
		t.Errorf("deleting a row that touches another account: got %v, want forbidden", err)
	}
	if got, err := f.deps.Get(ctx, f.tenantID, foreign.ID); err != nil || !got.IsActive {
		t.Errorf("a refused delete removed the row: %v", err)
	}
	if err := f.asAdminA.CheckExistingWrite(ctx, f.tenantID, foreign.ID); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
		t.Errorf("re-resolving a row that touches another account: got %v, want forbidden", err)
	}
	if err := f.asAdminA.SoftDelete(ctx, f.tenantID, own.ID); err != nil {
		t.Errorf("deleting the caller's own row must be allowed: %v", err)
	}
}

// A pin must name a node of the tenant, whoever calls; and the caller must
// manage the picked node's account.
func TestManualDependencyAuthz_Pin(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	dep, err := f.asAdminA.Create(ctx, queueDep(f, "orders", "not-collected-yet"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.deps.SetResolvedNodes(ctx, f.tenantID, dep.ID, "", uuid.NewString()); !errors.Is(err, flow_sources.ErrManualDependencyNodeNotFound) {
		t.Errorf("pinning to a node outside the tenant: got %v, want ErrManualDependencyNodeNotFound", err)
	}

	billing := f.collectIn(t, f.accountB, "210987654321", "billing")
	if _, err := f.asAdminA.SetResolvedNodes(ctx, f.tenantID, dep.ID, "", billing.ID); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
		t.Errorf("pinning to another account's node: got %v, want forbidden", err)
	}
	if _, err := f.asAdminA.SetResolvedNodes(ctx, f.tenantID, dep.ID, "", f.outside); !errors.Is(err, flow_sources.ErrManualDependencyForbidden) {
		t.Errorf("pinning to a node outside any account: got %v, want forbidden", err)
	}
	payments := f.collect(t, "SQSQueue", "payments")
	pinned, err := f.asAdminA.SetResolvedNodes(ctx, f.tenantID, dep.ID, "", payments.ID)
	if err != nil {
		t.Fatalf("pinning to the caller's own node: %v", err)
	}
	if pinned.ResolvedDestNodeID != payments.ID {
		t.Errorf("pin not applied: %s", pinned.ResolvedDestNodeID)
	}
}

// A CSV row refused for its accounts is rejected; the rest import.
func TestManualDependencyAuthz_CSVImport(t *testing.T) {
	f := newDepAuthzFixture(t)
	csv := "source_node_type,source_name,dest_node_type,dest_name,relationship_type\n" +
		"MessageQueue,orders,MessageQueue,payments,CALLS\n" +
		"MessageQueue,orders,MessageQueue,billing,CALLS\n"
	res, err := f.asAdminA.ImportCSV(context.Background(), f.tenantID, "", bytes.NewBufferString(csv))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if len(res.Imported) != 1 || len(res.Rejected) != 1 || res.Rejected[0].RowIndex != 1 ||
		res.Rejected[0].Error != flow_sources.ErrManualDependencyForbidden.Error() {
		t.Fatalf("expected row 0 imported and row 1 refused, got %+v / %+v", res.Imported, res.Rejected)
	}
}

// A node whose account no longer exists constrains nothing, so a tenant admin
// (who knows only the existing accounts) can still edit or delete the row.
func TestManualDependencyAuthz_RemovedAccountDoesNotLockTheRow(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	// "archive" lives in an account the tenant no longer has.
	f.collectIn(t, uuid.NewString(), "333333333333", "archive")
	dep, err := f.deps.Create(ctx, queueDep(f, "orders", "archive"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if dep.ResolutionStatus != flow_sources.ManualResolutionResolved {
		t.Fatalf("fixture: dependency should resolve to the archived node, got %s", dep.ResolutionStatus)
	}
	tenantAdmin := f.deps.WithAccountAuthorizer(func(accountID string) bool {
		return accountID == "" || accountID == f.account || accountID == f.accountB
	})
	if err := tenantAdmin.SoftDelete(ctx, f.tenantID, dep.ID); err != nil {
		t.Fatalf("a removed account must not lock the row: %v", err)
	}
}
