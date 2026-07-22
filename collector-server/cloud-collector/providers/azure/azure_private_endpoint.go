package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// privateEndpointService lists Azure Private Endpoints with full properties
// (subnet + privateLinkServiceConnections), so the KG can wire the private-link
// data path: consumer subnet <- PrivateEndpoint -> target PaaS resource
// (Key Vault / Storage / SQL / …). Without this the endpoints only arrive as
// minimal billing rows and end up orphaned.
type privateEndpointService struct {
}

func (s *privateEndpointService) Name() string {
	return "microsoft.network/privateendpoints"
}

// Scope is regional-listing, but we enumerate per subscription (endpoints carry
// their own region), matching the publicIp/NIC listers.
func (s *privateEndpointService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *privateEndpointService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	for _, subID := range strings.Split(session.SubscriptionID, ",") {
		if strings.TrimSpace(subID) == "" {
			continue
		}
		client, err := armnetwork.NewPrivateEndpointsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create private endpoint client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, pe := range page.Value {
				if pe == nil || pe.ID == nil {
					continue
				}
				name, resType, location := "", "", ""
				if pe.Name != nil {
					name = *pe.Name
				}
				if pe.Type != nil {
					resType = *pe.Type
				}
				if pe.Location != nil {
					location = *pe.Location
				}
				allResources = append(allResources, providers.Resource{
					Id:          *pe.ID,
					Name:        name,
					Type:        resType,
					Region:      location,
					Tags:        toAzureTags(pe.Tags),
					Meta:        structToMap(pe),
					Status:      providers.ResourceStatusActive,
					CreatedAt:   time.Time{},
					Arn:         *pe.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *privateEndpointService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *privateEndpointService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *privateEndpointService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("no apply actions supported for private endpoints")
}

func (s *privateEndpointService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{Success: false, Message: "unsupported"}, errors.ErrUnsupported
}

func (s *privateEndpointService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *privateEndpointService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{
		Id:          providers.ServiceApplicationId{Name: resource.Name, Kind: "privateendpoint", Namespace: resource.Region},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}, nil
}
