package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

type blobStorageService struct {
}

func (s *blobStorageService) Name() string {
	return "Microsoft.Storage/storageAccounts"
}

// Scope returns the service scope - this is a regional service
func (s *blobStorageService) Scope() ServiceScope {
	return ServiceScopeRegional
}

const (
	StorageKey1 = "key1"
	StorageKey2 = "key2"
)

func isValidStorageKeyName(k string) bool {
	return k == StorageKey1 || k == StorageKey2
}

func (s *blobStorageService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		storageAccountsClient, err := armstorage.NewAccountsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create storage accounts client: %w", err)
		}

		storageAccountsPager := storageAccountsClient.NewListPager(nil)
		for storageAccountsPager.More() {
			storageAccountsPage, err := storageAccountsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to list storage accounts: %w", err)
			}

			for _, storageAccount := range storageAccountsPage.Value {
				status := providers.ResourceStatusUnknown
				if storageAccount.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*storageAccount.Properties.ProvisioningState)]; ok {
						status = val
					}
				}
				createdAt := time.Time{}
				if storageAccount.Properties.CreationTime != nil {
					createdAt = *storageAccount.Properties.CreationTime
				}
				allResources = append(allResources, providers.Resource{
					Id:          *storageAccount.ID,
					Name:        *storageAccount.Name,
					Type:        *storageAccount.Type,
					Region:      *storageAccount.Location,
					Tags:        toAzureTags(storageAccount.Tags),
					Meta:        structToMap(storageAccount),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *storageAccount.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *blobStorageService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *blobStorageService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	for _, resource := range existingResources {
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "azure_missing_tags",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		if properties, ok := resource.Meta["properties"].(map[string]interface{}); ok {
			// Check for Secure Transfer
			if supportsHttpsTrafficOnly, ok := properties["supportsHttpsTrafficOnly"].(bool); !ok || !supportsHttpsTrafficOnly {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_secure_transfer_disabled",
					Severity:     providers.RecommendationSeverityHigh,
					Data: map[string]any{
						"reason":                   "Storage account should enforce secure transfer (HTTPS) for data in transit",
						"storageAccount":           resource,
						"supportsHttpsTrafficOnly": supportsHttpsTrafficOnly,
						"properties":               properties,
						"tags":                     resource.Tags,
						"meta":                     resource.Meta,
						"status":                   resource.Status,
						"createdAt":                resource.CreatedAt,
						"arn":                      resource.Arn,
						"serviceName":              resource.ServiceName,
						"id":                       resource.Id,
						"type":                     resource.Type,
						"region":                   resource.Region,
						"name":                     resource.Name,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Anonymous Access
			if allowBlobPublicAccess, ok := properties["allowBlobPublicAccess"].(bool); ok && allowBlobPublicAccess {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_anonymous_access_enabled",
					Severity:     providers.RecommendationSeverityCritical,
					Data: map[string]any{
						"reason":                "Storage account allows anonymous public access to blobs",
						"storageAccount":        resource,
						"allowBlobPublicAccess": allowBlobPublicAccess,
						"properties":            properties,
						"tags":                  resource.Tags,
						"meta":                  resource.Meta,
						"status":                resource.Status,
						"createdAt":             resource.CreatedAt,
						"arn":                   resource.Arn,
						"serviceName":           resource.ServiceName,
						"id":                    resource.Id,
						"type":                  resource.Type,
						"region":                resource.Region,
						"name":                  resource.Name,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Minimum TLS Version
			if minTlsVersion, ok := properties["minimumTlsVersion"].(string); !ok || minTlsVersion != "TLS1_2" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_minimum_tls_version_not_set_to_1_2",
					Severity:     providers.RecommendationSeverityMedium,
					Data: map[string]any{
						"reason":            "Storage account should enforce minimum TLS version 1.2 for secure communication",
						"storageAccount":    resource,
						"minimumTlsVersion": minTlsVersion,
						"properties":        properties,
						"tags":              resource.Tags,
						"meta":              resource.Meta,
						"status":            resource.Status,
						"createdAt":         resource.CreatedAt,
						"arn":               resource.Arn,
						"serviceName":       resource.ServiceName,
						"id":                resource.Id,
						"type":              resource.Type,
						"region":            resource.Region,
						"name":              resource.Name,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Public Network Access
			if publicNetworkAccess, ok := properties["publicNetworkAccess"].(string); ok && publicNetworkAccess != "Disabled" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_public_network_access_enabled",
					Severity:     providers.RecommendationSeverityCritical,
					Data: map[string]any{
						"reason":              "Storage account allows public network access",
						"storageAccount":      resource,
						"publicNetworkAccess": publicNetworkAccess,
						"properties":          properties,
						"tags":                resource.Tags,
						"meta":                resource.Meta,
						"status":              resource.Status,
						"createdAt":           resource.CreatedAt,
						"arn":                 resource.Arn,
						"serviceName":         resource.ServiceName,
						"id":                  resource.Id,
						"type":                resource.Type,
						"region":              resource.Region,
						"name":                resource.Name,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Shared Key Authorization
			if allowSharedKeyAccess, ok := properties["allowSharedKeyAccess"].(bool); ok && allowSharedKeyAccess {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_storage_shared_key_authorization_enabled",
					Severity:     providers.RecommendationSeverityHigh,
					Data: map[string]any{
						"reason": "Storage account should not allow shared key authorization",
						"storageAccount": map[string]any{
							"id":     resource.Id,
							"name":   resource.Name,
							"type":   resource.Type,
							"region": resource.Region,
							"tags":   resource.Tags,
							"meta":   resource.Meta,
							"status": resource.Status,
						},
						"allowSharedKeyAccess": allowSharedKeyAccess,
						"properties":           properties,
						"tags":                 resource.Tags,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Geo-Redundant Storage (GRS)
			if sku, ok := resource.Meta["sku"].(map[string]interface{}); ok {
				if name, ok := sku["name"].(string); ok && !strings.Contains(strings.ToLower(name), "grs") {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_storage_geo_redundant_storage_disabled",
						Severity:     providers.RecommendationSeverityHigh,
						Data: map[string]any{
							"reason":         "Storage account should use Geo-Redundant Storage (GRS) for better durability",
							"storageAccount": resource,
							"sku":            sku,
							"properties":     properties,
							"tags":           resource.Tags,
							"meta":           resource.Meta,
							"status":         resource.Status,
							"createdAt":      resource.CreatedAt,
							"arn":            resource.Arn,
							"serviceName":    resource.ServiceName,
							"id":             resource.Id,
							"type":           resource.Type,
							"region":         resource.Region,
							"name":           resource.Name,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for Soft Delete
			if blobSoftDeletePolicy, ok := properties["deleteRetentionPolicy"].(map[string]interface{}); ok {
				if enabled, ok := blobSoftDeletePolicy["enabled"].(bool); !ok || !enabled {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_storage_soft_delete_disabled",
						Severity:     providers.RecommendationSeverityMedium,
						Data: map[string]any{
							"reason":                "Enable soft delete to protect blobs from accidental deletion",
							"storageAccount":        resource,
							"deleteRetentionPolicy": blobSoftDeletePolicy,
							"properties":            properties,
							"tags":                  resource.Tags,
							"meta":                  resource.Meta,
							"status":                resource.Status,
							"createdAt":             resource.CreatedAt,
							"arn":                   resource.Arn,
							"serviceName":           resource.ServiceName,
							"id":                    resource.Id,
							"type":                  resource.Type,
							"region":                resource.Region,
							"name":                  resource.Name,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for Versioning
			if isVersioningEnabled, ok := properties["isVersioningEnabled"].(bool); !ok || !isVersioningEnabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_storage_versioning_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Data: map[string]any{
						"reason":         "Enable versioning to protect blobs from accidental overwrites and deletions",
						"storageAccount": resource,
						"properties":     properties,
						"tags":           resource.Tags,
						"meta":           resource.Meta,
						"status":         resource.Status,
						"createdAt":      resource.CreatedAt,
						"arn":            resource.Arn,
						"serviceName":    resource.ServiceName,
						"id":             resource.Id,
						"type":           resource.Type,
						"region":         resource.Region,
						"name":           resource.Name,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for Customer-Managed Keys (CMK)
			if encryption, ok := properties["encryption"].(map[string]interface{}); ok {
				if keySource, ok := encryption["keySource"].(string); ok && keySource != "Microsoft.Keyvault" {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_storage_cmk_disabled",
						Severity:     providers.RecommendationSeverityHigh,
						Data: map[string]any{
							"reason":         "Storage account should use Customer-Managed Keys (CMK) for enhanced security",
							"storageAccount": resource,
							"encryption":     encryption,
							"properties":     properties,
							"tags":           resource.Tags,
							"meta":           resource.Meta,
							"status":         resource.Status,
							"createdAt":      resource.CreatedAt,
							"arn":            resource.Arn,
							"serviceName":    resource.ServiceName,
							"id":             resource.Id,
							"type":           resource.Type,
							"region":         resource.Region,
							"name":           resource.Name,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check for Logging
			if logging, ok := properties["logging"].(map[string]interface{}); ok {
				if _, ok := logging["read"]; !ok {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_storage_logging_for_read_access_disabled",
						Severity:     providers.RecommendationSeverityMedium,
						Data: map[string]any{
							"reason":         "Storage account should have logging enabled for read access",
							"storageAccount": resource,
							"logging":        logging,
							"properties":     properties,
							"tags":           resource.Tags,
							"meta":           resource.Meta,
							"status":         resource.Status,
							"createdAt":      resource.CreatedAt,
							"arn":            resource.Arn,
							"serviceName":    resource.ServiceName,
							"id":             resource.Id,
							"type":           resource.Type,
							"region":         resource.Region,
							"name":           resource.Name,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
				if _, ok := logging["write"]; !ok {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_storage_logging_for_write_access_disabled",
						Severity:     providers.RecommendationSeverityMedium,
						Data: map[string]any{
							"reason":         "Storage account should have logging enabled for write access",
							"storageAccount": resource,
							"logging":        logging,
							"properties":     properties,
							"tags":           resource.Tags,
							"meta":           resource.Meta,
							"status":         resource.Status,
							"createdAt":      resource.CreatedAt,
							"arn":            resource.Arn,
							"serviceName":    resource.ServiceName,
							"id":             resource.Id,
							"type":           resource.Type,
							"region":         resource.Region,
							"name":           resource.Name,
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
				if _, ok := logging["delete"]; !ok {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_storage_logging_for_delete_access_disabled",
						Severity:     providers.RecommendationSeverityMedium,
						Data: map[string]any{
							"reason":         "Storage account should have logging enabled for delete access",
							"storageAccount": resource,
							"logging":        logging,
							"properties":     properties,
							"tags":           resource.Tags,
							"meta":           resource.Meta,
							"status":         resource.Status,
							"createdAt":      resource.CreatedAt,
							"arn":            resource.Arn,
							"serviceName":    resource.ServiceName,
							"id":             resource.Id,
							"type":           resource.Type,
							"region":         resource.Region,
							"name":           resource.Name,
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
	return recommendations, nil
}

func (s *blobStorageService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for blob",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Extract subscription ID from resource ID
	parts := strings.Split(recommendation.ResourceId, "/")
	var subscriptionID string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
			break
		}
	}
	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}

	client, err := armstorage.NewAccountsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return fmt.Errorf("failed to create storage accounts client: %w", err)
	}

	// Extract resource group and storage account name from resource ID
	var resourceGroup, storageAccountName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "storageAccounts" && i+1 < len(parts) {
			storageAccountName = parts[i+1]
		}
	}

	if resourceGroup == "" || storageAccountName == "" {
		return fmt.Errorf("failed to extract resource group or storage account name from resource ID: %s", recommendation.ResourceId)
	}

	// Get the current storage account configuration
	accountResp, err := client.GetProperties(ctx.GetContext(), resourceGroup, storageAccountName, nil)
	if err != nil {
		return fmt.Errorf("failed to get storage account: %w", err)
	}

	storageAccount := accountResp.Account
	if storageAccount.Properties == nil {
		return fmt.Errorf("storage account properties are nil")
	}

	// Apply the recommendation based on the rule name
	modified := false
	switch recommendation.RuleName {
	case "azure_storage_secure_transfer_disabled":
		// Enable HTTPS-only traffic
		enabled := true
		storageAccount.Properties.EnableHTTPSTrafficOnly = &enabled
		modified = true
		logger.Info("applying recommendation: enabling HTTPS-only traffic", "storageAccount", storageAccountName)

	case "azure_storage_anonymous_access_enabled":
		// Disable anonymous blob public access
		disabled := false
		storageAccount.Properties.AllowBlobPublicAccess = &disabled
		modified = true
		logger.Info("applying recommendation: disabling anonymous blob access", "storageAccount", storageAccountName)

	case "azure_storage_minimum_tls_version_not_set_to_1_2":
		// Set minimum TLS version to 1.2
		tlsVersion := armstorage.MinimumTLSVersionTLS12
		storageAccount.Properties.MinimumTLSVersion = &tlsVersion
		modified = true
		logger.Info("applying recommendation: setting minimum TLS version to 1.2", "storageAccount", storageAccountName)

	case "azure_storage_shared_key_authorization_enabled":
		// Disable shared key access (use Azure AD instead)
		disabled := false
		storageAccount.Properties.AllowSharedKeyAccess = &disabled
		modified = true
		logger.Info("applying recommendation: disabling shared key authorization", "storageAccount", storageAccountName)

	case "azure_storage_public_network_access_enabled":
		// Cannot auto-fix this - requires user to set up private endpoints or network rules
		return fmt.Errorf("cannot auto-apply recommendation '%s': network configuration must be specified manually", recommendation.RuleName)

	case "azure_storage_geo_redundant_storage_disabled":
		// Cannot auto-fix this - changing SKU requires data migration
		return fmt.Errorf("cannot auto-apply recommendation '%s': SKU change requires manual data migration", recommendation.RuleName)

	case "azure_storage_soft_delete_disabled":
		// Cannot auto-fix this - requires blob service properties update
		return fmt.Errorf("cannot auto-apply recommendation '%s': soft delete requires blob service properties configuration", recommendation.RuleName)

	case "azure_storage_versioning_disabled":
		// Cannot auto-fix this - requires blob service properties update
		return fmt.Errorf("cannot auto-apply recommendation '%s': versioning requires blob service properties configuration", recommendation.RuleName)

	case "azure_storage_cmk_disabled":
		// Cannot auto-fix this - requires Key Vault setup
		return fmt.Errorf("cannot auto-apply recommendation '%s': customer-managed keys require Key Vault configuration", recommendation.RuleName)

	case "azure_storage_logging_for_read_access_disabled",
		"azure_storage_logging_for_write_access_disabled",
		"azure_storage_logging_for_delete_access_disabled":
		// Cannot auto-fix this - requires diagnostic settings configuration
		return fmt.Errorf("cannot auto-apply recommendation '%s': logging configuration must be set up manually", recommendation.RuleName)

	case "azure_missing_tags":
		// Cannot auto-fix this - requires user to specify tags
		return fmt.Errorf("cannot auto-apply recommendation '%s': tags must be specified manually", recommendation.RuleName)

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}

	if !modified {
		return fmt.Errorf("no changes were made for recommendation: %s", recommendation.RuleName)
	}

	// Update the storage account
	updateParams := armstorage.AccountUpdateParameters{
		Properties: &armstorage.AccountPropertiesUpdateParameters{
			EnableHTTPSTrafficOnly: storageAccount.Properties.EnableHTTPSTrafficOnly,
			AllowBlobPublicAccess:  storageAccount.Properties.AllowBlobPublicAccess,
			MinimumTLSVersion:      storageAccount.Properties.MinimumTLSVersion,
			AllowSharedKeyAccess:   storageAccount.Properties.AllowSharedKeyAccess,
		},
	}
	_, err = client.Update(ctx.GetContext(), resourceGroup, storageAccountName, updateParams, nil)
	if err != nil {
		return fmt.Errorf("failed to update storage account: %w", err)
	}

	logger.Info("successfully applied recommendation", "ruleName", recommendation.RuleName, "storageAccount", storageAccountName)
	return nil
}

func (s *blobStorageService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
			break
		}
	}
	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}

	client, err := armstorage.NewAccountsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create storage accounts client: %v", err),
		}, err
	}

	// Extract resource group and storage account name from resource ID
	var resourceGroup, storageAccountName string
	for i, part := range parts {
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "storageAccounts" && i+1 < len(parts) {
			storageAccountName = parts[i+1]
		}
	}

	if resourceGroup == "" || storageAccountName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or storage account name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	// Get the current storage account configuration
	accountResp, err := client.GetProperties(ctx.GetContext(), resourceGroup, storageAccountName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to get storage account: %v", err),
		}, err
	}

	storageAccount := accountResp.Account
	if storageAccount.Properties == nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: "storage account properties are nil",
		}, fmt.Errorf("storage account properties are nil")
	}

	// Execute the command
	switch command.Command {
	case "enable_https_only":
		enabled := true
		storageAccount.Properties.EnableHTTPSTrafficOnly = &enabled
		logger.Info("executing command: enabling HTTPS-only traffic", "storageAccount", storageAccountName)

	case "disable_https_only":
		disabled := false
		storageAccount.Properties.EnableHTTPSTrafficOnly = &disabled
		logger.Info("executing command: disabling HTTPS-only traffic", "storageAccount", storageAccountName)

	case "enable_blob_public_access":
		enabled := true
		storageAccount.Properties.AllowBlobPublicAccess = &enabled
		logger.Info("executing command: enabling blob public access", "storageAccount", storageAccountName)

	case "disable_blob_public_access":
		disabled := false
		storageAccount.Properties.AllowBlobPublicAccess = &disabled
		logger.Info("executing command: disabling blob public access", "storageAccount", storageAccountName)

	case "set_minimum_tls_version":
		if version, ok := command.Args["tls_version"].(string); ok {
			var tlsVersion armstorage.MinimumTLSVersion
			switch version {
			case "TLS1_0":
				tlsVersion = armstorage.MinimumTLSVersionTLS10
			case "TLS1_1":
				tlsVersion = armstorage.MinimumTLSVersionTLS11
			case "TLS1_2":
				tlsVersion = armstorage.MinimumTLSVersionTLS12
			default:
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("invalid TLS version: %s. Supported: TLS1_0, TLS1_1, TLS1_2", version),
				}, fmt.Errorf("invalid TLS version: %s", version)
			}
			storageAccount.Properties.MinimumTLSVersion = &tlsVersion
			logger.Info("executing command: setting minimum TLS version", "storageAccount", storageAccountName, "tlsVersion", version)
		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "tls_version argument is required and must be a string (TLS1_0, TLS1_1, TLS1_2)",
			}, fmt.Errorf("missing or invalid tls_version argument")
		}

	case "enable_shared_key_access":
		enabled := true
		storageAccount.Properties.AllowSharedKeyAccess = &enabled
		logger.Info("executing command: enabling shared key access", "storageAccount", storageAccountName)

	case "disable_shared_key_access":
		disabled := false
		storageAccount.Properties.AllowSharedKeyAccess = &disabled
		logger.Info("executing command: disabling shared key access", "storageAccount", storageAccountName)

	case "regenerate_keys":
		if keyName, ok := command.Args["key_name"].(string); ok {

			if !isValidStorageKeyName(keyName) {
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("invalid key_name: %s. Supported: %s, %s", keyName, StorageKey1, StorageKey2),
				}, fmt.Errorf("invalid key_name: %s", keyName)
			}

			params := armstorage.AccountRegenerateKeyParameters{
				KeyName: to.Ptr(keyName),
			}

			_, err := client.RegenerateKey(ctx.GetContext(), resourceGroup, storageAccountName, params, nil)
			if err != nil {
				return providers.ApplyCommandResponse{
					Success: false,
					Message: fmt.Sprintf("failed to regenerate storage account key: %v", err),
				}, err
			}

			logger.Info(
				"executing command: regenerating storage account key",
				"storageAccount", storageAccountName,
				"keyName", keyName,
			)

			return providers.ApplyCommandResponse{
				Success: true,
				Message: fmt.Sprintf(
					"successfully regenerated key '%s' for storage account '%s'",
					keyName, storageAccountName,
				),
			}, nil

		} else {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("key_name argument is required and must be one of: %s, %s", StorageKey1, StorageKey2),
			}, fmt.Errorf("missing or invalid key_name argument")
		}

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s. Supported commands: enable_https_only, disable_https_only, enable_blob_public_access, disable_blob_public_access, set_minimum_tls_version, enable_shared_key_access, disable_shared_key_access, regenerate_keys", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}

	// Update the storage account (for all commands except regenerate_keys which already returned)
	updateParams := armstorage.AccountUpdateParameters{
		Properties: &armstorage.AccountPropertiesUpdateParameters{
			EnableHTTPSTrafficOnly: storageAccount.Properties.EnableHTTPSTrafficOnly,
			AllowBlobPublicAccess:  storageAccount.Properties.AllowBlobPublicAccess,
			MinimumTLSVersion:      storageAccount.Properties.MinimumTLSVersion,
			AllowSharedKeyAccess:   storageAccount.Properties.AllowSharedKeyAccess,
		},
	}
	_, err = client.Update(ctx.GetContext(), resourceGroup, storageAccountName, updateParams, nil)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to update storage account: %v", err),
		}, err
	}

	logger.Info("successfully executed command", "command", command.Command, "storageAccount", storageAccountName)
	return providers.ApplyCommandResponse{
		Success: true,
		Message: fmt.Sprintf("successfully executed command '%s' on storage account '%s'", command.Command, storageAccountName),
	}, nil
}

func (s *blobStorageService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	// Azure Storage Account logs are typically found in Log Analytics workspace
	// The resourceId itself can be used to query storage account logs via Azure Monitor
	return resourceId, nil
}

func (s *blobStorageService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	rg, err := extractResourceGroup(resource.Id)
	if err != nil {
		return providers.ServiceMapApplication{}, fmt.Errorf("failed to extract resource group: %w", err)
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams: []providers.UpstreamLink{
			{
				Id: providers.ServiceApplicationId{
					Name:      rg,
					Kind:      "Microsoft.Resources/resourceGroups",
					Namespace: resource.Region,
				}.Key(),
			},
		},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}
