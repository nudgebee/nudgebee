package core

import (
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/internal/testenv"

	"github.com/google/uuid"
)

// These tests pin the seed-only pod fan-out rule: synth Pods are attached
// only to the Node/Workload entities the caller explicitly seeds, never to
// Node/Workload entities the BFS merely reaches transitively. Without this,
// expanding a Namespace pulls in every Workload under it and then every Pod
// of every Workload, swamping the frontend graph.
//
// Topology seeded per test:
//
//	nudgebee (Namespace) ◀──BELONGS_TO── api (Workload, Deployment)
//
// plus one active row in public.k8s_pods (api-pod-1) managed by the api
// Workload. Seeding the Workload must surface the pod; seeding the
// Namespace (which reaches the Workload at depth 1) must not.

// insertTestPod inserts a single active row into public.k8s_pods matching a
// Deployment-kind Workload named workloadName in the given namespace. Returns
// the pod's cloud_resource_id (which is also its synth node ID).
func insertTestPod(t *testing.T, service *Service, tenantID, accountID, namespace, workloadName, podName string) string {
	t.Helper()
	podID := uuid.New().String()
	now := time.Now()
	_, err := service.dbManager.Db.Exec(`
		INSERT INTO public.k8s_pods (
			tenant_id, cloud_account_id, cloud_resource_id, external_id,
			name, namespace, status, node_name, workload_type, workload_name,
			is_active, restart_count, creation_time, last_seen, labels, meta
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4,
			$5, $6, 'Running', 'k8s-node-1', 'Deployment', $7,
			true, '{}'::jsonb, $8, $8, '{}'::jsonb, '{}'::jsonb
		)`,
		tenantID, accountID, podID, namespace+"/Pod/"+podName,
		podName, namespace, workloadName, now)
	if err != nil {
		t.Fatalf("Failed to insert test pod: %v", err)
	}
	return podID
}

func cleanupTestPods(t *testing.T, service *Service, tenantID string) {
	t.Helper()
	if _, err := service.dbManager.Db.Exec(
		"DELETE FROM public.k8s_pods WHERE tenant_id = $1::uuid", tenantID); err != nil {
		t.Logf("Warning: Failed to cleanup test pods: %v", err)
	}
}

// seedNamespaceWorkload seeds a Workload (Deployment) → Namespace topology and
// returns the two nodes keyed "workload" and "namespace".
func seedNamespaceWorkload(t *testing.T, service *Service, tenantID, accountID, namespace, workloadName string) map[string]*DbNode {
	t.Helper()

	ns := createTestNode(t, namespace, NodeTypeNamespace, tenantID, accountID)

	wl := createTestNode(t, workloadName, NodeTypeWorkload, tenantID, accountID)
	// PodsForWorkload matches on (name, kind, namespace); createTestNode only
	// sets name, so add the two the synthesizer additionally requires.
	wl.Properties["kind"] = "Deployment"
	wl.Properties["namespace"] = namespace

	nodes := map[string]*DbNode{"namespace": ns, "workload": wl}
	if err := service.SaveNodes([]*DbNode{ns, wl}, 0); err != nil {
		t.Fatalf("Failed to save nodes: %v", err)
	}

	edge := createTestEdge(t, wl.ID, ns.ID, RelationshipBelongsTo, tenantID, accountID)
	if err := service.SaveEdges([]*DbEdge{edge}, []*DbNode{ns, wl}, 1); err != nil {
		t.Fatalf("Failed to save edges: %v", err)
	}
	return nodes
}

func containsNodeType(nodes []KgNode, nt NodeType) bool {
	for _, n := range nodes {
		if n.NodeType == nt {
			return true
		}
	}
	return false
}

// TestPodFanout_SeedWorkload_ShowsPods: seeding the Workload directly must
// surface its synth Pod.
func TestPodFanout_SeedWorkload_ShowsPods(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedNamespaceWorkload(t, service, tenantID, accountID, "nudgebee", "api")
	podID := insertTestPod(t, service, tenantID, accountID, "nudgebee", "api", "api-pod-1")
	defer cleanupTestData(t, dbManager, tenantID)
	defer cleanupTestPods(t, service, tenantID)

	result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["workload"].ID}, 1, nil, true)
	if err != nil {
		t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
	}

	if !containsNodeType(result.Nodes, NodeTypePod) {
		t.Errorf("seeding the Workload should surface its pod, but no Pod node was returned (nodes=%d)", len(result.Nodes))
	}
	found := false
	for _, n := range result.Nodes {
		if n.ID == podID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected synth pod %s in results", podID)
	}
}

// TestPodFanout_SeedNamespace_HidesPods: seeding the Namespace reaches the
// Workload at depth 1, but its pods must NOT be fanned out — this is the
// explosion the seed-only rule prevents.
func TestPodFanout_SeedNamespace_HidesPods(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedNamespaceWorkload(t, service, tenantID, accountID, "nudgebee", "api")
	insertTestPod(t, service, tenantID, accountID, "nudgebee", "api", "api-pod-1")
	defer cleanupTestData(t, dbManager, tenantID)
	defer cleanupTestPods(t, service, tenantID)

	result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["namespace"].ID}, 1, nil, true)
	if err != nil {
		t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
	}

	// The Workload should be reachable from the Namespace at depth 1...
	if !containsNodeType(result.Nodes, NodeTypeWorkload) {
		t.Fatalf("expected the Workload to be reachable from the Namespace at level 1")
	}
	// ...but its pods must not be dragged in.
	if containsNodeType(result.Nodes, NodeTypePod) {
		t.Errorf("seeding the Namespace must not fan out pods of transitively-reached Workloads, but a Pod node was returned")
	}
}
