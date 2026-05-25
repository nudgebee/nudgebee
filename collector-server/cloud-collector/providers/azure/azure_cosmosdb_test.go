package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCosmosDBService_Name(t *testing.T) {
	svc := &cosmosDBService{}
	assert.Equal(t, "Microsoft.DocumentDB/databaseAccounts", svc.Name())
}

func TestCosmosDBService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armcosmos.DatabaseAccountGetResults, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve cosmos db accounts",
			mockSetup: func() ([]armcosmos.DatabaseAccountGetResults, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DocumentDB/databaseAccounts/my-cosmos-db"
				name := "my-cosmos-db"
				typeName := "Microsoft.DocumentDB/databaseAccounts"
				location := "eastus"
				provisioningState := "Succeeded"

				return []armcosmos.DatabaseAccountGetResults{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armcosmos.DatabaseAccountGetProperties{
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
				assert.Equal(t, "my-cosmos-db", res.Name)
				assert.Equal(t, "Microsoft.DocumentDB/databaseAccounts", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.DocumentDB/databaseAccounts", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve cosmos db with failed provisioning",
			mockSetup: func() ([]armcosmos.DatabaseAccountGetResults, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DocumentDB/databaseAccounts/failed-db"
				name := "failed-db"
				typeName := "Microsoft.DocumentDB/databaseAccounts"
				location := "westus"
				provisioningState := "Failed"

				return []armcosmos.DatabaseAccountGetResults{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armcosmos.DatabaseAccountGetProperties{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				// Failed state should map based on nbStatusFromAzureProvisioningState
				assert.NotEqual(t, providers.ResourceStatusActive, resources[0].Status)
			},
		},
		{
			name: "retrieve multiple cosmos db accounts",
			mockSetup: func() ([]armcosmos.DatabaseAccountGetResults, error) {
				accounts := []armcosmos.DatabaseAccountGetResults{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DocumentDB/databaseAccounts/db-" + string(rune(i))
					name := "db-" + string(rune(i))
					typeName := "Microsoft.DocumentDB/databaseAccounts"
					location := "eastus"
					provisioningState := "Succeeded"

					accounts = append(accounts, armcosmos.DatabaseAccountGetResults{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armcosmos.DatabaseAccountGetProperties{
							ProvisioningState: &provisioningState,
						},
					})
				}
				return accounts, nil
			},
			expectedCount: 3,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				assert.Len(t, resources, 3)
			},
		},
		{
			name: "retrieve cosmos db with unknown provisioning state",
			mockSetup: func() ([]armcosmos.DatabaseAccountGetResults, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DocumentDB/databaseAccounts/unknown-db"
				name := "unknown-db"
				typeName := "Microsoft.DocumentDB/databaseAccounts"
				location := "eastus"
				provisioningState := "Updating"

				return []armcosmos.DatabaseAccountGetResults{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armcosmos.DatabaseAccountGetProperties{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				// Unknown provisioning states should have appropriate status
				assert.NotEmpty(t, resources[0].Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, accounts, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				var resources []providers.Resource
				for _, db := range accounts {
					status := providers.ResourceStatusUnknown
					if db.Properties != nil && db.Properties.ProvisioningState != nil {
						if val, ok := nbStatusFromAzureProvisioningState[*db.Properties.ProvisioningState]; ok {
							status = val
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *db.ID,
						Name:        *db.Name,
						Type:        *db.Type,
						Region:      *db.Location,
						Tags:        toAzureTags(db.Tags),
						Status:      status,
						ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestCosmosDBService_GetRecommendations(t *testing.T) {
	svc := &cosmosDBService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                          string
		existingResources             []providers.Resource
		expectedRecommendations       int
		expectedAutomaticFailoverRule bool
		expectedSingleRegionRule      bool
	}{
		{
			name: "no recommendations for well-configured cosmos db",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/secure-db",
					Name:        "secure-db",
					Type:        "Microsoft.DocumentDB/databaseAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableAutomaticFailover": true,
							"locations": []interface{}{
								map[string]interface{}{"locationName": "East US"},
								map[string]interface{}{"locationName": "West US"},
							},
						},
					},
				},
			},
			expectedRecommendations:       0,
			expectedAutomaticFailoverRule: false,
			expectedSingleRegionRule:      false,
		},
		{
			name: "recommendation for automatic failover disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/no-failover-db",
					Name:        "no-failover-db",
					Type:        "Microsoft.DocumentDB/databaseAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableAutomaticFailover": false,
							"locations": []interface{}{
								map[string]interface{}{"locationName": "East US"},
								map[string]interface{}{"locationName": "West US"},
							},
						},
					},
				},
			},
			expectedRecommendations:       1,
			expectedAutomaticFailoverRule: true,
			expectedSingleRegionRule:      false,
		},
		{
			name: "recommendation for single region deployment",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/single-region-db",
					Name:        "single-region-db",
					Type:        "Microsoft.DocumentDB/databaseAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableAutomaticFailover": true,
							"locations": []interface{}{
								map[string]interface{}{"locationName": "East US"},
							},
						},
					},
				},
			},
			expectedRecommendations:       1,
			expectedAutomaticFailoverRule: false,
			expectedSingleRegionRule:      true,
		},
		{
			name: "multiple recommendations for poorly configured cosmos db",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/poorly-configured-db",
					Name:        "poorly-configured-db",
					Type:        "Microsoft.DocumentDB/databaseAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableAutomaticFailover": false,
							"locations": []interface{}{
								map[string]interface{}{"locationName": "East US"},
							},
						},
					},
				},
			},
			expectedRecommendations:       2,
			expectedAutomaticFailoverRule: true,
			expectedSingleRegionRule:      true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/no-meta-db",
					Name:        "no-meta-db",
					Type:        "Microsoft.DocumentDB/databaseAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
		{
			name: "no recommendation when locations property missing",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/no-locations-db",
					Name:        "no-locations-db",
					Type:        "Microsoft.DocumentDB/databaseAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.DocumentDB/databaseAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableAutomaticFailover": true,
						},
					},
				},
			},
			expectedRecommendations:       0,
			expectedAutomaticFailoverRule: false,
			expectedSingleRegionRule:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			if tt.expectedAutomaticFailoverRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_cosmosdb_automatic_failover_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryConfiguration, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected automatic failover recommendation not found")
			}

			if tt.expectedSingleRegionRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_cosmosdb_single_region" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryConfiguration, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected single region recommendation not found")
			}
		})
	}
}

func TestCosmosDBService_ApplyRecommendation(t *testing.T) {
	svc := &cosmosDBService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	recommendation := providers.Recommendation{}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestCosmosDBService_ApplyCommand(t *testing.T) {
	svc := &cosmosDBService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{}

	resp, err := svc.ApplyCommand(ctx, account, command)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Equal(t, providers.ApplyCommandResponse{}, resp)
}

func TestCosmosDBService_QueryMetrices(t *testing.T) {
	svc := &cosmosDBService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestCosmosDBService_GetServiceMap(t *testing.T) {
	svc := &cosmosDBService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-cosmos-db",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-cosmos-db", serviceMap.Id.Name)
	assert.Equal(t, "cosmosdb", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestCosmosDBService_GetLogGroupName(t *testing.T) {
	svc := &cosmosDBService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DocumentDB/databaseAccounts/my-cosmos-db"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	require.NoError(t, err)
	assert.Equal(t, resourceID, logGroup)
}
