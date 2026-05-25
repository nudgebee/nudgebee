package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mariadb/armmariadb"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type mariadbService struct {
}

func (s *mariadbService) Name() string {
	return "Microsoft.DBforMariaDB/servers"
}

// Scope returns the service scope - this is a regional service
func (s *mariadbService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *mariadbService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armmariadb.NewServersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create mariadb client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, server := range page.Value {
				status := providers.ResourceStatusUnknown
				if server.Properties != nil && server.Properties.UserVisibleState != nil {
					state := string(*server.Properties.UserVisibleState)
					switch state {
					case "Ready":
						status = providers.ResourceStatusActive
					case "Stopped", "Disabled":
						status = providers.ResourceStatusInactive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *server.ID,
					Name:        *server.Name,
					Type:        *server.Type,
					Region:      *server.Location,
					Tags:        toAzureTags(server.Tags),
					Meta:        structToMap(server),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *server.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *mariadbService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for mariadb",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	}

	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *mariadbService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and server name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, serverName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "servers" && i+1 < len(parts) {
			serverName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || serverName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or server name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armmariadb.NewServersClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create mariadb client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_mariadb_ssl_disabled":
		// Enable SSL enforcement
		logger.Info("applying command: enabling SSL enforcement", "serverName", serverName)

		serverResp, err := client.Get(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get mariadb server: %v", err),
			}, err
		}

		server := serverResp.Server
		if server.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "mariadb server properties are nil",
			}, fmt.Errorf("mariadb server properties are nil")
		}

		// Update server to enable SSL
		sslEnforcement := armmariadb.SSLEnforcementEnumEnabled
		poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, serverName, armmariadb.ServerUpdateParameters{
			Properties: &armmariadb.ServerUpdateParametersProperties{
				SSLEnforcement: &sslEnforcement,
			},
		}, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update mariadb server: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mariadb server update: %v", err),
			}, err
		}

		logger.Info("successfully enabled SSL enforcement", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled SSL enforcement for mariadb server '%s'", serverName),
		}, nil

	case "azure_mariadb_backup_disabled":
		// Enable backup retention
		logger.Info("applying command: enabling backup retention", "serverName", serverName)

		serverResp, err := client.Get(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get mariadb server: %v", err),
			}, err
		}

		server := serverResp.Server
		if server.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "mariadb server properties are nil",
			}, fmt.Errorf("mariadb server properties are nil")
		}

		// Update backup retention to 7 days (minimum recommended)
		backupRetentionDays := int32(7)
		poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, serverName, armmariadb.ServerUpdateParameters{
			Properties: &armmariadb.ServerUpdateParametersProperties{
				StorageProfile: &armmariadb.StorageProfile{
					BackupRetentionDays: &backupRetentionDays,
				},
			},
		}, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update mariadb server backup: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mariadb server backup update: %v", err),
			}, err
		}

		logger.Info("successfully enabled backup retention", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled backup retention for mariadb server '%s'", serverName),
		}, nil

	case "start_server":
		// Start the mariadb server
		logger.Info("applying command: starting mariadb server", "serverName", serverName)
		poller, err := client.BeginStart(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start mariadb server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mariadb server start: %v", err),
			}, err
		}
		logger.Info("successfully started mariadb server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started mariadb server '%s'", serverName),
		}, nil

	case "stop_server":
		// Stop the mariadb server
		logger.Info("applying command: stopping mariadb server", "serverName", serverName)
		poller, err := client.BeginStop(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop mariadb server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mariadb server stop: %v", err),
			}, err
		}
		logger.Info("successfully stopped mariadb server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped mariadb server '%s'", serverName),
		}, nil

	case "restart_server":
		// Restart the mariadb server
		logger.Info("applying command: restarting mariadb server", "serverName", serverName)
		poller, err := client.BeginRestart(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to restart mariadb server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mariadb server restart: %v", err),
			}, err
		}
		logger.Info("successfully restarted mariadb server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully restarted mariadb server '%s'", serverName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *mariadbService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *mariadbService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check SSL enforcement and backup retention
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check SSL enforcement
			if sslEnforcement, ok := props["sslEnforcement"].(string); ok && sslEnforcement != string(armmariadb.SSLEnforcementEnumEnabled) {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_mariadb_ssl_disabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "MariaDB server should enforce SSL connections to ensure encrypted data transmission and protect against eavesdropping"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check backup retention
			if storageProfile, ok := props["storageProfile"].(map[string]interface{}); ok {
				if retentionDays, ok := storageProfile["backupRetentionDays"].(float64); ok && retentionDays < 7 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_mariadb_backup_disabled",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "MariaDB server should have backup retention of at least 7 days to ensure data recovery capability and meet compliance requirements"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if server is stopped for cost optimization
			if state, ok := props["userVisibleState"].(string); ok && state == "Stopped" {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "azure_mariadb_server_stopped",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"reason": "MariaDB server is stopped; consider deleting if not needed to reduce unnecessary costs"},
					Action:              providers.RecommendationActionDelete,
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

func (s *mariadbService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "mariadb",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *mariadbService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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

	return "", errors.New("log analytics workspace not found for resource")
}
