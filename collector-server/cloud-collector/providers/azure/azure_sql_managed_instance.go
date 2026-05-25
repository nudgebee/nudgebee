package azure

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

type sqlManagedInstanceService struct {
}

func (s *sqlManagedInstanceService) Name() string {
	return "Microsoft.Sql/managedInstances"
}

func (s *sqlManagedInstanceService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *sqlManagedInstanceService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	subscriptionIDs := strings.Split(session.SubscriptionID, ",")
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}

		instancesClient, err := armsql.NewManagedInstancesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create managed instances client: %w", err)
		}

		databasesClient, err := armsql.NewManagedDatabasesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create managed databases client: %w", err)
		}

		instancesPager := instancesClient.NewListPager(nil)
		for instancesPager.More() {
			page, err := instancesPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to list sql managed instances: %w", err)
			}

			for _, mi := range page.Value {
				if mi == nil || mi.ID == nil || mi.Name == nil || mi.Type == nil || mi.Location == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if mi.Properties != nil && mi.Properties.ProvisioningState != nil {
					if val, ok := nbStatusFromAzureProvisioningState[string(*mi.Properties.ProvisioningState)]; ok {
						status = val
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *mi.ID,
					Name:        *mi.Name,
					Type:        *mi.Type,
					Region:      *mi.Location,
					Tags:        toAzureTags(mi.Tags),
					Meta:        structToMap(mi),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *mi.ID,
					ServiceName: s.Name(),
				})

				resourceGroup, err := extractResourceGroup(*mi.ID)
				if err != nil {
					ctx.GetLogger().Warn("sql managed instance: failed to extract resource group, skipping databases", "instance", *mi.Name, "error", err)
					continue
				}

				dbPager := databasesClient.NewListByInstancePager(resourceGroup, *mi.Name, nil)
				for dbPager.More() {
					dbPage, err := dbPager.NextPage(ctx.GetContext())
					if err != nil {
						ctx.GetLogger().Warn("sql managed instance: failed to list managed databases", "instance", *mi.Name, "error", err)
						break
					}
					for _, db := range dbPage.Value {
						if db == nil || db.ID == nil || db.Name == nil || db.Type == nil || db.Location == nil {
							continue
						}
						dbStatus := providers.ResourceStatusUnknown
						createdAt := time.Time{}
						if db.Properties != nil {
							if db.Properties.Status != nil {
								if val, ok := nbStatusFromAzureProvisioningState[string(*db.Properties.Status)]; ok {
									dbStatus = val
								}
							}
							if db.Properties.CreationDate != nil {
								createdAt = *db.Properties.CreationDate
							}
						}
						allResources = append(allResources, providers.Resource{
							Id:          *db.ID,
							Name:        *db.Name,
							Type:        *db.Type,
							Region:      *db.Location,
							Tags:        toAzureTags(db.Tags),
							Meta:        structToMap(db),
							Status:      dbStatus,
							CreatedAt:   createdAt,
							Arn:         *db.ID,
							ServiceName: s.Name(),
						})
					}
				}
			}
		}
	}
	return allResources, nil
}

func (s *sqlManagedInstanceService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *sqlManagedInstanceService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	return []providers.Recommendation{}, nil
}

func (s *sqlManagedInstanceService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("ApplyRecommendation not implemented for Microsoft.Sql/managedInstances")
}

func (s *sqlManagedInstanceService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("ApplyCommand not implemented for Microsoft.Sql/managedInstances")
}

func (s *sqlManagedInstanceService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (s *sqlManagedInstanceService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}
