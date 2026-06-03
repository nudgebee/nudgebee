package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
)

type entraIDService struct {
}

func (s *entraIDService) Name() string {
	return "microsoft.authorization/roleassignments"
}

// Scope returns the service scope - Entra ID is a global service
func (s *entraIDService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *entraIDService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cred, session, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	var allResources []providers.Resource
	var subscriptionIDs = strings.Split(session.SubscriptionID, ",")

	// First, fetch service principals with credentials using Microsoft Graph API
	graphClient, err := msgraphsdk.NewGraphServiceClientWithCredentials(cred, []string{"https://graph.microsoft.com/.default"})
	if err != nil {
		return nil, fmt.Errorf("failed to create graph client: %w", err)
	}

	// Create a map to store service principal credential info
	servicePrincipalCredentials := make(map[string]map[string]interface{})

	// Fetch all service principals with password credentials
	spResult, err := graphClient.ServicePrincipals().Get(ctx.GetContext(), &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{
			Select: []string{"id", "appId", "displayName", "passwordCredentials", "keyCredentials"},
		},
	})
	if err == nil && spResult != nil && spResult.GetValue() != nil {
		for _, sp := range spResult.GetValue() {
			if sp.GetId() == nil {
				continue
			}

			spID := *sp.GetId()
			credInfo := make(map[string]interface{})

			// Check password credentials (client secrets)
			passwordCreds := sp.GetPasswordCredentials()
			if len(passwordCreds) > 0 {
				hasExpiredCreds := false
				hasExpiringSoonCreds := false
				var earliestExpiry *time.Time

				for _, cred := range passwordCreds {
					if cred.GetEndDateTime() != nil {
						endTime := *cred.GetEndDateTime()
						if earliestExpiry == nil || endTime.Before(*earliestExpiry) {
							earliestExpiry = &endTime
						}

						if endTime.Before(time.Now()) {
							hasExpiredCreds = true
						} else if endTime.Before(time.Now().Add(30 * 24 * time.Hour)) {
							hasExpiringSoonCreds = true
						}
					}
				}

				credInfo["hasPasswordCredentials"] = true
				credInfo["passwordCredentialsExpired"] = hasExpiredCreds
				credInfo["passwordCredentialsExpiringSoon"] = hasExpiringSoonCreds
				if earliestExpiry != nil {
					credInfo["earliestPasswordExpiry"] = earliestExpiry.Format(time.RFC3339)
				}
			}

			// Check key credentials (certificates)
			keyCreds := sp.GetKeyCredentials()
			if len(keyCreds) > 0 {
				hasExpiredKeys := false
				hasExpiringKeys := false
				var earliestKeyExpiry *time.Time

				for _, cred := range keyCreds {
					if cred.GetEndDateTime() != nil {
						endTime := *cred.GetEndDateTime()
						if earliestKeyExpiry == nil || endTime.Before(*earliestKeyExpiry) {
							earliestKeyExpiry = &endTime
						}

						if endTime.Before(time.Now()) {
							hasExpiredKeys = true
						} else if endTime.Before(time.Now().Add(30 * 24 * time.Hour)) {
							hasExpiringKeys = true
						}
					}
				}

				credInfo["hasKeyCredentials"] = true
				credInfo["keyCredentialsExpired"] = hasExpiredKeys
				credInfo["keyCredentialsExpiringSoon"] = hasExpiringKeys
				if earliestKeyExpiry != nil {
					credInfo["earliestKeyExpiry"] = earliestKeyExpiry.Format(time.RFC3339)
				}
			}

			servicePrincipalCredentials[spID] = credInfo
		}
	}

	// Now fetch role assignments
	for _, subID := range subscriptionIDs {
		if strings.TrimSpace(subID) == "" {
			continue
		}

		// Get role assignments (users, groups, service principals with roles)
		roleClient, err := armauthorization.NewRoleAssignmentsClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create role assignments client: %w", err)
		}

		pager := roleClient.NewListForSubscriptionPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}

			for _, assignment := range page.Value {
				if assignment.ID == nil || assignment.Name == nil || assignment.Type == nil {
					continue
				}

				status := providers.ResourceStatusActive
				meta := structToMap(assignment.Properties)

				// Enrich metadata with service principal credential info if available
				if assignment.Properties != nil && assignment.Properties.PrincipalID != nil {
					principalID := *assignment.Properties.PrincipalID
					if credInfo, exists := servicePrincipalCredentials[principalID]; exists {
						// Merge credential info into meta
						for k, v := range credInfo {
							meta[k] = v
						}
					}
				}

				allResources = append(allResources, providers.Resource{
					Id:          *assignment.ID,
					Name:        *assignment.Name,
					Type:        *assignment.Type,
					Region:      "global", // Entra ID is global
					Tags:        map[string][]string{},
					Meta:        meta,
					Status:      status,
					Arn:         *assignment.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}

	return allResources, nil
}

func (s *entraIDService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *entraIDService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		properties := resource.Meta

		// Check for overly permissive role assignments
		if roleDefinitionID, ok := properties["roleDefinitionId"].(string); ok {
			if strings.Contains(strings.ToLower(roleDefinitionID), "owner") ||
				strings.Contains(strings.ToLower(roleDefinitionID), "contributor") {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_entra_id_overly_permissive_role",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"reason":      fmt.Sprintf("Role assignment has overly permissive role: %s", roleDefinitionID),
						"meta":        properties,
						"id":          resource.Id,
						"type":        resource.Type,
						"region":      resource.Region,
						"serviceName": resource.ServiceName,
						"status":      resource.Status,
						"name":        resource.Name,
						"arn":         resource.Arn,
						"createdAt":   resource.CreatedAt,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for service principal credentials that are expired or expiring soon
		if principalType, ok := properties["principalType"].(string); ok && principalType == "ServicePrincipal" {
			// Check for expired password credentials
			if expired, ok := properties["passwordCredentialsExpired"].(bool); ok && expired {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_entra_id_service_principal_credentials_expired",
					Severity:     providers.RecommendationSeverityCritical,
					Savings:      0,
					Data: map[string]any{
						"reason":      "Service principal has expired password credentials",
						"meta":        properties,
						"tags":        resource.Tags,
						"id":          resource.Id,
						"type":        resource.Type,
						"region":      resource.Region,
						"serviceName": resource.ServiceName,
						"status":      resource.Status,
						"name":        resource.Name,
						"arn":         resource.Arn,
						"createdAt":   resource.CreatedAt,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			} else if expiringSoon, ok := properties["passwordCredentialsExpiringSoon"].(bool); ok && expiringSoon {
				// Check for credentials expiring within 30 days
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_entra_id_service_principal_credentials_expiring_soon",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"reason":      fmt.Sprintf("Service principal has password credentials expiring soon within: %s", properties["earliestPasswordExpiry"]),
						"meta":        properties,
						"tags":        resource.Tags,
						"id":          resource.Id,
						"type":        resource.Type,
						"region":      resource.Region,
						"serviceName": resource.ServiceName,
						"status":      resource.Status,
						"name":        resource.Name,
						"arn":         resource.Arn,
						"createdAt":   resource.CreatedAt,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check for expired key credentials (certificates)
			if expired, ok := properties["keyCredentialsExpired"].(bool); ok && expired {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_entra_id_service_principal_certificates_expired",
					Severity:     providers.RecommendationSeverityCritical,
					Savings:      0,
					Data: map[string]any{
						"reason":      "Service principal has expired key credentials (certificates)",
						"meta":        properties,
						"tags":        resource.Tags,
						"id":          resource.Id,
						"type":        resource.Type,
						"region":      resource.Region,
						"serviceName": resource.ServiceName,
						"status":      resource.Status,
						"name":        resource.Name,
						"arn":         resource.Arn,
						"createdAt":   resource.CreatedAt,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			} else if expiringSoon, ok := properties["keyCredentialsExpiringSoon"].(bool); ok && expiringSoon {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "azure_entra_id_service_principal_certificates_expiring_soon",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"reason":      fmt.Sprintf("Service principal has key credentials (certificates) expiring soon within: %s", properties["earliestKeyExpiry"]),
						"meta":        properties,
						"tags":        resource.Tags,
						"id":          resource.Id,
						"type":        resource.Type,
						"region":      resource.Region,
						"serviceName": resource.ServiceName,
						"status":      resource.Status,
						"name":        resource.Name,
						"arn":         resource.Arn,
						"createdAt":   resource.CreatedAt,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check for Guest users with privileged roles
		if principalType, ok := properties["principalType"].(string); ok && principalType == "Guest" {
			if roleDefinitionID, ok := properties["roleDefinitionId"].(string); ok {
				if strings.Contains(strings.ToLower(roleDefinitionID), "admin") ||
					strings.Contains(strings.ToLower(roleDefinitionID), "owner") {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "azure_entra_id_guest_with_privileged_role",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"reason":      fmt.Sprintf("Guest user has privileged role assigned: %s", roleDefinitionID),
							"meta":        properties,
							"tags":        resource.Tags,
							"id":          resource.Id,
							"type":        resource.Type,
							"region":      resource.Region,
							"serviceName": resource.ServiceName,
							"status":      resource.Status,
							"name":        resource.Name,
							"arn":         resource.Arn,
							"createdAt":   resource.CreatedAt,
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

func (s *entraIDService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *entraIDService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Entra ID role assignments cannot be auto-fixed due to security implications
	// These require manual review and approval
	return providers.ApplyCommandResponse{
		Success: false,
		Message: fmt.Sprintf("cannot auto-apply command for Entra ID: %s requires manual review and approval for security reasons", command.Command),
	}, errors.ErrUnsupported
}

func (s *entraIDService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return "", fmt.Errorf("failed to create azure credential: %w", err)
	}

	_, err = extractSubscriptionID(resourceId)
	if err != nil {
		return "", fmt.Errorf("failed to extract subscription id from resource id: %w", err)
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

func (s *entraIDService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      s.Name(),
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
