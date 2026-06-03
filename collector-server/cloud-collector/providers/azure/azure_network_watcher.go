package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

type networkWatcherService struct {
}

func (s *networkWatcherService) Name() string {
	return "microsoft.network/networkwatchers"
}

func (s *networkWatcherService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *networkWatcherService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armnetwork.NewWatchersClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create network watcher client: %w", err)
		}

		pager := client.NewListAllPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, watcher := range page.Value {
				status := providers.ResourceStatusUnknown
				if watcher.Properties != nil && watcher.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*watcher.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *watcher.ID,
					Name:        *watcher.Name,
					Type:        *watcher.Type,
					Region:      *watcher.Location,
					Tags:        toAzureTags(watcher.Tags),
					Meta:        structToMap(watcher),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *watcher.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *networkWatcherService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *networkWatcherService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *networkWatcherService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("no apply actions supported for network watchers")
}

func (s *networkWatcherService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("unknown command: %s", command.Command),
	}, fmt.Errorf("unknown command: %s", command.Command)
}

func (s *networkWatcherService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *networkWatcherService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "networkwatcher",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}
