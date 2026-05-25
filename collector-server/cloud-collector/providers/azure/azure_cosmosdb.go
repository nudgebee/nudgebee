package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type cosmosDBService struct {
}

func (s *cosmosDBService) Name() string {
	return "Microsoft.DocumentDB/databaseAccounts"
}

// Scope returns the service scope - this is a regional service
func (s *cosmosDBService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *cosmosDBService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armcosmos.NewDatabaseAccountsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create cosmosdb client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, db := range page.Value {
				status := providers.ResourceStatusUnknown
				if db.Properties != nil && db.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[*db.Properties.ProvisioningState]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *db.ID,
					Name:        *db.Name,
					Type:        *db.Type,
					Region:      *db.Location,
					Tags:        toAzureTags(db.Tags),
					Meta:        structToMap(db),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *db.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *cosmosDBService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *cosmosDBService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to create azure credential"}, fmt.Errorf("failed to create azure credential: %w", err)
	}

	subId, err := extractSubscriptionID(command.ResourceId)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to extract subscription id"}, fmt.Errorf("failed to extract subscription id from resource id: %w", err)
	}

	rg, err := extractResourceGroup(command.ResourceId)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to extract resource group"}, fmt.Errorf("failed to extract resource group from resource id: %w", err)
	}

	parts := strings.Split(command.ResourceId, "/")
	accountName := parts[len(parts)-1]

	client, err := armcosmos.NewDatabaseAccountsClient(subId, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to create cosmosdb client"}, fmt.Errorf("failed to create cosmosdb client: %w", err)
	}

	dbAccount, err := client.Get(ctx.GetContext(), rg, accountName, nil)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to get cosmosdb account"}, fmt.Errorf("failed to get cosmosdb account: %w", err)
	}

	updateParams := armcosmos.DatabaseAccountCreateUpdateParameters{
		Properties: &armcosmos.DatabaseAccountCreateUpdateProperties{
			EnableAutomaticFailover:       dbAccount.Properties.EnableAutomaticFailover,
			Locations:                     dbAccount.Properties.Locations,
			IsVirtualNetworkFilterEnabled: dbAccount.Properties.IsVirtualNetworkFilterEnabled,
			EnableMultipleWriteLocations:  dbAccount.Properties.EnableMultipleWriteLocations,
			DatabaseAccountOfferType:      dbAccount.Properties.DatabaseAccountOfferType,
		},
		Tags:     dbAccount.Tags,
		Location: dbAccount.Location,
	}

	switch command.Command {
	case "azure_cosmosdb_automatic_failover_disabled":
		logger.Info("applying recommendation: enabling automatic failover", "cosmosDbAccount", accountName)
		updateParams.Properties.EnableAutomaticFailover = to.Ptr(true)
	case "azure_cosmosdb_single_region":
		return providers.ApplyCommandResponse{Success: false, Message: "adding a new region requires manual configuration"}, fmt.Errorf("unsupported command: adding a new region requires manual configuration")
	default:
		return providers.ApplyCommandResponse{Success: false, Message: "unknown command"}, fmt.Errorf("unknown command: %s", command.Command)
	}

	poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), rg, accountName, updateParams, nil)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to begin update for cosmosdb account"}, fmt.Errorf("failed to begin update for cosmosdb account: %w", err)
	}

	_, err = poller.PollUntilDone(ctx.GetContext(), nil)
	if err != nil {
		return providers.ApplyCommandResponse{Success: false, Message: "failed to complete update for cosmosdb account"}, fmt.Errorf("failed to complete update for cosmosdb account: %w", err)
	}

	return providers.ApplyCommandResponse{Success: true, Message: "Successfully applied command: " + command.Command}, nil
}

func (s *cosmosDBService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *cosmosDBService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check automatic failover
			if enableAutomaticFailover, ok := props["enableAutomaticFailover"].(bool); !ok || !enableAutomaticFailover {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_cosmosdb_automatic_failover_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{"reason": "Enable automatic failover for high availability",
						"cosmosDbAccount":         resource,
						"enableAutomaticFailover": enableAutomaticFailover,
						"properties":              props,
						"meta":                    meta,
						"tags":                    resource.Tags,
						"status":                  resource.Status,
						"region":                  resource.Region,
						"type":                    resource.Type,
						"name":                    resource.Name,
						"id":                      resource.Id,
						"arn":                     resource.Arn,
						"createdAt":               resource.CreatedAt,
						"serviceName":             resource.ServiceName,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check multi-region
			if locations, ok := props["locations"].([]interface{}); ok && len(locations) < 2 {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_cosmosdb_single_region",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{"reason": "Consider multi-region replication for disaster recovery",
						"cosmosDbAccount": resource,
						"locations":       locations,
						"properties":      props,
						"meta":            meta,
						"tags":            resource.Tags,
						"status":          resource.Status,
						"region":          resource.Region,
						"type":            resource.Type,
						"name":            resource.Name,
						"id":              resource.Id,
						"arn":             resource.Arn,
						"createdAt":       resource.CreatedAt,
						"serviceName":     resource.ServiceName,
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
	return allRecommendations, nil
}

func (s *cosmosDBService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "cosmosdb",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *cosmosDBService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}

	client, err := armmonitor.NewDiagnosticSettingsClient(cred, getAzureAuditOpts(ctx))
	if err != nil {
		return "", fmt.Errorf("failed to create diagnostic settings client: %w", err)
	}

	pager := client.NewListPager(resourceId, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx.GetContext())
		if err != nil {
			return "", fmt.Errorf("failed to get next page of diagnostic settings: %w", err)
		}

		for _, setting := range page.Value {
			if setting.Properties != nil && setting.Properties.WorkspaceID != nil && *setting.Properties.WorkspaceID != "" {
				return *setting.Properties.WorkspaceID, nil
			}
		}
	}
	return "", fmt.Errorf("log analytics workspace not found for resource: %s", resourceId)
}
