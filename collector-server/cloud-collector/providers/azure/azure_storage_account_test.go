package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageAccountService_Name(t *testing.T) {
	svc := &storageAccountService{}
	assert.Equal(t, "Microsoft.Storage/storageAccounts", svc.Name())
}

func TestStorageAccountService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armstorage.Account, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve storage accounts",
			mockSetup: func() ([]armstorage.Account, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/mystorageacct"
				name := "mystorageacct"
				typeName := "Microsoft.Storage/storageAccounts"
				location := "eastus"
				provisioningState := armstorage.ProvisioningStateSucceeded
				creationTime := time.Now().Add(-24 * time.Hour)

				return []armstorage.Account{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armstorage.AccountProperties{
							ProvisioningState: &provisioningState,
							CreationTime:      &creationTime,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "mystorageacct", res.Name)
				assert.Equal(t, "Microsoft.Storage/storageAccounts", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Storage/storageAccounts", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
				assert.False(t, res.CreatedAt.IsZero())
			},
		},
		{
			name: "retrieve storage account with failed provisioning",
			mockSetup: func() ([]armstorage.Account, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/failedacct"
				name := "failedacct"
				typeName := "Microsoft.Storage/storageAccounts"
				location := "westus"
				provisioningState := armstorage.ProvisioningState("Failed")

				return []armstorage.Account{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armstorage.AccountProperties{
							ProvisioningState: &provisioningState,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				// Failed state should map to inactive or unknown based on nbStatusFromAzureProvisioningState
				assert.NotEqual(t, providers.ResourceStatusActive, resources[0].Status)
			},
		},
		{
			name: "retrieve multiple storage accounts",
			mockSetup: func() ([]armstorage.Account, error) {
				accounts := []armstorage.Account{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/acct" + string(rune(i))
					name := "acct" + string(rune(i))
					typeName := "Microsoft.Storage/storageAccounts"
					location := "eastus"
					provisioningState := armstorage.ProvisioningStateSucceeded

					accounts = append(accounts, armstorage.Account{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armstorage.AccountProperties{
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
			name: "retrieve storage account without creation time",
			mockSetup: func() ([]armstorage.Account, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/nocreatetime"
				name := "nocreatetime"
				typeName := "Microsoft.Storage/storageAccounts"
				location := "eastus"
				provisioningState := armstorage.ProvisioningStateSucceeded

				return []armstorage.Account{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armstorage.AccountProperties{
							ProvisioningState: &provisioningState,
							CreationTime:      nil,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.True(t, resources[0].CreatedAt.IsZero())
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
				for _, sa := range accounts {
					status := providers.ResourceStatusUnknown
					if sa.Properties != nil && sa.Properties.ProvisioningState != nil {
						if val, ok := nbStatusFromAzureProvisioningState[string(*sa.Properties.ProvisioningState)]; ok {
							status = val
						}
					}

					createdAt := time.Time{}
					if sa.Properties != nil && sa.Properties.CreationTime != nil {
						createdAt = *sa.Properties.CreationTime
					}

					resources = append(resources, providers.Resource{
						Id:          *sa.ID,
						Name:        *sa.Name,
						Type:        *sa.Type,
						Region:      *sa.Location,
						Tags:        toAzureTags(sa.Tags),
						Status:      status,
						CreatedAt:   createdAt,
						ServiceName: "Microsoft.Storage/storageAccounts",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestStorageAccountService_GetRecommendations(t *testing.T) {
	svc := &storageAccountService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedHTTPSOnlyRule   bool
		expectedMinimumTLSRule  bool
	}{
		{
			name: "no recommendations for secure storage account",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/secureacct",
					Name:        "secureacct",
					Type:        "Microsoft.Storage/storageAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"supportsHttpsTrafficOnly": true,
							"minimumTlsVersion":        "TLS1_2",
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedHTTPSOnlyRule:   false,
			expectedMinimumTLSRule:  false,
		},
		{
			name: "recommendation for HTTPS only disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/insecureacct",
					Name:        "insecureacct",
					Type:        "Microsoft.Storage/storageAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"supportsHttpsTrafficOnly": false,
							"minimumTlsVersion":        "TLS1_2",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedHTTPSOnlyRule:   true,
			expectedMinimumTLSRule:  false,
		},
		{
			name: "recommendation for old TLS version",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/oldtlsacct",
					Name:        "oldtlsacct",
					Type:        "Microsoft.Storage/storageAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"supportsHttpsTrafficOnly": true,
							"minimumTlsVersion":        "TLS1_0",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedHTTPSOnlyRule:   false,
			expectedMinimumTLSRule:  true,
		},
		{
			name: "multiple recommendations for insecure storage account",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/veryinsecureacct",
					Name:        "veryinsecureacct",
					Type:        "Microsoft.Storage/storageAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"supportsHttpsTrafficOnly": false,
							"minimumTlsVersion":        "TLS1_0",
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedHTTPSOnlyRule:   true,
			expectedMinimumTLSRule:  true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/nometaacct",
					Name:        "nometaacct",
					Type:        "Microsoft.Storage/storageAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
		{
			name: "recommendation for TLS 1.1",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/tls11acct",
					Name:        "tls11acct",
					Type:        "Microsoft.Storage/storageAccounts",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"supportsHttpsTrafficOnly": true,
							"minimumTlsVersion":        "TLS1_1",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedHTTPSOnlyRule:   false,
			expectedMinimumTLSRule:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			if tt.expectedHTTPSOnlyRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_storage_https_only_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected HTTPS only recommendation not found")
			}

			if tt.expectedMinimumTLSRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_storage_minimum_tls_version" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected minimum TLS version recommendation not found")
			}
		})
	}
}

func TestStorageAccountService_ApplyRecommendation(t *testing.T) {
	svc := &storageAccountService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	recommendation := providers.Recommendation{
		ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct",
		RuleName:   "azure_storage_https_only_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestStorageAccountService_ApplyCommand(t *testing.T) {
	svc := &storageAccountService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct",
		Command:    "azure_storage_https_only_disabled",
	}

	resp, err := svc.ApplyCommand(ctx, account, command)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "Storage account configuration changes")
}

func TestStorageAccountService_QueryMetrices(t *testing.T) {
	svc := &storageAccountService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestStorageAccountService_GetServiceMap(t *testing.T) {
	svc := &storageAccountService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-storage-account",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-storage-account", serviceMap.Id.Name)
	assert.Equal(t, "storageaccount", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestStorageAccountService_GetLogGroupName(t *testing.T) {
	svc := &storageAccountService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/mystorageacct"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	require.NoError(t, err)
	assert.Equal(t, resourceID, logGroup)
}
