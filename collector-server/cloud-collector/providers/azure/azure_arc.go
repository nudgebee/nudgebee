package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcompute/armhybridcompute/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type arcService struct {
}

func (s *arcService) Name() string {
	return "microsoft.hybridcompute/machines"
}

// Scope returns the service scope - this is a regional service
func (s *arcService) Scope() ServiceScope {
	return ServiceScopeRegional
}

func (s *arcService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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

		// Get Azure Arc-enabled servers (Hybrid Compute machines)
		machinesClient, err := armhybridcompute.NewMachinesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create hybrid compute machines client: %w", err)
		}

		machinesPager := machinesClient.NewListBySubscriptionPager(nil)
		for machinesPager.More() {
			page, err := machinesPager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page of machines: %w", err)
			}

			for _, machine := range page.Value {
				if machine.ID == nil || machine.Name == nil || machine.Type == nil {
					continue
				}

				status := providers.ResourceStatusUnknown
				if machine.Properties != nil && machine.Properties.Status != nil {
					switch *machine.Properties.Status {
					case armhybridcompute.StatusTypesConnected:
						status = providers.ResourceStatusActive
					case armhybridcompute.StatusTypesDisconnected:
						status = providers.ResourceStatusInactive
					case armhybridcompute.StatusTypesError:
						status = providers.ResourceStatusInactive
					}
				}

				meta := structToMap(machine.Properties)
				if meta == nil {
					meta = make(map[string]any)
				}

				// Add additional metadata
				if machine.Properties != nil {
					if machine.Properties.OSName != nil {
						meta["osName"] = *machine.Properties.OSName
					}
					if machine.Properties.OSVersion != nil {
						meta["osVersion"] = *machine.Properties.OSVersion
					}
					if machine.Properties.AgentVersion != nil {
						meta["agentVersion"] = *machine.Properties.AgentVersion
					}
					if machine.Properties.LastStatusChange != nil {
						meta["lastStatusChange"] = machine.Properties.LastStatusChange.Format(time.RFC3339)
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:     *machine.ID,
					Name:   *machine.Name,
					Type:   *machine.Type,
					Region: normalizeAzureRegion(*machine.Location),
					Tags:   toAzureTags(machine.Tags),
					Meta:   meta,
					Status: status,
					// Azure Arc Hybrid Compute Machines do not expose a creation timestamp.
					// CreatedAt is intentionally left as zero time to indicate "unknown".
					CreatedAt:   time.Time{},
					Arn:         *machine.ID,
					ServiceName: s.Name(),
				})
			}
		}

		// Also get Azure Arc-enabled Kubernetes clusters
		// Note: This would require armhybridkubernetes SDK, skipping for now to focus on core Arc servers
	}

	return allResources, nil
}

func (s *arcService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *arcService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for disconnected Arc machines
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "azure_arc_machine_disconnected",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"reason": "The Azure Arc machine is disconnected. Investigate connectivity issues.",
					"machine": map[string]any{
						"name":   resource.Name,
						"region": resource.Region,
					},
					"status":      resource.Status,
					"arn":         resource.Arn,
					"type":        resource.Type,
					"tags":        resource.Tags,
					"region":      resource.Region,
					"meta":        resource.Meta,
					"id":          resource.Id,
					"serviceName": resource.ServiceName,
					"createdAt":   resource.CreatedAt,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check for Arc machines in error state (disconnected status check above already covers this)

		// Check for outdated Arc agent version
		if agentVersion, ok := properties["agentVersion"].(string); ok {
			// Simple version check - in production, you'd compare against known versions
			if strings.HasPrefix(agentVersion, "0.") || strings.HasPrefix(agentVersion, "1.0") {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_arc_outdated_agent",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"reason":         "The Azure Arc agent version is outdated. Consider updating to the latest version for security and feature improvements.",
						"currentVersion": agentVersion,
						"machine": map[string]any{
							"name":   resource.Name,
							"region": resource.Region,
						},
						"region":      resource.Region,
						"meta":        resource.Meta,
						"id":          resource.Id,
						"serviceName": resource.ServiceName,
						"createdAt":   resource.CreatedAt,
						"status":      resource.Status,
						"arn":         resource.Arn,
						"type":        resource.Type,
						"tags":        resource.Tags,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for machines without recent status updates (stale)
		if lastStatusChange, ok := properties["lastStatusChange"].(string); ok {
			if t, err := time.Parse(time.RFC3339, lastStatusChange); err == nil {
				if time.Since(t) > 24*time.Hour {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "azure_arc_machine_stale_status",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"reason":           "The Azure Arc machine has not reported status in over 24 hours. Verify connectivity and health.",
							"lastStatusChange": lastStatusChange,
							"machine": map[string]any{
								"name":   resource.Name,
								"region": resource.Region,
							},
							"osName":       properties["osName"],
							"osVersion":    properties["osVersion"],
							"agentVersion": properties["agentVersion"],
							"status":       resource.Status,
							"arn":          resource.Arn,
							"type":         resource.Type,
							"tags":         resource.Tags,
							"region":       resource.Region,
							"meta":         resource.Meta,
							"id":           resource.Id,
							"serviceName":  resource.ServiceName,
							"createdAt":    resource.CreatedAt,
						},
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

	return recommendations, nil
}

func (s *arcService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *arcService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Arc machines require manual intervention for most operations
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Arc: %s requires manual intervention on the connected machine", command.Command),
	}, errors.ErrUnsupported
}

func (s *arcService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

	return "", errors.New("log group name not found")
}

func (s *arcService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	rg, err := extractResourceGroup(resource.Id)
	if err != nil {
		return providers.ServiceMapApplication{}, fmt.Errorf("failed to extract resource group: %w", err)
	}

	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams: []providers.UpstreamLink{
			{
				Id: providers.ServiceApplicationId{
					Name:      rg,
					Kind:      "Microsoft.Resources/resourceGroups",
					Namespace: resource.Region,
				}.Key(),
			},
		},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
