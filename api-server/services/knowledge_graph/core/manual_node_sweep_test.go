package core

import (
	"io"
	"log/slog"
	"testing"

	"nudgebee/services/internal/testenv"
)

// A declared node is kept alive by its declaration, not by builds: the node
// sweep must never retire one, even of an infra-authoritative type such as a
// Kubernetes Workload, and even when the manual flow source ran but did not
// re-emit it. The stale collector Workload proves the sweep did run.
func TestMarkInactiveNodes_NeverSweepsDeclaredNodes(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	const tenant = "5e1f0d6a-7c3b-4c55-9a1e-6d0b4f2a9e11"
	const account = "6a2e1b7c-8d4c-4d66-8b2f-7e1c5a3b0f22"
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_node WHERE tenant_id = $1::uuid`, tenant)
	})

	workload := func(name, source string) *DbNode {
		key := BuildUniqueKey(CloudProviderK8s, account, "", NodeTypeWorkload, "payments", name)
		n := NewNode(NodeTypeWorkload, key, map[string]interface{}{"name": name, "namespace": "payments", "specific_type": "KubernetesDeployment"}, tenant, account, source)
		n.Level = "Tenant"
		return n
	}
	declared := workload("declared-checkout", ManualNodeSource)
	collected := workload("collected-stale", "k8s")

	for _, tc := range []struct {
		name        string
		flowSources map[string]FlowSourceInterface
		successful  []string
	}{
		{"no flow sources registered", nil, nil},
		{"manual flow source registered and ran", map[string]FlowSourceInterface{ManualNodeSource: nil, "ebpf": nil}, []string{ManualNodeSource}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestRequestContext()
			svc := NewService(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), dbm)
			svc.flowSources = tc.flowSources
			if err := svc.SaveNodes([]*DbNode{declared, collected}, 1); err != nil {
				t.Fatalf("seed: %v", err)
			}
			// A full build at version 2 that re-emitted neither node.
			if _, err := svc.markInactiveNodes(ctx, tenant, nil, 2, nil, tc.successful, true); err != nil {
				t.Fatalf("markInactiveNodes: %v", err)
			}
			active := func(id string) bool {
				var a bool
				if err := dbm.Db.Get(&a, `SELECT is_active FROM knowledge_graph_node WHERE id = $1`, id); err != nil {
					t.Fatalf("read %s: %v", id, err)
				}
				return a
			}
			if !active(declared.ID) {
				t.Error("the sweep retired a declared node")
			}
			if active(collected.ID) {
				t.Error("control: the stale collector workload should have been swept")
			}
		})
	}
}
