package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity"
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
						"pricingTier":                  "Standard",
						"securityContactConfiguration": map[string]interface{}{"email": "secops@example.com"},
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
						"pricingTier":                  "Free",
						"securityContactConfiguration": map[string]interface{}{"email": "secops@example.com"},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_defender_free_tier"},
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
	if os.Getenv("AZURE_CLIENT_ID") == "" {
		t.Skip("Skipping integration test that requires Azure credentials")
	}
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
				ResourceId: "/subscriptions/sub-123/resourcegroups/rg/providers/microsoft.storage/storageaccounts/acct",
				Command:    "azure_defender_assessment_1ff0b4c9-ed56-4de6-be9c-d7ab39645926",
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
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
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

func TestStripAssessmentSuffix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "resource-scoped assessment",
			in:   "/subscriptions/sub-123/resourcegroups/rg/providers/microsoft.storage/storageaccounts/acct/providers/microsoft.security/assessments/1c5de8e1",
			want: "/subscriptions/sub-123/resourcegroups/rg/providers/microsoft.storage/storageaccounts/acct",
		},
		{
			name: "subscription-scoped assessment",
			in:   "/subscriptions/sub-123/providers/microsoft.security/assessments/1ff0b4c9",
			want: "/subscriptions/sub-123",
		},
		{
			name: "mixed-case suffix",
			in:   "/subscriptions/sub-123/providers/Microsoft.Security/assessments/assess-1",
			want: "/subscriptions/sub-123",
		},
		{
			name: "no assessment suffix returns input unchanged",
			in:   "/subscriptions/sub-123/resourcegroups/rg",
			want: "/subscriptions/sub-123/resourcegroups/rg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripAssessmentSuffix(tt.in))
		})
	}
}

func TestAssessedResourceID(t *testing.T) {
	id := "/subscriptions/Sub-123/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/Acct"
	src := armsecurity.SourceAzure
	details := &armsecurity.AzureResourceDetails{ID: &id, Source: &src}
	// Lowercased to match the external_resource_id storage convention.
	assert.Equal(t, strings.ToLower(id), assessedResourceID(details))

	// Nil ID yields empty.
	assert.Equal(t, "", assessedResourceID(&armsecurity.AzureResourceDetails{Source: &src}))
}

func TestAzureResourceTypeFromID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "resource-scoped id yields provider/type",
			in:   "/subscriptions/sub-123/resourcegroups/rg/providers/microsoft.storage/storageaccounts/acct",
			want: "microsoft.storage/storageaccounts",
		},
		{
			name: "mixed-case is normalized",
			in:   "/subscriptions/sub-123/resourceGroups/RG/providers/Microsoft.Compute/virtualMachines/vm1",
			want: "microsoft.compute/virtualmachines",
		},
		{
			name: "subscription-scoped id",
			in:   "/subscriptions/sub-123",
			want: "subscription",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, azureResourceTypeFromID(tt.in))
		})
	}
}
