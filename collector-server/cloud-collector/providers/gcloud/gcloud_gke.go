package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
)

const (
	ServiceNameGKE = "Kubernetes Engine"
)

type gkeService struct{}

func (s *gkeService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for GKE
	// Common metrics: container/cpu/usage_time, container/memory/usage,
	// container/network/received_bytes_count, container/network/sent_bytes_count,
	// node/cpu/allocatable_utilization, node/memory/allocatable_utilization
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *gkeService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := container.NewClusterManagerClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GKE cluster manager client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close GKE cluster manager client", "error", cerr)
		}
	}()
	var resources []providers.Resource

	// List all clusters in the project
	// GKE clusters can be regional or zonal
	parent := fmt.Sprintf("projects/%s/locations/%s", session.ProjectId, region)
	if region == "" || region == "global" {
		parent = fmt.Sprintf("projects/%s/locations/-", session.ProjectId)
	}

	req := &containerpb.ListClustersRequest{
		Parent: parent,
	}

	resp, err := client.ListClusters(ctx.GetContext(), req)
	if err != nil {
		return nil, fmt.Errorf("failed to list GKE clusters: %w", err)
	}

	for _, cluster := range resp.Clusters {
		// Filter by region if specified
		if region != "" && region != "global" {
			// Extract region from location (zone format: us-central1-a, region format: us-central1)
			clusterRegion := cluster.Location
			if strings.Contains(cluster.Location, "-") {
				parts := strings.Split(cluster.Location, "-")
				if len(parts) >= 2 {
					clusterRegion = strings.Join(parts[:2], "-")
				}
			}
			if !strings.HasPrefix(clusterRegion, region) {
				continue
			}
		}

		resource := s.clusterToResource(cluster, session.ProjectId)
		resources = append(resources, resource)
	}

	return resources, nil
}

func (s *gkeService) clusterToResource(cluster *containerpb.Cluster, projectId string) providers.Resource {
	// Use cluster name as resource ID (matches GCP Monitoring cluster_name label)
	resourceId := cluster.Name

	// Store full path for reference
	selfLink := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectId, cluster.Location, cluster.Name)
	if cluster.SelfLink != "" {
		selfLink = cluster.SelfLink
	}

	// Extract region from location
	region := cluster.Location
	if strings.Contains(cluster.Location, "-") && len(strings.Split(cluster.Location, "-")) == 3 {
		// It's a zone, extract region
		parts := strings.Split(cluster.Location, "-")
		region = strings.Join(parts[:2], "-")
	}

	// Extract tags/labels
	tags := make(map[string][]string)
	if cluster.ResourceLabels != nil {
		for key, value := range cluster.ResourceLabels {
			tags[key] = []string{value}
		}
	}

	// Determine status
	status := gkeStatusToNbStatus(cluster.Status)

	// Extract creation timestamp
	createdAt := time.Now()
	if cluster.CreateTime != "" {
		if parsed, err := time.Parse(time.RFC3339, cluster.CreateTime); err == nil {
			createdAt = parsed
		}
	}

	// Convert cluster to map for Meta field
	meta := structToMap(cluster)
	meta["selfLink"] = selfLink

	// Resource type includes cluster tier (standard/autopilot)
	resourceType := "container.googleapis.com/Cluster"
	if cluster.Autopilot != nil && cluster.Autopilot.Enabled {
		resourceType = "container.googleapis.com/Cluster/Autopilot"
	}

	return providers.Resource{
		Id:          resourceId, // Cluster name (matches GCP Monitoring cluster_name)
		Name:        cluster.Name,
		Type:        resourceType,
		Arn:         selfLink, // Full path for ARN
		ServiceName: ServiceNameGKE,
		Status:      status,
		Region:      region,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

func gkeStatusToNbStatus(status containerpb.Cluster_Status) providers.ResourceStatus {
	switch status {
	case containerpb.Cluster_RUNNING:
		return providers.ResourceStatusActive
	case containerpb.Cluster_RECONCILING, containerpb.Cluster_PROVISIONING:
		return providers.ResourceStatusActive
	case containerpb.Cluster_STOPPING, containerpb.Cluster_ERROR, containerpb.Cluster_DEGRADED:
		return providers.ResourceStatusInactive
	case containerpb.Cluster_STATUS_UNSPECIFIED:
		return providers.ResourceStatusUnknown
	default:
		return providers.ResourceStatusUnknown
	}
}

func (s *gkeService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	// Load GCP alarm templates for GKE
	gkeAlarmTemplates, err := LoadGCPAlarmTemplates("Kubernetes Engine")
	if err != nil {
		ctx.GetLogger().Warn("Failed to load GCP GKE alarm templates", "error", err)
		gkeAlarmTemplates = []providers.AlarmTemplate{} // Continue with other recommendations
	}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameGKE {
			continue
		}

		// Check for missing Cloud Monitoring alert policies
		for _, template := range gkeAlarmTemplates {
			// Check if alarm is missing
			resourceFilter := fmt.Sprintf("resource.type=\"k8s_cluster\" AND resource.labels.cluster_name=\"%s\"", resource.Name)
			isMissing, err := IsAlarmMissing(resource, template, resourceFilter)
			if err != nil {
				ctx.GetLogger().Warn("Failed to check if alarm is missing", "error", err, "template", template.Name)
				continue
			}

			if !isMissing {
				// Alarm already exists, skip
				continue
			}

			// Calculate threshold from struct
			threshold := template.ThresholdRules.Default
			if threshold == 0 {
				threshold = 0.80
			}

			// Build alarm configuration for the recommendation data
			alarmConfig := buildGCPAlarmConfig(resource, template, threshold, []providers.AlarmDimension{
				{Name: "cluster_name", Value: resource.Name},
			})

			// Create recommendation
			recommendation := providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     template.Name,
				Severity:     providers.RecommendationSeverityFromString(template.Severity),
				Savings:      0,
				Data: map[string]any{
					"cluster_id":     resource.Id,
					"cluster_name":   resource.Name,
					"cluster_region": resource.Region,
					"metric_name":    template.Configuration.MetricName,
					"threshold":      threshold,
					"alarm_config":   alarmConfig,
					"alarm_type":     template.AlarmType,
					"reason":         template.Description,
					"project_id":     account.AccountNumber,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// Recommendation 1: Check for clusters without labels
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_gke_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":   resource.Id,
					"cluster_name": resource.Name,
					"region":       resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check for inactive clusters
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "gcp_gke_inactive_cluster",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":   resource.Id,
					"cluster_name": resource.Name,
					"region":       resource.Region,
					"status":       resource.Status,
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 3: Check for old clusters (potential upgrade candidates)
		if time.Since(resource.CreatedAt) > 180*24*time.Hour {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryInfraUpgrade,
				RuleName:     "gcp_gke_old_cluster",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":   resource.Id,
					"cluster_name": resource.Name,
					"region":       resource.Region,
					"age_days":     int(time.Since(resource.CreatedAt).Hours() / 24),
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 4: Check autoscaling configuration
		if nodePools, ok := resource.Meta["nodePools"].([]interface{}); ok {
			for _, np := range nodePools {
				if nodePool, ok := np.(map[string]interface{}); ok {
					if autoscaling, ok := nodePool["autoscaling"].(map[string]interface{}); ok {
						if enabled, ok := autoscaling["enabled"].(bool); ok && !enabled {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName: providers.RecommendationCategoryConfiguration,
								RuleName:     "gcp_gke_no_autoscaling",
								Severity:     providers.RecommendationSeverityMedium,
								Savings:      0,
								Data: map[string]any{
									"cluster_id":   resource.Id,
									"cluster_name": resource.Name,
									"region":       resource.Region,
									"node_pool":    nodePool["name"],
								},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
							break
						}
					}
				}
			}
		}

		// Recommendation 5: Check for binary authorization
		if binaryAuth, ok := resource.Meta["binaryAuthorization"].(map[string]interface{}); ok {
			if enabled, ok := binaryAuth["enabled"].(bool); ok && !enabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "gcp_gke_no_binary_authorization",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"cluster_id":   resource.Id,
						"cluster_name": resource.Name,
						"region":       resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 6: Check for network policy
		if networkPolicy, ok := resource.Meta["networkPolicy"].(map[string]interface{}); ok {
			if enabled, ok := networkPolicy["enabled"].(bool); ok && !enabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "gcp_gke_no_network_policy",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"cluster_id":   resource.Id,
						"cluster_name": resource.Name,
						"region":       resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 7: Check for maintenance window
		if maintenancePolicy, ok := resource.Meta["maintenancePolicy"].(map[string]interface{}); ok {
			if window, ok := maintenancePolicy["window"].(map[string]interface{}); !ok || window == nil {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_gke_no_maintenance_window",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"cluster_id":   resource.Id,
						"cluster_name": resource.Name,
						"region":       resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 8: Check for workload identity
		if workloadIdentityConfig, ok := resource.Meta["workloadIdentityConfig"].(map[string]interface{}); !ok || workloadIdentityConfig == nil {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "gcp_gke_no_workload_identity",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":   resource.Id,
					"cluster_name": resource.Name,
					"region":       resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 9: Check for logging enabled
		if loggingService, ok := resource.Meta["loggingService"].(string); ok && (loggingService == "none" || loggingService == "") {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_gke_logging_disabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":   resource.Id,
					"cluster_name": resource.Name,
					"region":       resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 10: Check for monitoring enabled
		if monitoringService, ok := resource.Meta["monitoringService"].(string); ok && (monitoringService == "none" || monitoringService == "") {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_gke_monitoring_disabled",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":   resource.Id,
					"cluster_name": resource.Name,
					"region":       resource.Region,
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

func (s *gkeService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/alert policy recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("gcp: applying alarm recommendation for GKE",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateGCPAlertPolicyFromRecommendation(ctx, account, recommendation)
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := container.NewClusterManagerClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create GKE cluster manager client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close GKE cluster manager client", "error", cerr)
		}
	}()

	clusterName, ok := recommendation.Data["cluster_name"].(string)
	if !ok || clusterName == "" {
		return fmt.Errorf("cluster_name not found in recommendation data")
	}

	location := recommendation.ResourceRegion
	if location == "" {
		return fmt.Errorf("cluster location not found")
	}

	switch recommendation.RuleName {
	case "gcp_gke_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gcloud CLI")

	case "gcp_gke_inactive_cluster":
		// Delete the inactive cluster
		name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", session.ProjectId, location, clusterName)
		req := &containerpb.DeleteClusterRequest{
			Name: name,
		}

		op, err := client.DeleteCluster(ctx.GetContext(), req)
		if err != nil {
			return fmt.Errorf("failed to delete GKE cluster: %w", err)
		}

		ctx.GetLogger().Info("successfully initiated GKE cluster deletion", "cluster", clusterName, "operation", op.Name)
		return nil

	case "gcp_gke_old_cluster":
		return fmt.Errorf("cluster upgrade requires manual review and cannot be automatically applied")

	case "gcp_gke_no_autoscaling":
		return fmt.Errorf("automatic autoscaling configuration not yet implemented - please enable autoscaling manually via GCP console")

	case "gcp_gke_no_binary_authorization":
		return fmt.Errorf("automatic binary authorization configuration not yet implemented - please enable manually via GCP console")

	case "gcp_gke_no_network_policy":
		return fmt.Errorf("automatic network policy configuration not yet implemented - please enable manually via GCP console")

	case "gcp_gke_no_maintenance_window":
		return fmt.Errorf("automatic maintenance window configuration not yet implemented - please configure manually via GCP console")

	case "gcp_gke_no_workload_identity":
		return fmt.Errorf("automatic workload identity configuration not yet implemented - please enable manually via GCP console")

	case "gcp_gke_logging_disabled":
		return fmt.Errorf("automatic logging configuration not yet implemented - please enable logging manually via GCP console")

	case "gcp_gke_monitoring_disabled":
		return fmt.Errorf("automatic monitoring configuration not yet implemented - please enable monitoring manually via GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

func (s *gkeService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := container.NewClusterManagerClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create GKE cluster manager client: %w", err)
	}

	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close GKE cluster manager client", "error", cerr)
		}
	}()

	switch command.Command {
	case "delete":
		clusterName, ok := command.Args["cluster_name"].(string)
		if !ok || clusterName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("cluster_name arg required")
		}
		location, ok := command.Args["location"].(string)
		if !ok || location == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("location arg required")
		}

		name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", session.ProjectId, location, clusterName)
		req := &containerpb.DeleteClusterRequest{
			Name: name,
		}

		op, err := client.DeleteCluster(ctx.GetContext(), req)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to delete GKE cluster: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully initiated deletion of GKE cluster %s in location %s (operation: %s)", clusterName, location, op.Name),
		}, nil

	case "resize":
		clusterName, ok := command.Args["cluster_name"].(string)
		if !ok || clusterName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("cluster_name arg required")
		}
		location, ok := command.Args["location"].(string)
		if !ok || location == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("location arg required")
		}
		nodePoolName, ok := command.Args["node_pool_name"].(string)
		if !ok || nodePoolName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("node_pool_name arg required")
		}
		nodeCount, ok := command.Args["node_count"].(int)
		if !ok {
			if nodeCountFloat, ok := command.Args["node_count"].(float64); ok {
				nodeCount = int(nodeCountFloat)
			} else {
				return providers.ApplyCommandResponse{}, fmt.Errorf("node_count arg required")
			}
		}

		name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", session.ProjectId, location, clusterName, nodePoolName)
		req := &containerpb.SetNodePoolSizeRequest{
			Name:      name,
			NodeCount: int32(nodeCount),
		}

		op, err := client.SetNodePoolSize(ctx.GetContext(), req)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to resize node pool: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully initiated resize of node pool %s to %d nodes (operation: %s)", nodePoolName, nodeCount, op.Name),
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unsupported command: %s", command.Command)
	}
}

func (s *gkeService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, resourceId string) string {
	if resourceId == "" {
		return `resource.type="k8s_container" OR resource.type="k8s_cluster"`
	}
	return fmt.Sprintf(`resource.type="k8s_cluster" resource.labels.cluster_name="%s"`, resourceId)
}
