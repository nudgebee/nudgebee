package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSentinelService_Name(t *testing.T) {
	svc := &sentinelService{}
	assert.Equal(t, "microsoft.securityinsights/alertrules", svc.Name())
}

func TestSentinelService_GetRecommendations(t *testing.T) {
	svc := &sentinelService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for properly configured alert rule",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws/providers/Microsoft.SecurityInsights/alertRules/rule-1",
					Name:        "rule-1",
					Type:        "Microsoft.SecurityInsights/alertRules",
					Region:      "eastus",
					ServiceName: "microsoft.securityinsights/alertrules",
					Meta: map[string]interface{}{
						"enabled":         true,
						"automationRules": []string{"auto-1"},
						"dataConnectors":  []string{"connector-1"},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for disabled alert rule",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws/providers/Microsoft.SecurityInsights/alertRules/rule-2",
					Name:        "rule-2",
					Type:        "Microsoft.SecurityInsights/alertRules",
					Region:      "eastus",
					ServiceName: "microsoft.securityinsights/alertrules",
					Meta: map[string]interface{}{
						"enabled": false,
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_sentinel_alert_rule_disabled"},
		},
		{
			name: "recommendation for stale incident",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws/providers/Microsoft.SecurityInsights/incidents/inc-2",
					Name:        "inc-2",
					Type:        "Microsoft.SecurityInsights/incidents",
					Region:      "eastus",
					ServiceName: "microsoft.securityinsights/alertrules",
					Meta: map[string]interface{}{
						"status":         "Active",
						"createdTimeUtc": time.Now().Add(-45 * 24 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_sentinel_stale_incident"},
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
						break
					}
				}
				assert.True(t, found, "Expected recommendation rule %s not found", expectedRule)
			}
		})
	}
}

func TestSentinelService_ApplyRecommendation(t *testing.T) {
	svc := &sentinelService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws/providers/Microsoft.SecurityInsights/alertRules/rule-1",
		RuleName:   "azure_sentinel_alert_rule_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestSentinelService_ApplyCommand(t *testing.T) {
	svc := &sentinelService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws/providers/Microsoft.SecurityInsights/alertRules/rule-1",
		Command:    "azure_sentinel_alert_rule_disabled",
	}

	resp, err := svc.ApplyCommand(ctx, account, command)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "requires manual configuration")
}

func TestSentinelService_QueryMetrices(t *testing.T) {
	svc := &sentinelService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestSentinelService_GetServiceMap(t *testing.T) {
	svc := &sentinelService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Id:     "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws/providers/Microsoft.SecurityInsights/alertRules/rule-1",
		Name:   "rule-1",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, resource.Id, serviceMap.Id.Name)
	assert.Equal(t, "microsoft.securityinsights/alertrules", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestSentinelService_GetLogGroupName(t *testing.T) {
	svc := &sentinelService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws"

	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	// This will fail because we don't have real credentials
	assert.Error(t, err)
}
