package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

type userAssignedIdentityService struct {
}

func (s *userAssignedIdentityService) Name() string {
	return "microsoft.managedidentity/userassignedidentities"
}

func (s *userAssignedIdentityService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *userAssignedIdentityService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armmsi.NewUserAssignedIdentitiesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create user assigned identity client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, identity := range page.Value {
				allResources = append(allResources, providers.Resource{
					Id:          *identity.ID,
					Name:        *identity.Name,
					Type:        *identity.Type,
					Region:      *identity.Location,
					Tags:        toAzureTags(identity.Tags),
					Meta:        structToMap(identity),
					Status:      providers.ResourceStatusActive,
					CreatedAt:   time.Time{},
					Arn:         *identity.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *userAssignedIdentityService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *userAssignedIdentityService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *userAssignedIdentityService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("no apply actions supported for user assigned identities")
}

func (s *userAssignedIdentityService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("unknown command: %s", command.Command),
	}, fmt.Errorf("unknown command: %s", command.Command)
}

func (s *userAssignedIdentityService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *userAssignedIdentityService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "userassignedidentity",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}
