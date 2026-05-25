package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestGetNodeNeighbors_Integration tests the GetNodeNeighbors method with actual database operations
// This test requires a database connection and is meant to be run as an integration test
func TestGetNodeNeighbors_Integration(t *testing.T) {
	// Skip this test if running in short mode (unit tests only)
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup: Initialize database connection
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	ctx := newTestRequestContext()
	service := NewService(ctx, slog.Default(), dbManager)

	// Test data
	tenantID := uuid.New().String()
	accountID := uuid.New().String()

	// Step 1: Create test nodes
	testNodes := []*DbNode{
		{
			ID:             uuid.New().String(),
			NodeType:       NodeTypeService,
			UniqueKey:      fmt.Sprintf("test:Service:api-service:%s", tenantID),
			Properties:     map[string]interface{}{"name": "api-service"},
			CloudAccountID: accountID,
			TenantID:       tenantID,
			Source:         "test",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ID:             uuid.New().String(),
			NodeType:       NodeTypeDatabase,
			UniqueKey:      fmt.Sprintf("test:Database:postgres:%s", tenantID),
			Properties:     map[string]interface{}{"name": "postgres"},
			CloudAccountID: accountID,
			TenantID:       tenantID,
			Source:         "test",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ID:             uuid.New().String(),
			NodeType:       NodeTypeService,
			UniqueKey:      fmt.Sprintf("test:Service:worker-service:%s", tenantID),
			Properties:     map[string]interface{}{"name": "worker-service"},
			CloudAccountID: accountID,
			TenantID:       tenantID,
			Source:         "test",
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	}

	// Save test nodes to database
	err = service.SaveNodes(testNodes, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	// Cleanup: Defer deletion of test data
	defer func() {
		cleanupTestData(t, dbManager, tenantID)
	}()

	// Step 2: Create test edges connecting the nodes
	testEdges := []*DbEdge{
		{
			ID:                uuid.New().String(),
			SourceNodeID:      testNodes[0].ID, // api-service -> postgres
			DestinationNodeID: testNodes[1].ID,
			RelationshipType:  RelationshipCalls,
			Properties:        map[string]interface{}{"protocol": "tcp"},
			CloudAccountID:    accountID,
			TenantID:          tenantID,
			Source:            "test",
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
		{
			ID:                uuid.New().String(),
			SourceNodeID:      testNodes[0].ID, // api-service -> worker-service
			DestinationNodeID: testNodes[2].ID,
			RelationshipType:  RelationshipCalls,
			Properties:        map[string]interface{}{"protocol": "http"},
			CloudAccountID:    accountID,
			TenantID:          tenantID,
			Source:            "test",
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	}

	// Save test edges to database
	err = service.SaveEdges(testEdges, testNodes, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	// Step 3: Test GetNodeNeighbors for the api-service node
	t.Run("GetNodeNeighbors returns target node and neighbors", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, testNodes[0].ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		// Verify we got the expected number of nodes (1 target + 2 neighbors)
		if len(result.Nodes) != 3 {
			t.Errorf("GetNodeNeighbors() nodes count = %v, want 3 (1 target + 2 neighbors)", len(result.Nodes))
		}

		// Verify we got the expected number of edges
		if len(result.Edges) != 2 {
			t.Errorf("GetNodeNeighbors() edges count = %v, want 2", len(result.Edges))
		}

		// Verify tenant ID and account ID
		if result.TenantID != tenantID {
			t.Errorf("GetNodeNeighbors() TenantID = %v, want %v", result.TenantID, tenantID)
		}

		if result.AccountID != accountID {
			t.Errorf("GetNodeNeighbors() AccountID = %v, want %v", result.AccountID, accountID)
		}
	})

	// Step 4: Test GetNodeNeighbors for a leaf node (postgres - has incoming edges only)
	t.Run("GetNodeNeighbors for leaf node with incoming edges", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, testNodes[1].ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		// Verify we got the target node + 1 neighbor (api-service)
		if len(result.Nodes) != 2 {
			t.Errorf("GetNodeNeighbors() nodes count = %v, want 2 (1 target + 1 neighbor)", len(result.Nodes))
		}

		// Verify we got 1 edge (api-service -> postgres)
		if len(result.Edges) != 1 {
			t.Errorf("GetNodeNeighbors() edges count = %v, want 1", len(result.Edges))
		}
	})

	// Step 5: Test GetNodeNeighbors for non-existent node
	t.Run("GetNodeNeighbors for non-existent node returns error", func(t *testing.T) {
		nonExistentID := uuid.New().String()
		_, err := service.GetNodeNeighbors(ctx, nonExistentID)
		if err == nil {
			t.Error("GetNodeNeighbors() with non-existent node_id should return error")
		}
		if err != nil && !contains(err.Error(), "node not found") {
			t.Errorf("GetNodeNeighbors() error = %v, want error containing 'node not found'", err)
		}
	})
}

// TestGetNodeNeighbors_BidirectionalEdges tests that the method correctly handles bidirectional relationships
func TestGetNodeNeighbors_BidirectionalEdges(t *testing.T) {
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

	// Create two nodes
	node1 := &DbNode{
		ID:             uuid.New().String(),
		NodeType:       NodeTypeService,
		UniqueKey:      fmt.Sprintf("test:Service:service-a:%s", tenantID),
		Properties:     map[string]interface{}{"name": "service-a"},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	node2 := &DbNode{
		ID:             uuid.New().String(),
		NodeType:       NodeTypeService,
		UniqueKey:      fmt.Sprintf("test:Service:service-b:%s", tenantID),
		Properties:     map[string]interface{}{"name": "service-b"},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Save nodes
	err = service.SaveNodes([]*DbNode{node1, node2}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create bidirectional edges
	edge1 := &DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      node1.ID,
		DestinationNodeID: node2.ID,
		RelationshipType:  RelationshipCalls,
		Properties:        map[string]interface{}{"direction": "forward"},
		CloudAccountID:    accountID,
		TenantID:          tenantID,
		Source:            "test",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	edge2 := &DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      node2.ID,
		DestinationNodeID: node1.ID,
		RelationshipType:  RelationshipCalls,
		Properties:        map[string]interface{}{"direction": "backward"},
		CloudAccountID:    accountID,
		TenantID:          tenantID,
		Source:            "test",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	err = service.SaveEdges([]*DbEdge{edge1, edge2}, []*DbNode{node1, node2}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edges: %v", err)
	}

	// Test from node1's perspective
	t.Run("GetNodeNeighbors handles bidirectional edges from node1", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, node1.ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		// Should have node1 + node2
		if len(result.Nodes) != 2 {
			t.Errorf("GetNodeNeighbors() nodes count = %v, want 2", len(result.Nodes))
		}

		// Should have both edges
		if len(result.Edges) != 2 {
			t.Errorf("GetNodeNeighbors() edges count = %v, want 2", len(result.Edges))
		}
	})

	// Test from node2's perspective - should get the same results
	t.Run("GetNodeNeighbors handles bidirectional edges from node2", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, node2.ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		// Should have node1 + node2
		if len(result.Nodes) != 2 {
			t.Errorf("GetNodeNeighbors() nodes count = %v, want 2", len(result.Nodes))
		}

		// Should have both edges
		if len(result.Edges) != 2 {
			t.Errorf("GetNodeNeighbors() edges count = %v, want 2", len(result.Edges))
		}
	})
}

// TestGetNodeNeighbors_NodeWithNoEdges tests that the method correctly handles isolated nodes
func TestGetNodeNeighbors_NodeWithNoEdges(t *testing.T) {
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

	// Create an isolated node (no edges)
	isolatedNode := &DbNode{
		ID:             uuid.New().String(),
		NodeType:       NodeTypeService,
		UniqueKey:      fmt.Sprintf("test:Service:isolated-service:%s", tenantID),
		Properties:     map[string]interface{}{"name": "isolated-service"},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err = service.SaveNodes([]*DbNode{isolatedNode}, 0)
	if err != nil {
		t.Fatalf("Failed to save test node: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("GetNodeNeighbors for isolated node returns only the node", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, isolatedNode.ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		// Should only have the isolated node itself
		if len(result.Nodes) != 1 {
			t.Errorf("GetNodeNeighbors() nodes count = %v, want 1 (isolated node only)", len(result.Nodes))
		}

		// Should have no edges
		if len(result.Edges) != 0 {
			t.Errorf("GetNodeNeighbors() edges count = %v, want 0", len(result.Edges))
		}

		// Verify it's the correct node
		if len(result.Nodes) > 0 && result.Nodes[0].ID != isolatedNode.ID {
			t.Errorf("GetNodeNeighbors() returned node ID = %v, want %v", result.Nodes[0].ID, isolatedNode.ID)
		}
	})
}

// TestGetNodeNeighbors_NodeProperties tests that node properties are correctly returned
func TestGetNodeNeighbors_NodeProperties(t *testing.T) {
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

	// Create node with specific properties
	testNode := &DbNode{
		ID:        uuid.New().String(),
		NodeType:  NodeTypeService,
		UniqueKey: fmt.Sprintf("test:Service:test-service:%s", tenantID),
		Properties: map[string]interface{}{
			"name":        "test-service",
			"version":     "1.0.0",
			"environment": "production",
			"tags":        []string{"api", "critical"},
		},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err = service.SaveNodes([]*DbNode{testNode}, 0)
	if err != nil {
		t.Fatalf("Failed to save test node: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	t.Run("GetNodeNeighbors preserves node properties", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, testNode.ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		if len(result.Nodes) != 1 {
			t.Fatalf("GetNodeNeighbors() nodes count = %v, want 1", len(result.Nodes))
		}

		returnedNode := result.Nodes[0]

		// Verify node type
		if returnedNode.NodeType != NodeTypeService {
			t.Errorf("GetNodeNeighbors() node type = %v, want %v", returnedNode.NodeType, NodeTypeService)
		}

		// Verify source is extracted correctly
		if returnedNode.Source != "test" {
			t.Errorf("GetNodeNeighbors() node source = %v, want 'test'", returnedNode.Source)
		}

		// Note: Properties are stored in the Labels field in KgNode
		// Verify that labels/properties are present
		if returnedNode.Labels == nil {
			t.Error("GetNodeNeighbors() node labels should not be nil")
		}
	})
}

// cleanupTestData removes all test data for a given tenant from the database
func cleanupTestData(t *testing.T, dbManager *database.DatabaseManager, tenantID string) {
	// Delete test edges
	_, err := dbManager.Db.Exec(
		"DELETE FROM knowledge_graph_edge WHERE tenant_id = $1",
		tenantID)
	if err != nil {
		t.Logf("Warning: Failed to cleanup test edges: %v", err)
	}

	// Delete test nodes
	_, err = dbManager.Db.Exec(
		"DELETE FROM knowledge_graph_node WHERE tenant_id = $1",
		tenantID)
	if err != nil {
		t.Logf("Warning: Failed to cleanup test nodes: %v", err)
	}
}

// TestGetNodeNeighbors_EdgeProperties tests that edge properties are correctly returned
func TestGetNodeNeighbors_EdgeProperties(t *testing.T) {
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

	// Create two nodes
	node1 := &DbNode{
		ID:             uuid.New().String(),
		NodeType:       NodeTypeService,
		UniqueKey:      fmt.Sprintf("test:Service:service-1:%s", tenantID),
		Properties:     map[string]interface{}{"name": "service-1"},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	node2 := &DbNode{
		ID:             uuid.New().String(),
		NodeType:       NodeTypeDatabase,
		UniqueKey:      fmt.Sprintf("test:Database:db-1:%s", tenantID),
		Properties:     map[string]interface{}{"name": "db-1"},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err = service.SaveNodes([]*DbNode{node1, node2}, 0)
	if err != nil {
		t.Fatalf("Failed to save test nodes: %v", err)
	}

	defer cleanupTestData(t, dbManager, tenantID)

	// Create edge with specific properties
	testEdge := &DbEdge{
		ID:                uuid.New().String(),
		SourceNodeID:      node1.ID,
		DestinationNodeID: node2.ID,
		RelationshipType:  RelationshipCalls,
		Properties: map[string]interface{}{
			"protocol":   "postgresql",
			"port":       5432,
			"latency_ms": 12.5,
			"requests":   1000,
			"error_rate": 0.01,
		},
		CloudAccountID: accountID,
		TenantID:       tenantID,
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err = service.SaveEdges([]*DbEdge{testEdge}, []*DbNode{node1, node2}, 1)
	if err != nil {
		t.Fatalf("Failed to save test edge: %v", err)
	}

	t.Run("GetNodeNeighbors preserves edge properties", func(t *testing.T) {
		result, err := service.GetNodeNeighbors(ctx, node1.ID)
		if err != nil {
			t.Fatalf("GetNodeNeighbors() error = %v", err)
		}

		if len(result.Edges) != 1 {
			t.Fatalf("GetNodeNeighbors() edges count = %v, want 1", len(result.Edges))
		}

		returnedEdge := result.Edges[0]

		// Verify edge relationship type
		if returnedEdge.RelationshipType != RelationshipCalls {
			t.Errorf("GetNodeNeighbors() edge type = %v, want %v",
				returnedEdge.RelationshipType, RelationshipCalls)
		}

		// Verify edge properties are present
		if returnedEdge.Properties == nil {
			t.Fatal("GetNodeNeighbors() edge properties should not be nil")
			return
		}

		// Verify specific property values
		if protocol, ok := returnedEdge.Properties["protocol"].(string); !ok || protocol != "postgresql" {
			t.Errorf("GetNodeNeighbors() edge protocol = %v, want 'postgresql'",
				returnedEdge.Properties["protocol"])
		}

		// Verify numeric properties (they may be returned as different numeric types)
		if port, ok := returnedEdge.Properties["port"]; !ok {
			t.Error("GetNodeNeighbors() edge should have 'port' property")
		} else {
			// Convert to JSON and back to normalize numeric types
			portJSON, _ := json.Marshal(port)
			var portVal float64
			if err := json.Unmarshal(portJSON, &portVal); err != nil {
				t.Errorf("GetNodeNeighbors() failed to unmarshal port: %v", err)
			} else if portVal != 5432 {
				t.Errorf("GetNodeNeighbors() edge port = %v, want 5432", portVal)
			}
		}
	})
}
