package core

import (
	"testing"
	"time"
)

func TestGenerateNodeID(t *testing.T) {
	tests := []struct {
		name      string
		uniqueKey string
		wantEmpty bool
	}{
		{"Valid key", "service:test-service", false},
		{"Complex key", "database:postgres:us-east-1", false},
		{"Empty key", "", false}, // Should still generate ID
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := GenerateNodeID(tt.uniqueKey)
			if tt.wantEmpty && id != "" {
				t.Errorf("GenerateNodeID() = %v, want empty", id)
			}
			if !tt.wantEmpty && id == "" {
				t.Errorf("GenerateNodeID() returned empty, want non-empty")
			}

			// Test deterministic generation
			id2 := GenerateNodeID(tt.uniqueKey)
			if id != id2 {
				t.Errorf("GenerateNodeID() not deterministic: %v != %v", id, id2)
			}
		})
	}
}

func TestGenerateEdgeID(t *testing.T) {
	tests := []struct {
		name       string
		sourceID   string
		destID     string
		relType    RelationshipType
		wantUnique bool
	}{
		{"Standard edge", "node-1", "node-2", RelationshipCalls, true},
		{"Different relationship", "node-1", "node-2", RelationshipPublishesTo, true},
		{"Reversed nodes", "node-2", "node-1", RelationshipCalls, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := GenerateEdgeID(tt.sourceID, tt.destID, tt.relType)
			if id == "" {
				t.Errorf("GenerateEdgeID() returned empty")
			}

			// Test deterministic generation
			id2 := GenerateEdgeID(tt.sourceID, tt.destID, tt.relType)
			if id != id2 {
				t.Errorf("GenerateEdgeID() not deterministic: %v != %v", id, id2)
			}
		})
	}
}

func TestNewNode(t *testing.T) {
	// uniqueKey must already be a fully-built 6-part key with cloud_provider in
	// position 0; NewNode no longer prepends source.
	uniqueKey := "aws:account-1::Service::test-service"
	properties := map[string]interface{}{
		"name": "test-service",
		"port": 8080,
	}

	node := NewNode(NodeTypeService, uniqueKey, properties, "tenant-1", "account-1", "aws")

	if node.ID == "" {
		t.Errorf("NewNode() ID is empty")
	}

	if node.NodeType != NodeTypeService {
		t.Errorf("NewNode() NodeType = %v, want %v", node.NodeType, NodeTypeService)
	}

	if node.UniqueKey != uniqueKey {
		t.Errorf("NewNode() UniqueKey = %v, want %v", node.UniqueKey, uniqueKey)
	}

	if node.TenantID != "tenant-1" {
		t.Errorf("NewNode() TenantID = %v, want %v", node.TenantID, "tenant-1")
	}

	if node.Source != "aws" {
		t.Errorf("NewNode() Source = %v, want %v", node.Source, "aws")
	}
}

func TestNewEdge(t *testing.T) {
	properties := map[string]interface{}{
		"protocol": "HTTP",
	}

	edge := NewEdge("node-1", "node-2", RelationshipCalls, properties, "tenant-1", "account-1", "test")

	if edge.ID == "" {
		t.Errorf("NewEdge() ID is empty")
	}

	if edge.SourceNodeID != "node-1" {
		t.Errorf("NewEdge() SourceNodeID = %v, want %v", edge.SourceNodeID, "node-1")
	}

	if edge.DestinationNodeID != "node-2" {
		t.Errorf("NewEdge() DestinationNodeID = %v, want %v", edge.DestinationNodeID, "node-2")
	}

	if edge.RelationshipType != RelationshipCalls {
		t.Errorf("NewEdge() RelationshipType = %v, want %v", edge.RelationshipType, RelationshipCalls)
	}
}

func TestMergeProperties(t *testing.T) {
	existing := map[string]interface{}{
		"key1": "value1",
		"key2": "value2",
	}

	new := map[string]interface{}{
		"key2": "new-value2",
		"key3": "value3",
	}

	merged := MergeProperties(existing, new)

	if merged["key1"] != "value1" {
		t.Errorf("MergeProperties() key1 = %v, want %v", merged["key1"], "value1")
	}

	if merged["key2"] != "new-value2" {
		t.Errorf("MergeProperties() key2 = %v, want %v (should be overwritten)", merged["key2"], "new-value2")
	}

	if merged["key3"] != "value3" {
		t.Errorf("MergeProperties() key3 = %v, want %v", merged["key3"], "value3")
	}
}

func TestDeduplicateNodes(t *testing.T) {
	now := time.Now()
	later := now.Add(1 * time.Minute)

	nodes := []*DbNode{
		{
			ID:        "id-1",
			NodeType:  NodeTypeService,
			UniqueKey: "service:test",
			Properties: map[string]interface{}{
				"version": "1.0",
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "id-2", // Different ID but same unique key
			NodeType:  NodeTypeService,
			UniqueKey: "service:test",
			Properties: map[string]interface{}{
				"version": "2.0",
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: later,
		},
		{
			ID:        "id-3",
			NodeType:  NodeTypeDatabase,
			UniqueKey: "database:postgres",
			Properties: map[string]interface{}{
				"port": 5432,
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	deduplicated := DeduplicateNodes(nodes)

	if len(deduplicated) != 2 {
		t.Errorf("DeduplicateNodes() length = %v, want %v", len(deduplicated), 2)
	}

	// Find the service node
	var serviceNode *DbNode
	for _, node := range deduplicated {
		if node.NodeType == NodeTypeService {
			serviceNode = node
			break
		}
	}

	if serviceNode == nil {
		t.Fatalf("Service node not found after deduplication")
		return
	}

	// Should have merged properties and latest update time
	if serviceNode.Properties["version"] != "2.0" {
		t.Errorf("Merged node version = %v, want %v (should use newer value)", serviceNode.Properties["version"], "2.0")
	}

	if !serviceNode.UpdatedAt.Equal(later) {
		t.Errorf("Merged node UpdatedAt = %v, want %v (should use latest)", serviceNode.UpdatedAt, later)
	}
}

func TestDeduplicateNodesWithIDMapping(t *testing.T) {
	now := time.Now()
	later := now.Add(1 * time.Minute)

	// Simulate: AWS source creates node with ID "aws-1"
	// eBPF source later creates node with ID "ebpf-2" but same UniqueKey
	nodes := []*DbNode{
		{
			ID:        "aws-1",
			NodeType:  NodeTypeService,
			UniqueKey: "service:test",
			Properties: map[string]interface{}{
				"source": "aws",
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "ebpf-2", // Different ID but same unique key (simulates eBPF creating duplicate)
			NodeType:  NodeTypeService,
			UniqueKey: "service:test",
			Properties: map[string]interface{}{
				"source":   "ebpf",
				"language": "go",
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: later,
		},
		{
			ID:        "aws-3",
			NodeType:  NodeTypeDatabase,
			UniqueKey: "database:postgres",
			Properties: map[string]interface{}{
				"port": 5432,
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	deduplicated, idMapping := DeduplicateNodesWithIDMapping(nodes)

	// Should have 2 unique nodes
	if len(deduplicated) != 2 {
		t.Errorf("DeduplicateNodesWithIDMapping() length = %v, want %v", len(deduplicated), 2)
	}

	// ID mapping should map all original IDs
	if len(idMapping) != 3 {
		t.Errorf("ID mapping length = %v, want %v", len(idMapping), 3)
	}

	// aws-1 should map to itself (it's the surviving node)
	if idMapping["aws-1"] != "aws-1" {
		t.Errorf("idMapping[aws-1] = %v, want aws-1", idMapping["aws-1"])
	}

	// ebpf-2 should map to aws-1 (aws-1 was first with same UniqueKey)
	if idMapping["ebpf-2"] != "aws-1" {
		t.Errorf("idMapping[ebpf-2] = %v, want aws-1 (should map to surviving node)", idMapping["ebpf-2"])
	}

	// aws-3 should map to itself
	if idMapping["aws-3"] != "aws-3" {
		t.Errorf("idMapping[aws-3] = %v, want aws-3", idMapping["aws-3"])
	}

	// Verify the surviving service node has merged properties
	var serviceNode *DbNode
	for _, node := range deduplicated {
		if node.NodeType == NodeTypeService {
			serviceNode = node
			break
		}
	}

	if serviceNode == nil {
		t.Fatalf("Service node not found after deduplication")
		return
	}

	// Should keep the first node's ID
	if serviceNode.ID != "aws-1" {
		t.Errorf("Surviving node ID = %v, want aws-1", serviceNode.ID)
	}

	// Should have merged properties from both nodes
	if serviceNode.Properties["language"] != "go" {
		t.Errorf("Merged node should have language property from ebpf node")
	}

	// Should have latest update time
	if !serviceNode.UpdatedAt.Equal(later) {
		t.Errorf("Merged node UpdatedAt = %v, want %v (should use latest)", serviceNode.UpdatedAt, later)
	}
}

func TestDeduplicateEdges(t *testing.T) {
	now := time.Now()
	later := now.Add(1 * time.Minute)

	edges := []*DbEdge{
		{
			ID:                "edge-1",
			SourceNodeID:      "node-1",
			DestinationNodeID: "node-2",
			RelationshipType:  RelationshipCalls,
			Properties: map[string]interface{}{
				"count": 10,
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:                "edge-1", // Same edge ID
			SourceNodeID:      "node-1",
			DestinationNodeID: "node-2",
			RelationshipType:  RelationshipCalls,
			Properties: map[string]interface{}{
				"count": 20,
			},
			TenantID:  "tenant-1",
			CreatedAt: now,
			UpdatedAt: later,
		},
	}

	deduplicated := DeduplicateEdges(edges)

	if len(deduplicated) != 1 {
		t.Errorf("DeduplicateEdges() length = %v, want %v", len(deduplicated), 1)
	}

	if deduplicated[0].Properties["count"] != 20 {
		t.Errorf("Merged edge count = %v, want %v (should use newer value)", deduplicated[0].Properties["count"], 20)
	}
}

// TestDeduplicateEdgesWithPriorityMultiSource asserts that when two flow
// sources emit a CALLS edge between the same node pair, the surviving edge
// is the one whose Source has the higher priority (lower numeric value) in
// EdgeTypePriorities. This is the core invariant edge_priority.go enforces;
// without this test, a refactor of the priority lookup or composite-key shape
// could silently swap which source wins.
func TestDeduplicateEdgesWithPriorityMultiSource(t *testing.T) {
	type pairCase struct {
		name                  string
		sources               []string // arrival order
		expectSurvivor        string
		expectMergedFromLower bool
	}
	cases := []pairCase{
		{
			name:                  "k8s_beats_ebpf_higher_first",
			sources:               []string{"k8s", "ebpf"},
			expectSurvivor:        "k8s",
			expectMergedFromLower: true,
		},
		{
			name:                  "k8s_beats_ebpf_lower_first",
			sources:               []string{"ebpf", "k8s"},
			expectSurvivor:        "k8s",
			expectMergedFromLower: true,
		},
		{
			name:                  "ebpf_beats_traces",
			sources:               []string{"traces", "ebpf"},
			expectSurvivor:        "ebpf",
			expectMergedFromLower: true,
		},
		{
			name:                  "k8s_alone",
			sources:               []string{"k8s"},
			expectSurvivor:        "k8s",
			expectMergedFromLower: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edges := make([]*DbEdge, 0, len(tc.sources))
			for i, src := range tc.sources {
				edges = append(edges, &DbEdge{
					ID:                "edge-" + tc.name + "-" + src,
					SourceNodeID:      "node-A",
					DestinationNodeID: "node-B",
					RelationshipType:  RelationshipCalls,
					Source:            src,
					CloudAccountID:    "acct-1",
					TenantID:          "tenant-1",
					Properties: map[string]interface{}{
						"latency_ms":    100 + i,
						src + "_marker": true,
					},
				})
			}

			deduplicated := DeduplicateEdgesWithPriority(edges)
			if len(deduplicated) != 1 {
				t.Fatalf("DeduplicateEdgesWithPriority() length = %d, want 1", len(deduplicated))
			}
			survivor := deduplicated[0]
			if survivor.Source != tc.expectSurvivor {
				t.Errorf("survivor Source = %q, want %q", survivor.Source, tc.expectSurvivor)
			}

			contributing, _ := survivor.Properties["contributing_sources"].([]string)
			if len(contributing) == 0 {
				t.Errorf("contributing_sources missing on survivor")
			}

			if !tc.expectMergedFromLower {
				return
			}

			// At least one prefixed property from a lower-priority source must
			// be merged onto the survivor (latency_ms is in MetricsToMerge).
			foundPrefixed := false
			for _, src := range tc.sources {
				if src == tc.expectSurvivor {
					continue
				}
				if _, ok := survivor.Properties[src+"_latency_ms"]; ok {
					foundPrefixed = true
					break
				}
			}
			if !foundPrefixed {
				t.Errorf("expected at least one prefixed metric (e.g. ebpf_latency_ms) merged onto %s survivor; props=%v",
					tc.expectSurvivor, survivor.Properties)
			}
		})
	}
}

func TestValidateNode(t *testing.T) {
	tests := []struct {
		name    string
		node    *DbNode
		wantErr bool
	}{
		{
			name: "Valid node",
			node: &DbNode{
				NodeType:  NodeTypeService,
				UniqueKey: "service:test",
				TenantID:  "tenant-1",
			},
			wantErr: false,
		},
		{
			name:    "Nil node",
			node:    nil,
			wantErr: true,
		},
		{
			name: "Missing node type",
			node: &DbNode{
				UniqueKey: "service:test",
				TenantID:  "tenant-1",
			},
			wantErr: true,
		},
		{
			name: "Missing unique key",
			node: &DbNode{
				NodeType: NodeTypeService,
				TenantID: "tenant-1",
			},
			wantErr: true,
		},
		{
			name: "Missing tenant ID",
			node: &DbNode{
				NodeType:  NodeTypeService,
				UniqueKey: "service:test",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNode(tt.node)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEdge(t *testing.T) {
	tests := []struct {
		name    string
		edge    *DbEdge
		wantErr bool
	}{
		{
			name: "Valid edge",
			edge: &DbEdge{
				SourceNodeID:      "node-1",
				DestinationNodeID: "node-2",
				RelationshipType:  RelationshipCalls,
				TenantID:          "tenant-1",
			},
			wantErr: false,
		},
		{
			name:    "Nil edge",
			edge:    nil,
			wantErr: true,
		},
		{
			name: "Missing source node",
			edge: &DbEdge{
				DestinationNodeID: "node-2",
				RelationshipType:  RelationshipCalls,
				TenantID:          "tenant-1",
			},
			wantErr: true,
		},
		{
			name: "Missing destination node",
			edge: &DbEdge{
				SourceNodeID:     "node-1",
				RelationshipType: RelationshipCalls,
				TenantID:         "tenant-1",
			},
			wantErr: true,
		},
		{
			name: "Missing relationship type",
			edge: &DbEdge{
				SourceNodeID:      "node-1",
				DestinationNodeID: "node-2",
				TenantID:          "tenant-1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEdge(tt.edge)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEdge() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetNodeProperty(t *testing.T) {
	node := &DbNode{
		Properties: map[string]interface{}{
			"name": "test-service",
			"port": 8080,
		},
	}

	tests := []struct {
		name      string
		key       string
		wantValue interface{}
		wantOk    bool
	}{
		{"Existing string property", "name", "test-service", true},
		{"Existing int property", "port", 8080, true},
		{"Non-existing property", "missing", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, ok := GetNodeProperty(node, tt.key)
			if ok != tt.wantOk {
				t.Errorf("GetNodeProperty() ok = %v, want %v", ok, tt.wantOk)
			}
			if ok && val != tt.wantValue {
				t.Errorf("GetNodeProperty() value = %v, want %v", val, tt.wantValue)
			}
		})
	}
}

func TestSetNodeProperty(t *testing.T) {
	node := &DbNode{
		Properties: make(map[string]interface{}),
	}

	SetNodeProperty(node, "name", "test-service")
	SetNodeProperty(node, "port", 8080)

	if node.Properties["name"] != "test-service" {
		t.Errorf("SetNodeProperty() name = %v, want %v", node.Properties["name"], "test-service")
	}

	if node.Properties["port"] != 8080 {
		t.Errorf("SetNodeProperty() port = %v, want %v", node.Properties["port"], 8080)
	}
}
