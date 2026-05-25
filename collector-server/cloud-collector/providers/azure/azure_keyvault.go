package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
)

type keyVaultService struct {
}

func (s *keyVaultService) Name() string {
	return "Microsoft.KeyVault/vaults"
}

// Scope returns the service scope - this is a regional service
func (s *keyVaultService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *keyVaultService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	var subscriptionIDs = strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}
		client, err := armkeyvault.NewVaultsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create keyvault client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, vault := range page.Value {
				status := providers.ResourceStatusActive

				allResources = append(allResources, providers.Resource{
					Id:          *vault.ID,
					Name:        *vault.Name,
					Type:        *vault.Type,
					Region:      *vault.Location,
					Tags:        toAzureTags(vault.Tags),
					Meta:        structToMap(vault),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *vault.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *keyVaultService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *keyVaultService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Key Vault operations are sensitive and most recommendations require manual review
	// Return unsupported for security reasons - Key Vault changes should be reviewed manually
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("Key Vault configuration changes for command '%s' must be applied manually for security reasons", command.Command),
	}, errors.ErrUnsupported
}

func (s *keyVaultService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *keyVaultService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check soft delete
			if softDelete, ok := props["enableSoftDelete"].(bool); !ok || !softDelete {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_keyvault_soft_delete_disabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "Key Vault should have soft delete enabled to allow recovery of deleted vaults, keys, secrets, and certificates within the retention period"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check purge protection
			if purgeProtection, ok := props["enablePurgeProtection"].(bool); !ok || !purgeProtection {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_keyvault_purge_protection_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"reason": "Key Vault should have purge protection enabled to prevent permanent deletion and ensure compliance with data retention policies"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}
	return allRecommendations, nil
}

func (s *keyVaultService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "keyvault",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *keyVaultService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Azure Key Vault logs are typically found in Log Analytics workspace
	// Format: /subscriptions/{subscription}/resourceGroups/{rg}/providers/Microsoft.KeyVault/vaults/{name}
	return resourceId, nil
}
