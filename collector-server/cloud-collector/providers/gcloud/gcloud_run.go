package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"

	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/api/iterator"
)

const ServiceNameRun = "Cloud Run"

type cloudRunService struct{}

func (s *cloudRunService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for Cloud Run
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *cloudRunService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := run.NewServicesClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud Run client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Cloud Run client", "error", cerr)
		}
	}()

	var resources []providers.Resource

	// List all Cloud Run services in the project
	req := &runpb.ListServicesRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", session.ProjectId),
	}

	it := client.ListServices(ctx.GetContext(), req)
	for {
		service, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Cloud Run — API disabled or permission denied", "error", err)
				break
			}
			ctx.GetLogger().Error("failed to list Cloud Run services", "error", err)
			break
		}

		resource := runServiceToResource(service, session.ProjectId)
		resources = append(resources, resource)
	}

	ctx.GetLogger().Info("retrieved Cloud Run services", "count", len(resources), "projectId", session.ProjectId)
	return resources, nil
}

func runServiceToResource(service *runpb.Service, projectId string) providers.Resource {
	// Extract location from service name (format: projects/{project}/locations/{location}/services/{service})
	parts := strings.Split(service.Name, "/")
	location := "global"
	serviceName := service.Name
	if len(parts) >= 6 {
		location = parts[3]
		serviceName = parts[5]
	}

	// Get service URL
	serviceUrl := service.Uri

	// Get container image
	containerImage := ""
	if service.Template != nil && len(service.Template.Containers) > 0 {
		containerImage = service.Template.Containers[0].Image
	}

	// Get resource limits
	cpuLimit := ""
	memoryLimit := ""
	if service.Template != nil && len(service.Template.Containers) > 0 {
		resources := service.Template.Containers[0].Resources
		if resources != nil {
			// ResourceRequirements has Limits map
			if resources.Limits != nil {
				if cpu, ok := resources.Limits["cpu"]; ok {
					cpuLimit = cpu
				}
				if mem, ok := resources.Limits["memory"]; ok {
					memoryLimit = mem
				}
			}
		}
	}

	// Get scaling configuration
	minInstances := int32(0)
	maxInstances := int32(100)
	if service.Template != nil && service.Template.Scaling != nil {
		minInstances = service.Template.Scaling.MinInstanceCount
		maxInstances = service.Template.Scaling.MaxInstanceCount
	}

	// Get ingress settings
	ingress := "INGRESS_TRAFFIC_ALL"
	if service.Ingress != runpb.IngressTraffic_INGRESS_TRAFFIC_UNSPECIFIED {
		ingress = service.Ingress.String()
	}

	// Get traffic allocation
	trafficInfo := make(map[string]interface{})
	for i, traffic := range service.Traffic {
		trafficInfo[fmt.Sprintf("target_%d", i)] = map[string]interface{}{
			"revision": traffic.Revision,
			"percent":  traffic.Percent,
			"type":     traffic.Type.String(),
		}
	}

	// Use service name only as resource ID (matches GCP Monitoring service_name label)
	// The full path is like: projects/my-project/locations/us-central1/services/my-service
	resourceId := serviceName
	selfLink := service.Name

	// Tags/Labels
	tags := make(map[string][]string)
	for key, value := range service.Labels {
		tags[key] = []string{value}
	}

	// Meta information
	meta := map[string]interface{}{
		"url":              serviceUrl,
		"container_image":  containerImage,
		"cpu_limit":        cpuLimit,
		"memory_limit":     memoryLimit,
		"min_instances":    minInstances,
		"max_instances":    maxInstances,
		"ingress":          ingress,
		"traffic":          trafficInfo,
		"generation":       service.Generation,
		"latest_ready_rev": service.LatestReadyRevision,
		"latest_created":   service.LatestCreatedRevision,
		"selfLink":         selfLink,
	}

	// Determine status
	status := providers.ResourceStatusUnknown
	if service.TerminalCondition != nil {
		conditionState := service.TerminalCondition.State.String()
		if conditionState == "CONDITION_SUCCEEDED" || conditionState == "READY" {
			status = providers.ResourceStatusActive
		} else {
			status = providers.ResourceStatusInactive
		}
	}

	return providers.Resource{
		Id:          resourceId, // Service name only (matches GCP Monitoring service_name)
		Name:        serviceName,
		Type:        "run.googleapis.com/Service",
		Arn:         selfLink, // Full path for ARN
		ServiceName: ServiceNameRun,
		Status:      status,
		Region:      location,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   service.CreateTime.AsTime(),
	}
}

func (s *cloudRunService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameRun {
			continue
		}

		// Recommendation 1: Check if service has no labels
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "gcp_run_no_labels",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"service_id": resource.Id},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check for min_instances > 0 (always running)
		if minInstances, ok := resource.Meta["min_instances"].(int32); ok && minInstances > 0 {
			estimatedSavings := float64(minInstances) * 30.0 // Rough estimate: $30/month per idle instance
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing,
				RuleName:            "gcp_run_always_on",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             estimatedSavings,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"service_id": resource.Id, "min_instances": minInstances, "recommended": 0},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 3: Check for public ingress
		if ingress, ok := resource.Meta["ingress"].(string); ok && ingress == "INGRESS_TRAFFIC_ALL" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "gcp_run_public_ingress",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"service_id": resource.Id, "current_ingress": ingress},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 4: Check for high memory allocation
		if memLimit, ok := resource.Meta["memory_limit"].(string); ok && memLimit != "" {
			// Parse memory (e.g., "512Mi", "2Gi") and check if > 1024 MB (1 GB)
			memMB, err := parseMemoryToMB(memLimit)
			if err == nil && memMB > 1024 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "gcp_run_high_memory",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             10.0,
					Action:              providers.RecommendationActionModify,
					Data:                map[string]any{"service_id": resource.Id, "current_memory": memLimit, "current_memory_mb": memMB, "recommended_size": "Review actual usage"},
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 5: Check if service is not serving traffic
		if resource.Status != providers.ResourceStatusActive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "gcp_run_not_ready",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"service_id": resource.Id, "state": resource.Status},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (s *cloudRunService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := run.NewServicesClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create Cloud Run client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Cloud Run client", "error", cerr)
		}
	}()

	switch recommendation.RuleName {
	case "gcp_run_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gcloud CLI")

	case "gcp_run_always_on":
		return fmt.Errorf("automatic scaling configuration not yet implemented - please adjust min_instances manually via GCP console")

	case "gcp_run_public_ingress":
		return fmt.Errorf("ingress configuration changes require manual review - please configure via GCP console")

	case "gcp_run_high_memory":
		return fmt.Errorf("automatic memory adjustment not yet implemented - please adjust memory allocation manually via GCP console")

	case "gcp_run_not_ready":
		return fmt.Errorf("service state issues require manual investigation - check deployment logs in GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

func (s *cloudRunService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := run.NewServicesClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create Cloud Run client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Cloud Run client", "error", cerr)
		}
	}()

	switch command.Command {
	case "delete":
		serviceName, ok := command.Args["service_name"].(string)
		if !ok || serviceName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("service_name arg required")
		}

		// Check if location is provided, otherwise use default
		location, ok := command.Args["location"].(string)
		if !ok || location == "" {
			location = "us-central1" // default location
		}

		fullName := fmt.Sprintf("projects/%s/locations/%s/services/%s", session.ProjectId, location, serviceName)

		req := &runpb.DeleteServiceRequest{
			Name: fullName,
		}

		op, err := client.DeleteService(ctx.GetContext(), req)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to initiate service deletion: %w", err)
		}

		// Wait for operation to complete
		_, err = op.Wait(ctx.GetContext())
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("service deletion failed: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted service %s", serviceName),
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *cloudRunService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, resourceId string) string {
	if resourceId == "" {
		return `resource.type="cloud_run_revision"`
	}
	return fmt.Sprintf(`resource.type="cloud_run_revision" resource.labels.service_name="%s"`, resourceId)
}
