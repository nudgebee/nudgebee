package gcloud

import (
	"fmt"
	"strings"

	redis "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	"google.golang.org/api/iterator"

	"nudgebee/collector/cloud/providers"
)

const ServiceNameMemorystore = "Memorystore"

// memorystoreService lists Memorystore for Redis instances (regional). GetResources
// lists instances for the given region; the store loop iterates regions.
type memorystoreService struct{}

func (s *memorystoreService) GetMetrices(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (s *memorystoreService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	if region == "" || strings.EqualFold(region, "global") {
		return nil, nil
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := redis.NewCloudRedisClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create memorystore redis client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close memorystore redis client", "error", cerr)
		}
	}()

	resources := []providers.Resource{}
	it := client.ListInstances(ctx.GetContext(), &redispb.ListInstancesRequest{
		Parent: fmt.Sprintf("projects/%s/locations/%s", session.ProjectId, region),
	})
	for {
		inst, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Memorystore instances — API disabled, permission denied, or region unsupported", "error", err, "region", region)
				return resources, nil
			}
			return resources, fmt.Errorf("failed to list memorystore instances: %w", err)
		}
		resources = append(resources, memorystoreInstanceToResource(inst, region))
	}

	if len(resources) > 0 {
		ctx.GetLogger().Info("fetched Memorystore Redis instances", "count", len(resources), "region", region)
	}
	return resources, nil
}

func memorystoreInstanceToResource(inst *redispb.Instance, region string) providers.Resource {
	// inst.Name is "projects/{project}/locations/{location}/instances/{instance}"
	parts := strings.Split(inst.GetName(), "/")
	instID := parts[len(parts)-1]

	tags := make(map[string][]string)
	for k, v := range inst.GetLabels() {
		tags[k] = []string{v}
	}

	meta := map[string]interface{}{
		"selfLink":       inst.GetName(),
		"state":          inst.GetState().String(),
		"tier":           inst.GetTier().String(),
		"memory_size_gb": inst.GetMemorySizeGb(),
		"redis_version":  inst.GetRedisVersion(),
		"host":           inst.GetHost(),
		"port":           inst.GetPort(),
	}

	return providers.Resource{
		Id:          instID,
		Name:        instID,
		Type:        "memorystore",
		Arn:         inst.GetName(),
		ServiceName: ServiceNameMemorystore,
		Status:      providers.ResourceStatusActive,
		Region:      region,
		Tags:        tags,
		Meta:        meta,
	}
}

func (s *memorystoreService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	// Load GCP alarm templates for Memorystore (all instance-level metrics)
	memorystoreAlarmTemplates, err := LoadGCPAlarmTemplates(ServiceNameMemorystore)
	if err != nil {
		ctx.GetLogger().Warn("Failed to load GCP Memorystore alarm templates", "error", err)
		memorystoreAlarmTemplates = []providers.AlarmTemplate{}
	}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameMemorystore {
			continue
		}

		// redis_instance's instance_id label carries the full resource name
		// (projects/.../locations/.../instances/...), not the short id — use
		// the selfLink captured at discovery (memorystoreInstanceToResource),
		// reconstructing it when absent (a short id would never match the label).
		instancePath, _ := resource.Meta["selfLink"].(string)
		if instancePath == "" {
			instancePath = fmt.Sprintf("projects/%s/locations/%s/instances/%s", account.AccountNumber, resource.Region, resource.Id)
		}

		// Check for missing Cloud Monitoring alert policies
		for _, template := range memorystoreAlarmTemplates {
			resourceFilter := GetResourceFilterForService(ServiceNameMemorystore, instancePath)
			isMissing, err := IsAlarmMissing(resource, template, resourceFilter)
			if err != nil {
				ctx.GetLogger().Warn("Failed to check if alarm is missing", "error", err, "template", template.Name)
				continue
			}

			if !isMissing {
				// Alarm already exists, skip
				continue
			}

			// All Memorystore thresholds are static ratios/counts
			threshold := template.ThresholdRules.Default

			alarmConfig := buildGCPAlarmConfig(resource, template, threshold, []providers.AlarmDimension{
				{Name: "instance_id", Value: instancePath},
			})

			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     template.Name,
				Severity:     providers.RecommendationSeverityFromString(template.Severity),
				Savings:      0,
				Data: map[string]any{
					"instance_id":     resource.Id,
					"instance_name":   resource.Name,
					"instance_region": resource.Region,
					"tier":            resource.Meta["tier"],
					"memory_size_gb":  resource.Meta["memory_size_gb"],
					"metric_name":     template.Configuration.MetricName,
					"threshold":       threshold,
					"alarm_config":    alarmConfig,
					"alarm_type":      template.AlarmType,
					"reason":          template.Description,
					"metric_type":     template.MetricType,
					"project_id":      account.AccountNumber,
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

func (s *memorystoreService) ApplyRecommendation(_ providers.CloudProviderContext, _ providers.Account, _ providers.Recommendation) error {
	return fmt.Errorf("apply recommendation not supported for Memorystore")
}

func (s *memorystoreService) ApplyCommand(_ providers.CloudProviderContext, _ providers.Account, _ providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, fmt.Errorf("apply command not supported for Memorystore")
}

func (s *memorystoreService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}
