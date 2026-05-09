package azure

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

type dnsService struct {
}

func (s *dnsService) Name() string {
	return "Microsoft.Network/dnsZones"
}

// Scope returns the service scope - DNS is a global service
func (s *dnsService) Scope() ServiceScope {
	return ServiceScopeGlobal
}

func (s *dnsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
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
		client, err := armdns.NewZonesClient(subID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to create dns client: %w", err)
		}

		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				return nil, fmt.Errorf("failed to get next page: %w", err)
			}
			for _, zone := range page.Value {
				status := providers.ResourceStatusActive
				// DNS zones don't have a traditional status, assume active if they exist

				location := "global"
				if zone.Location != nil {
					location = *zone.Location
				}

				allResources = append(allResources, providers.Resource{
					Id:          *zone.ID,
					Name:        *zone.Name,
					Type:        *zone.Type,
					Region:      location,
					Tags:        toAzureTags(zone.Tags),
					Meta:        structToMap(zone),
					Status:      status,
					CreatedAt:   time.Time{},
					Arn:         *zone.ID,
					ServiceName: s.Name(),
				})
			}
		}
	}
	return allResources, nil
}

func (s *dnsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	_, err := s.ApplyCommand(ctx, account, providers.ApplyCommandRequest{
		ResourceId: recommendation.ResourceId,
		Command:    recommendation.RuleName,
	})
	return err
}

func (s *dnsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	logger := ctx.GetLogger()
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create azure credential: %v", err),
		}, err
	}

	// Extract subscription ID, resource group and zone name from resource ID
	subscriptionID, resourceGroup, zoneName, err := parseDNSZoneID(command.ResourceId)
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to extract resource group or zone name from resource ID: %s", command.ResourceId),
		}, fmt.Errorf("invalid resource ID")
	}

	client, err := armdns.NewZonesClient(subscriptionID, cred, getAzureAuditOpts(ctx))
	if err != nil {
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("failed to create dns client: %v", err),
		}, err
	}

	// Handle different commands
	switch command.Command {
	case "azure_dns_add_caa_record":
		// Add CAA record for security
		logger.Info("applying command: adding CAA record", "zoneName", zoneName)

		recordSetClient, err := armdns.NewRecordSetsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create record set client: %v", err),
			}, err
		}

		var flags int32
		if f, ok := command.Args["flags"].(float64); ok { // JSON numbers are float64
			flags = int32(f)
		} else {
			flags = 0
		}

		tag, ok := command.Args["tag"].(string)
		if !ok {
			tag = "issue"
		}

		value, ok := command.Args["value"].(string)
		if !ok || value == "" {
			return providers.ApplyCommandResponse{Success: false, Message: "CAA record 'value' is a required argument"}, fmt.Errorf("missing 'value' for CAA record")
		}

		// Create a CAA record set
		ttl := int64(3600)
		caaRecords := []*armdns.CaaRecord{
			{
				Flags: to.Ptr(flags),
				Tag:   to.Ptr(tag),
				Value: to.Ptr(value),
			},
		}

		recordSet := armdns.RecordSet{
			Properties: &armdns.RecordSetProperties{
				TTL:        &ttl,
				CaaRecords: caaRecords,
			},
		}

		_, err = recordSetClient.CreateOrUpdate(ctx.GetContext(), resourceGroup, zoneName, "@", armdns.RecordTypeCAA, recordSet, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to create CAA record: %v", err),
			}, err
		}

		logger.Info("successfully added CAA record", "zoneName", zoneName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully added CAA record for DNS zone '%s'", zoneName),
		}, nil

	case "azure_dns_enable_dnssec":
		// Enable DNSSEC (Note: Azure DNS doesn't support DNSSEC directly, this is a placeholder)
		logger.Info("applying command: enabling DNSSEC", "zoneName", zoneName)

		return providers.ApplyCommandResponse{
			Success: false,
			Message: "Azure DNS does not currently support DNSSEC. Consider using Azure DNS Private Resolver or third-party solutions.",
		}, errors.New("DNSSEC not supported")

	case "delete_zone":
		// Delete DNS zone
		logger.Info("applying command: deleting DNS zone", "zoneName", zoneName)
		poller, err := client.BeginDelete(ctx.GetContext(), resourceGroup, zoneName, nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to delete DNS zone: %v", err),
			}, err
		}
		_, err = poller.PollUntilDone(ctx.GetContext(), nil)
		if err != nil {
			return providers.ApplyCommandResponse{
				Success: false,
				Message: fmt.Sprintf("failed to wait for DNS zone deletion: %v", err),
			}, err
		}
		logger.Info("successfully deleted DNS zone", "zoneName", zoneName)
		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("successfully deleted DNS zone '%s'", zoneName),
		}, nil

	default:
		return providers.ApplyCommandResponse{
			Success: false,
			Message: fmt.Sprintf("unknown command: %s", command.Command),
		}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *dnsService) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAzureMonitorMetrics(ctx, account, filter)
}

func (s *dnsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	cred, _, err := getAzureCredsForAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential for recommendations: %w", err)
	}

	var allRecommendations []providers.Recommendation
	for _, resource := range existingResources {
		subscriptionID, resourceGroup, zoneName, err := parseDNSZoneID(resource.Id)
		if err != nil {
			// Log the error but continue to the next resource
			ctx.GetLogger().Error("failed to parse DNS Zone ID, skipping recommendations for resource", "resourceId", resource.Id, "error", err)
			continue
		}

		recordSetClient, err := armdns.NewRecordSetsClient(subscriptionID, cred, getAzureAuditOpts(ctx))
		if err != nil {
			ctx.GetLogger().Error("failed to create record set client, skipping recommendations for resource", "resourceId", resource.Id, "error", err)
			continue
		}

		hasCAARecords := false
		pager := recordSetClient.NewListByTypePager(resourceGroup, zoneName, armdns.RecordTypeCAA, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Error("failed to list CAA record sets, skipping check for resource", "resourceId", resource.Id, "error", err)
				hasCAARecords = true // Assume records exist to prevent erroneous recommendations on API failure
				break
			}
			if len(page.Value) > 0 {
				hasCAARecords = true
				break
			}
		}

		if !hasCAARecords {
			allRecommendations = append(allRecommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "azure_dns_add_caa_record",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{"reason": "Consider adding CAA records to control certificate issuance",
					"name":           resource.Name,
					"region":         resource.Region,
					"zoneName":       zoneName,
					"subscriptionId": subscriptionID,
					"resourceGroup":  resourceGroup,
					"resourceType":   resource.Type,
					"resourceId":     resource.Id,
					"tags":           resource.Tags,
					"meta":           resource.Meta,
					"status":         resource.Status,
					"createdAt":      resource.CreatedAt,
					"arn":            resource.Arn,
					"serviceName":    resource.ServiceName,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}
	return allRecommendations, nil
}

func (s *dnsService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Name,
			Kind:      "dns",
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	return app, nil
}

func (s *dnsService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
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

// parseDNSZoneID extracts the subscription ID, resource group, and zone name from a DNS Zone resource ID.
func parseDNSZoneID(resourceID string) (subscriptionID, resourceGroup, zoneName string, err error) {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if i+1 < len(parts) {
			switch strings.ToLower(part) {
			case "subscriptions":
				subscriptionID = parts[i+1]
			case "resourcegroups":
				resourceGroup = parts[i+1]
			case "dnszones":
				zoneName = parts[i+1]
			}
		}
	}

	if subscriptionID == "" || resourceGroup == "" || zoneName == "" {
		return "", "", "", fmt.Errorf("invalid DNS Zone resource ID format: %s", resourceID)
	}

	return subscriptionID, resourceGroup, zoneName, nil
}
