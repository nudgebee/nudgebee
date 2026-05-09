package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type expressRouteService struct {
}

// expressRouteClient is an interface to abstract the armnetwork.ExpressRouteCircuitsClient for mocking.
type expressRouteClient interface {
	NewListAllPager(options *armnetwork.ExpressRouteCircuitsClientListAllOptions) *runtime.Pager[armnetwork.ExpressRouteCircuitsClientListAllResponse]
}

// newExpressRouteClient is a variable that holds the function to create a new client.
// This can be replaced by a mock in tests.
var newExpressRouteClient = func(subID string, cred any, options any) (expressRouteClient, error) {
	return armnetwork.NewExpressRouteCircuitsClient(subID, cred.(*azidentity.ClientSecretCredential), nil)
}

func (s *expressRouteService) Name() string {
	return "Microsoft.Network/expressRouteCircuits"
}

// Scope returns the service scope - this is a regional service
func (s *expressRouteService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *expressRouteService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := newExpressRouteClient(subID, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create expressroute client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, circuit := range page.Value {
				status := providers.ResourceStatusUnknown
				if circuit.Properties != nil && circuit.Properties.CircuitProvisioningState != nil {
					state := strings.ToLower(*circuit.Properties.CircuitProvisioningState)
					switch state {
					case "enabled":
						status = providers.ResourceStatusActive
					case "disabled":
						status = providers.ResourceStatusInactive
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *circuit.ID,
					Name:        *circuit.Name,
					Type:        *circuit.Type,
					Region:      *circuit.Location,
					Tags:        toAzureTags(circuit.Tags),
					Meta:        structToMap(circuit),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *circuit.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *expressRouteService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *expressRouteService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and circuit name from resource ID
	parts := strings.Split(command.ResourceId, "/")
	var subscriptionID, resourceGroup, circuitName string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}
		if part == "resourceGroups" && i+1 < len(parts) {
			resourceGroup = parts[i+1]
		}
		if part == "expressRouteCircuits" && i+1 < len(parts) {
			circuitName = parts[i+1]
		}
	}

	if subscriptionID == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("could not determine subscription ID from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("missing subscription ID")
	}
	if resourceGroup == "" || circuitName == "" {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or circuit name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armnetwork.NewExpressRouteCircuitsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create expressroute client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_expressroute_enable_global_reach":
		// Enable Global Reach
		logger.Info("applying command: enabling ExpressRoute Global Reach", "circuitName", circuitName)

		circuitResp, err := client.Get(ctx.GetContext(), resourceGroup, circuitName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get expressroute circuit: %v", err),
			}, err
		}

		circuit := circuitResp.ExpressRouteCircuit
		if circuit.Properties == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "expressroute circuit properties are nil",
			}, fmt.Errorf("expressroute circuit properties are nil")
		}

		globalReachEnabled := true
		circuit.Properties.GlobalReachEnabled = &globalReachEnabled

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, circuitName, circuit, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update expressroute circuit: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for expressroute circuit update: %v", err),
			}, err
		}

		logger.Info("successfully enabled ExpressRoute Global Reach", "circuitName", circuitName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully enabled Global Reach for ExpressRoute circuit '%s'", circuitName),
		}, nil

	case "azure_expressroute_enable_standard_sku":
		// Upgrade to Standard SKU
		logger.Info("applying command: upgrading to Standard SKU", "circuitName", circuitName)

		circuitResp, err := client.Get(ctx.GetContext(), resourceGroup, circuitName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to get expressroute circuit: %v", err),
			}, err
		}

		circuit := circuitResp.ExpressRouteCircuit
		if circuit.SKU == nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: "expressroute circuit SKU is nil",
			}, fmt.Errorf("expressroute circuit SKU is nil")
		}

		standardTier := armnetwork.ExpressRouteCircuitSKUTierStandard
		circuit.SKU.Tier = &standardTier

		poller, err := client.BeginCreateOrUpdate(ctx.GetContext(), resourceGroup, circuitName, circuit, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to update expressroute circuit: %v", err),
			}, err
		}

		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for expressroute circuit update: %v", err),
			}, err
		}

		logger.Info("successfully upgraded to Standard SKU", "circuitName", circuitName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully upgraded ExpressRoute circuit '%s' to Standard SKU", circuitName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *expressRouteService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *expressRouteService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if Global Reach is disabled
		if props, ok := meta["properties"].(map[string]interface{}); ok {
			if globalReachEnabled, ok := props["globalReachEnabled"].(bool); !ok || !globalReachEnabled {
				allRecommendations = append(allRecommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "azure_expressroute_enable_global_reach",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{"reason": "Consider enabling ExpressRoute Global Reach for better connectivity",
						"resourceId":          resource.Id,
						"resourceName":        resource.Name,
						"resourceType":        resource.Type,
						"resourceRegion":      resource.Region,
						"resourceTags":        resource.Tags,
						"resourceArn":         resource.Arn,
						"resourceServiceName": resource.ServiceName,
						"resourceCreatedAt":   resource.CreatedAt,
						"resourceStatus":      resource.Status,
						"resourceMeta":        resource.Meta,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check SKU tier
			if sku, ok := meta["sku"].(map[string]interface{}); ok {
				if tier, ok := sku["tier"].(string); ok && strings.ToLower(tier) == "basic" {
					allRecommendations = append(allRecommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategoryConfiguration,
						RuleName:            "azure_expressroute_enable_standard_sku",
						Severity:            providers.RecommendationSeverityLow,
						Savings:             0,
						Data:                map[string]any{"reason": "Consider upgrading to Standard SKU for better features"},
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

func (s *expressRouteService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "expressroute",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *expressRouteService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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
