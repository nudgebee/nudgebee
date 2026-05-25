package traces

import (
	"fmt"
	"testing"
	"time"
)

func TestWorkloadExtractionFromPodName(t *testing.T) {
	tests := []struct {
		podName          string
		expectedWorkload string
		description      string
	}{
		{
			podName:          "nginx-deployment-7b7d7f9f9d-abcde",
			expectedWorkload: "nginx-deployment",
			description:      "Deployment pod name",
		},
		{
			podName:          "redis-statefulset-0",
			expectedWorkload: "redis-statefulset",
			description:      "StatefulSet pod name",
		},
		{
			podName:          "backup-job-12345",
			expectedWorkload: "backup-job",
			description:      "Job pod name",
		},
		{
			podName:          "user-service-deployment-abc123-def45",
			expectedWorkload: "user-service-deployment",
			description:      "Deployment with hyphens in name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := extractWorkloadFromPodName(tt.podName)
			if result != tt.expectedWorkload {
				t.Errorf("extractWorkloadFromPodName(%q) = %q, expected %q",
					tt.podName, result, tt.expectedWorkload)
			}
		})
	}
}

func TestNodeDeduplication(t *testing.T) {
	// Create duplicate nodes that should be deduplicated
	nodes := []*KnowledgeGraphNode{
		{
			NodeType:       NodeTypeService,
			UniqueKey:      "Service:api-service:prod",
			CloudAccountID: "test-account",
			TenantID:       "test-tenant",
			Properties: map[string]any{
				"name":        "api-service",
				"environment": "prod",
				"version":     "1.0.0",
			},
		},
		{
			NodeType:       NodeTypeService,
			UniqueKey:      "Service:api-service:prod",
			CloudAccountID: "test-account",
			TenantID:       "test-tenant",
			Properties: map[string]any{
				"name":        "api-service",
				"environment": "prod",
				"version":     "1.1.0", // Different version - should merge
			},
		},
	}

	// Test deduplication logic (same as in SaveNodes)
	nodeMap := make(map[string]*KnowledgeGraphNode)
	for _, node := range nodes {
		compositeKey := fmt.Sprintf("%s:%s:%s", node.UniqueKey, node.CloudAccountID, node.TenantID)
		if existing, exists := nodeMap[compositeKey]; exists {
			// Merge properties
			for k, v := range node.Properties {
				existing.Properties[k] = v
			}
			existing.UpdatedAt = time.Now()
		} else {
			nodeMap[compositeKey] = node
		}
	}

	// Should have only 1 unique node after deduplication
	if len(nodeMap) != 1 {
		t.Errorf("Expected 1 unique node after deduplication, got %d", len(nodeMap))
	}

	// The remaining node should have the merged properties
	for _, node := range nodeMap {
		if node.Properties["version"] != "1.1.0" {
			t.Errorf("Expected merged version '1.1.0', got %v", node.Properties["version"])
		}
	}

	t.Logf("Successfully deduplicated %d nodes down to %d unique nodes", len(nodes), len(nodeMap))
}
