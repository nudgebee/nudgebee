package sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"os"
	"testing"
)

// TestGenerateUniqueKeyForK8sResources tests the GenerateUniqueKey function for K8s resources
func TestGenerateUniqueKeyForK8sResources(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewK8sSource(K8sSourceConfig{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}, logger)
	if err != nil {
		t.Fatalf("Failed to create K8sSource: %v", err)
	}

	tests := []struct {
		name    string
		node    *core.DbNode
		wantKey string
	}{
		{
			name: "K8sService node",
			node: &core.DbNode{
				NodeType: core.NodeTypeK8sService,
				Properties: map[string]interface{}{
					"name":      "my-service",
					"namespace": "default",
					"cluster":   "test-cluster",
				},
			},
			wantKey: "k8s:test-cluster:none:K8sService:default:my-service",
		},
		{
			name: "ConfigMap node",
			node: &core.DbNode{
				NodeType: core.NodeTypeConfigMap,
				Properties: map[string]interface{}{
					"name":      "my-config",
					"namespace": "production",
					"cluster":   "prod-cluster",
				},
			},
			wantKey: "k8s:prod-cluster:none:ConfigMap:production:my-config",
		},
		{
			name: "K8sSecret node",
			node: &core.DbNode{
				NodeType: core.NodeTypeK8sSecret,
				Properties: map[string]interface{}{
					"name":      "my-secret",
					"namespace": "staging",
					"cluster":   "stage-cluster",
				},
			},
			wantKey: "k8s:stage-cluster:none:K8sSecret:staging:my-secret",
		},
		{
			name: "PVC node",
			node: &core.DbNode{
				NodeType: core.NodeTypePVC,
				Properties: map[string]interface{}{
					"name":      "data-pvc",
					"namespace": "default",
					"cluster":   "test-cluster",
				},
			},
			wantKey: "k8s:test-cluster:none:PersistentVolumeClaim:default:data-pvc",
		},
		{
			name: "PV node (cluster-scoped, no namespace)",
			node: &core.DbNode{
				NodeType: core.NodeTypePV,
				Properties: map[string]interface{}{
					"name":    "pv-volume",
					"cluster": "test-cluster",
				},
			},
			wantKey: "k8s:test-cluster:none:PersistentVolume:none:pv-volume",
		},
		{
			name: "Ingress node",
			node: &core.DbNode{
				NodeType: core.NodeTypeIngress,
				Properties: map[string]interface{}{
					"name":      "web-ingress",
					"namespace": "production",
					"cluster":   "prod-cluster",
				},
			},
			wantKey: "k8s:prod-cluster:none:Ingress:production:web-ingress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := source.GenerateUniqueKey(tt.node)

			if key != tt.wantKey {
				t.Errorf("GenerateUniqueKey() = %v, want %v", key, tt.wantKey)
			}

			// Test deterministic generation
			key2 := source.GenerateUniqueKey(tt.node)
			if key != key2 {
				t.Errorf("GenerateUniqueKey() not deterministic: %v != %v", key, key2)
			}
		})
	}
}

// TestConvertWorkloadsToGraph tests basic workload conversion without Helm
func TestConvertWorkloadsToGraph(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewK8sSource(K8sSourceConfig{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}, logger)
	if err != nil {
		t.Fatalf("Failed to create K8sSource: %v", err)
	}

	// Create test workloads
	workloads := []K8sWorkloadRow{
		{
			ID:          "workload-1",
			Kind:        "Deployment",
			Name:        "deployment-1",
			Namespace:   "default",
			ClusterName: "cluster-1",
			IsActive:    true,
		},
		{
			ID:          "workload-2",
			Kind:        "StatefulSet",
			Name:        "statefulset-1",
			Namespace:   "production",
			ClusterName: "cluster-1",
			IsActive:    true,
		},
	}

	k8sNodeMap := make(map[string]*core.DbNode)
	req := &core.SourceBuildRequest{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}
	nodes, edges, _, _, _ := source.convertWorkloadsToGraph(workloads, &k8sNodeMap, req)

	// Verify we have workload, namespace, and cluster nodes
	var workloadCount, namespaceCount, clusterCount int
	for _, node := range nodes {
		switch node.NodeType {
		case core.NodeTypeWorkload:
			workloadCount++
		case core.NodeTypeNamespace:
			namespaceCount++
		case core.NodeTypeCluster:
			clusterCount++
		}
	}

	if workloadCount != 2 {
		t.Errorf("Expected 2 workload nodes, got %d", workloadCount)
	}
	if namespaceCount != 2 {
		t.Errorf("Expected 2 namespace nodes, got %d", namespaceCount)
	}
	if clusterCount != 1 {
		t.Errorf("Expected 1 cluster node, got %d", clusterCount)
	}

	// Verify edges exist
	if len(edges) == 0 {
		t.Error("Expected edges to be created between nodes")
	}

	// Verify all nodes have required properties
	for _, node := range nodes {
		if node.TenantID != source.config.TenantID {
			t.Errorf("Node TenantID = %v, want %v", node.TenantID, source.config.TenantID)
		}
		if node.CloudAccountID != source.config.CloudAccountID {
			t.Errorf("Node CloudAccountID = %v, want %v", node.CloudAccountID, source.config.CloudAccountID)
		}
		if node.Source != "k8s" {
			t.Errorf("Node Source = %v, want %v", node.Source, "k8s")
		}
	}
}

// TestParseK8sPVsResponse_NestedJSON tests parsing of PVs with nested JSON wrapper
func TestParseK8sPVsResponse_NestedJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewK8sSource(K8sSourceConfig{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}, logger)
	if err != nil {
		t.Fatalf("Failed to create K8sSource: %v", err)
	}

	// Mock response with nested JSON structure (as provided by user)
	response := map[string]interface{}{
		"action":      "response",
		"request_id":  "1767255404431511157",
		"status_code": 200,
		"data": map[string]interface{}{
			"success": true,
			"findings": []interface{}{
				map[string]interface{}{
					"id":           "5010911f-f09e-4ee7-9232-5124aec99e94",
					"title":        "NudgeBee notification",
					"finding_type": "issue",
					"evidence": []interface{}{
						map[string]interface{}{
							"issue_id":  "5010911f-f09e-4ee7-9232-5124aec99e94",
							"file_type": "structured_data",
							// Nested JSON structure with wrapper
							"data":       `[{"type": "json", "data": "[{\"api_version\": null, \"kind\": null, \"metadata\": {\"name\": \"pvc-test-volume\", \"namespace\": null, \"uid\": \"test-uid-123\"}, \"spec\": {\"capacity\": {\"storage\": \"50Gi\"}, \"storage_class_name\": \"gp2\"}, \"status\": {\"phase\": \"Bound\"}}]", "additional_info": {}}]`,
							"account_id": "",
						},
					},
				},
			},
		},
		"output_type": "actions",
	}

	pvs, err := source.parseK8sPVsResponse(response)
	if err != nil {
		t.Fatalf("parseK8sPVsResponse() failed: %v", err)
	}

	if len(pvs) != 1 {
		t.Errorf("Expected 1 PV, got %d", len(pvs))
	}

	if len(pvs) > 0 {
		pv := pvs[0]
		if pv.Metadata.Name != "pvc-test-volume" {
			t.Errorf("Expected PV name 'pvc-test-volume', got '%s'", pv.Metadata.Name)
		}
		if pv.Metadata.UID != "test-uid-123" {
			t.Errorf("Expected PV UID 'test-uid-123', got '%s'", pv.Metadata.UID)
		}
		if pv.Status.Phase != "Bound" {
			t.Errorf("Expected PV phase 'Bound', got '%s'", pv.Status.Phase)
		}
	}
}

// TestParseK8sPVsResponse_DirectJSON tests parsing of PVs with direct JSON array (backward compatibility)
func TestParseK8sPVsResponse_DirectJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewK8sSource(K8sSourceConfig{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}, logger)
	if err != nil {
		t.Fatalf("Failed to create K8sSource: %v", err)
	}

	// Mock response with direct JSON array (legacy format)
	response := map[string]interface{}{
		"action":      "response",
		"request_id":  "test-request-id",
		"status_code": 200,
		"data": map[string]interface{}{
			"success": true,
			"findings": []interface{}{
				map[string]interface{}{
					"id":           "test-finding-id",
					"title":        "Test finding",
					"finding_type": "issue",
					"evidence": []interface{}{
						map[string]interface{}{
							"issue_id":  "test-issue-id",
							"file_type": "structured_data",
							// Direct JSON array (no wrapper)
							"data":       `[{"api_version": null, "kind": null, "metadata": {"name": "direct-pv", "namespace": null, "uid": "direct-uid-456"}, "spec": {"capacity": {"storage": "100Gi"}, "storage_class_name": "gp3"}, "status": {"phase": "Available"}}]`,
							"account_id": "",
						},
					},
				},
			},
		},
		"output_type": "actions",
	}

	pvs, err := source.parseK8sPVsResponse(response)
	if err != nil {
		t.Fatalf("parseK8sPVsResponse() failed: %v", err)
	}

	if len(pvs) != 1 {
		t.Errorf("Expected 1 PV, got %d", len(pvs))
	}

	if len(pvs) > 0 {
		pv := pvs[0]
		if pv.Metadata.Name != "direct-pv" {
			t.Errorf("Expected PV name 'direct-pv', got '%s'", pv.Metadata.Name)
		}
		if pv.Metadata.UID != "direct-uid-456" {
			t.Errorf("Expected PV UID 'direct-uid-456', got '%s'", pv.Metadata.UID)
		}
		if pv.Status.Phase != "Available" {
			t.Errorf("Expected PV phase 'Available', got '%s'", pv.Status.Phase)
		}
	}
}

// TestParseK8sPVCsResponse_NestedJSON tests parsing of PVCs with nested JSON wrapper
func TestParseK8sPVCsResponse_NestedJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, err := NewK8sSource(K8sSourceConfig{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}, logger)
	if err != nil {
		t.Fatalf("Failed to create K8sSource: %v", err)
	}

	// Mock response with nested JSON structure
	response := map[string]interface{}{
		"action":      "response",
		"request_id":  "test-request-id",
		"status_code": 200,
		"data": map[string]interface{}{
			"success": true,
			"findings": []interface{}{
				map[string]interface{}{
					"id":           "test-finding-id",
					"title":        "Test PVC finding",
					"finding_type": "issue",
					"evidence": []interface{}{
						map[string]interface{}{
							"issue_id":  "test-issue-id",
							"file_type": "structured_data",
							// Nested JSON structure with wrapper
							"data":       `[{"type": "json", "data": "[{\"api_version\": \"v1\", \"kind\": \"PersistentVolumeClaim\", \"metadata\": {\"name\": \"test-pvc\", \"namespace\": \"default\", \"uid\": \"pvc-uid-789\"}, \"spec\": {\"access_modes\": [\"ReadWriteOnce\"], \"resources\": {\"requests\": {\"storage\": \"10Gi\"}}, \"storage_class_name\": \"gp2\"}, \"status\": {\"phase\": \"Bound\"}}]", "additional_info": {}}]`,
							"account_id": "",
						},
					},
				},
			},
		},
		"output_type": "actions",
	}

	pvcs, err := source.parseK8sPVCsResponse(response)
	if err != nil {
		t.Fatalf("parseK8sPVCsResponse() failed: %v", err)
	}

	if len(pvcs) != 1 {
		t.Errorf("Expected 1 PVC, got %d", len(pvcs))
	}

	if len(pvcs) > 0 {
		pvc := pvcs[0]
		if pvc.Metadata.Name != "test-pvc" {
			t.Errorf("Expected PVC name 'test-pvc', got '%s'", pvc.Metadata.Name)
		}
		if pvc.Metadata.Namespace != "default" {
			t.Errorf("Expected PVC namespace 'default', got '%s'", pvc.Metadata.Namespace)
		}
		if pvc.Metadata.UID != "pvc-uid-789" {
			t.Errorf("Expected PVC UID 'pvc-uid-789', got '%s'", pvc.Metadata.UID)
		}
		if pvc.Status.Phase != "Bound" {
			t.Errorf("Expected PVC phase 'Bound', got '%s'", pvc.Status.Phase)
		}
	}
}
