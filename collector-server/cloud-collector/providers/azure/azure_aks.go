package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type aksService struct {
}

func (s *aksService) Name() string {
	return "Microsoft.ContainerService/managedClusters"
}

// Scope returns the service scope - this is a regional service
func (s *aksService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *aksService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		client, err := armcontainerservice.NewManagedClustersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create aks client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, cluster := range page.Value {
				status := providers.ResourceStatusUnknown
				if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
					provisioningState := string(*cluster.Properties.ProvisioningState)
					if val, ok := nbStatusFromAzureProvisioningState[provisioningState]; ok {
						status = val
					}
				}

				// Get power state
				if cluster.Properties != nil && cluster.Properties.PowerState != nil && cluster.Properties.PowerState.Code != nil {
					powerState := string(*cluster.Properties.PowerState.Code)
					switch powerState {
					case "Stopped":
						status = providers.ResourceStatusInactive
					case "Running":
						status = providers.ResourceStatusActive
					}
				}

				createdAt := time.Now().UTC()

				allResources = append(allResources, providers.Resource{
					Id:          *cluster.ID,
					Name:        *cluster.Name,
					Type:        *cluster.Type,
					Region:      normalizeAzureRegion(*cluster.Location),
					Tags:        toAzureTags(cluster.Tags),
					Meta:        structToMap(cluster),
					Status:      status,
					CreatedAt:   createdAt,
					Arn:         *cluster.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *aksService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *aksService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and cluster name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, clusterName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "managedClusters" && i+1 < len(parts) {
			clusterName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		subscriptionID = session.SubscriptionID
	}
	if resourceGroup == "" || clusterName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or cluster name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create aks client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_aks_enable_rbac":
		// Enable RBAC
		logger.Info("applying command: enabling RBAC", "cluster", clusterName)

		clusterResp, err := client.Get(ctx.GetContext(), resourceGroup, clusterName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get AKS cluster: %v", err),
			}, err
		}

		cluster := clusterResp.ManagedCluster
		if cluster.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("cluster properties are nil for cluster '%s'", clusterName)}, fmt.Errorf("cluster properties are nil for cluster '%s'", clusterName)
		}

		cluster.Properties.EnableRBAC = to.Ptr(true)

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, clusterName, cluster, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update AKS cluster: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for AKS cluster update: %v", err),
			}, err
		}

		logger.Info("successfully enabled RBAC", "cluster", clusterName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled RBAC for AKS cluster '%s'", clusterName),
		}, nil

	case "azure_aks_enable_network_policy":
		// Enable network policy
		logger.Info("applying command: enabling network policy", "cluster", clusterName)

		clusterResp, err := client.Get(ctx.GetContext(), resourceGroup, clusterName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get AKS cluster: %v", err),
			}, err
		}

		cluster := clusterResp.ManagedCluster
		if cluster.Properties == nil || cluster.Properties.NetworkProfile == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("cluster network properties are nil for cluster '%s'", clusterName),
			}, fmt.Errorf("cluster network properties are nil for cluster '%s'", clusterName)
		}

		logger.Info("network policy can only be set at cluster creation", "cluster", clusterName)
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("network policy for AKS cluster '%s' can only be set at creation time", clusterName),
		}, fmt.Errorf("network policy cannot be changed after creation")

	case "start_cluster":
		logger.Info("applying command: starting AKS cluster", "cluster", clusterName)
		poller, err := client.BeginStart(ctx.GetContext(), resourceGroup, clusterName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to start AKS cluster: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for AKS cluster start: %v", err),
			}, err
		}

		logger.Info("successfully started AKS cluster", "cluster", clusterName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully started AKS cluster '%s'", clusterName),
		}, nil

	case "stop_cluster":
		logger.Info("applying command: stopping AKS cluster", "cluster", clusterName)
		poller, err := client.BeginStop(ctx.GetContext(), resourceGroup, clusterName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to stop AKS cluster: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for AKS cluster stop: %v", err),
			}, err
		}

		logger.Info("successfully stopped AKS cluster", "cluster", clusterName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully stopped AKS cluster '%s'", clusterName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *aksService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *aksService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		if props, ok := meta["properties"].(map[string]interface{}); ok {
			// Check RBAC enabled
			if enableRBAC, ok := props["enableRBAC"].(bool); !ok || !enableRBAC {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "azure_aks_rbac_disabled",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"reason": "AKS cluster should have RBAC enabled"},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check network policy
			if networkProfile, ok := props["networkProfile"].(map[string]interface{}); ok {
				if networkPolicy, ok := networkProfile["networkPolicy"].(string); !ok || networkPolicy == "" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "azure_aks_network_policy_disabled",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": "Consider enabling network policy for AKS cluster"},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check if Azure Policy is enabled
			if addonProfiles, ok := props["addonProfiles"].(map[string]interface{}); ok {
				if azurePolicy, ok := addonProfiles["azurepolicy"].(map[string]interface{}); ok {
					if enabled, ok := azurePolicy["enabled"].(bool); !ok || !enabled {
						allRecommendations = append(allRecommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategorySecurity,
							RuleName:            "azure_aks_azure_policy_disabled",
							Severity:            providers.RecommendationSeverityLow,
							Savings:             0,
							Data:                map[string]any{"reason": "Consider enabling Azure Policy for AKS cluster"},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}

			// Check for old Kubernetes version
			if kubernetesVersion, ok := props["kubernetesVersion"].(string); ok {
				// A more robust check using semantic versioning.
				// This list should be updated as Kubernetes versions go EOL.
				// As of late 2023/early 2024, versions below 1.27 are unsupported by the community.
				unsupportedBefore := "v1.27.0"

				// Add 'v' prefix if it's missing for semver comparison
				v1 := kubernetesVersion
				if !strings.HasPrefix(v1, "v") {
					v1 = "v" + v1
				}

				if semver.Compare(v1, unsupportedBefore) < 0 {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryInfraUpgrade,
						RuleName:            "azure_aks_old_kubernetes_version",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"reason": fmt.Sprintf("Consider upgrading Kubernetes version from %s", kubernetesVersion)},
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
	return allRecommendations, nil
}

func (s *aksService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "aks-cluster",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *aksService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
