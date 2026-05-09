package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
)

type sshPublicKeyService struct {
}

func (s *sshPublicKeyService) Name() string {
	return "microsoft.compute/sshpublickeys"
}

func (s *sshPublicKeyService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *sshPublicKeyService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armcompute.NewSSHPublicKeysClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create SSH public key client: %w", err)
		}

		pager := client.NewListBySubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, key := range page.Value {
				if key == nil || key.ID == nil || key.Name == nil || key.Type == nil {
					continue
				}
				id := safeDeref(key.ID)
				allResources = append(allResources, providers.Resource{
					Id:          id,
					Name:        safeDeref(key.Name),
					Type:        safeDeref(key.Type),
					Region:      safeDeref(key.Location),
					Tags:        toAzureTags(key.Tags),
					Meta:        structToMap(key),
					Status:      providers.ResourceStatusActive,
					CreatedAt:   time.Time{},
					Arn:         id,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *sshPublicKeyService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *sshPublicKeyService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	return nil, nil
}

func (s *sshPublicKeyService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("no apply actions supported for SSH public keys")
}

func (s *sshPublicKeyService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("unknown command: %s", command.Command),
	}, fmt.Errorf("unknown command: %s", command.Command)
}

func (s *sshPublicKeyService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return resourceId, nil
}

func (s *sshPublicKeyService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "sshpublickey",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}
