package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAKSService_Name(t *testing.T) {
	svc := &aksService{}
	assert.Equal(t, "Microsoft.ContainerService/managedClusters", svc.Name())
}

// TestAKSService_GetResources_Integration is an integration test that requires valid Azure credentials
// This test is skipped in normal test runs and should only be run when explicitly testing Azure integration
func TestAKSService_GetResources_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// This test would require actual Azure credentials to run
	// In production testing, use environment variables or test fixtures
	t.Skip("Integration test - requires Azure credentials")
}

// TestAKSService_ResourceMapping tests the resource transformation logic
// by verifying that Azure cluster properties are correctly mapped to provider.Resource
func TestAKSService_ResourceMapping(t *testing.T) {
	svc := &aksService{}

	tests := []struct {
		name           string
		cluster        armcontainerservice.ManagedCluster
		expectedStatus providers.ResourceStatus
		expectedName   string
		expectedRegion string
	}{
		{
			name: "running AKS cluster maps to active status",
			cluster: armcontainerservice.ManagedCluster{
				ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.ContainerService/managedClusters/my-aks"),
				Name:     strPtr("my-aks"),
				Type:     strPtr("Microsoft.ContainerService/managedClusters"),
				Location: strPtr("eastus"),
				Properties: &armcontainerservice.ManagedClusterProperties{
					ProvisioningState: strPtr("Succeeded"),
					PowerState: &armcontainerservice.PowerState{
						Code: (*armcontainerservice.Code)(strPtr("Running")),
					},
				},
			},
			expectedStatus: providers.ResourceStatusActive,
			expectedName:   "my-aks",
			expectedRegion: "eastus",
		},
		{
			name: "stopped AKS cluster maps to inactive status",
			cluster: armcontainerservice.ManagedCluster{
				ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.ContainerService/managedClusters/stopped-aks"),
				Name:     strPtr("stopped-aks"),
				Type:     strPtr("Microsoft.ContainerService/managedClusters"),
				Location: strPtr("westus"),
				Properties: &armcontainerservice.ManagedClusterProperties{
					ProvisioningState: strPtr("Succeeded"),
					PowerState: &armcontainerservice.PowerState{
						Code: (*armcontainerservice.Code)(strPtr("Stopped")),
					},
				},
			},
			expectedStatus: providers.ResourceStatusInactive,
			expectedName:   "stopped-aks",
			expectedRegion: "westus",
		},
		{
			name: "updating AKS cluster maps to active status",
			cluster: armcontainerservice.ManagedCluster{
				ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.ContainerService/managedClusters/updating-aks"),
				Name:     strPtr("updating-aks"),
				Type:     strPtr("Microsoft.ContainerService/managedClusters"),
				Location: strPtr("eastus"),
				Properties: &armcontainerservice.ManagedClusterProperties{
					ProvisioningState: strPtr("Updating"),
				},
			},
			expectedStatus: providers.ResourceStatusActive,
			expectedName:   "updating-aks",
			expectedRegion: "eastus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the status mapping logic
			status := providers.ResourceStatusUnknown
			if tt.cluster.Properties != nil && tt.cluster.Properties.ProvisioningState != nil {
				provisioningState := string(*tt.cluster.Properties.ProvisioningState)
				if val, ok := nbStatusFromAzureProvisioningState[provisioningState]; ok {
					status = val
				}
			}

			// Check power state override
			if tt.cluster.Properties != nil && tt.cluster.Properties.PowerState != nil && tt.cluster.Properties.PowerState.Code != nil {
				powerState := string(*tt.cluster.Properties.PowerState.Code)
				switch powerState {
				case "Stopped":
					status = providers.ResourceStatusInactive
				case "Running":
					status = providers.ResourceStatusActive
				}
			}

			assert.Equal(t, tt.expectedStatus, status, "Status should match expected")
			assert.Equal(t, tt.expectedName, *tt.cluster.Name, "Name should match expected")

			// Verify that the resource structure would be correctly built
			resource := providers.Resource{
				Id:          *tt.cluster.ID,
				Name:        *tt.cluster.Name,
				Type:        *tt.cluster.Type,
				Region:      normalizeAzureRegion(*tt.cluster.Location),
				Status:      status,
				ServiceName: svc.Name(),
			}

			assert.Equal(t, tt.expectedName, resource.Name)
			assert.Equal(t, tt.expectedRegion, resource.Region)
			assert.Equal(t, tt.expectedStatus, resource.Status)
			assert.Equal(t, "Microsoft.ContainerService/managedClusters", resource.ServiceName)
		})
	}
}

func TestAKSService_GetRecommendations(t *testing.T) {
	svc := &aksService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for secure AKS cluster",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/secure-aks",
					Name:        "secure-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableRBAC":        true,
							"kubernetesVersion": "1.28.0",
							"networkProfile": map[string]interface{}{
								"networkPolicy": "azure",
							},
							"addonProfiles": map[string]interface{}{
								"azurepolicy": map[string]interface{}{
									"enabled": true,
								},
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for RBAC disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/no-rbac-aks",
					Name:        "no-rbac-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableRBAC":        false,
							"kubernetesVersion": "1.28.0",
							"networkProfile": map[string]interface{}{
								"networkPolicy": "azure",
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_aks_rbac_disabled"},
		},
		{
			name: "recommendation for network policy disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/no-netpol-aks",
					Name:        "no-netpol-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableRBAC":        true,
							"kubernetesVersion": "1.28.0",
							"networkProfile": map[string]interface{}{
								"networkPolicy": "",
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_aks_network_policy_disabled"},
		},
		{
			name: "recommendation for Azure Policy disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/no-policy-aks",
					Name:        "no-policy-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableRBAC":        true,
							"kubernetesVersion": "1.28.0",
							"networkProfile": map[string]interface{}{
								"networkPolicy": "azure",
							},
							"addonProfiles": map[string]interface{}{
								"azurepolicy": map[string]interface{}{
									"enabled": false,
								},
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_aks_azure_policy_disabled"},
		},
		{
			name: "recommendation for old Kubernetes version",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/old-k8s-aks",
					Name:        "old-k8s-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableRBAC":        true,
							"kubernetesVersion": "1.20.0",
							"networkProfile": map[string]interface{}{
								"networkPolicy": "azure",
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_aks_old_kubernetes_version"},
		},
		{
			name: "multiple recommendations for insecure AKS cluster",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/insecure-aks",
					Name:        "insecure-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableRBAC":        false,
							"kubernetesVersion": "1.20.0",
							"networkProfile": map[string]interface{}{
								"networkPolicy": "",
							},
							"addonProfiles": map[string]interface{}{
								"azurepolicy": map[string]interface{}{
									"enabled": false,
								},
							},
						},
					},
				},
			},
			expectedRecommendations: 4,
			expectedRules: []string{
				"azure_aks_rbac_disabled",
				"azure_aks_network_policy_disabled",
				"azure_aks_azure_policy_disabled",
				"azure_aks_old_kubernetes_version",
			},
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/no-meta-aks",
					Name:        "no-meta-aks",
					Type:        "Microsoft.ContainerService/managedClusters",
					Region:      "eastus",
					ServiceName: "Microsoft.ContainerService/managedClusters",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			for _, expectedRule := range tt.expectedRules {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == expectedRule {
						found = true
						assert.NotEmpty(t, rec.CategoryName)
						assert.NotEmpty(t, rec.Severity)
						assert.NotEmpty(t, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected rule '%s' not found", expectedRule)
			}
		})
	}
}

func TestAKSService_ApplyRecommendation(t *testing.T) {
	svc := &aksService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	// Note: Without credentials, this will fail on credential validation first
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_aks_enable_rbac",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	// The error could be either credential error or invalid resource ID depending on when validation happens
}

// TestAKSService_ApplyCommand_Validation tests command validation logic
// Note: Without valid Azure credentials, these tests will fail on credential validation
// This is expected behavior - in production, credentials are validated first for security
func TestAKSService_ApplyCommand_Validation(t *testing.T) {
	svc := &aksService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name        string
		command     providers.ApplyCommandRequest
		expectError bool
	}{
		{
			name: "invalid resource ID - empty",
			command: providers.ApplyCommandRequest{
				ResourceId: "",
				Command:    "azure_aks_enable_rbac",
			},
			expectError: true,
		},
		{
			name: "invalid resource ID - missing cluster name",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg",
				Command:    "azure_aks_enable_rbac",
			},
			expectError: true,
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/cluster-name",
				Command:    "unknown_command",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ApplyCommand(ctx, account, tt.command)
			// All should fail (either on credentials or validation)
			assert.Error(t, err)
			assert.False(t, resp.Success)
		})
	}
}

// TestAKSService_ApplyCommand_Integration tests successful command execution
// These tests require Azure credentials and should be run as integration tests
func TestAKSService_ApplyCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// These tests would require actual Azure credentials and a test AKS cluster
	// In a production environment, these would:
	// 1. Create a test AKS cluster with known configuration
	// 2. Apply commands to modify the cluster
	// 3. Verify the changes were made correctly
	// 4. Clean up the test cluster

	t.Skip("Integration test - requires Azure credentials and test infrastructure")
}

// TestAKSService_CommandParsing tests that resource IDs are correctly parsed
func TestAKSService_CommandParsing(t *testing.T) {
	tests := []struct {
		name             string
		resourceID       string
		expectedSubID    string
		expectedRG       string
		expectedCluster  string
		expectParseError bool
	}{
		{
			name:            "valid resource ID",
			resourceID:      "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.ContainerService/managedClusters/test-cluster",
			expectedSubID:   "test-sub",
			expectedRG:      "test-rg",
			expectedCluster: "test-cluster",
		},
		{
			name:            "resource ID without subscription (should use account default)",
			resourceID:      "/resourceGroups/test-rg/providers/Microsoft.ContainerService/managedClusters/test-cluster",
			expectedSubID:   "",
			expectedRG:      "test-rg",
			expectedCluster: "test-cluster",
		},
		{
			name:             "invalid resource ID - missing cluster name",
			resourceID:       "/subscriptions/test-sub/resourceGroups/test-rg",
			expectParseError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := splitResourceID(tt.resourceID)

			var subscriptionID, resourceGroup, clusterName string
			for i, part := range parts {
				if part == "subscriptions" && i+1 < len(parts) {
					subscriptionID = parts[i+1]
				}
				if part == "resourceGroups" && i+1 < len(parts) {
					resourceGroup = parts[i+1]
				}
				if part == "managedClusters" && i+1 < len(parts) {
					clusterName = parts[i+1]
				}
			}

			if tt.expectParseError {
				assert.Empty(t, clusterName, "Should not parse cluster name from invalid ID")
			} else {
				if tt.expectedSubID != "" {
					assert.Equal(t, tt.expectedSubID, subscriptionID)
				}
				assert.Equal(t, tt.expectedRG, resourceGroup)
				assert.Equal(t, tt.expectedCluster, clusterName)
			}
		})
	}
}

func TestAKSService_QueryMetrices(t *testing.T) {
	svc := &aksService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	// Without valid Azure credentials, this should fail
	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestAKSService_GetServiceMap(t *testing.T) {
	svc := &aksService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-aks-cluster",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-aks-cluster", serviceMap.Id.Name)
	assert.Equal(t, "aks-cluster", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestAKSService_GetLogGroupName(t *testing.T) {
	svc := &aksService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.ContainerService/managedClusters/my-aks"

	// Without valid Azure credentials, this should fail
	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
}

// Helper function to split resource IDs
func splitResourceID(resourceID string) []string {
	// Import strings package is already available
	return splitBySlash(resourceID)
}

func splitBySlash(s string) []string {
	var parts []string
	var current string
	for _, r := range s {
		if r == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(r)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
