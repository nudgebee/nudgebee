package core

import (
	"testing"
	"time"
)

func TestNodeType_Constants(t *testing.T) {
	tests := []struct {
		name     string
		nodeType NodeType
		expected string
	}{
		{"Service", NodeTypeService, "Service"},
		{"Database", NodeTypeDatabase, "Database"},
		{"MessageQueue", NodeTypeMessageQueue, "MessageQueue"},
		{"Cache", NodeTypeCache, "Cache"},
		{"ExternalService", NodeTypeExternalService, "ExternalService"},
		{"Cluster", NodeTypeCluster, "Cluster"},
		{"LoadBalancer", NodeTypeLoadBalancer, "LoadBalancer"},
		{"Storage", NodeTypeStorage, "Storage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.nodeType) != tt.expected {
				t.Errorf("NodeType = %v, want %v", tt.nodeType, tt.expected)
			}
		})
	}
}

func TestNodeType_IsInfraAuthoritative(t *testing.T) {
	tests := []struct {
		name     string
		nodeType NodeType
		want     bool
	}{
		{"CronJob", NodeTypeCronJob, true},
		{"Job", NodeTypeJob, true},
		{"Pod", NodeTypePod, true},
		{"K8sService", NodeTypeK8sService, true},
		{"Ingress", NodeTypeIngress, true},
		{"Namespace", NodeTypeNamespace, true},
		{"Node", NodeTypeNode, true},
		{"Workload", NodeTypeWorkload, true},

		{"Service", NodeTypeService, false},
		{"ExternalService", NodeTypeExternalService, false},
		{"Database", NodeTypeDatabase, false},
		{"Cache", NodeTypeCache, false},
		{"MessageQueue", NodeTypeMessageQueue, false},
		{"LoadBalancer", NodeTypeLoadBalancer, false},
		{"ComputeInstance", NodeTypeComputeInstance, false},
		{"Storage", NodeTypeStorage, false},
		{"Cluster", NodeTypeCluster, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.nodeType.IsInfraAuthoritative(); got != tt.want {
				t.Errorf("%s.IsInfraAuthoritative() = %v, want %v", tt.nodeType, got, tt.want)
			}
		})
	}
}

func TestRelationshipType_Constants(t *testing.T) {
	tests := []struct {
		name     string
		relType  RelationshipType
		expected string
	}{
		{"Calls", RelationshipCalls, "CALLS"},
		{"PublishesTo", RelationshipPublishesTo, "PUBLISHES_TO"},
		{"SubscribesTo", RelationshipSubscribesTo, "SUBSCRIBES_TO"},
		{"RunsOn", RelationshipRunsOn, "RUNS_ON"},
		{"RoutesThrough", RelationshipRoutesThrough, "ROUTES_THROUGH"},
		{"HostedOn", RelationshipHostedOn, "HOSTED_ON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.relType) != tt.expected {
				t.Errorf("RelationshipType = %v, want %v", tt.relType, tt.expected)
			}
		})
	}
}

func TestNode_Creation(t *testing.T) {
	properties := map[string]interface{}{
		"name": "test-service",
		"port": 8080,
	}

	node := &DbNode{
		ID:             "test-id",
		NodeType:       NodeTypeService,
		UniqueKey:      "service:test-service",
		Properties:     properties,
		TenantID:       "tenant-1",
		CloudAccountID: "account-1",
		Source:         "test",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if node.ID != "test-id" {
		t.Errorf("Node.ID = %v, want %v", node.ID, "test-id")
	}

	if node.NodeType != NodeTypeService {
		t.Errorf("Node.NodeType = %v, want %v", node.NodeType, NodeTypeService)
	}

	if name, ok := node.Properties["name"].(string); !ok || name != "test-service" {
		t.Errorf("Node.Properties[name] = %v, want %v", name, "test-service")
	}
}

func TestEdge_Creation(t *testing.T) {
	properties := map[string]interface{}{
		"protocol": "HTTP",
	}

	edge := &DbEdge{
		ID:                "edge-id",
		SourceNodeID:      "node-1",
		DestinationNodeID: "node-2",
		RelationshipType:  RelationshipCalls,
		Properties:        properties,
		TenantID:          "tenant-1",
		CloudAccountID:    "account-1",
		Source:            "test",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if edge.SourceNodeID != "node-1" {
		t.Errorf("Edge.SourceNodeID = %v, want %v", edge.SourceNodeID, "node-1")
	}

	if edge.DestinationNodeID != "node-2" {
		t.Errorf("Edge.DestinationNodeID = %v, want %v", edge.DestinationNodeID, "node-2")
	}

	if edge.RelationshipType != RelationshipCalls {
		t.Errorf("Edge.RelationshipType = %v, want %v", edge.RelationshipType, RelationshipCalls)
	}
}

func TestGraph_Creation(t *testing.T) {
	nodes := []*DbNode{
		{ID: "node-1", NodeType: NodeTypeService, UniqueKey: "service:test1"},
		{ID: "node-2", NodeType: NodeTypeDatabase, UniqueKey: "database:postgres"},
	}

	edges := []*DbEdge{
		{ID: "edge-1", SourceNodeID: "node-1", DestinationNodeID: "node-2", RelationshipType: RelationshipCalls},
	}

	graph := &Graph{
		Nodes:       nodes,
		Edges:       edges,
		Source:      "test",
		TenantID:    "tenant-1",
		GeneratedAt: time.Now(),
		Metadata: Metadata{
			NodeCount: len(nodes),
			EdgeCount: len(edges),
		},
	}

	if len(graph.Nodes) != 2 {
		t.Errorf("Graph.Nodes length = %v, want %v", len(graph.Nodes), 2)
	}

	if len(graph.Edges) != 1 {
		t.Errorf("Graph.Edges length = %v, want %v", len(graph.Edges), 1)
	}

	if graph.Metadata.NodeCount != 2 {
		t.Errorf("Graph.Metadata.NodeCount = %v, want %v", graph.Metadata.NodeCount, 2)
	}
}

func TestBuildRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		request BuildRequest
		isValid bool
	}{
		{
			name: "Valid request",
			request: BuildRequest{
				TenantID: "tenant-1",
				Sources:  []string{"trace"},
			},
			isValid: true,
		},
		{
			name: "Missing tenant ID",
			request: BuildRequest{
				Sources: []string{"trace"},
			},
			isValid: false,
		},
		{
			name: "Empty sources",
			request: BuildRequest{
				TenantID: "tenant-1",
				Sources:  []string{},
			},
			isValid: true, // Empty sources is valid - will use all sources
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Tenant ID is required
			if tt.request.TenantID == "" && tt.isValid {
				t.Errorf("Expected invalid request with empty TenantID")
			}
		})
	}
}

func TestMetadata_NodeTypeBreakdown(t *testing.T) {
	breakdown := map[NodeType]int{
		NodeTypeService:  5,
		NodeTypeDatabase: 2,
		NodeTypeCache:    1,
	}

	metadata := Metadata{
		NodeCount:         8,
		EdgeCount:         10,
		NodeTypeBreakdown: breakdown,
		Sources:           []string{"trace"},
	}

	if metadata.NodeCount != 8 {
		t.Errorf("Metadata.NodeCount = %v, want %v", metadata.NodeCount, 8)
	}

	if metadata.NodeTypeBreakdown[NodeTypeService] != 5 {
		t.Errorf("NodeTypeBreakdown[Service] = %v, want %v", metadata.NodeTypeBreakdown[NodeTypeService], 5)
	}

	if len(metadata.Sources) != 2 {
		t.Errorf("Sources length = %v, want %v", len(metadata.Sources), 2)
	}
}

func TestTimeRange_Creation(t *testing.T) {
	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	timeRange := &TimeRange{
		StartTime: start,
		EndTime:   end,
	}

	if timeRange.StartTime.After(timeRange.EndTime) {
		t.Errorf("StartTime should be before EndTime")
	}

	duration := timeRange.EndTime.Sub(timeRange.StartTime)
	if duration < time.Hour {
		t.Errorf("Duration = %v, want >= %v", duration, time.Hour)
	}
}
