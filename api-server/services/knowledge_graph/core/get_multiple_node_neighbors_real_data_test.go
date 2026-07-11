package core

import (
	"fmt"
	"log/slog"
	"testing"

	"nudgebee/services/internal/testenv"

	"github.com/google/uuid"
)

// These tests exercise GetMultipleNodeNeighbors against a realistic multi-hop
// service topology. The graph is *seeded by the test* under a fresh tenant /
// account rather than read from whatever happens to be in the developer's
// metastore, so the assertions are reproducible on any database.
//
// The topology mirrors the real nudgebee dependency graph the original version
// of this file hardcoded live node UUIDs for (rpc -> auto-pilot-server ->
// rabbitmq -> ...). All edges are CALLS unless noted:
//
//	rpc ──▶ auto-pilot-server ──▶ rabbitmq ──▶ postgres
//	rpc ──▶ services-server
//	rpc ──▶ ticket-server
//	auto-pilot-server ──▶ kube-dns
//	auto-pilot-server ──▶ nudgebee   (Namespace, BELONGS_TO)
//
// Depths from rpc: auto-pilot-server/services-server/ticket-server = 1,
// rabbitmq/kube-dns/nudgebee = 2, postgres = 3.

// seedServiceTopology inserts the topology above under the given tenant/account
// and returns the created nodes keyed by workload name so tests can reference
// their generated IDs (e.g. nodes["rpc"].ID) instead of hardcoded UUIDs.
func seedServiceTopology(t *testing.T, service *Service, tenantID, accountID string) map[string]*DbNode {
	t.Helper()

	nodes := map[string]*DbNode{
		"rpc":               createTestNode(t, "rpc", NodeTypeWorkload, tenantID, accountID),
		"auto-pilot-server": createTestNode(t, "auto-pilot-server", NodeTypeWorkload, tenantID, accountID),
		"rabbitmq":          createTestNode(t, "rabbitmq", NodeTypeWorkload, tenantID, accountID),
		"services-server":   createTestNode(t, "services-server", NodeTypeWorkload, tenantID, accountID),
		"ticket-server":     createTestNode(t, "ticket-server", NodeTypeWorkload, tenantID, accountID),
		"kube-dns":          createTestNode(t, "kube-dns", NodeTypeWorkload, tenantID, accountID),
		"postgres":          createTestNode(t, "postgres", NodeTypeDatabase, tenantID, accountID),
		"nudgebee":          createTestNode(t, "nudgebee", NodeTypeNamespace, tenantID, accountID),
	}

	nodeList := make([]*DbNode, 0, len(nodes))
	for _, n := range nodes {
		nodeList = append(nodeList, n)
	}
	if err := service.SaveNodes(nodeList, 0); err != nil {
		t.Fatalf("Failed to save topology nodes: %v", err)
	}

	edges := []*DbEdge{
		createTestEdge(t, nodes["rpc"].ID, nodes["auto-pilot-server"].ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodes["rpc"].ID, nodes["services-server"].ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodes["rpc"].ID, nodes["ticket-server"].ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodes["auto-pilot-server"].ID, nodes["rabbitmq"].ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodes["auto-pilot-server"].ID, nodes["kube-dns"].ID, RelationshipCalls, tenantID, accountID),
		createTestEdge(t, nodes["auto-pilot-server"].ID, nodes["nudgebee"].ID, RelationshipBelongsTo, tenantID, accountID),
		createTestEdge(t, nodes["rabbitmq"].ID, nodes["postgres"].ID, RelationshipCalls, tenantID, accountID),
	}
	if err := service.SaveEdges(edges, nodeList, 1); err != nil {
		t.Fatalf("Failed to save topology edges: %v", err)
	}

	return nodes
}

// TestRealData_GetMultipleNodeNeighbors_Level1 tests level 1 against the seeded topology.
// Starting from auto-pilot-server, should return direct neighbors only.
func TestRealData_GetMultipleNodeNeighbors_Level1(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedServiceTopology(t, service, tenantID, accountID)
	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("Level 1 from auto-pilot-server returns direct neighbors", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["auto-pilot-server"].ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		fmt.Printf("Level 1 results: %d nodes, %d edges\n", len(result.Nodes), len(result.Edges))

		// Should have at least auto-pilot-server + some neighbors
		if len(result.Nodes) < 2 {
			t.Errorf("Expected at least 2 nodes (auto-pilot-server + neighbors), got %d", len(result.Nodes))
		}

		// Verify auto-pilot-server is in results
		found := false
		for _, node := range result.Nodes {
			if node.ID == nodes["auto-pilot-server"].ID {
				found = true
				fmt.Printf("Found starting node: %s (type: %s)\n", node.Properties["name"], node.NodeType)
				break
			}
		}
		if !found {
			t.Errorf("Starting node auto-pilot-server not found in results")
		}

		// Log all returned nodes for debugging
		fmt.Printf("Returned nodes at level 1:\n")
		for _, node := range result.Nodes {
			name := ""
			if n, ok := node.Properties["name"].(string); ok {
				name = n
			}
			fmt.Printf("  - %s: %s (ID: %s)\n", node.NodeType, name, node.ID)
		}
	})
}

// TestRealData_GetMultipleNodeNeighbors_Level2 tests level 2 against the seeded topology.
// Starting from rpc, level 2 should reach rabbitmq through auto-pilot-server.
func TestRealData_GetMultipleNodeNeighbors_Level2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedServiceTopology(t, service, tenantID, accountID)
	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("Level 2 from rpc reaches rabbitmq through auto-pilot-server", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["rpc"].ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		fmt.Printf("Level 2 results: %d nodes, %d edges\n", len(result.Nodes), len(result.Edges))

		// Check if rabbitmq is reachable at level 2
		// Path: rpc -> auto-pilot-server -> rabbitmq
		rabbitmqFound := false
		autoPilotFound := false
		rpcFound := false

		for _, node := range result.Nodes {
			switch node.ID {
			case nodes["rpc"].ID:
				rpcFound = true
			case nodes["auto-pilot-server"].ID:
				autoPilotFound = true
			case nodes["rabbitmq"].ID:
				rabbitmqFound = true
			}
		}

		if !rpcFound {
			t.Errorf("Starting node rpc not found in results")
		}
		if !autoPilotFound {
			t.Errorf("Level 1 neighbor auto-pilot-server not found in results")
		}
		if !rabbitmqFound {
			t.Errorf("Level 2 neighbor rabbitmq not found in results (path: rpc -> auto-pilot-server -> rabbitmq)")
		}

		fmt.Printf("Level 2 traversal verified: rpc=%v, auto-pilot-server=%v, rabbitmq=%v\n",
			rpcFound, autoPilotFound, rabbitmqFound)
	})
}

// TestRealData_GetMultipleNodeNeighbors_Level3 tests level 3 against the seeded topology.
// Starting from rpc, level 3 should reach rabbitmq's neighbor (postgres).
func TestRealData_GetMultipleNodeNeighbors_Level3(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedServiceTopology(t, service, tenantID, accountID)
	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("Level 3 from rpc returns more nodes than level 2", func(t *testing.T) {
		// Get level 2 results
		resultLevel2, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["rpc"].ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(level=2) error = %v", err)
		}

		// Get level 3 results
		resultLevel3, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["rpc"].ID}, 3, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(level=3) error = %v", err)
		}

		fmt.Printf("Level 2: %d nodes, %d edges\n", len(resultLevel2.Nodes), len(resultLevel2.Edges))
		fmt.Printf("Level 3: %d nodes, %d edges\n", len(resultLevel3.Nodes), len(resultLevel3.Edges))

		// Level 3 reaches postgres (rpc -> auto-pilot-server -> rabbitmq -> postgres),
		// which is not reachable at level 2, so it must have strictly more nodes.
		if len(resultLevel3.Nodes) <= len(resultLevel2.Nodes) {
			t.Errorf("Level 3 should have more nodes than level 2 (postgres at depth 3). Level 2: %d, Level 3: %d",
				len(resultLevel2.Nodes), len(resultLevel3.Nodes))
		}

		// Level 3 should have >= edges than level 2
		if len(resultLevel3.Edges) < len(resultLevel2.Edges) {
			t.Errorf("Level 3 should have >= edges than level 2. Level 2: %d, Level 3: %d",
				len(resultLevel2.Edges), len(resultLevel3.Edges))
		}

		// Verify postgres specifically appears only at level 3.
		if containsID(extractNodeIDs(resultLevel2.Nodes), nodes["postgres"].ID) {
			t.Errorf("postgres should NOT be reachable from rpc at level 2")
		}
		if !containsID(extractNodeIDs(resultLevel3.Nodes), nodes["postgres"].ID) {
			t.Errorf("postgres should be reachable from rpc at level 3")
		}
	})
}

// TestRealData_GetMultipleNodeNeighbors_MultipleStartingNodes tests starting from multiple nodes
func TestRealData_GetMultipleNodeNeighbors_MultipleStartingNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedServiceTopology(t, service, tenantID, accountID)
	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("Multiple starting nodes combines neighbors", func(t *testing.T) {
		// Get neighbors from rpc alone
		resultRpc, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["rpc"].ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(rpc) error = %v", err)
		}

		// Get neighbors from rabbitmq alone
		resultRabbitmq, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["rabbitmq"].ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(rabbitmq) error = %v", err)
		}

		// Get neighbors from both rpc and rabbitmq
		resultBoth, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["rpc"].ID, nodes["rabbitmq"].ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(rpc+rabbitmq) error = %v", err)
		}

		fmt.Printf("RPC only: %d nodes, %d edges\n", len(resultRpc.Nodes), len(resultRpc.Edges))
		fmt.Printf("Rabbitmq only: %d nodes, %d edges\n", len(resultRabbitmq.Nodes), len(resultRabbitmq.Edges))
		fmt.Printf("Both: %d nodes, %d edges\n", len(resultBoth.Nodes), len(resultBoth.Edges))

		// Both starting nodes should be in results
		rpcFound := false
		rabbitmqFound := false
		for _, node := range resultBoth.Nodes {
			if node.ID == nodes["rpc"].ID {
				rpcFound = true
			}
			if node.ID == nodes["rabbitmq"].ID {
				rabbitmqFound = true
			}
		}

		if !rpcFound {
			t.Errorf("rpc not found in combined results")
		}
		if !rabbitmqFound {
			t.Errorf("rabbitmq not found in combined results")
		}

		// Combined should have at least as many unique nodes as max of individual
		maxIndividual := len(resultRpc.Nodes)
		if len(resultRabbitmq.Nodes) > maxIndividual {
			maxIndividual = len(resultRabbitmq.Nodes)
		}
		if len(resultBoth.Nodes) < maxIndividual {
			t.Errorf("Combined results should have >= nodes than individual. Max individual: %d, Combined: %d",
				maxIndividual, len(resultBoth.Nodes))
		}
	})
}

// TestRealData_GetMultipleNodeNeighbors_CompareLevels compares all levels for same starting node
func TestRealData_GetMultipleNodeNeighbors_CompareLevels(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedServiceTopology(t, service, tenantID, accountID)
	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("Compare levels 1, 2, 3 from auto-pilot-server", func(t *testing.T) {
		result1, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["auto-pilot-server"].ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("Level 1 error: %v", err)
		}

		result2, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["auto-pilot-server"].ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("Level 2 error: %v", err)
		}

		result3, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["auto-pilot-server"].ID}, 3, nil, true)
		if err != nil {
			t.Fatalf("Level 3 error: %v", err)
		}

		fmt.Printf("=== Results from auto-pilot-server ===\n")
		fmt.Printf("Level 1: %d nodes, %d edges\n", len(result1.Nodes), len(result1.Edges))
		fmt.Printf("Level 2: %d nodes, %d edges\n", len(result2.Nodes), len(result2.Edges))
		fmt.Printf("Level 3: %d nodes, %d edges\n", len(result3.Nodes), len(result3.Edges))

		// Verify monotonic increase (or equal)
		if len(result2.Nodes) < len(result1.Nodes) {
			t.Errorf("Level 2 nodes (%d) should be >= Level 1 nodes (%d)",
				len(result2.Nodes), len(result1.Nodes))
		}
		if len(result3.Nodes) < len(result2.Nodes) {
			t.Errorf("Level 3 nodes (%d) should be >= Level 2 nodes (%d)",
				len(result3.Nodes), len(result2.Nodes))
		}

		// Log sample node types at each level
		fmt.Printf("\nSample nodes at Level 1:\n")
		logSampleNodes(result1.Nodes, 5)

		fmt.Printf("\nAdditional nodes at Level 2 (first 5):\n")
		level2Only := findNewNodes(result1.Nodes, result2.Nodes)
		logSampleNodes(level2Only, 5)

		fmt.Printf("\nAdditional nodes at Level 3 (first 5):\n")
		level3Only := findNewNodes(result2.Nodes, result3.Nodes)
		logSampleNodes(level3Only, 5)
	})
}

// TestRealData_GetMultipleNodeNeighbors_VerifyEdgeConnectivity verifies edges connect discovered nodes
func TestRealData_GetMultipleNodeNeighbors_VerifyEdgeConnectivity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager := testenv.RequireMetastore(t)

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()
	nodes := seedServiceTopology(t, service, tenantID, accountID)
	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("All edges connect nodes in the result set", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodes["auto-pilot-server"].ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Build set of node IDs
		nodeIDSet := make(map[string]bool)
		for _, node := range result.Nodes {
			nodeIDSet[node.ID] = true
		}

		// Verify all edges connect nodes in the set
		invalidEdges := 0
		for _, edge := range result.Edges {
			if !nodeIDSet[edge.SourceNodeID] {
				fmt.Printf("Edge %s has source %s not in node set\n", edge.ID, edge.SourceNodeID)
				invalidEdges++
			}
			if !nodeIDSet[edge.DestinationNodeID] {
				fmt.Printf("Edge %s has destination %s not in node set\n", edge.ID, edge.DestinationNodeID)
				invalidEdges++
			}
		}

		if invalidEdges > 0 {
			t.Errorf("Found %d edges with endpoints not in the node set", invalidEdges)
		} else {
			fmt.Printf("All %d edges correctly connect nodes within the result set\n", len(result.Edges))
		}
	})
}

// Helper functions for these tests

func logSampleNodes(nodes []KgNode, limit int) {
	count := 0
	for _, node := range nodes {
		if count >= limit {
			break
		}
		name := ""
		if n, ok := node.Properties["name"].(string); ok {
			name = n
		}
		fmt.Printf("  - %s: %s\n", node.NodeType, name)
		count++
	}
}

func findNewNodes(oldNodes, newNodes []KgNode) []KgNode {
	oldSet := make(map[string]bool)
	for _, node := range oldNodes {
		oldSet[node.ID] = true
	}

	var newOnly []KgNode
	for _, node := range newNodes {
		if !oldSet[node.ID] {
			newOnly = append(newOnly, node)
		}
	}
	return newOnly
}
