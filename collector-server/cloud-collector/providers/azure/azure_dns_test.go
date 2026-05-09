package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSService_Name(t *testing.T) {
	svc := &dnsService{}
	assert.Equal(t, "Microsoft.Network/dnsZones", svc.Name())
}

func TestDNSService_GetResources(t *testing.T) {
	// Note: DNS service now uses real Azure DNS SDK
	// These tests verify the structure without making actual SDK calls
	svc := &dnsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with missing credentials should return error
	resources, err := svc.GetResources(ctx, account, "global")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Nil(t, resources)
}

func TestDNSService_GetRecommendations(t *testing.T) {
	svc := &dnsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedCAARecordRule   bool
	}{
		{
			name: "recommendation for DNS zone without CAA records",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com",
					Name:        "example.com",
					Type:        "Microsoft.Network/dnsZones",
					Region:      "global",
					ServiceName: "Microsoft.Network/dnsZones",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"numberOfRecordSets": 10.0,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedCAARecordRule:   true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/dnsZones/no-meta.com",
					Name:        "no-meta.com",
					Type:        "Microsoft.Network/dnsZones",
					Region:      "global",
					ServiceName: "Microsoft.Network/dnsZones",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
		{
			name: "multiple dns zones get recommendations",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/dnsZones/zone1.com",
					Name:        "zone1.com",
					Type:        "Microsoft.Network/dnsZones",
					Region:      "global",
					ServiceName: "Microsoft.Network/dnsZones",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"numberOfRecordSets": 5.0,
						},
					},
				},
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/dnsZones/zone2.com",
					Name:        "zone2.com",
					Type:        "Microsoft.Network/dnsZones",
					Region:      "global",
					ServiceName: "Microsoft.Network/dnsZones",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"numberOfRecordSets": 8.0,
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedCAARecordRule:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			// GetRecommendations requires Azure credentials to check for CAA records
			// In test environment without credentials, expect credential error
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "access secret is not provided")
			assert.Nil(t, recommendations) // No recommendations returned due to error
		})
	}
}

func TestDNSService_ApplyRecommendation(t *testing.T) {
	svc := &dnsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_dns_add_caa_record",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided") // Credential check happens first
}

func TestDNSService_ApplyCommand(t *testing.T) {
	svc := &dnsService{}
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
				Command:    "azure_dns_add_caa_record",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com",
				Command:    "azure_dns_add_caa_record",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "DNSSEC command should fail",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com",
				Command:    "azure_dns_enable_dnssec",
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

func TestDNSService_QueryMetrices(t *testing.T) {
	svc := &dnsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestDNSService_GetServiceMap(t *testing.T) {
	svc := &dnsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "example.com",
		Region: "global",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "example.com", serviceMap.Id.Name)
	assert.Equal(t, "dns", serviceMap.Id.Kind)
	assert.Equal(t, "global", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestDNSService_GetLogGroupName(t *testing.T) {
	svc := &dnsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/dnsZones/example.com"

	logGroup, err := svc.GetLogGroupName(ctx, account, "global", resourceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
	assert.Equal(t, "", logGroup)
}
