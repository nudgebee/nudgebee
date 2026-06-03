package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservices/armrecoveryservices"
)

type recoveryServicesVaultService struct {
}

func (s *recoveryServicesVaultService) Name() string {
	return "microsoft.recoveryservices/vaults"
}

func (s *recoveryServicesVaultService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *recoveryServicesVaultService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armrecoveryservices.NewVaultsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create recovery services vault client: %w", err)
		}

		pager := client.NewListBySubscriptionIDPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, vault := range page.Value {
				status := providers.ResourceStatusUnknown
				if vault.Properties != nil && vault.Properties.ProvisioningState != nil {
					if *vault.Properties.ProvisioningState == "Succeeded" {
						status = providers.ResourceStatusActive
					}
				}

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

func (s *recoveryServicesVaultService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *recoveryServicesVaultService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check if soft delete is enabled
			if securitySettings, ok := props["securitySettings"].(map[string]interface{}); ok {
				if softDeleteSettings, ok := securitySettings["softDeleteSettings"].(map[string]interface{}); ok {
					softDeleteState, _ := softDeleteSettings["softDeleteState"].(string)
					if softDeleteState != "Enabled" && softDeleteState != "AlwaysON" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     "azure_recovery_vault_soft_delete_disabled",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"resource_id":     resource.Id,
								"resource_name":   resource.Name,
								"resource_type":   resource.Type,
								"resource_region": resource.Region,
								"service_name":    resource.ServiceName,
								"reason":          "Recovery Services Vault does not have soft delete enabled. Soft delete protects backup data from accidental or malicious deletion by retaining data for 14 additional days.",
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}

			// Check redundancy type
			if storageSettings, ok := props["storageSettings"].([]interface{}); ok {
				for _, setting := range storageSettings {
					if settingMap, ok := setting.(map[string]interface{}); ok {
						datastoreType, _ := settingMap["datastoreType"].(string)
						storageType, _ := settingMap["type"].(string)
						if datastoreType == "VaultStore" && storageType == "LocallyRedundant" {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName: providers.RecommendationCategoryConfiguration,
								RuleName:     "azure_recovery_vault_lrs_storage",
								Severity:     providers.RecommendationSeverityMedium,
								Savings:      0,
								Data: map[string]any{
									"resource_id":     resource.Id,
									"resource_name":   resource.Name,
									"resource_type":   resource.Type,
									"resource_region": resource.Region,
									"service_name":    resource.ServiceName,
									"reason":          "Recovery Services Vault is using Locally Redundant Storage (LRS). Consider Geo-Redundant Storage (GRS) for critical backup data to protect against regional outages.",
								},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}
					}
				}
			}
		}
	}
	return recommendations, nil
}

func (s *recoveryServicesVaultService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("recovery services vault modifications require manual configuration")
}

func (s *recoveryServicesVaultService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("unknown command: %s", command.Command),
	}, fmt.Errorf("unknown command: %s", command.Command)
}

func (s *recoveryServicesVaultService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *recoveryServicesVaultService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "recoveryservicesvault",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}
