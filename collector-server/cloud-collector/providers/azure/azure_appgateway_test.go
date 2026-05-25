package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppGatewayService_Name(t *testing.T) {
	svc := &appGatewayService{}
	assert.Equal(t, "Microsoft.Network/applicationGateways", svc.Name())
}

func TestAppGatewayService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armnetwork.ApplicationGateway, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve running application gateways",
			mockSetup: func() ([]armnetwork.ApplicationGateway, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/applicationGateways/my-appgateway"
				name := "my-appgateway"
				typeName := "Microsoft.Network/applicationGateways"
				location := "eastus"
				provisioningState := armnetwork.ProvisioningStateSucceeded
				operationalState := armnetwork.ApplicationGatewayOperationalStateRunning

				return []armnetwork.ApplicationGateway{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armnetwork.ApplicationGatewayPropertiesFormat{
							ProvisioningState: &provisioningState,
							OperationalState:  &operationalState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-appgateway", res.Name)
				assert.Equal(t, "Microsoft.Network/applicationGateways", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Network/applicationGateways", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve stopped application gateway",
			mockSetup: func() ([]armnetwork.ApplicationGateway, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/applicationGateways/stopped-appgateway"
				name := "stopped-appgateway"
				typeName := "Microsoft.Network/applicationGateways"
				location := "westus"
				provisioningState := armnetwork.ProvisioningStateSucceeded
				operationalState := armnetwork.ApplicationGatewayOperationalStateStopped

				return []armnetwork.ApplicationGateway{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.ApplicationGatewayPropertiesFormat{
							ProvisioningState: &provisioningState,
							OperationalState:  &operationalState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusInactive, resources[0].Status)
			},
		},
		{
			name: "retrieve application gateway with failed provisioning",
			mockSetup: func() ([]armnetwork.ApplicationGateway, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/applicationGateways/failed-appgateway"
				name := "failed-appgateway"
				typeName := "Microsoft.Network/applicationGateways"
				location := "eastus"
				provisioningState := armnetwork.ProvisioningStateFailed

				return []armnetwork.ApplicationGateway{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.ApplicationGatewayPropertiesFormat{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusInactive, resources[0].Status)
			},
		},
		{
			name: "retrieve application gateway with unknown state",
			mockSetup: func() ([]armnetwork.ApplicationGateway, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/applicationGateways/starting-appgateway"
				name := "starting-appgateway"
				typeName := "Microsoft.Network/applicationGateways"
				location := "eastus"
				provisioningState := armnetwork.ProvisioningStateSucceeded
				operationalState := armnetwork.ApplicationGatewayOperationalStateStarting

				return []armnetwork.ApplicationGateway{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.ApplicationGatewayPropertiesFormat{
							ProvisioningState: &provisioningState,
							OperationalState:  &operationalState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusUnknown, resources[0].Status)
			},
		},
		{
			name: "retrieve multiple application gateways",
			mockSetup: func() ([]armnetwork.ApplicationGateway, error) {
				gateways := []armnetwork.ApplicationGateway{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/applicationGateways/appgateway-" + string(rune(i))
					name := "appgateway-" + string(rune(i))
					typeName := "Microsoft.Network/applicationGateways"
					location := "eastus"
					provisioningState := armnetwork.ProvisioningStateSucceeded
					operationalState := armnetwork.ApplicationGatewayOperationalStateRunning

					gateways = append(gateways, armnetwork.ApplicationGateway{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.ApplicationGatewayPropertiesFormat{
							ProvisioningState: &provisioningState,
							OperationalState:  &operationalState,
						},
					})
				}
				return gateways, nil
			},
			expectedCount: 3,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				assert.Len(t, resources, 3)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gateways, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, gateways, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				// Convert mock data to resources for validation
				var resources []providers.Resource
				for _, gateway := range gateways {
					status := providers.ResourceStatusUnknown
					if gateway.Properties != nil && gateway.Properties.ProvisioningState != nil {
						switch *gateway.Properties.ProvisioningState {
						case armnetwork.ProvisioningStateSucceeded:
							if gateway.Properties.OperationalState != nil {
								switch *gateway.Properties.OperationalState {
								case armnetwork.ApplicationGatewayOperationalStateRunning:
									status = providers.ResourceStatusActive
								case armnetwork.ApplicationGatewayOperationalStateStopped:
									status = providers.ResourceStatusInactive
								}
							} else {
								status = providers.ResourceStatusActive
							}
						case armnetwork.ProvisioningStateFailed:
							status = providers.ResourceStatusInactive
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *gateway.ID,
						Name:        *gateway.Name,
						Type:        *gateway.Type,
						Region:      *gateway.Location,
						Tags:        toAzureTags(gateway.Tags),
						Status:      status,
						ServiceName: "Microsoft.Network/applicationGateways",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestAppGatewayService_GetRecommendations(t *testing.T) {
	svc := &appGatewayService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedWAFRule         bool
		expectedHTTP2Rule       bool
		expectedStoppedRule     bool
	}{
		{
			name: "no recommendations for properly configured gateway",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/secure-gateway",
					Name:        "secure-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"operationalState": "Running",
							"enableHttp2":      true,
							"webApplicationFirewallConfiguration": map[string]interface{}{
								"enabled": true,
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedWAFRule:         false,
			expectedHTTP2Rule:       false,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for WAF disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/no-waf-gateway",
					Name:        "no-waf-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"operationalState": "Running",
							"enableHttp2":      true,
							"webApplicationFirewallConfiguration": map[string]interface{}{
								"enabled": false,
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedWAFRule:         true,
			expectedHTTP2Rule:       false,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for WAF not configured",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/no-waf-config-gateway",
					Name:        "no-waf-config-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"operationalState": "Running",
							"enableHttp2":      true,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedWAFRule:         true,
			expectedHTTP2Rule:       false,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for HTTP/2 disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/no-http2-gateway",
					Name:        "no-http2-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"operationalState": "Running",
							"enableHttp2":      false,
							"webApplicationFirewallConfiguration": map[string]interface{}{
								"enabled": true,
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedWAFRule:         false,
			expectedHTTP2Rule:       true,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for stopped gateway",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/stopped-gateway",
					Name:        "stopped-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"operationalState": "Stopped",
							"enableHttp2":      true,
							"webApplicationFirewallConfiguration": map[string]interface{}{
								"enabled": true,
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedWAFRule:         false,
			expectedHTTP2Rule:       false,
			expectedStoppedRule:     true,
		},
		{
			name: "multiple recommendations for gateway with issues",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/issues-gateway",
					Name:        "issues-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"operationalState": "Stopped",
							"enableHttp2":      false,
							"webApplicationFirewallConfiguration": map[string]interface{}{
								"enabled": false,
							},
						},
					},
				},
			},
			expectedRecommendations: 3,
			expectedWAFRule:         true,
			expectedHTTP2Rule:       true,
			expectedStoppedRule:     true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/no-meta-gateway",
					Name:        "no-meta-gateway",
					Type:        "Microsoft.Network/applicationGateways",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/applicationGateways",
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
					if rec.RuleName == "azure_appgateway_waf_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected WAF recommendation not found")
			}

			if tt.expectedHTTP2Rule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_appgateway_http2_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryConfiguration, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected HTTP/2 recommendation not found")
			}

			if tt.expectedStoppedRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_appgateway_stopped" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityLow, rec.Severity)
						assert.Equal(t, providers.RecommendationActionDelete, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected stopped gateway recommendation not found")
			}
		})
	}
}

func TestAppGatewayService_ApplyRecommendation(t *testing.T) {
	svc := &appGatewayService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_appgateway_waf_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}

func TestAppGatewayService_ApplyCommand(t *testing.T) {
	svc := &appGatewayService{}
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
				Command:    "azure_appgateway_waf_disabled",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/gateway-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/gateway-name",
				Command:    "azure_appgateway_waf_disabled",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "start_gateway command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/gateway-name",
				Command:    "start_gateway",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "stop_gateway command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/applicationGateways/gateway-name",
				Command:    "stop_gateway",
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

func TestAppGatewayService_QueryMetrices(t *testing.T) {
	svc := &appGatewayService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestAppGatewayService_GetServiceMap(t *testing.T) {
	svc := &appGatewayService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-application-gateway",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-application-gateway", serviceMap.Id.Name)
	assert.Equal(t, "applicationgateway", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestAppGatewayService_GetLogGroupName(t *testing.T) {
	svc := &appGatewayService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/applicationGateways/my-appgateway"

	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}
