package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

type postgresService struct {
}

func (s *postgresService) Name() string {
	return "Microsoft.DBforPostgreSQL/flexibleServers"
}

// Scope returns the service scope - this is a regional service
func (s *postgresService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *postgresService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armpostgresqlflexibleservers.NewServersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create postgres client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, server := range page.Value {
				status := providers.ResourceStatusUnknown
				if server.Properties != nil && server.Properties.State != nil {
					switch *server.Properties.State {
					case armpostgresqlflexibleservers.ServerStateReady:
						status = providers.ResourceStatusActive
					case armpostgresqlflexibleservers.ServerStateStopped:
						status = providers.ResourceStatusInactive
					case armpostgresqlflexibleservers.ServerStateDisabled:
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

func (s *postgresService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for postgres",
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

func (s *postgresService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
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
		if part == "flexibleServers" && i+1 < len(parts) {
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

	client, err := armpostgresqlflexibleservers.NewServersClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create postgres client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_postgres_backup_disabled":
		// Enable backup retention
		logger.Info("applying command: enabling backup retention", "serverName", serverName)

		serverResp, err := client.Get(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get postgres server: %v", err),
			}, err
		}

		server := serverResp.Server
		if server.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "postgres server properties are nil",
			}, fmt.Errorf("postgres server properties are nil")
		}

		// Update backup retention to 7 days (minimum recommended)
		backupRetentionDays := int32(7)
		poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, serverName, armpostgresqlflexibleservers.ServerForUpdate{
			Properties: &armpostgresqlflexibleservers.ServerPropertiesForUpdate{
				Backup: &armpostgresqlflexibleservers.Backup{
					BackupRetentionDays: &backupRetentionDays,
				},
			},
		}, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update postgres server backup: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for postgres server backup update: %v", err),
			}, err
		}

		logger.Info("successfully enabled backup retention", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled backup retention for postgres server '%s'", serverName),
		}, nil

	case "start_server":
		// Start the postgres server
		logger.Info("applying command: starting postgres server", "serverName", serverName)
		poller, err := client.BeginStart(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start postgres server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for postgres server start: %v", err),
			}, err
		}
		logger.Info("successfully started postgres server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started postgres server '%s'", serverName),
		}, nil

	case "stop_server":
		// Stop the postgres server
		logger.Info("applying command: stopping postgres server", "serverName", serverName)
		poller, err := client.BeginStop(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop postgres server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for postgres server stop: %v", err),
			}, err
		}
		logger.Info("successfully stopped postgres server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped postgres server '%s'", serverName),
		}, nil

	case "restart_server":
		// Restart the postgres server
		logger.Info("applying command: restarting postgres server", "serverName", serverName)
		poller, err := client.BeginRestart(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to restart postgres server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for postgres server restart: %v", err),
			}, err
		}
		logger.Info("successfully restarted postgres server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully restarted postgres server '%s'", serverName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *postgresService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *postgresService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check backup retention
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			if backup, ok := props["backup"].(map[string]interface{}); ok {
				if retentionDays, ok := backup["backupRetentionDays"].(float64); ok && retentionDays < 7 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_postgres_backup_disabled",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "PostgreSQL server should have backup retention of at least 7 days to ensure data recovery capability and meet compliance requirements"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Note: SSL/TLS enforcement for PostgreSQL Flexible Servers is controlled via the
			// 'require_secure_transport' server parameter, which requires querying the
			// Configurations API. Since flexible servers enforce SSL by default and this
			// setting is rarely disabled, we don't include SSL recommendations here.
			// For comprehensive SSL checking, implement a separate configuration audit.

			// Check if server is stopped for cost optimization
			if state, ok := props["state"].(string); ok && state == string(armpostgresqlflexibleservers.ServerStateStopped) {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "azure_postgres_server_stopped",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"reason": "PostgreSQL server is stopped; consider deleting if not needed to reduce unnecessary costs"},
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

func (s *postgresService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "postgres",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *postgresService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
