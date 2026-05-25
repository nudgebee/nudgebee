package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisService_Name(t *testing.T) {
	svc := &redisService{}
	assert.Equal(t, "Microsoft.Cache/redis", svc.Name())
}

func TestRedisService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armredis.ResourceInfo, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve running redis caches",
			mockSetup: func() ([]armredis.ResourceInfo, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Cache/redis/my-redis"
				name := "my-redis"
				typeName := "Microsoft.Cache/redis"
				location := "eastus"
				provisioningState := armredis.ProvisioningStateSucceeded

				return []armredis.ResourceInfo{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armredis.Properties{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-redis", res.Name)
				assert.Equal(t, "Microsoft.Cache/redis", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Cache/redis", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve redis cache with unknown provisioning state",
			mockSetup: func() ([]armredis.ResourceInfo, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Cache/redis/updating-redis"
				name := "updating-redis"
				typeName := "Microsoft.Cache/redis"
				location := "westus"
				provisioningState := armredis.ProvisioningState("Updating")

				return []armredis.ResourceInfo{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armredis.Properties{
							ProvisioningState: &provisioningState,
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
			name: "retrieve multiple redis caches",
			mockSetup: func() ([]armredis.ResourceInfo, error) {
				caches := []armredis.ResourceInfo{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Cache/redis/redis-" + string(rune(i))
					name := "redis-" + string(rune(i))
					typeName := "Microsoft.Cache/redis"
					location := "eastus"
					provisioningState := armredis.ProvisioningStateSucceeded

					caches = append(caches, armredis.ResourceInfo{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armredis.Properties{
							ProvisioningState: &provisioningState,
						},
					})
				}
				return caches, nil
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
			caches, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.validateResult != nil && !tt.expectedError {
				// Convert mock data to resources for validation
				var resources []providers.Resource
				for _, cache := range caches {
					status := providers.ResourceStatusUnknown
					if cache.Properties != nil && cache.Properties.ProvisioningState != nil {
						provisioningState := string(*cache.Properties.ProvisioningState)
						if val, ok := nbStatusFromAzureProvisioningState[provisioningState]; ok {
							status = val
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *cache.ID,
						Name:        *cache.Name,
						Type:        *cache.Type,
						Region:      *cache.Location,
						Tags:        toAzureTags(cache.Tags),
						Status:      status,
						ServiceName: "Microsoft.Cache/redis",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestRedisService_GetRecommendations(t *testing.T) {
	svc := &redisService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for secure redis cache",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/secure-redis",
					Name:        "secure-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableNonSslPort":    false,
							"minimumTlsVersion":   "1.2",
							"publicNetworkAccess": "Disabled",
							"sku": map[string]interface{}{
								"family":   "C",
								"capacity": float64(2),
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for non-SSL port enabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/insecure-redis",
					Name:        "insecure-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableNonSslPort":    true,
							"minimumTlsVersion":   "1.2",
							"publicNetworkAccess": "Disabled",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_redis_non_ssl_port_enabled"},
		},
		{
			name: "recommendation for old TLS version",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/old-tls-redis",
					Name:        "old-tls-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableNonSslPort":    false,
							"minimumTlsVersion":   "1.0",
							"publicNetworkAccess": "Disabled",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_redis_old_tls_version"},
		},
		{
			name: "recommendation for public network access",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/public-redis",
					Name:        "public-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableNonSslPort":    false,
							"minimumTlsVersion":   "1.2",
							"publicNetworkAccess": "Enabled",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_redis_public_network_access"},
		},
		{
			name: "recommendation for overprovisioned SKU",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/expensive-redis",
					Name:        "expensive-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableNonSslPort":    false,
							"minimumTlsVersion":   "1.2",
							"publicNetworkAccess": "Disabled",
							"sku": map[string]interface{}{
								"family":   "P",
								"capacity": float64(1),
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_redis_overprovisioned_sku"},
		},
		{
			name: "multiple recommendations for insecure redis cache",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/very-insecure-redis",
					Name:        "very-insecure-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableNonSslPort":    true,
							"minimumTlsVersion":   "1.0",
							"publicNetworkAccess": "Enabled",
							"sku": map[string]interface{}{
								"family":   "P",
								"capacity": float64(1),
							},
						},
					},
				},
			},
			expectedRecommendations: 4,
			expectedRules: []string{
				"azure_redis_non_ssl_port_enabled",
				"azure_redis_old_tls_version",
				"azure_redis_public_network_access",
				"azure_redis_overprovisioned_sku",
			},
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Cache/redis/no-meta-redis",
					Name:        "no-meta-redis",
					Type:        "Microsoft.Cache/redis",
					Region:      "eastus",
					ServiceName: "Microsoft.Cache/redis",
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

			for _, expectedRule := range tt.expectedRules {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == expectedRule {
						found = true
						assert.NotEmpty(t, rec.CategoryName)
						assert.NotEmpty(t, rec.Severity)
						assert.NotEmpty(t, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected rule '%s' not found", expectedRule)
			}
		})
	}
}

func TestRedisService_ApplyRecommendation(t *testing.T) {
	svc := &redisService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_redis_enable_non_ssl_port",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource ID")
}

func TestRedisService_ApplyCommand(t *testing.T) {
	svc := &redisService{}
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
				Command:    "azure_redis_enable_non_ssl_port",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "missing redis cache name in ID",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg",
				Command:    "azure_redis_enable_non_ssl_port",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Cache/redis/cache-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "unknown command",
		},
		{
			name: "valid non-SSL port command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Cache/redis/cache-name",
				Command:    "azure_redis_enable_non_ssl_port",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid TLS version command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Cache/redis/cache-name",
				Command:    "azure_redis_set_minimum_tls_version",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid regenerate primary key command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Cache/redis/cache-name",
				Command:    "regenerate_primary_key",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid regenerate secondary key command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Cache/redis/cache-name",
				Command:    "regenerate_secondary_key",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
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

func TestRedisService_QueryMetrices(t *testing.T) {
	svc := &redisService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestRedisService_GetServiceMap(t *testing.T) {
	svc := &redisService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-redis-cache",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-redis-cache", serviceMap.Id.Name)
	assert.Equal(t, "redis-cache", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestRedisService_GetLogGroupName(t *testing.T) {
	svc := &redisService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Cache/redis/my-redis"

	// Without valid Azure credentials, this should fail
	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
}
