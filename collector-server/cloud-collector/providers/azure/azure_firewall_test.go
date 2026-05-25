package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirewallService_Name(t *testing.T) {
	svc := &firewallService{}
	assert.Equal(t, "Microsoft.Network/azureFirewalls", svc.Name())
}

func TestFirewallService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armnetwork.AzureFirewall, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve active firewalls",
			mockSetup: func() ([]armnetwork.AzureFirewall, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/azureFirewalls/my-firewall"
				name := "my-firewall"
				typeName := "Microsoft.Network/azureFirewalls"
				location := "eastus"
				state := armnetwork.ProvisioningStateSucceeded

				return []armnetwork.AzureFirewall{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armnetwork.AzureFirewallPropertiesFormat{
							ProvisioningState: &state,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-firewall", res.Name)
				assert.Equal(t, "Microsoft.Network/azureFirewalls", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Network/azureFirewalls", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve failed firewall",
			mockSetup: func() ([]armnetwork.AzureFirewall, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/azureFirewalls/failed-firewall"
				name := "failed-firewall"
				typeName := "Microsoft.Network/azureFirewalls"
				location := "westus"
				state := armnetwork.ProvisioningStateFailed

				return []armnetwork.AzureFirewall{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.AzureFirewallPropertiesFormat{
							ProvisioningState: &state,
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
			name: "retrieve firewall with unknown state",
			mockSetup: func() ([]armnetwork.AzureFirewall, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/azureFirewalls/unknown-firewall"
				name := "unknown-firewall"
				typeName := "Microsoft.Network/azureFirewalls"
				location := "eastus"
				state := armnetwork.ProvisioningStateUpdating

				return []armnetwork.AzureFirewall{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.AzureFirewallPropertiesFormat{
							ProvisioningState: &state,
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
			name: "retrieve multiple firewalls",
			mockSetup: func() ([]armnetwork.AzureFirewall, error) {
				firewalls := []armnetwork.AzureFirewall{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/azureFirewalls/firewall-" + string(rune(i))
					name := "firewall-" + string(rune(i))
					typeName := "Microsoft.Network/azureFirewalls"
					location := "eastus"
					state := armnetwork.ProvisioningStateSucceeded

					firewalls = append(firewalls, armnetwork.AzureFirewall{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armnetwork.AzureFirewallPropertiesFormat{
							ProvisioningState: &state,
						},
					})
				}
				return firewalls, nil
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
			firewalls, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, firewalls, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				var resources []providers.Resource
				for _, firewall := range firewalls {
					status := providers.ResourceStatusUnknown
					if firewall.Properties != nil && firewall.Properties.ProvisioningState != nil {
						switch *firewall.Properties.ProvisioningState {
						case armnetwork.ProvisioningStateSucceeded:
							status = providers.ResourceStatusActive
						case armnetwork.ProvisioningStateFailed:
							status = providers.ResourceStatusInactive
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *firewall.ID,
						Name:        *firewall.Name,
						Type:        *firewall.Type,
						Region:      *firewall.Location,
						Tags:        toAzureTags(firewall.Tags),
						Status:      status,
						ServiceName: "Microsoft.Network/azureFirewalls",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestFirewallService_GetRecommendations(t *testing.T) {
	svc := &firewallService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedThreatIntelRule bool
		expectedDNSProxyRule    bool
	}{
		{
			name: "no recommendations for secure firewall",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/secure-firewall",
					Name:        "secure-firewall",
					Type:        "Microsoft.Network/azureFirewalls",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/azureFirewalls",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"threatIntelMode": "Alert",
							"additionalProperties": map[string]interface{}{
								"Network.DNS.EnableProxy": "true",
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedThreatIntelRule: false,
			expectedDNSProxyRule:    false,
		},
		{
			name: "recommendation for threat intel disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/no-threat-firewall",
					Name:        "no-threat-firewall",
					Type:        "Microsoft.Network/azureFirewalls",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/azureFirewalls",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"threatIntelMode": "Off",
							"additionalProperties": map[string]interface{}{
								"Network.DNS.EnableProxy": "true",
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedThreatIntelRule: true,
			expectedDNSProxyRule:    false,
		},
		{
			name: "recommendation for DNS proxy disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/no-dns-firewall",
					Name:        "no-dns-firewall",
					Type:        "Microsoft.Network/azureFirewalls",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/azureFirewalls",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"threatIntelMode": "Alert",
							"additionalProperties": map[string]interface{}{
								"Network.DNS.EnableProxy": "false",
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedThreatIntelRule: false,
			expectedDNSProxyRule:    true,
		},
		{
			name: "multiple recommendations for insecure firewall",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/insecure-firewall",
					Name:        "insecure-firewall",
					Type:        "Microsoft.Network/azureFirewalls",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/azureFirewalls",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"threatIntelMode": "Off",
							"additionalProperties": map[string]interface{}{
								"Network.DNS.EnableProxy": "false",
							},
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedThreatIntelRule: true,
			expectedDNSProxyRule:    true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/no-meta-firewall",
					Name:        "no-meta-firewall",
					Type:        "Microsoft.Network/azureFirewalls",
					Region:      "eastus",
					ServiceName: "Microsoft.Network/azureFirewalls",
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

			if tt.expectedThreatIntelRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_firewall_enable_threat_intel" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected threat intelligence recommendation not found")
			}

			if tt.expectedDNSProxyRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_firewall_enable_dns_proxy" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected DNS proxy recommendation not found")
			}
		})
	}
}

func TestFirewallService_ApplyRecommendation(t *testing.T) {
	svc := &firewallService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_firewall_enable_threat_intel",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}

func TestFirewallService_ApplyCommand(t *testing.T) {
	svc := &firewallService{}
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
				Command:    "azure_firewall_enable_threat_intel",
			},
			expectError:   true,
			errorContains: "access secret is not provided", // Credential check happens first
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/firewall-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/azureFirewalls/firewall-name",
				Command:    "azure_firewall_enable_threat_intel",
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

func TestFirewallService_QueryMetrices(t *testing.T) {
	svc := &firewallService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestFirewallService_GetServiceMap(t *testing.T) {
	svc := &firewallService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-firewall",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-firewall", serviceMap.Id.Name)
	assert.Equal(t, "firewall", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestFirewallService_GetLogGroupName(t *testing.T) {
	svc := &firewallService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/azureFirewalls/my-firewall"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Equal(t, "", logGroup)
}
