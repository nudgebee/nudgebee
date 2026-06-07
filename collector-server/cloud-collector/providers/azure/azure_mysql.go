package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

type mysqlService struct {
}

func (s *mysqlService) Name() string {
	return "Microsoft.DBforMySQL/flexibleServers"
}

// Scope returns the service scope - this is a regional service
func (s *mysqlService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *mysqlService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armmysqlflexibleservers.NewServersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create mysql client: %w", err)
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
					case armmysqlflexibleservers.ServerStateReady:
						status = providers.ResourceStatusActive
					case armmysqlflexibleservers.ServerStateStopped:
						status = providers.ResourceStatusInactive
					case armmysqlflexibleservers.ServerStateDisabled:
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

func (s *mysqlService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/metric alert recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("azure: applying alarm recommendation for mysql",
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

func (s *mysqlService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and server name from resource ID.
	// Parsed case-insensitively: stored Azure IDs are lowercased (see parseAzureResourceIDSegments).
	segments := parseAzureResourceIDSegments(command.ResourceId)
	subscriptionID := segments["subscriptions"]
	resourceGroup := segments["resourcegroups"]
	serverName := segments["flexibleservers"]

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || serverName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or server name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armmysqlflexibleservers.NewServersClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create mysql client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_mysql_backup_disabled":
		// Enable backup retention
		logger.Info("applying command: enabling backup retention", "serverName", serverName)

		serverResp, err := client.Get(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get mysql server: %v", err),
			}, err
		}

		server := serverResp.Server
		if server.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "mysql server properties are nil",
			}, fmt.Errorf("mysql server properties are nil")
		}

		// Update backup retention to 7 days (minimum recommended)
		backupRetentionDays := int32(7)
		poller, err := client.BeginUpdate(ctx.GetContext(), resourceGroup, serverName, armmysqlflexibleservers.ServerForUpdate{
			Properties: &armmysqlflexibleservers.ServerPropertiesForUpdate{
				Backup: &armmysqlflexibleservers.Backup{
					BackupRetentionDays: &backupRetentionDays,
				},
			},
		}, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update mysql server backup: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mysql server backup update: %v", err),
			}, err
		}

		logger.Info("successfully enabled backup retention", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled backup retention for mysql server '%s'", serverName),
		}, nil

	case "start_server":
		// Start the mysql server
		logger.Info("applying command: starting mysql server", "serverName", serverName)
		poller, err := client.BeginStart(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start mysql server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mysql server start: %v", err),
			}, err
		}
		logger.Info("successfully started mysql server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started mysql server '%s'", serverName),
		}, nil

	case "stop_server":
		// Stop the mysql server
		logger.Info("applying command: stopping mysql server", "serverName", serverName)
		poller, err := client.BeginStop(ctx.GetContext(), resourceGroup, serverName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop mysql server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mysql server stop: %v", err),
			}, err
		}
		logger.Info("successfully stopped mysql server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped mysql server '%s'", serverName),
		}, nil

	case "restart_server":
		// Restart the mysql server
		logger.Info("applying command: restarting mysql server", "serverName", serverName)
		poller, err := client.BeginRestart(ctx.GetContext(), resourceGroup, serverName, armmysqlflexibleservers.ServerRestartParameter{}, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to restart mysql server: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for mysql server restart: %v", err),
			}, err
		}
		logger.Info("successfully restarted mysql server", "serverName", serverName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully restarted mysql server '%s'", serverName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *mysqlService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *mysqlService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
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
						RuleName:            "azure_mysql_backup_disabled",
						Severity:            providers.RecommendationSeverityHigh,
						Savings:             0,
						Data:                map[string]any{"reason": "MySQL server should have backup retention of at least 7 days to ensure data recovery capability and meet compliance requirements"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Note: SSL/TLS enforcement for MySQL Flexible Servers is controlled via the
			// 'require_secure_transport' server parameter, which requires querying the
			// Configurations API. Since flexible servers enforce SSL by default and this
			// setting is rarely disabled, we don't include SSL recommendations here.
			// For comprehensive SSL checking, implement a separate configuration audit.

			// Check if server is stopped for cost optimization
			if state, ok := props["state"].(string); ok && state == string(armmysqlflexibleservers.ServerStateStopped) {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "azure_mysql_server_stopped",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"reason": "MySQL server is stopped; consider deleting if not needed to reduce unnecessary costs"},
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

func (s *mysqlService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "mysql",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *mysqlService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
