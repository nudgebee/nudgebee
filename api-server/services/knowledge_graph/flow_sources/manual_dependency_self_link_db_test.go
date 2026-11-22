package flow_sources_test

import (
	"context"
	"errors"
	"testing"

	"nudgebee/services/knowledge_graph/flow_sources"
)

// A declaration from a node to itself is refused on create, however the two
// sides name it, and nothing is written.
func TestManualDependencySelfLink_Create(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	// A region qualifier the resolver does not match on: two different
	// endpoint texts for one node.
	sameNodeTwice := queueDep(f, "orders", "orders")
	sameNodeTwice.DestRegion = "us-east-1"

	for name, dep := range map[string]flow_sources.ManualDependency{
		"identical endpoints":           queueDep(f, "orders", "orders"),
		"two texts for one node":        sameNodeTwice,
		"identical unmatched endpoints": queueDep(f, "not-collected-yet", "not-collected-yet"),
	} {
		before := f.activeDeps(t)
		if _, err := f.asAdminA.Create(ctx, dep); !errors.Is(err, flow_sources.ErrManualDependencySelfLink) {
			t.Errorf("%s: got %v, want ErrManualDependencySelfLink", name, err)
		}
		if after := f.activeDeps(t); after != before {
			t.Errorf("%s: a refused create left a row (%d -> %d)", name, before, after)
		}
	}

	if _, err := f.asAdminA.Create(ctx, queueDep(f, "orders", "payments")); err != nil {
		t.Fatalf("a declaration between two different nodes must still be created: %v", err)
	}
}

// Editing a row so that both sides are the same node is refused, and the row
// keeps what it had.
func TestManualDependencySelfLink_Update(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	row, err := f.asAdminA.Create(ctx, queueDep(f, "orders", "payments"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.asAdminA.Update(ctx, f.tenantID, row.ID, queueDep(f, "orders", "orders")); !errors.Is(err, flow_sources.ErrManualDependencySelfLink) {
		t.Errorf("got %v, want ErrManualDependencySelfLink", err)
	}
	got, err := f.deps.Get(ctx, f.tenantID, row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DestName != "payments" {
		t.Errorf("a refused update changed the row: dest = %s", got.DestName)
	}
}

// Pinning both endpoints to one node is refused; pinning them to two nodes
// still resolves the row.
func TestManualDependencySelfLink_Pin(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	row, err := f.asAdminA.Create(ctx, queueDep(f, "orders", "payments"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	orders, payments := row.ResolvedSourceNodeID, row.ResolvedDestNodeID
	if orders == "" || payments == "" {
		t.Fatalf("fixture rows must resolve: src %q dst %q", orders, payments)
	}

	if _, err := f.asAdminA.SetResolvedNodes(ctx, f.tenantID, row.ID, orders, orders); !errors.Is(err, flow_sources.ErrManualDependencySelfLink) {
		t.Errorf("got %v, want ErrManualDependencySelfLink", err)
	}
	if _, err := f.asAdminA.SetResolvedNodes(ctx, f.tenantID, row.ID, orders, payments); err != nil {
		t.Errorf("pinning the two sides to two nodes must be allowed: %v", err)
	}
}

// Where one endpoint text names two nodes, only a pin can say which, so the
// text is not yet a self-link: the row is created unresolved and judged when
// the operator pins it. The form works this way — it creates from text and
// pins afterwards — so refusing on text here would make a legitimate
// cross-account pair impossible to declare.
func TestManualDependencySelfLink_AmbiguousTextIsPinnable(t *testing.T) {
	f := newDepAuthzFixture(t)
	ctx := context.Background()

	inA := f.collectIn(t, f.account, "123456789012", "shipping")
	inB := f.collectIn(t, f.accountB, "210987654321", "shipping")

	row, err := f.deps.Create(ctx, queueDep(f, "shipping", "shipping"))
	if err != nil {
		t.Fatalf("an endpoint text matching two nodes must still be declarable: %v", err)
	}
	if row.ResolutionStatus == flow_sources.ManualResolutionResolved {
		t.Fatalf("a row whose sides each match two nodes cannot be resolved: %s", row.ResolutionStatus)
	}

	if _, err := f.deps.SetResolvedNodes(ctx, f.tenantID, row.ID, inA.ID, inA.ID); !errors.Is(err, flow_sources.ErrManualDependencySelfLink) {
		t.Errorf("pinning both sides to one node: got %v, want ErrManualDependencySelfLink", err)
	}
	if _, err := f.deps.SetResolvedNodes(ctx, f.tenantID, row.ID, inA.ID, inB.ID); err != nil {
		t.Errorf("pinning the sides to the two nodes the text matches: %v", err)
	}
	// Editing such a row (notes only) must stay possible.
	edited := queueDep(f, "shipping", "shipping")
	edited.Notes = "two queues, one name"
	if _, err := f.deps.Update(ctx, f.tenantID, row.ID, edited); err != nil {
		t.Errorf("editing a pinned ambiguous row: %v", err)
	}
}
