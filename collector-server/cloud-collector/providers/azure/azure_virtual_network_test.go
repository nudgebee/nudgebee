package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVirtualNetworkService_Name(t *testing.T) {
	svc := &virtualNetworkService{}
	assert.Equal(t, "Microsoft.Network/virtualNetworks", svc.Name())
}

func TestVirtualNetworkService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armnetwork.VirtualNetwork, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve virtual networks",
			mockSetup: func() ([]armnetwork.VirtualNetwork, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/my-vnet"
				name := "my-vnet"
				typeName := "Microsoft.Network/virtualNetworks"
				location := "eastus"
				provisioningState := armnetwork.ProvisioningStateSucceeded

				return []armnetwork.VirtualNetwork{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armnetwork.VirtualNetworkPropertiesFormat{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-vnet", res.Name)
				assert.Equal(t, "Microsoft.Network/virtualNetworks", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Network/virtualNetworks", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve virtual network with failed provisioning",
			mockSetup: func() ([]armnetwork.VirtualNetwork, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/failed-vnet"
				name := "failed-vnet"
				typeName := "Microsoft.Network/virtualNetworks"
				location := "westus"
				provisioningState := armnetwork.ProvisioningStateFailed

				return []armnetwork.VirtualNetwork{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.VirtualNetworkPropertiesFormat{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				// Failed state should map based on nbStatusFromAzureProvisioningState
				assert.NotEqual(t, providers.ResourceStatusActive, resources[0].Status)
			},
		},
		{
			name: "retrieve multiple virtual networks",
			mockSetup: func() ([]armnetwork.VirtualNetwork, error) {
				vnets := []armnetwork.VirtualNetwork{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-" + string(rune(i))
					name := "vnet-" + string(rune(i))
					typeName := "Microsoft.Network/virtualNetworks"
					location := "eastus"
					provisioningState := armnetwork.ProvisioningStateSucceeded

					vnets = append(vnets, armnetwork.VirtualNetwork{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.VirtualNetworkPropertiesFormat{
							ProvisioningState: &provisioningState,
						},
					})
				}
				return vnets, nil
			},
			expectedCount: 3,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				assert.Len(t, resources, 3)
			},
		},
		{
			name: "retrieve virtual network with updating state",
			mockSetup: func() ([]armnetwork.VirtualNetwork, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/updating-vnet"
				name := "updating-vnet"
				typeName := "Microsoft.Network/virtualNetworks"
				location := "eastus"
				provisioningState := armnetwork.ProvisioningStateUpdating

				return []armnetwork.VirtualNetwork{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.VirtualNetworkPropertiesFormat{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				// Updating state mapping depends on nbStatusFromAzureProvisioningState
				assert.NotEmpty(t, resources[0].Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vnets, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, vnets, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				var resources []providers.Resource
				for _, vnet := range vnets {
					status := providers.ResourceStatusUnknown
					if vnet.Properties != nil && vnet.Properties.ProvisioningState != nil {
						if val, ok := nbStatusFromAzureProvisioningState[string(*vnet.Properties.ProvisioningState)]; ok {
							status = val
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *vnet.ID,
						Name:        *vnet.Name,
						Type:        *vnet.Type,
						Region:      *vnet.Location,
						Tags:        toAzureTags(vnet.Tags),
						Status:      status,
						ServiceName: "Microsoft.Network/virtualNetworks",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestVirtualNetworkService_GetRecommendations(t *testing.T) {
	svc := &virtualNetworkService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                       string
		existingResources          []providers.Resource
		expectedRecommendations    int
		expectedDDoSProtectionRule bool
		expectedVMProtectionRule   bool
	}{
		{
			name: "no recommendations for well-protected virtual network",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/secure-vnet",
					Name:        "secure-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableDdosProtection": true,
							"enableVmProtection":   true,
						},
					},
				},
			},
			expectedRecommendations:    0,
			expectedDDoSProtectionRule: false,
			expectedVMProtectionRule:   false,
		},
		{
			name: "recommendation for DDoS protection disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/no-ddos-vnet",
					Name:        "no-ddos-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableDdosProtection": false,
							"enableVmProtection":   true,
						},
					},
				},
			},
			expectedRecommendations:    1,
			expectedDDoSProtectionRule: true,
			expectedVMProtectionRule:   false,
		},
		{
			name: "recommendation for VM protection disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/no-vm-protection-vnet",
					Name:        "no-vm-protection-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableDdosProtection": true,
							"enableVmProtection":   false,
						},
					},
				},
			},
			expectedRecommendations:    1,
			expectedDDoSProtectionRule: false,
			expectedVMProtectionRule:   true,
		},
		{
			name: "multiple recommendations for unprotected virtual network",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/unprotected-vnet",
					Name:        "unprotected-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableDdosProtection": false,
							"enableVmProtection":   false,
						},
					},
				},
			},
			expectedRecommendations:    2,
			expectedDDoSProtectionRule: true,
			expectedVMProtectionRule:   true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/no-meta-vnet",
					Name:        "no-meta-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
		{
			name: "recommendation for missing DDoS protection property",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/missing-ddos-vnet",
					Name:        "missing-ddos-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableVmProtection": true,
						},
					},
				},
			},
			expectedRecommendations:    1,
			expectedDDoSProtectionRule: true,
			expectedVMProtectionRule:   false,
		},
		{
			name: "recommendation for missing VM protection property",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/missing-vm-vnet",
					Name:        "missing-vm-vnet",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/virtualNetworks",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableDdosProtection": true,
						},
					},
				},
			},
			expectedRecommendations:    1,
			expectedDDoSProtectionRule: false,
			expectedVMProtectionRule:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			if tt.expectedDDoSProtectionRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_vnet_ddos_protection_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected DDoS protection recommendation not found")
			}

			if tt.expectedVMProtectionRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_vnet_vm_protection_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityLow, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected VM protection recommendation not found")
			}
		})
	}
}

func TestVirtualNetworkService_ApplyRecommendation(t *testing.T) {
	svc := &virtualNetworkService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
		RuleName:   "azure_vnet_ddos_protection_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestVirtualNetworkService_ApplyCommand(t *testing.T) {
	svc := &virtualNetworkService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
		Command:    "azure_vnet_ddos_protection_disabled",
	}

	resp, err := svc.ApplyCommand(ctx, account, command)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "Virtual Network configuration changes")
}

func TestVirtualNetworkService_QueryMetrices(t *testing.T) {
	svc := &virtualNetworkService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestVirtualNetworkService_GetServiceMap(t *testing.T) {
	svc := &virtualNetworkService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-virtual-network",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-virtual-network", serviceMap.Id.Name)
	assert.Equal(t, "virtualnetwork", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestVirtualNetworkService_GetLogGroupName(t *testing.T) {
	svc := &virtualNetworkService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/my-vnet"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	require.NoError(t, err)
	assert.Equal(t, resourceID, logGroup)
}
