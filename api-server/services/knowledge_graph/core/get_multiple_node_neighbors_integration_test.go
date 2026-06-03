package core

import (
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestGetMultipleNodeNeighbors_Level1 tests the default behavior (1 level = direct neighbors only)
func TestGetMultipleNodeNeighbors_Level1(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create a linear chain: A -> B -> C -> D
	// Level 1 from A should return A, B only
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)
	nodeD := createTestNode(t, "service-d", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC, nodeD}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create edges: A -> B -> C -> D
	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)
	edgeCD := createTestEdge(t, nodeC.ID, nodeD.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC, edgeCD}, []*DbNode{nodeA, nodeB, nodeC, nodeD}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Level 1 returns only direct neighbors", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have A + B (2 nodes)
		if len(result.Nodes) != 2 {
			t.Errorf("GetMultipleNodeNeighbors(level=1) nodes count = %v, want 2 (A + B)", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		// Should have 1 edge (A -> B)
		if len(result.Edges) != 1 {
			t.Errorf("GetMultipleNodeNeighbors(level=1) edges count = %v, want 1", len(result.Edges))
		}

		// Verify nodes include A and B
		nodeIDs := extractNodeIDs(result.Nodes)
		if !containsID(nodeIDs, nodeA.ID) {
			t.Errorf("Result should contain node A")
		}
		if !containsID(nodeIDs, nodeB.ID) {
			t.Errorf("Result should contain node B")
		}
		if containsID(nodeIDs, nodeC.ID) {
			t.Errorf("Result should NOT contain node C at level 1")
		}
	})
}

// TestGetMultipleNodeNeighbors_Level2 tests 2-hop traversal (neighbors of neighbors)
func TestGetMultipleNodeNeighbors_Level2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create a linear chain: A -> B -> C -> D
	// Level 2 from A should return A, B, C
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)
	nodeD := createTestNode(t, "service-d", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC, nodeD}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create edges: A -> B -> C -> D
	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)
	edgeCD := createTestEdge(t, nodeC.ID, nodeD.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC, edgeCD}, []*DbNode{nodeA, nodeB, nodeC, nodeD}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Level 2 returns neighbors of neighbors", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have A + B + C (3 nodes)
		if len(result.Nodes) != 3 {
			t.Errorf("GetMultipleNodeNeighbors(level=2) nodes count = %v, want 3 (A + B + C)", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		// Should have 2 edges (A -> B, B -> C)
		if len(result.Edges) != 2 {
			t.Errorf("GetMultipleNodeNeighbors(level=2) edges count = %v, want 2", len(result.Edges))
		}

		// Verify nodes include A, B, and C
		nodeIDs := extractNodeIDs(result.Nodes)
		if !containsID(nodeIDs, nodeA.ID) {
			t.Errorf("Result should contain node A")
		}
		if !containsID(nodeIDs, nodeB.ID) {
			t.Errorf("Result should contain node B")
		}
		if !containsID(nodeIDs, nodeC.ID) {
			t.Errorf("Result should contain node C")
		}
		if containsID(nodeIDs, nodeD.ID) {
			t.Errorf("Result should NOT contain node D at level 2")
		}
	})
}

// TestGetMultipleNodeNeighbors_Level3 tests 3-hop traversal
func TestGetMultipleNodeNeighbors_Level3(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create a linear chain: A -> B -> C -> D -> E
	// Level 3 from A should return A, B, C, D
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)
	nodeD := createTestNode(t, "service-d", NodeTypeService, tenantID, accountID)
	nodeE := createTestNode(t, "service-e", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC, nodeD, nodeE}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create edges: A -> B -> C -> D -> E
	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)
	edgeCD := createTestEdge(t, nodeC.ID, nodeD.ID, RelationshipCalls, tenantID, accountID)
	edgeDE := createTestEdge(t, nodeD.ID, nodeE.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC, edgeCD, edgeDE}, []*DbNode{nodeA, nodeB, nodeC, nodeD, nodeE}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Level 3 returns up to 3 hops", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 3, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have A + B + C + D (4 nodes)
		if len(result.Nodes) != 4 {
			t.Errorf("GetMultipleNodeNeighbors(level=3) nodes count = %v, want 4 (A + B + C + D)", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		// Should have 3 edges (A -> B, B -> C, C -> D)
		if len(result.Edges) != 3 {
			t.Errorf("GetMultipleNodeNeighbors(level=3) edges count = %v, want 3", len(result.Edges))
		}

		// Verify nodes include A, B, C, D but not E
		nodeIDs := extractNodeIDs(result.Nodes)
		if !containsID(nodeIDs, nodeA.ID) {
			t.Errorf("Result should contain node A")
		}
		if !containsID(nodeIDs, nodeD.ID) {
			t.Errorf("Result should contain node D")
		}
		if containsID(nodeIDs, nodeE.ID) {
			t.Errorf("Result should NOT contain node E at level 3")
		}
	})
}

// TestGetMultipleNodeNeighbors_CycleHandling tests that cycles don't cause infinite loops
func TestGetMultipleNodeNeighbors_CycleHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create a cycle: A -> B -> C -> A
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create cyclic edges: A -> B -> C -> A
	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)
	edgeCA := createTestEdge(t, nodeC.ID, nodeA.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC, edgeCA}, []*DbNode{nodeA, nodeB, nodeC}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Cycle handling at level 3 does not infinite loop", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 3, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have exactly 3 nodes (A, B, C) - no duplicates due to cycle
		if len(result.Nodes) != 3 {
			t.Errorf("GetMultipleNodeNeighbors(cycle, level=3) nodes count = %v, want 3", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		// Should have 3 edges
		if len(result.Edges) != 3 {
			t.Errorf("GetMultipleNodeNeighbors(cycle, level=3) edges count = %v, want 3", len(result.Edges))
		}

		// Verify all nodes are present
		nodeIDs := extractNodeIDs(result.Nodes)
		if !containsID(nodeIDs, nodeA.ID) || !containsID(nodeIDs, nodeB.ID) || !containsID(nodeIDs, nodeC.ID) {
			t.Errorf("Result should contain all cycle nodes (A, B, C)")
		}
	})

	t.Run("Cycle handling at level 2", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// At level 2: A -> B -> C, and C -> A (but A is already visited)
		// Should still have exactly 3 nodes
		if len(result.Nodes) != 3 {
			t.Errorf("GetMultipleNodeNeighbors(cycle, level=2) nodes count = %v, want 3", len(result.Nodes))
		}
	})
}

// TestGetMultipleNodeNeighbors_BidirectionalEdges tests bidirectional edge traversal
func TestGetMultipleNodeNeighbors_BidirectionalEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create: A <-> B -> C
	// From C with level 2, should reach A via B
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create edges: A -> B, B -> A (bidirectional), B -> C
	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBA := createTestEdge(t, nodeB.ID, nodeA.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBA, edgeBC}, []*DbNode{nodeA, nodeB, nodeC}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Level 2 from C reaches A through B", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeC.ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have C + B + A (3 nodes)
		if len(result.Nodes) != 3 {
			t.Errorf("GetMultipleNodeNeighbors(level=2) nodes count = %v, want 3", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		// Verify A is reachable from C at level 2
		nodeIDs := extractNodeIDs(result.Nodes)
		if !containsID(nodeIDs, nodeA.ID) {
			t.Errorf("Node A should be reachable from C at level 2")
		}
	})
}

// TestGetMultipleNodeNeighbors_MultipleStartingNodes tests starting from multiple nodes
func TestGetMultipleNodeNeighbors_MultipleStartingNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create two separate chains: A -> B and C -> D
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)
	nodeD := createTestNode(t, "service-d", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC, nodeD}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create edges: A -> B, C -> D (two separate chains)
	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeCD := createTestEdge(t, nodeC.ID, nodeD.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeCD}, []*DbNode{nodeA, nodeB, nodeC, nodeD}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Multiple starting nodes returns combined neighbors", func(t *testing.T) {
		// Start from both A and C
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID, nodeC.ID}, 1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have A + B + C + D (4 nodes)
		if len(result.Nodes) != 4 {
			t.Errorf("GetMultipleNodeNeighbors(multiple starts) nodes count = %v, want 4", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		// Should have 2 edges
		if len(result.Edges) != 2 {
			t.Errorf("GetMultipleNodeNeighbors(multiple starts) edges count = %v, want 2", len(result.Edges))
		}
	})
}

// TestGetMultipleNodeNeighbors_DefaultLevel tests that level defaults to 1
func TestGetMultipleNodeNeighbors_DefaultLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create: A -> B -> C
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC}, []*DbNode{nodeA, nodeB, nodeC}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Level 0 defaults to 1", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 0, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should behave like level 1 (A + B only)
		if len(result.Nodes) != 2 {
			t.Errorf("GetMultipleNodeNeighbors(level=0) nodes count = %v, want 2", len(result.Nodes))
		}
	})

	t.Run("Negative level defaults to 1", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, -1, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should behave like level 1 (A + B only)
		if len(result.Nodes) != 2 {
			t.Errorf("GetMultipleNodeNeighbors(level=-1) nodes count = %v, want 2", len(result.Nodes))
		}
	})
}

// TestGetMultipleNodeNeighbors_LevelCap tests that levels are capped at 3
func TestGetMultipleNodeNeighbors_LevelCap(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create: A -> B -> C -> D -> E -> F (5 hops)
	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)
	nodeD := createTestNode(t, "service-d", NodeTypeService, tenantID, accountID)
	nodeE := createTestNode(t, "service-e", NodeTypeService, tenantID, accountID)
	nodeF := createTestNode(t, "service-f", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC, nodeD, nodeE, nodeF}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)
	edgeCD := createTestEdge(t, nodeC.ID, nodeD.ID, RelationshipCalls, tenantID, accountID)
	edgeDE := createTestEdge(t, nodeD.ID, nodeE.ID, RelationshipCalls, tenantID, accountID)
	edgeEF := createTestEdge(t, nodeE.ID, nodeF.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC, edgeCD, edgeDE, edgeEF}, []*DbNode{nodeA, nodeB, nodeC, nodeD, nodeE, nodeF}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Level 10 is capped at 3", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 10, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should behave like level 3 (A + B + C + D only, not E or F)
		if len(result.Nodes) != 4 {
			t.Errorf("GetMultipleNodeNeighbors(level=10) nodes count = %v, want 4 (capped at level 3)", len(result.Nodes))
			logNodeIDs(t, result.Nodes)
		}

		nodeIDs := extractNodeIDs(result.Nodes)
		if containsID(nodeIDs, nodeE.ID) {
			t.Errorf("Node E should NOT be included (level > 3)")
		}
		if containsID(nodeIDs, nodeF.ID) {
			t.Errorf("Node F should NOT be included (level > 3)")
		}
	})
}

// TestGetMultipleNodeNeighbors_IsolatedNode tests handling of nodes with no edges
func TestGetMultipleNodeNeighbors_IsolatedNode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Create isolated node with no edges
	isolatedNode := createTestNode(t, "isolated-service", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{isolatedNode}, 0)
	if err != nil {
		t.Fatalf("Failed to save test node: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("Isolated node returns only itself", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{isolatedNode.ID}, 3, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors() error = %v", err)
		}

		// Should have only the isolated node
		if len(result.Nodes) != 1 {
			t.Errorf("GetMultipleNodeNeighbors(isolated) nodes count = %v, want 1", len(result.Nodes))
		}

		// Should have no edges
		if len(result.Edges) != 0 {
			t.Errorf("GetMultipleNodeNeighbors(isolated) edges count = %v, want 0", len(result.Edges))
		}
	})
}

// TestGetMultipleNodeNeighbors_TreeMode verifies that subgraph=false collapses the
// induced subgraph to a BFS spanning forest by dropping edges between same-depth
// (sibling) nodes. Uses the same cycle fixture as TestGetMultipleNodeNeighbors_CycleHandling.
//
// Topology: A -> B -> C -> A (3-node cycle).
// Seeded at A with levels=2:
//   - Subgraph mode (current default): all 3 edges returned (induced subgraph).
//   - Tree mode: B and C are both at min-depth 1 from A, so the B->C edge is a
//     sibling edge (|Δdepth|=0) and is dropped. Only A->B and C->A remain.
func TestGetMultipleNodeNeighbors_TreeMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	nodeA := createTestNode(t, "service-a", NodeTypeService, tenantID, accountID)
	nodeB := createTestNode(t, "service-b", NodeTypeService, tenantID, accountID)
	nodeC := createTestNode(t, "service-c", NodeTypeService, tenantID, accountID)

	err = service.SaveNodes([]*DbNode{nodeA, nodeB, nodeC}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}
	defer cleanupTestData(t, dbManager, tenantID)

	edgeAB := createTestEdge(t, nodeA.ID, nodeB.ID, RelationshipCalls, tenantID, accountID)
	edgeBC := createTestEdge(t, nodeB.ID, nodeC.ID, RelationshipCalls, tenantID, accountID)
	edgeCA := createTestEdge(t, nodeC.ID, nodeA.ID, RelationshipCalls, tenantID, accountID)

	err = service.SaveEdges([]*DbEdge{edgeAB, edgeBC, edgeCA}, []*DbNode{nodeA, nodeB, nodeC}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	t.Run("Subgraph mode returns all 3 cycle edges", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 2, nil, true)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(subgraph) error = %v", err)
		}
		if len(result.Nodes) != 3 {
			t.Errorf("subgraph mode nodes count = %v, want 3", len(result.Nodes))
		}
		if len(result.Edges) != 3 {
			t.Errorf("subgraph mode edges count = %v, want 3 (all cycle edges)", len(result.Edges))
		}
	})

	t.Run("Tree mode drops the sibling edge B<->C", func(t *testing.T) {
		result, err := service.GetMultipleNodeNeighbors(ctx, []string{nodeA.ID}, 2, nil, false)
		if err != nil {
			t.Fatalf("GetMultipleNodeNeighbors(tree) error = %v", err)
		}

		// Same node set as subgraph mode.
		if len(result.Nodes) != 3 {
			t.Errorf("tree mode nodes count = %v, want 3", len(result.Nodes))
		}

		// Strictly fewer edges than the cycle's 3 — B->C is a sibling edge.
		if len(result.Edges) != 2 {
			t.Errorf("tree mode edges count = %v, want 2 (sibling B<->C dropped)", len(result.Edges))
		}

		// The remaining edges must each touch the seed node A and not be the B<->C edge.
		for _, e := range result.Edges {
			if e.ID == edgeBC.ID {
				t.Errorf("tree mode unexpectedly kept sibling edge B->C (id=%s)", e.ID)
			}
			if e.SourceNodeID != nodeA.ID && e.DestinationNodeID != nodeA.ID {
				t.Errorf("tree mode edge %s does not connect to seed A", e.ID)
			}
		}
	})
}

// Helper functions

func createTestNode(t *testing.T, name string, nodeType NodeType, tenantID, accountID string) *DbNode {
	return &DbNode{
		ID:              uuid.New().String(),
		NodeType:        nodeType,
		UniqueKey:       fmt.Sprintf("test:%s:%s:%s:%s", accountID, "us-east-1", nodeType, name),
		Properties:      map[string]interface{}{"name": name},
		Labels:          map[string]string{"test": "true"},
		QueryAttributes: map[string]interface{}{},
		CloudAccountID:  accountID,
		TenantID:        tenantID,
		Level:           "Tenant",
		Source:          "test",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

func createTestEdge(t *testing.T, sourceID, destID string, relType RelationshipType, tenantID, accountID string) *DbEdge {
	return &DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      sourceID,
		DestinationNodeID: destID,
		RelationshipType:  relType,
		Properties:        map[string]interface{}{},
		CloudAccountID:    accountID,
		TenantID:          tenantID,
		Level:             "Tenant",
		Source:            "test",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
}

func extractNodeIDs(nodes []KgNode) []string {
	ids := make([]string, len(nodes))
	for i, node := range nodes {
		ids[i] = node.ID
	}
	return ids
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func logNodeIDs(t *testing.T, nodes []KgNode) {
	t.Logf("Returned %d nodes:", len(nodes))
	for _, node := range nodes {
		t.Logf("  - ID: %s, Type: %s", node.ID, node.NodeType)
	}
}
