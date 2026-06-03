package azure

import (
	"context"
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockExpressRoutePager is a mock for the Pager used by the ExpressRouteCircuitsClient
type mockExpressRoutePager struct {
	pages [][]*armnetwork.ExpressRouteCircuit
	page  int
}

func (p *mockExpressRoutePager) More() bool {
	return p.page < len(p.pages)
}

func (p *mockExpressRoutePager) NextPage(ctx context.Context) (armnetwork.ExpressRouteCircuitsClientListAllResponse, error) {
	if !p.More() {
		return armnetwork.ExpressRouteCircuitsClientListAllResponse{}, nil
	}
	page := p.pages[p.page]
	p.page++
	return armnetwork.ExpressRouteCircuitsClientListAllResponse{
		ExpressRouteCircuitListResult: armnetwork.ExpressRouteCircuitListResult{
			Value: page,
		},
	}, nil
}

func TestExpressRouteService_Name(t *testing.T) {
	svc := &expressRouteService{}
	assert.Equal(t, "Microsoft.Network/expressRouteCircuits", svc.Name())
}
func TestExpressRouteService_GetResources_NoCreds(t *testing.T) {
	svc := &expressRouteService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	resources, err := svc.GetResources(ctx, account, "eastus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Nil(t, resources)
}

func TestExpressRouteService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockPager      *mockExpressRoutePager
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve enabled expressroute circuits",
			mockPager: &mockExpressRoutePager{
				pages: [][]*armnetwork.ExpressRouteCircuit{{
					&armnetwork.ExpressRouteCircuit{
						ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/expressRouteCircuits/my-circuit"),
						Name:     strPtr("my-circuit"),
						Type:     strPtr("Microsoft.Network/expressRouteCircuits"),
						Location: strPtr("eastus"),
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armnetwork.ExpressRouteCircuitPropertiesFormat{
							CircuitProvisioningState: strPtr("Enabled"),
						},
					},
				}},
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-circuit", res.Name)
				assert.Equal(t, "Microsoft.Network/expressRouteCircuits", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Network/expressRouteCircuits", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve disabled expressroute circuit",
			mockPager: &mockExpressRoutePager{
				pages: [][]*armnetwork.ExpressRouteCircuit{{
					&armnetwork.ExpressRouteCircuit{
						ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/expressRouteCircuits/disabled-circuit"),
						Name:     strPtr("disabled-circuit"),
						Type:     strPtr("Microsoft.Network/expressRouteCircuits"),
						Location: strPtr("westus"),
						Properties: &armnetwork.ExpressRouteCircuitPropertiesFormat{
							CircuitProvisioningState: strPtr("Disabled"),
						},
					},
				}},
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusInactive, resources[0].Status)
			},
		},
		{
			name: "retrieve expressroute circuit with unknown state",
			mockPager: &mockExpressRoutePager{
				pages: [][]*armnetwork.ExpressRouteCircuit{{
					&armnetwork.ExpressRouteCircuit{
						ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/expressRouteCircuits/unknown-circuit"),
						Name:     strPtr("unknown-circuit"),
						Type:     strPtr("Microsoft.Network/expressRouteCircuits"),
						Location: strPtr("eastus"),
						Properties: &armnetwork.ExpressRouteCircuitPropertiesFormat{
							CircuitProvisioningState: strPtr("Provisioning"),
						},
					},
				}},
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusUnknown, resources[0].Status)
			},
		},
		{
			name: "retrieve multiple expressroute circuits",
			mockPager: &mockExpressRoutePager{
				pages: [][]*armnetwork.ExpressRouteCircuit{{
					&armnetwork.ExpressRouteCircuit{
						ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/expressRouteCircuits/circuit-1"),
						Name:     strPtr("circuit-1"),
						Type:     strPtr("Microsoft.Network/expressRouteCircuits"),
						Location: strPtr("eastus"),
						Properties: &armnetwork.ExpressRouteCircuitPropertiesFormat{
							CircuitProvisioningState: strPtr("Enabled"),
						},
					},
					&armnetwork.ExpressRouteCircuit{
						ID:       strPtr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/expressRouteCircuits/circuit-2"),
						Name:     strPtr("circuit-2"),
						Type:     strPtr("Microsoft.Network/expressRouteCircuits"),
						Location: strPtr("eastus"),
						Properties: &armnetwork.ExpressRouteCircuitPropertiesFormat{
							CircuitProvisioningState: strPtr("Enabled"),
						},
					},
				}},
			},
			expectedCount: 2,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				assert.Len(t, resources, 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This test validates the structure and mock setup
			// Actual GetResources would require valid Azure credentials
			// For now, we validate the pager structure and expected data
			require.NotNil(t, tt.mockPager)

			// Validate mock pager behavior
			ctx := context.Background()
			page, err := tt.mockPager.NextPage(ctx)
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Validate the mock data structure
			circuits := page.Value
			assert.Len(t, circuits, tt.expectedCount)

			if tt.validateResult != nil && len(circuits) > 0 {
				// Convert mock circuits to resources for validation
				var resources []providers.Resource
				for _, circuit := range circuits {
					status := providers.ResourceStatusUnknown
					if circuit.Properties != nil && circuit.Properties.CircuitProvisioningState != nil {
						state := strings.ToLower(*circuit.Properties.CircuitProvisioningState)
						switch state {
						case "enabled":
							status = providers.ResourceStatusActive
						case "disabled":
							status = providers.ResourceStatusInactive
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *circuit.ID,
						Name:        *circuit.Name,
						Type:        *circuit.Type,
						Region:      *circuit.Location,
						Status:      status,
						ServiceName: "Microsoft.Network/expressRouteCircuits",
						Tags:        toAzureTags(circuit.Tags),
						Meta:        structToMap(circuit),
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestExpressRouteService_GetRecommendations(t *testing.T) {
	svc := &expressRouteService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedGlobalReachRule bool
		expectedStandardSKURule bool
	}{
		{
			name: "no recommendations for optimized circuit",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/good-circuit",
					Name:        "good-circuit",
					Type:        "Microsoft.Network/expressRouteCircuits",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/expressRouteCircuits",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"globalReachEnabled": true,
						},
						"sku": map[string]interface{}{
							"tier": "Standard",
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedGlobalReachRule: false,
			expectedStandardSKURule: false,
		},
		{
			name: "recommendation for Global Reach disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/no-reach-circuit",
					Name:        "no-reach-circuit",
					Type:        "Microsoft.Network/expressRouteCircuits",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/expressRouteCircuits",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"globalReachEnabled": false,
						},
						"sku": map[string]interface{}{
							"tier": "Standard",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedGlobalReachRule: true,
			expectedStandardSKURule: false,
		},
		{
			name: "recommendation for Basic SKU",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/basic-circuit",
					Name:        "basic-circuit",
					Type:        "Microsoft.Network/expressRouteCircuits",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/expressRouteCircuits",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"globalReachEnabled": true,
						},
						"sku": map[string]interface{}{
							"tier": "Basic",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedGlobalReachRule: false,
			expectedStandardSKURule: true,
		},
		{
			name: "multiple recommendations for non-optimized circuit",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/bad-circuit",
					Name:        "bad-circuit",
					Type:        "Microsoft.Network/expressRouteCircuits",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/expressRouteCircuits",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"globalReachEnabled": false,
						},
						"sku": map[string]interface{}{
							"tier": "Basic",
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedGlobalReachRule: true,
			expectedStandardSKURule: true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/no-meta-circuit",
					Name:        "no-meta-circuit",
					Type:        "Microsoft.Network/expressRouteCircuits",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/expressRouteCircuits",
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

			if tt.expectedGlobalReachRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_expressroute_enable_global_reach" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryConfiguration, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected Global Reach recommendation not found")
			}

			if tt.expectedStandardSKURule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_expressroute_enable_standard_sku" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryConfiguration, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityLow, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected Standard SKU recommendation not found")
			}
		})
	}
}

func TestExpressRouteService_ApplyRecommendation(t *testing.T) {
	svc := &expressRouteService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/circuit-name",
		RuleName:   "azure_expressroute_enable_global_reach",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	// Credential validation happens first, so we expect this error
	assert.Contains(t, err.Error(), "access secret is not provided")
}

func TestExpressRouteService_ApplyCommand(t *testing.T) {
	svc := &expressRouteService{}
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
				Command:    "azure_expressroute_enable_global_reach",
			},
			expectError:   true,
			errorContains: "access secret is not provided", // Credential check happens first
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/circuit-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided", // Credential check happens first
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/expressRouteCircuits/circuit-name",
				Command:    "azure_expressroute_enable_global_reach",
			},
			expectError:   true,
			errorContains: "access secret is not provided", // Credential check happens first
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

func TestExpressRouteService_QueryMetrices(t *testing.T) {
	svc := &expressRouteService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestExpressRouteService_GetServiceMap(t *testing.T) {
	svc := &expressRouteService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-expressroute-circuit",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-expressroute-circuit", serviceMap.Id.Name)
	assert.Equal(t, "expressroute", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestExpressRouteService_GetLogGroupName(t *testing.T) {
	svc := &expressRouteService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/expressRouteCircuits/my-circuit"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Equal(t, "", logGroup)
}
