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
	ServiceNameDisk = "compute.googleapis.com/Disk"
)

type diskService struct{}

func (s *diskService) Name() string {
	return ServiceNameDisk
}

func (s *diskService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Disks don't have direct metrics in Cloud Monitoring
	// Disk I/O metrics are attached to instances
	return providers.QueryMetricsResponse{Items: []providers.MetricItem{}}, nil
}

func (s *diskService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	disksClient, err := compute.NewDisksRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create disks client: %w", err)
	}
	defer func() {
		if cerr := disksClient.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close disks client", "error", cerr)
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
				ctx.GetLogger().Warn("skipping Disks — Compute Engine API disabled or permission denied", "error", err)
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

	var allDisks []providers.Resource
	projectId := session.ProjectId

	// List disks in each zone
zoneLoop:
	for _, zone := range zones {
		req := &computepb.ListDisksRequest{
			Project: projectId,
			Zone:    zone,
		}

		it := disksClient.List(ctx.GetContext(), req)
		for {
			disk, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				RecordGCPPermissionError(ctx, err)
				if isGCPPermissionOrNotFoundError(err) {
					ctx.GetLogger().Warn("skipping disks — API disabled or permission denied", "zone", zone, "error", err)
					break zoneLoop
				}
				ctx.GetLogger().Error("failed to list disks", "zone", zone, "error", err)
				break
			}

			// Extract region from zone (e.g., us-central1-a -> us-central1)
			diskRegion := region
			if lastHyphen := strings.LastIndex(zone, "-"); lastHyphen > 0 {
				diskRegion = zone[:lastHyphen]
			}

			// Build resource ID (self link)
			resourceId := fmt.Sprintf("projects/%s/zones/%s/disks/%s", projectId, zone, *disk.Name)
			if disk.SelfLink != nil {
				resourceId = *disk.SelfLink
			}

			// Extract tags/labels
			tags := make(map[string][]string)
			if disk.Labels != nil {
				for key, value := range disk.Labels {
					tags[key] = []string{value}
				}
			}

			// Determine status based on whether disk is attached
			// Active = attached to an instance, Inactive = unattached
			status := providers.ResourceStatusInactive
			if len(disk.Users) > 0 {
				status = providers.ResourceStatusActive
			}

			// Extract creation timestamp
			createdAt := time.Now()
			if disk.CreationTimestamp != nil {
				if parsed, err := time.Parse(time.RFC3339, *disk.CreationTimestamp); err == nil {
					createdAt = parsed
				}
			}

			// Convert disk to map for Meta field
			meta := structToMap(disk)

			// Add additional metadata for easier querying
			if disk.SizeGb != nil {
				meta["sizeGb"] = *disk.SizeGb
			}
			if disk.Type != nil {
				// Type is a URL like "zones/us-central1-a/diskTypes/pd-standard"
				// Extract just the disk type name
				diskType := *disk.Type
				if parts := strings.Split(diskType, "/"); len(parts) > 0 {
					diskType = parts[len(parts)-1]
				}
				meta["diskType"] = diskType
			}

			allDisks = append(allDisks, providers.Resource{
				Id:          resourceId,
				Name:        *disk.Name,
				Type:        "storage", // This matches the frontend expectation in EC2_EBS_SERVICE_MAP
				Arn:         resourceId,
				ServiceName: ServiceNameDisk,
				Status:      status,
				Region:      diskRegion,
				Tags:        tags,
				Meta:        meta,
				CreatedAt:   createdAt,
			})
		}
	}

	ctx.GetLogger().Info("collected GCP persistent disks", "region", region, "count", len(allDisks))
	return allDisks, nil
}

func (s *diskService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	var recommendations []providers.Recommendation

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameDisk {
			continue
		}

		// Recommendation: Delete unattached disks
		if resource.Status == providers.ResourceStatusInactive {
			var sizeGb float64
			var diskType string

			if size, ok := resource.Meta["sizeGb"].(int64); ok {
				sizeGb = float64(size)
			}
			if dt, ok := resource.Meta["diskType"].(string); ok {
				diskType = dt
			}

			// Estimate monthly cost (rough approximation)
			// pd-standard: $0.04/GB/month, pd-ssd: $0.17/GB/month, pd-balanced: $0.10/GB/month
			monthlyCost := 0.0
			switch diskType {
			case "pd-ssd":
				monthlyCost = sizeGb * 0.17
			case "pd-balanced":
				monthlyCost = sizeGb * 0.10
			case "pd-standard":
				monthlyCost = sizeGb * 0.04
			default:
				monthlyCost = sizeGb * 0.04 // Default to standard pricing
			}

			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "gcp_disk_unattached",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      monthlyCost,
				Data: map[string]any{
					"resource_id":        resource.Id,
					"resource_name":      resource.Name,
					"resource_region":    resource.Region,
					"service_name":       resource.ServiceName,
					"disk_size_gb":       sizeGb,
					"disk_type":          diskType,
					"reason":             "Disk is not attached to any instance and is incurring storage costs without being used.",
					"recommended_action": "Create a snapshot for backup, then delete the unattached disk to save costs.",
					"benefits": []string{
						"Eliminate storage costs for unused disks",
						"Simplified resource inventory",
						"Reduced management overhead",
					},
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation: Upgrade pd-standard to pd-balanced for better performance
		if diskType, ok := resource.Meta["diskType"].(string); ok && diskType == "pd-standard" {
			if sizeGb, ok := resource.Meta["sizeGb"].(int64); ok && sizeGb >= 10 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "gcp_disk_type_upgrade",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0, // This is a cost increase but performance improvement
					Data: map[string]any{
						"resource_id":      resource.Id,
						"resource_name":    resource.Name,
						"resource_region":  resource.Region,
						"service_name":     resource.ServiceName,
						"current_type":     "pd-standard",
						"recommended_type": "pd-balanced",
						"disk_size_gb":     sizeGb,
						"reason":           "Standard persistent disks (pd-standard) provide basic performance. Upgrading to Balanced persistent disks (pd-balanced) offers better IOPS and throughput for a modest cost increase.",
						"benefits": []string{
							"3x better IOPS (up to 6,000 IOPS vs 2,000 IOPS)",
							"Better throughput (240 MB/s vs 180 MB/s)",
							"Improved application performance",
							"Better for database and transaction workloads",
						},
						"cost_impact": fmt.Sprintf("Cost increases by approximately $%.2f/month", float64(sizeGb)*(0.10-0.04)),
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation: Missing labels/tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_disk_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"resource_id":     resource.Id,
					"resource_name":   resource.Name,
					"resource_region": resource.Region,
					"service_name":    resource.ServiceName,
					"reason":          "Disk has no labels applied. Labels are essential for cost allocation, resource organization, and lifecycle management.",
					"benefits": []string{
						"Better cost tracking and allocation",
						"Easier resource organization",
						"Compliance and governance enforcement",
					},
					"recommended_labels": []string{"environment", "owner", "cost-center", "application"},
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

func (s *diskService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return fmt.Errorf("ApplyRecommendation not implemented for GCP Disk service")
}

func (s *diskService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{
		Success: false,
		Message: "ApplyCommand not implemented for GCP Disk service",
	}, fmt.Errorf("not implemented")
}

func (s *diskService) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", fmt.Errorf("log group not applicable for GCP Disk resources")
}

func (s *diskService) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, resource providers.Resource) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resource.Id,
			Kind:      ServiceNameDisk,
			Namespace: resource.Region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// If disk is attached to instances, add them as upstreams
	if users, ok := resource.Meta["users"].([]interface{}); ok && len(users) > 0 {
		for _, user := range users {
			if userURL, ok := user.(string); ok {
				link := providers.ServiceApplicationLink{
					Id: providers.ServiceApplicationId{
						Name:      userURL,
						Kind:      "compute.googleapis.com/Instance",
						Namespace: resource.Region,
					},
				}
				app.Upstreams = append(app.Upstreams, link.ToUpstreamLink())
			}
		}
	}

	return app, nil
}

func (s *diskService) GetLogFilter(ctx providers.CloudProviderContext, account providers.Account, resourceId string) string {
	// GCP doesn't have specific disk logs, but we can query for disk-related operations
	if resourceId == "" {
		return `resource.type="gce_disk"`
	}
	return fmt.Sprintf(`resource.type="gce_disk" protoPayload.resourceName="%s"`, resourceId)
}
