package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

type filesService struct {
}

func (s *filesService) Name() string {
	return "Microsoft.Storage/storageAccounts/fileServices"
}

// Scope returns the service scope - this is a regional service
func (s *filesService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *filesService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// First, get all storage accounts
		accountsClient, err := armstorage.NewAccountsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create storage accounts client: %w", err)
		}

		accountsPager := accountsClient.NewListPager(nil)
		for accountsPager.More() {
			accountsPage, err := accountsPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of storage accounts: %w", err)
			}

			for _, storageAccount := range accountsPage.Value {
				if storageAccount.Name == nil || storageAccount.ID == nil {
					continue
				}

				// Extract resource group from storage account ID
				resourceGroup, err := extractResourceGroup(*storageAccount.ID)
				if err != nil || resourceGroup == "" {
					continue
				}

				// Get file shares for this storage account
				fileSharesClient, err := armstorage.NewFileSharesClient(subID, cred, getAzureAuditOpts(ctx))
				if err != nil {
					return nil, fmt.Errorf("failed to create file shares client: %w", err)
				}

				fileSharesPager := fileSharesClient.NewListPager(resourceGroup, *storageAccount.Name, nil)
				for fileSharesPager.More() {
					fileSharesPage, err := fileSharesPager.NextPage(ctx.GetContext())
					if err != nil {
						// If we can't list file shares, it might be because the service doesn't exist
						// This is not an error condition
						break
					}

					for _, fileShare := range fileSharesPage.Value {
						status := providers.ResourceStatusActive
						if fileShare.Properties != nil && fileShare.Properties.EnabledProtocols != nil {
							// File share is active if it has enabled protocols
							if *fileShare.Properties.EnabledProtocols == "" {
								status = providers.ResourceStatusUnknown
							}
						}

						createdAt := time.Time{}
						if fileShare.Properties != nil && fileShare.Properties.LastModifiedTime != nil {
							createdAt = *fileShare.Properties.LastModifiedTime
						}

						// Construct full resource ID for file share
						fileShareID := fmt.Sprintf("%s/fileServices/default/shares/%s", *storageAccount.ID, *fileShare.Name)

						allResources = append(allResources, providers.Resource{
							Id:          fileShareID,
							Name:        *fileShare.Name,
							Type:        s.Name(),
							Region:      normalizeAzureRegion(*storageAccount.Location),
							Tags:        toAzureTags(storageAccount.Tags),
							Meta:        structToMap(fileShare),
							Status:      status,
							CreatedAt:   createdAt,
							Arn:         fileShareID,
							ServiceName: s.Name(),
						})
					}
				}
			}
		}
	}
	return allResources, nil
}

func (s *filesService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *filesService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group, storage account name, and file share name from resource ID
	// Format: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Storage/storageAccounts/{sa}/fileServices/default/shares/{share}
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, storageAccountName, fileShareName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "storageAccounts" && i+1 < len(parts) {
			storageAccountName = parts[i+1]
		}
		if part == "shares" && i+1 < len(parts) {
			fileShareName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || storageAccountName == "" || fileShareName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource identifiers from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	fileSharesClient, err := armstorage.NewFileSharesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create file shares client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_files_enable_smb_encryption":
		logger.Info("applying command: enabling SMB encryption", "fileShare", fileShareName)

		// Get current file share
		fileShareResp, err := fileSharesClient.Get(ctx.GetContext(), resourceGroup, storageAccountName, fileShareName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get file share: %v", err),
			}, err
		}

		fileShare := fileShareResp.FileShare
		if fileShare.FileShareProperties == nil {
			fileShare.FileShareProperties = &armstorage.FileShareProperties{}
		}

		// Enable SMB encryption
		fileShare.FileShareProperties.EnabledProtocols = to.Ptr(armstorage.EnabledProtocolsSMB)

		_, err = fileSharesClient.Update(ctx.GetContext(), resourceGroup, storageAccountName, fileShareName, fileShare, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update file share: %v", err),
			}, err
		}

		logger.Info("successfully enabled SMB encryption", "fileShare", fileShareName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled SMB encryption for file share '%s'", fileShareName),
		}, nil

	case "azure_files_set_quota":
		// Set quota for file share
		logger.Info("applying command: setting quota", "fileShare", fileShareName)

		// Get current file share
		fileShareResp, err := fileSharesClient.Get(ctx.GetContext(), resourceGroup, storageAccountName, fileShareName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get file share: %v", err),
			}, err
		}

		fileShare := fileShareResp.FileShare
		if fileShare.FileShareProperties == nil {
			fileShare.FileShareProperties = &armstorage.FileShareProperties{}
		}
		// TODO: Make quota configurable via command parameters
		// Set a default quota of 100 GB if not specified
		// In a real implementation, this would come from command parameters
		defaultQuota := int32(100)
		if quota, ok := command.Args["quota"].(int32); ok {
			defaultQuota = quota
		}
		fileShare.FileShareProperties.ShareQuota = &defaultQuota

		_, err = fileSharesClient.Update(ctx.GetContext(), resourceGroup, storageAccountName, fileShareName, fileShare, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update file share: %v", err),
			}, err
		}

		logger.Info("successfully set quota", "fileShare", fileShareName, "quota", defaultQuota)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully set quota for file share '%s' to %d GB", fileShareName, defaultQuota),
		}, nil

	case "delete_file_share":
		// Delete file share
		logger.Info("applying command: deleting file share", "fileShare", fileShareName)

		_, err := fileSharesClient.Delete(ctx.GetContext(), resourceGroup, storageAccountName, fileShareName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete file share: %v", err),
			}, err
		}

		logger.Info("successfully deleted file share", "fileShare", fileShareName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted file share '%s'", fileShareName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *filesService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *filesService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check if quota is set
			if shareQuota, ok := props["shareQuota"].(float64); ok {
				// Check if quota is very large (potential cost issue)
				if shareQuota > 5120 { // 5TB
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryRightSizing,
						RuleName:            "azure_files_large_quota",
						Severity:            providers.RecommendationSeverityLow,
						Savings:             0,
						Data:                map[string]any{"reason": fmt.Sprintf("File share has a large quota of %.0f GB; consider reducing the quota to optimize costs if the full capacity is not needed", shareQuota)},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if SMB protocol is enabled (security recommendation)
			if enabledProtocols, ok := props["enabledProtocols"].(string); ok {
				if enabledProtocols == "" || enabledProtocols == "NFS" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_files_smb_not_enabled",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": "Consider enabling SMB protocol with encryption for enhanced security and broader client compatibility"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if file share has snapshots (backup recommendation)
			// Note: This would require additional API calls to check snapshots
			// For now, we'll just check if lastModifiedTime is very old
			if lastModifiedTime, ok := props["lastModifiedTime"].(string); ok {
				parsedTime, err := time.Parse(time.RFC3339, lastModifiedTime)
				if err == nil {
					daysSinceModified := time.Since(parsedTime).Hours() / 24
					if daysSinceModified > 180 { // 6 months
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryRightSizing,
							RuleName:            "azure_files_unused_file_share",
							Severity:            providers.RecommendationSeverityLow,
							Savings:             0,
							Data:                map[string]any{"reason": fmt.Sprintf("File share has not been modified in %.0f days; consider deleting if it is no longer needed to reduce storage costs", daysSinceModified)},
							Action:              providers.RecommendationActionDelete,
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
	return allRecommendations, nil
}

func (s *filesService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "azure-files",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *filesService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Azure Files logs are typically found in storage account logs
	// Extract storage account ID from file share ID
	parts := strings.Split(resourceId, "/fileServices")
	if len(parts) > 0 {
		return parts[0], nil
	}
	return resourceId, nil
}
