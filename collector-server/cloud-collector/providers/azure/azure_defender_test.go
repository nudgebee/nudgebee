package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefenderService_Name(t *testing.T) {
	svc := &defenderService{}
	assert.Equal(t, "microsoft.security/pricings", svc.Name())
}

func TestDefenderService_GetRecommendations(t *testing.T) {
	svc := &defenderService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for standard tier",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/providers/Microsoft.Security/pricings/VirtualMachines",
					Name:        "VirtualMachines",
					Type:        "Microsoft.Security/pricings",
					Region:      "global",
					ServiceName: "microsoft.security/pricings",
					Meta: map[string]interface{}{
						"pricingTier": "Standard",
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for free tier",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/providers/Microsoft.Security/pricings/VirtualMachines",
					Name:        "VirtualMachines",
					Type:        "Microsoft.Security/pricings",
					Region:      "global",
					ServiceName: "microsoft.security/pricings",
					Meta: map[string]interface{}{
						"pricingTier": "Free",
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_defender_free_tier"},
		},
		{
			name: "recommendation for unhealthy assessment",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/providers/Microsoft.Security/assessments/assess-1",
					Name:        "assess-1",
					Type:        "Microsoft.Security/assessments",
					Region:      "global",
					ServiceName: "microsoft.security/pricings",
					Meta: map[string]interface{}{
						"displayName": "Critical vulnerability found",
						"status": map[string]interface{}{
							"code": "Unhealthy",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_defender_unhealthy_assessment"},
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
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected recommendation rule %s not found", expectedRule)
			}
		})
	}
}

func TestDefenderService_ApplyRecommendation(t *testing.T) {
	svc := &defenderService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-123/providers/Microsoft.Security/pricings/VirtualMachines",
		RuleName:   "azure_defender_free_tier",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	// This will fail because we don't have real credentials
	assert.Error(t, err)
}

func TestDefenderService_ApplyCommand(t *testing.T) {
	svc := &defenderService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name              string
		command           providers.ApplyCommandRequest
		expectUnsupported bool
	}{
		{
			name: "unsupported unhealthy assessment command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-123/providers/Microsoft.Security/assessments/assess-1",
				Command:    "azure_defender_unhealthy_assessment",
			},
			expectUnsupported: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ApplyCommand(ctx, account, tt.command)
			assert.False(t, resp.Success)
			if tt.expectUnsupported {
				assert.ErrorIs(t, err, errors.ErrUnsupported)
			}
		})
	}
}

func TestDefenderService_QueryMetrices(t *testing.T) {
	svc := &defenderService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestDefenderService_GetServiceMap(t *testing.T) {
	svc := &defenderService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Id:     "/subscriptions/sub-123/providers/Microsoft.Security/pricings/VirtualMachines",
		Name:   "VirtualMachines",
		Region: "global",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, resource.Id, serviceMap.Id.Name)
	assert.Equal(t, "microsoft.security/pricings", serviceMap.Id.Kind)
	assert.Equal(t, "global", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestDefenderService_GetLogGroupName(t *testing.T) {
	svc := &defenderService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/providers/Microsoft.Security/pricings/VirtualMachines"

	_, err := svc.GetLogGroupName(ctx, account, "global", resourceID)
	// This will fail because we don't have real credentials
	assert.Error(t, err)
}
