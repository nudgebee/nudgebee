package core

import (
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"testing"
)

// Real data test constants for account a2a30b02-0f67-42e5-a2ab-c658230fd798
// Graph structure discovered:
//
//	hasura -> auto-pilot-server -> rabbitmq -> (many services)
//	hasura -> services-server, ticket-server, notifications, etc.
//	auto-pilot-server -> kube-dns, namespace, helm charts, etc.
const (
	// Test account ID
	// testAccountID = "a2a30b02-0f67-42e5-a2ab-c658230fd798"

	// Node IDs for testing (from real data)
	nodeIDHasura          = "de8199fc-ca4a-5593-82b6-df2692a8fd34" // Workload: hasura
	nodeIDAutoPilotServer = "64d74d3e-a241-5cb3-a969-2000ddf8e60b" // Workload: auto-pilot-server
	nodeIDRabbitmq        = "83eda4ae-005d-5aa1-8614-a0bb36283c51" // Workload: rabbitmq
	nodeIDServicesServer  = "81ed9dd1-0e69-55c1-83b8-eca295cfb398" // Workload: services-server
	nodeIDKubeDns         = "3f11af19-fcce-5388-ad04-dd2041688975" // Workload: kube-dns
	nodeIDTicketServer    = "b7560848-7813-533a-89ce-4387038a4bd7" // Workload: ticket-server
	nodeIDNamespace       = "1f6c4061-c8b9-5162-a2bf-f814a4c10174" // Namespace: nudgebee
)

// TestRealData_GetMultipleNodeNeighbors_Level1 tests level 1 with real data
// Starting from auto-pilot-server, should return direct neighbors only
func TestRealData_GetMultipleNodeNeighbors_Level1(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	t.Run("Level 1 from auto-pilot-server returns direct neighbors", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDAutoPilotServer}, 1, nil, true)
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
			if node.ID == nodeIDAutoPilotServer {
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

// TestRealData_GetMultipleNodeNeighbors_Level2 tests level 2 with real data
// Starting from hasura, level 2 should reach rabbitmq through auto-pilot-server
func TestRealData_GetMultipleNodeNeighbors_Level2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	t.Run("Level 2 from hasura reaches rabbitmq through auto-pilot-server", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDHasura}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		fmt.Printf("Level 2 results: %d nodes, %d edges\n", len(result.Nodes), len(result.Edges))

		// Check if rabbitmq is reachable at level 2
		// Path: hasura -> auto-pilot-server -> rabbitmq
		rabbitmqFound := false
		autoPilotFound := false
		actionFound := false

		for _, node := range result.Nodes {
			switch node.ID {
			case nodeIDHasura:
				actionFound = true
			case nodeIDAutoPilotServer:
				autoPilotFound = true
			case nodeIDRabbitmq:
				rabbitmqFound = true
			}
		}

		if !actionFound {
			t.Errorf("Starting node hasura not found in results")
		}
		if !autoPilotFound {
			t.Errorf("Level 1 neighbor auto-pilot-server not found in results")
		}
		if !rabbitmqFound {
			t.Errorf("Level 2 neighbor rabbitmq not found in results (path: hasura -> auto-pilot-server -> rabbitmq)")
		}

		fmt.Printf("Level 2 traversal verified: hasura=%v, auto-pilot-server=%v, rabbitmq=%v\n",
			actionFound, autoPilotFound, rabbitmqFound)
	})
}

// TestRealData_GetMultipleNodeNeighbors_Level3 tests level 3 with real data
// Starting from hasura, level 3 should reach rabbitmq's neighbors
func TestRealData_GetMultipleNodeNeighbors_Level3(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	t.Run("Level 3 from hasura returns more nodes than level 2", func(t *testing.T) {
		// Get level 2 results
		resultLevel2, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDHasura}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(level=2) error = %v", err)
		}

		// Get level 3 results
		resultLevel3, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDHasura}, 3, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(level=3) error = %v", err)
		}

		fmt.Printf("Level 2: %d nodes, %d edges\n", len(resultLevel2.Nodes), len(resultLevel2.Edges))
		fmt.Printf("Level 3: %d nodes, %d edges\n", len(resultLevel3.Nodes), len(resultLevel3.Edges))

		// Level 3 should have >= nodes than level 2
		if len(resultLevel3.Nodes) < len(resultLevel2.Nodes) {
			t.Errorf("Level 3 should have >= nodes than level 2. Level 2: %d, Level 3: %d",
				len(resultLevel2.Nodes), len(resultLevel3.Nodes))
		}

		// Level 3 should have >= edges than level 2
		if len(resultLevel3.Edges) < len(resultLevel2.Edges) {
			t.Errorf("Level 3 should have >= edges than level 2. Level 2: %d, Level 3: %d",
				len(resultLevel2.Edges), len(resultLevel3.Edges))
		}
	})
}

// TestRealData_GetMultipleNodeNeighbors_MultipleStartingNodes tests starting from multiple nodes
func TestRealData_GetMultipleNodeNeighbors_MultipleStartingNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	t.Run("Multiple starting nodes combines neighbors", func(t *testing.T) {
		// Get neighbors from hasura alone
		resultHasura, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDHasura}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(hasura) error = %v", err)
		}

		// Get neighbors from rabbitmq alone
		resultRabbitmq, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDRabbitmq}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(rabbitmq) error = %v", err)
		}

		// Get neighbors from both hasura and rabbitmq
		resultBoth, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDHasura, nodeIDRabbitmq}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(hasura+rabbitmq) error = %v", err)
		}

		fmt.Printf("Hasura only: %d nodes, %d edges\n", len(resultHasura.Nodes), len(resultHasura.Edges))
		fmt.Printf("Rabbitmq only: %d nodes, %d edges\n", len(resultRabbitmq.Nodes), len(resultRabbitmq.Edges))
		fmt.Printf("Both: %d nodes, %d edges\n", len(resultBoth.Nodes), len(resultBoth.Edges))

		// Both starting nodes should be in results
		actionFound := false
		rabbitmqFound := false
		for _, node := range resultBoth.Nodes {
			if node.ID == nodeIDHasura {
				actionFound = true
			}
			if node.ID == nodeIDRabbitmq {
				rabbitmqFound = true
			}
		}

		if !actionFound {
			t.Errorf("hasura not found in combined results")
		}
		if !rabbitmqFound {
			t.Errorf("rabbitmq not found in combined results")
		}

		// Combined should have at least as many unique nodes as max of individual
		maxIndividual := len(resultHasura.Nodes)
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

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	t.Run("Compare levels 1, 2, 3 from auto-pilot-server", func(t *testing.T) {
		result1, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDAutoPilotServer}, 1, nil, true)
		if err != nil {
			t.Fatalf("Level 1 error: %v", err)
		}

		result2, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDAutoPilotServer}, 2, nil, true)
		if err != nil {
			t.Fatalf("Level 2 error: %v", err)
		}

		result3, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDAutoPilotServer}, 3, nil, true)
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

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	t.Run("All edges connect nodes in the result set", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeIDAutoPilotServer}, 2, nil, true)
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

// Helper functions for real data tests

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
