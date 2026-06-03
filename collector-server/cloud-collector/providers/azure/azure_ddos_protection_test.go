package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDdosProtectionService_Name(t *testing.T) {
	svc := &ddosProtectionService{}
	assert.Equal(t, "microsoft.network/ddosprotectionplans", svc.Name())
}

func TestDdosProtectionService_GetRecommendations(t *testing.T) {
	svc := &ddosProtectionService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "recommendation for DDoS plan with no VNets",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-1",
					Name:        "plan-1",
					Type:        "Microsoft.Network/ddosProtectionPlans",
					Region:      "eastus",
					ServiceName: "microsoft.network/ddosprotectionplans",
					Meta: map[string]interface{}{
						"virtualNetworks": []interface{}{},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_ddos_protection_plan_no_vnets"},
		},
		{
			name: "no recommendation for DDoS plan with VNets",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-2",
					Name:        "plan-2",
					Type:        "Microsoft.Network/ddosProtectionPlans",
					Region:      "eastus",
					ServiceName: "microsoft.network/ddosprotectionplans",
					Meta: map[string]interface{}{
						"virtualNetworks": []interface{}{
							map[string]interface{}{
								"id": "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet-1",
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "no recommendation for non-DDoS plan resource type",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet-1",
					Name:        "vnet-1",
					Type:        "Microsoft.Network/virtualNetworks",
					Region:      "eastus",
					ServiceName: "microsoft.network/ddosprotectionplans",
					Meta: map[string]interface{}{
						"enableDdosProtection": false,
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
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
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						break
					}
				}
				assert.True(t, found, "Expected recommendation rule %s not found", expectedRule)
			}
		})
	}
}

func TestDdosProtectionService_ApplyRecommendation(t *testing.T) {
	svc := &ddosProtectionService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-1",
		RuleName:   "azure_ddos_protection_plan_no_vnets",
	}

	// Will fail on credential check before reaching the command handler
	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
}

func TestDdosProtectionService_ApplyCommand(t *testing.T) {
	svc := &ddosProtectionService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// ApplyCommand requires valid credentials, so all commands will fail
	// on the credential check before reaching the switch statement
	tests := []struct {
		name    string
		command providers.ApplyCommandRequest
	}{
		{
			name: "plan no vnets command fails on credentials",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-1",
				Command:    "azure_ddos_protection_plan_no_vnets",
			},
		},
		{
			name: "unknown command fails on credentials",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-1",
				Command:    "unknown_command",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ApplyCommand(ctx, account, tt.command)
			assert.Error(t, err)
			assert.False(t, resp.Success)
		})
	}
}

func TestDdosProtectionService_QueryMetrices(t *testing.T) {
	svc := &ddosProtectionService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	_, err := svc.QueryMetrices(ctx, account, filter)
	// getAzureMonitorMetrics validates StartDate/EndDate first
	assert.Error(t, err)
}

func TestDdosProtectionService_GetServiceMap(t *testing.T) {
	svc := &ddosProtectionService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Id:     "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-1",
		Name:   "plan-1",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, resource.Id, serviceMap.Id.Name)
	assert.Equal(t, "microsoft.network/ddosprotectionplans", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Len(t, serviceMap.Upstreams, 1)
	assert.Contains(t, serviceMap.Upstreams[0].Id, "Microsoft.Resources/resourceGroups")
	assert.Empty(t, serviceMap.Downstreams)
}

func TestDdosProtectionService_GetLogGroupName(t *testing.T) {
	svc := &ddosProtectionService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/plan-1"

	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	// This will fail because we don't have real credentials
	assert.Error(t, err)
}
