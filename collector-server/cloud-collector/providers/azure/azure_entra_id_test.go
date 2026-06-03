package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntraIDService_Name(t *testing.T) {
	svc := &entraIDService{}
	assert.Equal(t, "microsoft.authorization/roleassignments", svc.Name())
}

func TestEntraIDService_GetRecommendations(t *testing.T) {
	svc := &entraIDService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for properly configured role assignment",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-1",
					Name:        "assign-1",
					Type:        "Microsoft.Authorization/roleAssignments",
					Region:      "global",
					ServiceName: "microsoft.authorization/roleassignments",
					Meta: map[string]interface{}{
						"roleDefinitionId": "/subscriptions/sub-123/providers/Microsoft.Authorization/roleDefinitions/reader",
						"principalType":    "User",
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for overly permissive owner role",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-2",
					Name:        "assign-2",
					Type:        "Microsoft.Authorization/roleAssignments",
					Region:      "global",
					ServiceName: "microsoft.authorization/roleassignments",
					Meta: map[string]interface{}{
						"roleDefinitionId": "/subscriptions/sub-123/providers/Microsoft.Authorization/roleDefinitions/owner",
						"principalType":    "User",
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_entra_id_overly_permissive_role"},
		},
		{
			name: "recommendation for service principal",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-3",
					Name:        "assign-3",
					Type:        "Microsoft.Authorization/roleAssignments",
					Region:      "global",
					ServiceName: "microsoft.authorization/roleassignments",
					Meta: map[string]interface{}{
						"roleDefinitionId": "/subscriptions/sub-123/providers/Microsoft.Authorization/roleDefinitions/contributor",
						"principalType":    "ServicePrincipal",
					},
				},
			},
			expectedRecommendations: 2,
			expectedRules:           []string{"azure_entra_id_overly_permissive_role", "azure_entra_id_service_principal_no_expiration"},
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

func TestEntraIDService_ApplyRecommendation(t *testing.T) {
	svc := &entraIDService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-1",
		RuleName:   "azure_entra_id_overly_permissive_role",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestEntraIDService_ApplyCommand(t *testing.T) {
	svc := &entraIDService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-1",
		Command:    "azure_entra_id_overly_permissive_role",
	}

	resp, err := svc.ApplyCommand(ctx, account, command)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "requires manual review and approval")
}

func TestEntraIDService_QueryMetrices(t *testing.T) {
	svc := &entraIDService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestEntraIDService_GetServiceMap(t *testing.T) {
	svc := &entraIDService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Id:     "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-1",
		Name:   "assign-1",
		Region: "global",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, resource.Id, serviceMap.Id.Name)
	assert.Equal(t, "microsoft.authorization/roleassignments", serviceMap.Id.Kind)
	assert.Equal(t, "global", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestEntraIDService_GetLogGroupName(t *testing.T) {
	svc := &entraIDService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/assign-1"

	_, err := svc.GetLogGroupName(ctx, account, "global", resourceID)
	// This will fail because we don't have real credentials
	assert.Error(t, err)
}
