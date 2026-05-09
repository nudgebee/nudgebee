package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrontDoorService_Name(t *testing.T) {
	svc := &frontDoorService{}
	assert.Equal(t, "Microsoft.Network/frontDoors", svc.Name())
}

func TestFrontDoorService_GetResources(t *testing.T) {
	// Note: Front Door service now uses real Azure Front Door SDK
	// These tests verify the structure without making actual SDK calls
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with missing credentials should return error
	resources, err := svc.GetResources(ctx, account, "global")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Nil(t, resources)
}

func TestFrontDoorService_GetRecommendations(t *testing.T) {
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                      string
		existingResources         []providers.Resource
		expectedRecommendations   int
		expectedWAFRule           bool
		expectedHTTPSRedirectRule bool
	}{
		{
			name: "no recommendations for secure front door",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoors/secure-fd",
					Name:        "secure-fd",
					Type:        "Microsoft.Network/frontDoors",
					Region:      "global",
					ServiceName: "Microsoft.Network/frontDoors",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"webApplicationFirewallPolicyLink": map[string]interface{}{
								"id": "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoorWebApplicationFirewallPolicies/waf-policy",
							},
							"routingRules": []interface{}{
								map[string]interface{}{
									"properties": map[string]interface{}{
										"routeConfiguration": map[string]interface{}{
											"@odata.type": "#Microsoft.Azure.FrontDoor.Models.FrontdoorRedirectConfiguration",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRecommendations:   0,
			expectedWAFRule:           false,
			expectedHTTPSRedirectRule: false,
		},
		{
			name: "recommendation for WAF disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoors/no-waf-fd",
					Name:        "no-waf-fd",
					Type:        "Microsoft.Network/frontDoors",
					Region:      "global",
					ServiceName: "Microsoft.Network/frontDoors",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"routingRules": []interface{}{
								map[string]interface{}{
									"properties": map[string]interface{}{
										"routeConfiguration": map[string]interface{}{
											"@odata.type": "#Microsoft.Azure.FrontDoor.Models.FrontdoorRedirectConfiguration",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRecommendations:   1,
			expectedWAFRule:           true,
			expectedHTTPSRedirectRule: false,
		},
		{
			name: "recommendation for HTTPS redirect disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoors/no-https-fd",
					Name:        "no-https-fd",
					Type:        "Microsoft.Network/frontDoors",
					Region:      "global",
					ServiceName: "Microsoft.Network/frontDoors",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"webApplicationFirewallPolicyLink": map[string]interface{}{
								"id": "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoorWebApplicationFirewallPolicies/waf-policy",
							},
							"routingRules": []interface{}{
								map[string]interface{}{
									"properties": map[string]interface{}{
										"acceptedProtocols": []interface{}{"Http"},
										"routeConfiguration": map[string]interface{}{
											"@odata.type": "#Microsoft.Azure.FrontDoor.Models.FrontdoorForwardingConfiguration",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRecommendations:   1,
			expectedWAFRule:           false,
			expectedHTTPSRedirectRule: true,
		},
		{
			name: "multiple recommendations for insecure front door",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoors/insecure-fd",
					Name:        "insecure-fd",
					Type:        "Microsoft.Network/frontDoors",
					Region:      "global",
					ServiceName: "Microsoft.Network/frontDoors",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"routingRules": []interface{}{
								map[string]interface{}{
									"properties": map[string]interface{}{
										"acceptedProtocols": []interface{}{"Http"},
										"routeConfiguration": map[string]interface{}{
											"@odata.type": "#Microsoft.Azure.FrontDoor.Models.FrontdoorForwardingConfiguration",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedRecommendations:   2,
			expectedWAFRule:           true,
			expectedHTTPSRedirectRule: true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/frontDoors/no-meta-fd",
					Name:        "no-meta-fd",
					Type:        "Microsoft.Network/frontDoors",
					Region:      "global",
					ServiceName: "Microsoft.Network/frontDoors",
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

			if tt.expectedWAFRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_frontdoor_enable_waf" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected WAF recommendation not found")
			}

			if tt.expectedHTTPSRedirectRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_frontdoor_enable_https_redirect" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected HTTPS redirect recommendation not found")
			}
		})
	}
}

func TestFrontDoorService_ApplyRecommendation(t *testing.T) {
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_frontdoor_enable_waf",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}

func TestFrontDoorService_ApplyCommand(t *testing.T) {
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name          string
		command       providers.ApplyCommandRequest
		expectError   bool
		errorContains string
	}{
		{
			name: "invalid resource ID",
			command: providers.ApplyCommandRequest{
				ResourceId: "",
				Command:    "azure_frontdoor_enable_waf",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/frontDoors/frontdoor-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/frontDoors/frontdoor-name",
				Command:    "azure_frontdoor_enable_https_redirect",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ApplyCommand(ctx, account, tt.command)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.False(t, resp.Success)
			} else {
				assert.NoError(t, err)
				assert.True(t, resp.Success)
			}
		})
	}
}

func TestFrontDoorService_QueryMetrices(t *testing.T) {
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestFrontDoorService_GetServiceMap(t *testing.T) {
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-frontdoor",
		Region: "global",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-frontdoor", serviceMap.Id.Name)
	assert.Equal(t, "frontdoor", serviceMap.Id.Kind)
	assert.Equal(t, "global", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestFrontDoorService_GetLogGroupName(t *testing.T) {
	svc := &frontDoorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/frontDoors/my-frontdoor"

	logGroup, err := svc.GetLogGroupName(ctx, account, "global", resourceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Equal(t, "", logGroup)
}
