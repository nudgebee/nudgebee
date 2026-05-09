package core

import (
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestNodeWithAttrs creates a test node with query_attributes populated for SearchNodes tests.
func createTestNodeWithAttrs(name string, nodeType NodeType, tenantID, accountID, namespace, cluster, source string) *DbNode {
	qa := map[string]interface{}{"name": name}
	if namespace != "" {
		qa["namespace"] = namespace
	}
	if cluster != "" {
		qa["cluster"] = cluster
	}
	return &DbNode{
		ID:              uuid.New().String(),
		NodeType:        nodeType,
		UniqueKey:       fmt.Sprintf("%s:%s:us-east-1:%s:%s:%s", source, accountID, nodeType, namespace, name),
		Properties:      map[string]interface{}{"name": name, "namespace": namespace, "cluster": cluster, "source": source},
		Labels:          map[string]string{"test": "true"},
		QueryAttributes: qa,
		CloudAccountID:  accountID,
		TenantID:        tenantID,
		Level:           "Tenant",
		Source:          source,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

// setupTraversalTestData creates a realistic infra topology for traversal tests:
//
//	Workload(app) --RUNS_ON--> Namespace(prod) --RUNS_ON--> Cluster(k8s-prod)
//	Workload(app) --EXPOSES--> K8sService(app-svc) --RUNS_ON--> Namespace(prod)
//	Workload(app) --PULLS_FROM--> ContainerRegistry(app-image)
//	Workload(api) --RUNS_ON--> Namespace(prod)
//	Workload(api) --RUNS_ON--> Namespace(staging)
//	Namespace(staging) --RUNS_ON--> Cluster(k8s-prod)
//	LoadBalancer(lb) --ROUTES_TO--> K8sService(app-svc)
//	LoadBalancer(lb) --HOSTED_ON--> SecurityGroup(sg-1)
func setupTraversalTestData(t *testing.T, service *Service, tenantID, accountID string) (nodes map[string]*DbNode, cleanup func()) {
	nodes = map[string]*DbNode{
		"app":       createTestNodeWithAttrs("app", NodeTypeWorkload, tenantID, accountID, "prod", "k8s-prod", "k8s"),
		"api":       createTestNodeWithAttrs("api", NodeTypeWorkload, tenantID, accountID, "prod", "k8s-prod", "k8s"),
		"prod":      createTestNodeWithAttrs("prod", NodeTypeNamespace, tenantID, accountID, "", "k8s-prod", "k8s"),
		"staging":   createTestNodeWithAttrs("staging", NodeTypeNamespace, tenantID, accountID, "", "k8s-prod", "k8s"),
		"k8s-prod":  createTestNodeWithAttrs("k8s-prod", NodeTypeCluster, tenantID, accountID, "", "", "k8s"),
		"app-svc":   createTestNodeWithAttrs("app-svc", NodeTypeK8sService, tenantID, accountID, "prod", "k8s-prod", "k8s"),
		"app-image": createTestNodeWithAttrs("app-image", NodeTypeContainerRegistry, tenantID, accountID, "", "", "aws"),
		"lb":        createTestNodeWithAttrs("lb-main", NodeTypeLoadBalancer, tenantID, accountID, "", "", "aws"),
		"sg":        createTestNodeWithAttrs("sg-1", NodeTypeSecurityGroup, tenantID, accountID, "", "", "aws"),
	}

	nodeSlice := make([]*DbNode, 0, len(nodes))
	for _, n := range nodes {
		nodeSlice = append(nodeSlice, n)
	}
	err := service.SaveNodes(nodeSlice, 0)
	require.NoError(t, err, "Failed to save test nodes")

	edges := []*DbEdge{
		createTestEdge(t, nodes["app"].ID, nodes["prod"].ID, RelationshipRunsOn, tenantID, accountID),
		createTestEdge(t, nodes["app"].ID, nodes["app-svc"].ID, RelationshipExposes, tenantID, accountID),
		createTestEdge(t, nodes["app"].ID, nodes["app-image"].ID, RelationshipPullsFrom, tenantID, accountID),
		createTestEdge(t, nodes["api"].ID, nodes["prod"].ID, RelationshipRunsOn, tenantID, accountID),
		createTestEdge(t, nodes["api"].ID, nodes["staging"].ID, RelationshipRunsOn, tenantID, accountID),
		createTestEdge(t, nodes["prod"].ID, nodes["k8s-prod"].ID, RelationshipRunsOn, tenantID, accountID),
		createTestEdge(t, nodes["staging"].ID, nodes["k8s-prod"].ID, RelationshipRunsOn, tenantID, accountID),
		createTestEdge(t, nodes["app-svc"].ID, nodes["prod"].ID, RelationshipRunsOn, tenantID, accountID),
		createTestEdge(t, nodes["lb"].ID, nodes["app-svc"].ID, RelationshipRoutesTo, tenantID, accountID),
		createTestEdge(t, nodes["lb"].ID, nodes["sg"].ID, RelationshipHostedOn, tenantID, accountID),
	}
	err = service.SaveEdges(edges, nodeSlice, 1)
	require.NoError(t, err, "Failed to save test edges")

	dbManager, _ := database.GetDatabaseManager(database.Metastore)
	cleanup = func() { cleanupTestData(t, dbManager, tenantID) }
	return
}

func newTestService(t *testing.T) (*Service, *database.DatabaseManager) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}
	ctx := newTestRequestContext()
	return NewService(ctx, slog.Default(), dbManager), dbManager
}

// ============================================================
// SearchNodes Tests
// ============================================================

func TestSearchNodes_ByName(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{Name: "app"})
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, nodes["app"].ID, result.Nodes[0].ID)
	assert.Equal(t, NodeTypeWorkload, result.Nodes[0].NodeType)
	assert.Equal(t, "app", result.Nodes[0].Name)
}

func TestSearchNodes_ByNamePattern(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{NamePattern: "app%"})
	require.NoError(t, err)
	// Should match: app, app-svc, app-image
	assert.Equal(t, 3, result.TotalCount)
	names := make([]string, len(result.Nodes))
	for i, n := range result.Nodes {
		names[i] = n.Name
	}
	assert.Contains(t, names, "app")
	assert.Contains(t, names, "app-svc")
	assert.Contains(t, names, "app-image")
}

func TestSearchNodes_ByNamespace(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{
		Namespace: "prod",
		NodeTypes: []NodeType{NodeTypeWorkload},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalCount) // app and api
	for _, n := range result.Nodes {
		assert.Equal(t, NodeTypeWorkload, n.NodeType)
		assert.Equal(t, "prod", n.Namespace)
	}
}

func TestSearchNodes_ByNodeTypes(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{
		NodeTypes: []NodeType{NodeTypeNamespace, NodeTypeCluster},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount) // prod, staging, k8s-prod
	for _, n := range result.Nodes {
		assert.True(t, n.NodeType == NodeTypeNamespace || n.NodeType == NodeTypeCluster,
			"unexpected node type: %s", n.NodeType)
	}
}

func TestSearchNodes_BySource(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{Source: "aws"})
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount) // app-image, lb, sg
	for _, n := range result.Nodes {
		assert.Equal(t, "aws", n.Source)
	}
}

func TestSearchNodes_ByLabels(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{
		Labels: map[string]string{"test": "true"},
	})
	require.NoError(t, err)
	assert.Equal(t, 9, result.TotalCount) // all test nodes have label test=true
}

func TestSearchNodes_ByCluster(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{
		Cluster:   "k8s-prod",
		NodeTypes: []NodeType{NodeTypeWorkload},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalCount) // app and api
}

func TestSearchNodes_CombinedFilters(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{
		Name:      "app",
		Namespace: "prod",
		NodeTypes: []NodeType{NodeTypeWorkload},
		Source:    "k8s",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "app", result.Nodes[0].Name)
}

func TestSearchNodes_Limit(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalCount)
}

func TestSearchNodes_LimitCappedAt100(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Requesting limit > 100 should be capped internally, but still return results
	result, err := service.SearchNodes(tenantID, SearchNodesParams{Limit: 999})
	require.NoError(t, err)
	assert.Equal(t, 9, result.TotalCount) // all 9 test nodes (< 100 cap)
}

func TestSearchNodes_NoResults(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.SearchNodes(tenantID, SearchNodesParams{Name: "nonexistent"})
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
	assert.Empty(t, result.Nodes)
}

func TestSearchNodes_EmptyParams(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Empty params should return all nodes (up to default limit 20)
	result, err := service.SearchNodes(tenantID, SearchNodesParams{})
	require.NoError(t, err)
	assert.Equal(t, 9, result.TotalCount) // all 9 test nodes
}

func TestSearchNodes_TenantIsolation(t *testing.T) {
	service, _ := newTestService(t)
	tenantA := uuid.New().String()
	tenantB := uuid.New().String()
	accountID := uuid.New().String()

	_, cleanupA := setupTraversalTestData(t, service, tenantA, accountID)
	defer cleanupA()

	// Search in tenant B should return nothing
	result, err := service.SearchNodes(tenantB, SearchNodesParams{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
}

// ============================================================
// TraverseDirectional Tests
// ============================================================

func TestTraverse_DownstreamDepth1(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app"].ID},
		Direction: TraverseDirectionDownstream,
		MaxDepth:  1,
	})
	require.NoError(t, err)
	assert.False(t, result.Truncated)
	assert.Equal(t, []string{nodes["app"].ID}, result.SeedNodeIDs)

	// Depth 1 downstream from app: prod (RUNS_ON), app-svc (EXPOSES), app-image (PULLS_FROM)
	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["app"].ID)
	assert.Contains(t, nodeIDs, nodes["prod"].ID)
	assert.Contains(t, nodeIDs, nodes["app-svc"].ID)
	assert.Contains(t, nodeIDs, nodes["app-image"].ID)
	assert.NotContains(t, nodeIDs, nodes["k8s-prod"].ID, "cluster should not be at depth 1")
}

func TestTraverse_DownstreamDepth2(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app"].ID},
		Direction: TraverseDirectionDownstream,
		MaxDepth:  2,
	})
	require.NoError(t, err)

	// Depth 2: app -> prod -> k8s-prod, app -> app-svc -> prod (already visited)
	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["k8s-prod"].ID, "cluster should be at depth 2")
	assert.Contains(t, nodeIDs, nodes["app-image"].ID)
}

func TestTraverse_UpstreamDepth1(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Upstream from namespace "prod": should find workloads and services that RUNS_ON/EXPOSES it
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["prod"].ID},
		Direction: TraverseDirectionUpstream,
		MaxDepth:  1,
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["prod"].ID)
	assert.Contains(t, nodeIDs, nodes["app"].ID, "app RUNS_ON prod")
	assert.Contains(t, nodeIDs, nodes["api"].ID, "api RUNS_ON prod")
	assert.Contains(t, nodeIDs, nodes["app-svc"].ID, "app-svc RUNS_ON prod")
	assert.NotContains(t, nodeIDs, nodes["k8s-prod"].ID, "cluster is downstream, not upstream")
}

func TestTraverse_UpstreamDepth2_ThroughIntermediateTypes(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Upstream from cluster k8s-prod, depth 2: should find Namespaces (depth 1) AND Workloads (depth 2)
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["k8s-prod"].ID},
		Direction: TraverseDirectionUpstream,
		MaxDepth:  2,
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["prod"].ID, "namespace prod at depth 1")
	assert.Contains(t, nodeIDs, nodes["staging"].ID, "namespace staging at depth 1")
	assert.Contains(t, nodeIDs, nodes["app"].ID, "workload app at depth 2 via prod")
	assert.Contains(t, nodeIDs, nodes["api"].ID, "workload api at depth 2 via prod/staging")
	assert.Contains(t, nodeIDs, nodes["app-svc"].ID, "K8sService at depth 2 via prod")
}

func TestTraverse_BothDirections(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Both directions from app-svc, depth 1
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app-svc"].ID},
		Direction: TraverseDirectionBoth,
		MaxDepth:  1,
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	// Downstream: app-svc RUNS_ON prod
	assert.Contains(t, nodeIDs, nodes["prod"].ID, "downstream: app-svc RUNS_ON prod")
	// Upstream: app EXPOSES app-svc, lb ROUTES_TO app-svc
	assert.Contains(t, nodeIDs, nodes["app"].ID, "upstream: app EXPOSES app-svc")
	assert.Contains(t, nodeIDs, nodes["lb"].ID, "upstream: lb ROUTES_TO app-svc")
}

func TestTraverse_RelationshipTypeFilter(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Downstream from app, only RUNS_ON
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:           []string{nodes["app"].ID},
		Direction:         TraverseDirectionDownstream,
		MaxDepth:          2,
		RelationshipTypes: []string{string(RelationshipRunsOn)},
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["prod"].ID, "prod via RUNS_ON")
	assert.Contains(t, nodeIDs, nodes["k8s-prod"].ID, "cluster via RUNS_ON chain")
	assert.NotContains(t, nodeIDs, nodes["app-svc"].ID, "app-svc is via EXPOSES, not RUNS_ON")
	assert.NotContains(t, nodeIDs, nodes["app-image"].ID, "app-image is via PULLS_FROM, not RUNS_ON")
}

func TestTraverse_ExcludeNodeTypes(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Downstream from lb, exclude SecurityGroup
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:          []string{nodes["lb"].ID},
		Direction:        TraverseDirectionDownstream,
		MaxDepth:         1,
		ExcludeNodeTypes: []NodeType{NodeTypeSecurityGroup},
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["app-svc"].ID, "app-svc via ROUTES_TO")
	assert.NotContains(t, nodeIDs, nodes["sg"].ID, "SecurityGroup should be excluded")
}

func TestTraverse_InlineSearch(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Use inline search to find workload "app" and traverse downstream
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		Name:            "app",
		Namespace:       "prod",
		SearchNodeTypes: []string{string(NodeTypeWorkload)},
		Direction:       TraverseDirectionDownstream,
		MaxDepth:        1,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, len(result.SeedNodeIDs))
	assert.Equal(t, nodes["app"].ID, result.SeedNodeIDs[0])

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["prod"].ID)
	assert.Contains(t, nodeIDs, nodes["app-svc"].ID)
	assert.Contains(t, nodeIDs, nodes["app-image"].ID)
}

func TestTraverse_InlineSearchNamePattern(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Use name_pattern to find workloads matching "ap%" and traverse
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NamePattern:     "ap%",
		SearchNodeTypes: []string{string(NodeTypeWorkload)},
		Direction:       TraverseDirectionDownstream,
		MaxDepth:        1,
	})
	require.NoError(t, err)
	// Should find both "app" and "api" workloads
	assert.Equal(t, 2, len(result.SeedNodeIDs))

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["app"].ID)
	assert.Contains(t, nodeIDs, nodes["api"].ID)
}

func TestTraverse_MaxNodesTruncation(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app"].ID},
		Direction: TraverseDirectionDownstream,
		MaxDepth:  2,
		MaxNodes:  3,
	})
	require.NoError(t, err)
	assert.True(t, result.Truncated, "should be truncated")
	assert.Greater(t, result.TotalDiscovered, 3, "total should exceed max_nodes")
	assert.LessOrEqual(t, len(result.Graph.Nodes), 3, "returned nodes should be <= max_nodes")
}

// ============================================================
// Validation Tests
// ============================================================

func TestTraverse_InvalidDirection(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()

	_, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{uuid.New().String()},
		Direction: "invalid",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid direction")
}

func TestTraverse_ConflictingInputs(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()

	_, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{uuid.New().String()},
		Name:      "something",
		Direction: TraverseDirectionDownstream,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provide either node_ids or search parameters, not both")
}

func TestTraverse_MissingInputs(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()

	_, err := service.TraverseDirectional(tenantID, TraverseParams{
		Direction: TraverseDirectionDownstream,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provide either node_ids or at least one search parameter")
}

func TestTraverse_TooManyNodeIDs(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()

	ids := make([]string, 11)
	for i := range ids {
		ids[i] = uuid.New().String()
	}

	_, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   ids,
		Direction: TraverseDirectionDownstream,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node_ids limited to 10")
}

func TestTraverse_MaxDepthClamped(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// max_depth=10 should be clamped to 3
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app"].ID},
		Direction: TraverseDirectionDownstream,
		MaxDepth:  10,
	})
	require.NoError(t, err)
	// Should not crash and should return results (clamped to 3)
	assert.Greater(t, len(result.Graph.Nodes), 0)
}

func TestTraverse_DefaultMaxDepth(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// max_depth=0 should default to 1
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app"].ID},
		Direction: TraverseDirectionDownstream,
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	// Depth 1 only: prod, app-svc, app-image (no k8s-prod)
	assert.Contains(t, nodeIDs, nodes["prod"].ID)
	assert.NotContains(t, nodeIDs, nodes["k8s-prod"].ID, "cluster is at depth 2, default is 1")
}

func TestTraverse_InlineSearchNoResults(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		Name:      "nonexistent-workload",
		Direction: TraverseDirectionDownstream,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalDiscovered)
	assert.Empty(t, result.Graph.Nodes)
	assert.Empty(t, result.Graph.Edges)
	assert.Empty(t, result.SeedNodeIDs)
	assert.False(t, result.Truncated)
}

// ============================================================
// Cycle Handling
// ============================================================

func TestTraverse_CycleHandling(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// A -> B -> C -> A (cycle)
	nodeA := createTestNodeWithAttrs("cycle-a", NodeTypeWorkload, tenantID, accountID, "test", "", "k8s")
	nodeB := createTestNodeWithAttrs("cycle-b", NodeTypeWorkload, tenantID, accountID, "test", "", "k8s")
	nodeC := createTestNodeWithAttrs("cycle-c", NodeTypeWorkload, tenantID, accountID, "test", "", "k8s")

	err := service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC}, 0)
	require.NoError(t, err)

	dbManager, _ := database.GetDatabaseManager(database.Metastore)
	defer cleanupTestData(t, dbManager, tenantID)

	edges := []*DbEdge{
		createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodeC.ID, nodeA.ID, RelationshipCalls, tenantID, accountID),
	}
	err = service.SaveEdges(edges, []*DbNode{nodeA, nodeB, nodeC}, 1)
	require.NoError(t, err)

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodeA.ID},
		Direction: TraverseDirectionDownstream,
		MaxDepth:  3,
	})
	require.NoError(t, err)

	// Should discover all 3 nodes without infinite loop
	assert.Equal(t, 3, len(result.Graph.Nodes), "should find all 3 cycle nodes")
	assert.False(t, result.Truncated)
}

// ============================================================
// Multiple Seed Nodes
// ============================================================

func TestTraverse_MultipleSeeds(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	// Traverse from both app and api workloads
	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:           []string{nodes["app"].ID, nodes["api"].ID},
		Direction:         TraverseDirectionDownstream,
		MaxDepth:          1,
		RelationshipTypes: []string{string(RelationshipRunsOn)},
	})
	require.NoError(t, err)

	assert.Equal(t, 2, len(result.SeedNodeIDs))
	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	assert.Contains(t, nodeIDs, nodes["prod"].ID, "both workloads RUNS_ON prod")
	assert.Contains(t, nodeIDs, nodes["staging"].ID, "api also RUNS_ON staging")
}

// ============================================================
// Edge Filtering in Response
// ============================================================

func TestTraverse_EdgesMatchTraversedNodes(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:           []string{nodes["app"].ID},
		Direction:         TraverseDirectionDownstream,
		MaxDepth:          1,
		RelationshipTypes: []string{string(RelationshipRunsOn)},
	})
	require.NoError(t, err)

	nodeIDs := slimNodeIDs(result.Graph.Nodes)
	// Both endpoints of every returned edge must belong to the result set.
	for _, edge := range result.Graph.Edges {
		assert.True(t, containsID(nodeIDs, edge.SourceNodeID) && containsID(nodeIDs, edge.DestinationNodeID),
			"edge %s connects nodes outside result set", edge.ID)
	}
	// Strict-tree default: with a single seed at depth=1, every edge's source
	// must equal the seed (no sibling-to-sibling edges).
	for _, edge := range result.Graph.Edges {
		assert.Equal(t, nodes["app"].ID, edge.SourceNodeID,
			"strict-tree default must not include edges that aren't rooted at the seed (got edge %s: %s -> %s)",
			edge.ID, edge.SourceNodeID, edge.DestinationNodeID)
	}
	// Should have RUNS_ON edge from app to prod
	found := false
	for _, edge := range result.Graph.Edges {
		if edge.SourceNodeID == nodes["app"].ID && edge.DestinationNodeID == nodes["prod"].ID {
			assert.Equal(t, string(RelationshipRunsOn), edge.RelationshipType)
			found = true
		}
	}
	assert.True(t, found, "should include RUNS_ON edge from app to prod")
}

// TestTraverse_StrictTreeIsDefault verifies that without InducedSubgraph the
// response excludes sibling-to-sibling edges (edges between two depth-1
// neighbors of the seed that the BFS never walked).
//
// Seed=app, depth=1, downstream, no relationship filter:
//
//	Discovered: [app, prod, app-svc, app-image]
//	BFS-walked edges: app->prod, app->app-svc, app->app-image
//	Induced-only (sibling) edge:  app-svc->prod  -- must NOT appear by default.
func TestTraverse_StrictTreeIsDefault(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:   []string{nodes["app"].ID},
		Direction: TraverseDirectionDownstream,
		MaxDepth:  1,
	})
	require.NoError(t, err)

	hasAppToProd := false
	hasAppToSvc := false
	hasAppToImage := false
	hasSvcToProd := false
	for _, edge := range result.Graph.Edges {
		switch {
		case edge.SourceNodeID == nodes["app"].ID && edge.DestinationNodeID == nodes["prod"].ID:
			hasAppToProd = true
		case edge.SourceNodeID == nodes["app"].ID && edge.DestinationNodeID == nodes["app-svc"].ID:
			hasAppToSvc = true
		case edge.SourceNodeID == nodes["app"].ID && edge.DestinationNodeID == nodes["app-image"].ID:
			hasAppToImage = true
		case edge.SourceNodeID == nodes["app-svc"].ID && edge.DestinationNodeID == nodes["prod"].ID:
			hasSvcToProd = true
		}
	}
	assert.True(t, hasAppToProd, "app->prod should be present (BFS-walked edge)")
	assert.True(t, hasAppToSvc, "app->app-svc should be present (BFS-walked edge)")
	assert.True(t, hasAppToImage, "app->app-image should be present (BFS-walked edge)")
	assert.False(t, hasSvcToProd, "app-svc->prod must be excluded by default (sibling-to-sibling edge)")
}

// TestTraverse_InducedSubgraphFlag verifies that with InducedSubgraph=true the
// sibling-to-sibling edge (app-svc->prod) is restored in the response.
func TestTraverse_InducedSubgraphFlag(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	result, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:         []string{nodes["app"].ID},
		Direction:       TraverseDirectionDownstream,
		MaxDepth:        1,
		InducedSubgraph: true,
	})
	require.NoError(t, err)

	hasSvcToProd := false
	for _, edge := range result.Graph.Edges {
		if edge.SourceNodeID == nodes["app-svc"].ID && edge.DestinationNodeID == nodes["prod"].ID {
			hasSvcToProd = true
		}
	}
	assert.True(t, hasSvcToProd, "app-svc->prod should be present with InducedSubgraph=true")
}

// TestTraverse_RejectsSearchNodeTypesWithNodeIDs verifies that combining
// node_ids with search_node_types yields a clear conflict error rather than
// silently dropping the type filter.
func TestTraverse_RejectsSearchNodeTypesWithNodeIDs(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	_, err := service.TraverseDirectional(tenantID, TraverseParams{
		NodeIDs:         []string{nodes["app"].ID},
		SearchNodeTypes: []string{string(NodeTypeLoadBalancer)},
		Direction:       TraverseDirectionDownstream,
		MaxDepth:        1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provide either node_ids or search parameters")
	assert.Contains(t, err.Error(), "search_node_types")
}

// TestTraverse_RejectsSearchNodeTypesAlone verifies that search_node_types
// without any narrowing field (name/name_pattern/namespace/cluster) is rejected
// — searching by type alone would match every node of that type in the tenant
// and is too broad to seed a traversal.
func TestTraverse_RejectsSearchNodeTypesAlone(t *testing.T) {
	service, _ := newTestService(t)
	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	_, cleanup := setupTraversalTestData(t, service, tenantID, accountID)
	defer cleanup()

	_, err := service.TraverseDirectional(tenantID, TraverseParams{
		SearchNodeTypes: []string{string(NodeTypeLoadBalancer)},
		Direction:       TraverseDirectionDownstream,
		MaxDepth:        1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search_node_types alone is too broad")
}

// ============================================================
// Helpers
// ============================================================

func slimNodeIDs(nodes []KgNodeSlim) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}
