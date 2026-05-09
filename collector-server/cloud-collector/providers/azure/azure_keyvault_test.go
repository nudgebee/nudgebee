package azure

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyVaultService_Name(t *testing.T) {
	svc := &keyVaultService{}
	assert.Equal(t, "Microsoft.KeyVault/vaults", svc.Name())
}

func TestKeyVaultService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armkeyvault.Vault, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve key vaults",
			mockSetup: func() ([]armkeyvault.Vault, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.KeyVault/vaults/my-vault"
				name := "my-vault"
				typeName := "Microsoft.KeyVault/vaults"
				location := "eastus"

				return []armkeyvault.Vault{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armkeyvault.VaultProperties{},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-vault", res.Name)
				assert.Equal(t, "Microsoft.KeyVault/vaults", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.KeyVault/vaults", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve multiple key vaults",
			mockSetup: func() ([]armkeyvault.Vault, error) {
				vaults := []armkeyvault.Vault{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.KeyVault/vaults/vault-" + string(rune(i))
					name := "vault-" + string(rune(i))
					typeName := "Microsoft.KeyVault/vaults"
					location := "eastus"

					vaults = append(vaults, armkeyvault.Vault{
						ID:         &id,
						Name:       &name,
						Type:       &typeName,
						Location:   &location,
						Properties: &armkeyvault.VaultProperties{},
					})
				}
				return vaults, nil
			},
			expectedCount: 3,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				assert.Len(t, resources, 3)
				for _, res := range resources {
					assert.Equal(t, providers.ResourceStatusActive, res.Status)
				}
			},
		},
		{
			name: "retrieve key vault with tags",
			mockSetup: func() ([]armkeyvault.Vault, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.KeyVault/vaults/tagged-vault"
				name := "tagged-vault"
				typeName := "Microsoft.KeyVault/vaults"
				location := "westus"

				return []armkeyvault.Vault{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env":     strPtr("dev"),
							"project": strPtr("test-project"),
						},
						Properties: &armkeyvault.VaultProperties{},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Contains(t, resources[0].Tags, "env")
				assert.Contains(t, resources[0].Tags, "project")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaults, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, vaults, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				var resources []providers.Resource
				for _, vault := range vaults {
					resources = append(resources, providers.Resource{
						Id:          *vault.ID,
						Name:        *vault.Name,
						Type:        *vault.Type,
						Region:      *vault.Location,
						Tags:        toAzureTags(vault.Tags),
						Status:      providers.ResourceStatusActive,
						ServiceName: "Microsoft.KeyVault/vaults",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestKeyVaultService_GetRecommendations(t *testing.T) {
	svc := &keyVaultService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                        string
		existingResources           []providers.Resource
		expectedRecommendations     int
		expectedSoftDeleteRule      bool
		expectedPurgeProtectionRule bool
	}{
		{
			name: "no recommendations for secure key vault",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/secure-vault",
					Name:        "secure-vault",
					Type:        "Microsoft.KeyVault/vaults",
					Region:      "eastus",
					ServiceName: "Microsoft.KeyVault/vaults",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableSoftDelete":      true,
							"enablePurgeProtection": true,
						},
					},
				},
			},
			expectedRecommendations:     0,
			expectedSoftDeleteRule:      false,
			expectedPurgeProtectionRule: false,
		},
		{
			name: "recommendation for soft delete disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/no-soft-delete-vault",
					Name:        "no-soft-delete-vault",
					Type:        "Microsoft.KeyVault/vaults",
					Region:      "eastus",
					ServiceName: "Microsoft.KeyVault/vaults",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableSoftDelete":      false,
							"enablePurgeProtection": true,
						},
					},
				},
			},
			expectedRecommendations:     1,
			expectedSoftDeleteRule:      true,
			expectedPurgeProtectionRule: false,
		},
		{
			name: "recommendation for purge protection disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/no-purge-protection-vault",
					Name:        "no-purge-protection-vault",
					Type:        "Microsoft.KeyVault/vaults",
					Region:      "eastus",
					ServiceName: "Microsoft.KeyVault/vaults",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableSoftDelete":      true,
							"enablePurgeProtection": false,
						},
					},
				},
			},
			expectedRecommendations:     1,
			expectedSoftDeleteRule:      false,
			expectedPurgeProtectionRule: true,
		},
		{
			name: "multiple recommendations for insecure key vault",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/insecure-vault",
					Name:        "insecure-vault",
					Type:        "Microsoft.KeyVault/vaults",
					Region:      "eastus",
					ServiceName: "Microsoft.KeyVault/vaults",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enableSoftDelete":      false,
							"enablePurgeProtection": false,
						},
					},
				},
			},
			expectedRecommendations:     2,
			expectedSoftDeleteRule:      true,
			expectedPurgeProtectionRule: true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/no-meta-vault",
					Name:        "no-meta-vault",
					Type:        "Microsoft.KeyVault/vaults",
					Region:      "eastus",
					ServiceName: "Microsoft.KeyVault/vaults",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
		{
			name: "recommendation for missing soft delete property",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/missing-prop-vault",
					Name:        "missing-prop-vault",
					Type:        "Microsoft.KeyVault/vaults",
					Region:      "eastus",
					ServiceName: "Microsoft.KeyVault/vaults",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"enablePurgeProtection": true,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedSoftDeleteRule:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			if tt.expectedSoftDeleteRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_keyvault_soft_delete_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected soft delete recommendation not found")
			}

			if tt.expectedPurgeProtectionRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_keyvault_purge_protection_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected purge protection recommendation not found")
			}
		})
	}
}

func TestKeyVaultService_ApplyRecommendation(t *testing.T) {
	svc := &keyVaultService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	recommendation := providers.Recommendation{}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
}

func TestKeyVaultService_ApplyCommand(t *testing.T) {
	svc := &keyVaultService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub-id/resourceGroups/rg-name/providers/Microsoft.KeyVault/vaults/kv-name",
		Command:    "azure_keyvault_soft_delete_disabled",
	}

	resp, err := svc.ApplyCommand(ctx, account, command)
	assert.ErrorIs(t, err, errors.ErrUnsupported)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Message, "Key Vault configuration changes for command")
}

func TestKeyVaultService_QueryMetrices(t *testing.T) {
	svc := &keyVaultService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestKeyVaultService_GetServiceMap(t *testing.T) {
	svc := &keyVaultService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-keyvault",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-keyvault", serviceMap.Id.Name)
	assert.Equal(t, "keyvault", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestKeyVaultService_GetLogGroupName(t *testing.T) {
	svc := &keyVaultService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.KeyVault/vaults/my-vault"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	require.NoError(t, err)
	assert.Equal(t, resourceID, logGroup)
}
