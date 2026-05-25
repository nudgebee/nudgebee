package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
)

const (
	ServiceNameNetworkInterface = "compute.googleapis.com/NetworkInterface"
)

type networkInterfaceService struct{}

func (s *networkInterfaceService) Name() string {
	return ServiceNameNetworkInterface
}

func (s *networkInterfaceService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Network interface metrics are collected at the instance level
	// (network/received_bytes_count, network/sent_bytes_count)
	return providers.QueryMetricsResponse{Items: []providers.MetricItem{}}, nil
}

func (s *networkInterfaceService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	instancesClient, err := compute.NewInstancesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create instances client: %w", err)
	}
	defer func() {
		if cerr := instancesClient.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close instances client", "error", cerr)
		}
	}()

	// Get zones client to list all zones in the region
	zonesClient, err := compute.NewZonesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create zones client: %w", err)
	}
	defer func() {
		if cerr := zonesClient.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close zones client", "error", cerr)
		}
	}()

	// Get all zones in the specified region
	zones := []string{}
	zoneReq := &computepb.ListZonesRequest{
		Project: session.ProjectId,
	}
	zoneIt := zonesClient.List(ctx.GetContext(), zoneReq)
	for {
		zone, err := zoneIt.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping NetworkInterfaces — Compute Engine API disabled or permission denied", "error", err)
				return []providers.Resource{}, nil
			}
			return nil, fmt.Errorf("failed to list zones: %w", err)
		}
		// Filter zones by region (zone name format: region-zone, e.g., us-central1-a)
		if zone.Name != nil && strings.HasPrefix(*zone.Name, region) {
			zones = append(zones, *zone.Name)
		}
	}

	// If no zones found for the region, return empty
	if len(zones) == 0 {
		ctx.GetLogger().Warn("no zones found for region", "region", region)
		return []providers.Resource{}, nil
	}

	var allNICs []providers.Resource
	projectId := session.ProjectId

	// List instances in each zone and extract their network interfaces
zoneLoop:
	for _, zone := range zones {
		req := &computepb.ListInstancesRequest{
			Project: projectId,
			Zone:    zone,
		}

		it := instancesClient.List(ctx.GetContext(), req)
		for {
			instance, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				RecordGCPPermissionError(ctx, err)
				if isGCPPermissionOrNotFoundError(err) {
					ctx.GetLogger().Warn("skipping network interfaces — API disabled or permission denied", "zone", zone, "error", err)
					break zoneLoop
				}
				ctx.GetLogger().Error("failed to list instances", "zone", zone, "error", err)
				break
			}

			// Extract region from zone
			nicRegion := region
			if lastHyphen := strings.LastIndex(zone, "-"); lastHyphen > 0 {
				nicRegion = zone[:lastHyphen]
			}

			// Extract network interfaces from the instance
			if instance.NetworkInterfaces != nil {
				for idx, nic := range instance.NetworkInterfaces {
					// Build a unique resource ID for this NIC
					// Format: projects/{project}/zones/{zone}/instances/{instance}/networkInterfaces/{index}
					resourceId := fmt.Sprintf("projects/%s/zones/%s/instances/%s/networkInterfaces/%d",
						projectId, zone, *instance.Name, idx)

					// Extract NIC name (use interface name if available, otherwise generate one)
					nicName := fmt.Sprintf("%s-nic%d", *instance.Name, idx)
					if nic.Name != nil {
						nicName = *nic.Name
					}

					// Extract tags/labels from the parent instance
					tags := make(map[string][]string)
					if instance.Labels != nil {
						for key, value := range instance.Labels {
							tags[key] = []string{value}
						}
					}

					// NICs are always active if the instance exists
					status := providers.ResourceStatusActive

					// Use instance creation time as NIC creation time
					createdAt := time.Now()
					if instance.CreationTimestamp != nil {
						if parsed, err := time.Parse(time.RFC3339, *instance.CreationTimestamp); err == nil {
							createdAt = parsed
						}
					}

					// Convert NIC to map for Meta field
					meta := structToMap(nic)

					// Add instance context to metadata
					meta["instanceName"] = *instance.Name
					meta["instanceId"] = fmt.Sprintf("projects/%s/zones/%s/instances/%s", projectId, zone, *instance.Name)
					if instance.Id != nil {
						meta["instanceNumericId"] = fmt.Sprintf("%d", *instance.Id)
					}
					meta["zone"] = zone
					meta["nicIndex"] = idx

					// Extract key NIC properties for easier querying
					if nic.NetworkIP != nil {
						meta["internalIP"] = *nic.NetworkIP
					}
					if nic.Network != nil {
						meta["network"] = *nic.Network
					}
					if nic.Subnetwork != nil {
						meta["subnetwork"] = *nic.Subnetwork
					}

					// Extract external IP if available
					if len(nic.AccessConfigs) > 0 {
						for _, accessConfig := range nic.AccessConfigs {
							if accessConfig.NatIP != nil {
								meta["externalIP"] = *accessConfig.NatIP
								break
							}
						}
					}

					allNICs = append(allNICs, providers.Resource{
						Id:          resourceId,
						Name:        nicName,
						Type:        ServiceNameNetworkInterface,
						Arn:         resourceId,
						ServiceName: ServiceNameNetworkInterface,
						Status:      status,
						Region:      nicRegion,
						Tags:        tags,
						Meta:        meta,
						CreatedAt:   createdAt,
					})
				}
			}
		}
	}

	ctx.GetLogger().Info("collected GCP network interfaces", "region", region, "count", len(allNICs))
	return allNICs, nil
}

func (s *networkInterfaceService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameNetworkInterface {
			continue
		}

		// Recommendation: Instances with multiple NICs may have unused interfaces
		// This is informational rather than actionable
		if nicIndex, ok := resource.Meta["nicIndex"].(int); ok && nicIndex > 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_nic_multiple_interfaces",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"resource_id":        resource.Id,
					"resource_name":      resource.Name,
					"resource_region":    resource.Region,
					"service_name":       resource.ServiceName,
					"instance_name":      resource.Meta["instanceName"],
					"nic_index":          nicIndex,
					"internal_ip":        resource.Meta["internalIP"],
					"reason":             "Instance has multiple network interfaces. Verify that all interfaces are necessary for your workload.",
					"recommended_action": "Review network interface usage and remove unused interfaces to simplify network configuration.",
					"benefits": []string{
						"Simplified network configuration",
						"Reduced potential attack surface",
						"Easier troubleshooting",
					},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation: NICs with external IPs may need security review
		if externalIP, ok := resource.Meta["externalIP"].(string); ok && externalIP != "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "gcp_nic_external_ip",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"resource_id":        resource.Id,
					"resource_name":      resource.Name,
					"resource_region":    resource.Region,
					"service_name":       resource.ServiceName,
					"instance_name":      resource.Meta["instanceName"],
					"external_ip":        externalIP,
					"internal_ip":        resource.Meta["internalIP"],
					"reason":             "Network interface has an external IP address, exposing the instance to the internet. Ensure proper firewall rules and security controls are in place.",
					"recommended_action": "Review firewall rules, consider using Cloud NAT for outbound traffic, or remove external IP if not needed.",
					"benefits": []string{
						"Reduced attack surface",
						"Better network security",
						"Compliance with security policies",
					},
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (s *networkInterfaceService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("ApplyRecommendation not implemented for GCP Network Interface service")
}

func (s *networkInterfaceService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{
		Success: false,
		Message: "ApplyCommand not implemented for GCP Network Interface service",
	}, fmt.Errorf("not implemented")
}

func (s *networkInterfaceService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", fmt.Errorf("log group not applicable for GCP Network Interface resources")
}

func (s *networkInterfaceService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      ServiceNameNetworkInterface,
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// Add instance as upstream
	if instanceId, ok := resource.Meta["instanceId"].(string); ok {
		link := providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      instanceId,
				Kind:      "compute.googleapis.com/Instance",
				Namespace: resource.Region,
			},
		}
		app.Upstreams = append(app.Upstreams, link.ToUpstreamLink())
	}

	// Add network/subnetwork as downstreams
	if network, ok := resource.Meta["network"].(string); ok {
		link := providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      network,
				Kind:      "compute.googleapis.com/Network",
				Namespace: "global",
			},
		}
		app.Downstreams = append(app.Downstreams, link.ToDownstreamLink())
	}

	if subnetwork, ok := resource.Meta["subnetwork"].(string); ok {
		link := providers.ServiceApplicationLink{
			Id: providers.ServiceApplicationId{
				Name:      subnetwork,
				Kind:      "compute.googleapis.com/Subnetwork",
				Namespace: resource.Region,
			},
		}
		app.Downstreams = append(app.Downstreams, link.ToDownstreamLink())
	}

	return app, nil
}

func (s *networkInterfaceService) GetLogFilter(ctx providers.CloudProviderContext, account providers.Account, resourceId string) string {
	// Network interface logs are typically part of instance logs or VPC flow logs
	if resourceId == "" {
		return `resource.type="gce_instance"`
	}

	// Try to get the numeric instance ID from GCP API using common helper
	// This extracts project/zone/instance from the resource ID and fetches numeric ID
	instanceId := GetInstanceNumericIdFromResourceId(ctx, account, resourceId)
	if instanceId != "" {
		return fmt.Sprintf(`resource.type="gce_instance" resource.labels.instance_id="%s"`, instanceId)
	}

	// Fallback: extract instance name manually and use it
	_, _, instanceName := parseInstanceInfoFromResourceId(resourceId)
	if instanceName != "" {
		return fmt.Sprintf(`resource.type="gce_instance" resource.labels.instance_id="%s"`, instanceName)
	}

	return `resource.type="gce_instance"`
}
