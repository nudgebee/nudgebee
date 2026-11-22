package core

// DB-gated tests for the flow-sourced-infra half of markInactiveNodes (#41619).
//
// A flow source can synthesise a row of an infra-authoritative type (an eBPF
// Workload for a workload the k8s inventory no longer has). Such a row must be
// retired by the sweep once the flow source that stamped it ran successfully
// and stopped reporting it — but only when the static source that owns the
// type actually ran, and never when the flow source itself failed.
//
// Before the fix the `sources`-named branch tested none of that: it restricted
// the sweep to `source = ANY(sources)`, and `sources` names static sources, so
// no flow-sourced row could ever be retired on a tenant whose filter names its
// sources. Those rows survived every build indefinitely.
//
// Skips cleanly when no Postgres is reachable.

import (
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
)

const markInactiveFlowInfraTenant = "b2ca6e00-0000-4000-8000-000000000004"

// seedFlowInfraFixture lays down the four rows every case below reasons about
// and returns the service to sweep with.
func seedFlowInfraFixture(t *testing.T) (*Service, *security.RequestContext) {
	t.Helper()
	dbm := testenv.RequireMetastore(t)
	ctx := newTestRequestContext()
	svc := NewService(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_node WHERE tenant_id = $1::uuid`, markInactiveFlowInfraTenant)
	})

	const acctID = "b2ca6e00-0000-4000-8000-0000000000cc"

	// The #41619 row: an eBPF-created Workload for a workload deleted from the
	// cluster. Infra-authoritative type, flow source, stale sync version.
	staleWorkload := NewNode(NodeTypeWorkload,
		BuildUniqueKey(CloudProviderK8s, acctID, "", NodeTypeWorkload, "app-100b", "payment-api-100b"),
		map[string]interface{}{"name": "payment-api-100b", "namespace": "app-100b"},
		markInactiveFlowInfraTenant, acctID, "ebpf")

	// A flow-sourced row of a NON-infra-authoritative type. The flow source
	// owns its lifecycle, so the sweep must never touch it however it is
	// scoped — this is the protection the gate clause must not widen away.
	staleService := NewNode(NodeTypeService,
		BuildUniqueKey(CloudProviderK8s, acctID, "", NodeTypeService, "app-100b", "logical-svc"),
		map[string]interface{}{"name": "logical-svc", "namespace": "app-100b"},
		markInactiveFlowInfraTenant, acctID, "ebpf")

	// A stale k8s row: in `sources`, so it was always sweepable. Guards against
	// the new clause accidentally narrowing the branch's original behaviour.
	staleK8s := NewNode(NodeTypeWorkload,
		BuildUniqueKey(CloudProviderK8s, acctID, "", NodeTypeWorkload, "app-100b", "k8s-owned"),
		map[string]interface{}{"name": "k8s-owned", "namespace": "app-100b"},
		markInactiveFlowInfraTenant, acctID, "k8s")

	// A stale row of a static source NOT in `sources` — must stay protected,
	// which is what scoping the sweep to named sources exists to do.
	staleAWS := NewNode(NodeTypeComputeInstance,
		BuildUniqueKey(CloudProviderAWS, acctID, "us-east-1", NodeTypeComputeInstance, "", "out-of-scope-ec2"),
		map[string]interface{}{"specific_type": "EC2Instance", "name": "out-of-scope-ec2"},
		markInactiveFlowInfraTenant, acctID, "aws")

	// An eBPF-only account's workload: created because nothing else can
	// describe that cluster, so the sweep must never retire it on silence.
	flowOwned := NewNode(NodeTypeWorkload,
		BuildUniqueKey(CloudProviderK8s, acctID, "", NodeTypeWorkload, "app-100b", "ebpf-only-api"),
		map[string]interface{}{
			"name": "ebpf-only-api", "namespace": "app-100b",
			FlowAuthoritativeProperty: true,
		},
		markInactiveFlowInfraTenant, acctID, "ebpf")

	if err := svc.SaveNodes([]*DbNode{staleWorkload, staleService, staleK8s, staleAWS, flowOwned}, 1); err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	return svc, ctx
}

// activeByName reads back is_active keyed on the node's name property.
func activeByName(t *testing.T, svc *Service) map[string]bool {
	t.Helper()
	rows, err := svc.dbManager.Query(
		`SELECT properties->>'name', is_active FROM knowledge_graph_node WHERE tenant_id = $1::uuid`,
		markInactiveFlowInfraTenant)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var isActive bool
		if err := rows.Scan(&name, &isActive); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[name] = isActive
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	return out
}

// TestMarkInactiveNodes_SourcesNamedSweepsStaleFlowInfra is the #41619
// regression test. Filter names its static sources (test has such tenants);
// eBPF ran successfully and no longer reports the deleted workload.
func TestMarkInactiveNodes_SourcesNamedSweepsStaleFlowInfra(t *testing.T) {
	svc, ctx := seedFlowInfraFixture(t)

	if _, err := svc.markInactiveNodes(ctx, markInactiveFlowInfraTenant, nil, 2,
		[]string{"aws_unused", "k8s"}, []string{"ebpf", "traces"}, true); err != nil {
		t.Fatalf("markInactiveNodes: %v", err)
	}

	active := activeByName(t, svc)
	if active["payment-api-100b"] {
		t.Error("stale eBPF Workload still active: a flow source is keeping a workload " +
			"alive that the inventory no longer has (#41619)")
	}
	if active["k8s-owned"] {
		t.Error("stale k8s Workload still active: the named-sources sweep regressed")
	}
	if !active["logical-svc"] {
		t.Error("flow-sourced Service was tombstoned; only infra-authoritative types " +
			"may be swept on sync version, the flow source owns the rest")
	}
	if !active["out-of-scope-ec2"] {
		t.Error("aws node tombstoned although 'aws' was not in sources; the sweep must " +
			"stay scoped to producers that ran")
	}
	if !active["ebpf-only-api"] {
		t.Error("flow_authoritative workload tombstoned: it belongs to an account with no " +
			"inventory, so nothing can tell a deleted workload from a quiet one (#41003)")
	}
}

// TestMarkInactiveNodes_FailedFlowSourceProtectsItsInfraRows: if eBPF errored
// this cycle, its rows prove nothing about what still exists, so they must be
// left alone even though their type is infra-authoritative.
func TestMarkInactiveNodes_FailedFlowSourceProtectsItsInfraRows(t *testing.T) {
	svc, ctx := seedFlowInfraFixture(t)

	// successfulFlowSources omits ebpf — only traces completed.
	if _, err := svc.markInactiveNodes(ctx, markInactiveFlowInfraTenant, nil, 2,
		[]string{"k8s"}, []string{"traces"}, true); err != nil {
		t.Fatalf("markInactiveNodes: %v", err)
	}

	active := activeByName(t, svc)
	if !active["payment-api-100b"] {
		t.Error("eBPF Workload tombstoned although the eBPF source did not complete; " +
			"a failed flow source must not retire the rows it stamped")
	}
	if active["k8s-owned"] {
		t.Error("stale k8s Workload still active: a failed flow source must not protect " +
			"another source's rows")
	}
}

// TestMarkInactiveNodes_SourcesWithoutK8sProtectsFlowInfra is the guard on the
// new clause. Every infra-authoritative type a flow source can synthesise is
// K8s-shaped, so k8s_source is the oracle for whether such a row is stale. A
// build narrowed to other sources (buildRequestForFilter produces these by
// intersecting a claim with a filter) never consulted that oracle and must not
// retire those rows.
func TestMarkInactiveNodes_SourcesWithoutK8sProtectsFlowInfra(t *testing.T) {
	svc, ctx := seedFlowInfraFixture(t)

	if _, err := svc.markInactiveNodes(ctx, markInactiveFlowInfraTenant, nil, 2,
		[]string{"gcp"}, []string{"ebpf"}, true); err != nil {
		t.Fatalf("markInactiveNodes: %v", err)
	}

	active := activeByName(t, svc)
	if !active["payment-api-100b"] {
		t.Error("eBPF Workload tombstoned on a build that never ran the k8s source; " +
			"nothing in that build could prove the workload is gone")
	}
	if !active["k8s-owned"] {
		t.Error("k8s Workload tombstoned although 'k8s' was not in sources")
	}
}
