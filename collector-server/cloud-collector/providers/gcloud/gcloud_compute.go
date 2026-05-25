package gcloud

import (
	"encoding/json"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
)

const (
	ServiceNameCompute = "Compute Engine"
)

type computeEngineService struct{}

func (s *computeEngineService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for Compute Engine
	// Common metrics: cpu/utilization, disk/read_bytes_count, disk/write_bytes_count,
	// network/received_bytes_count, network/sent_bytes_count, uptime
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *computeEngineService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := compute.NewInstancesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close compute client", "error", cerr)
		}
	}()

	var resources []providers.Resource

	// GCP uses zones, not regions. Convert region to zones
	// For now, we'll list all instances in the project and filter by region
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
				ctx.GetLogger().Warn("skipping Compute instances — Compute Engine API disabled or permission denied", "error", err)
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
		return resources, nil
	}

	// Create machine types client for fetching instance type details
	machineTypesClient, err := compute.NewMachineTypesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		ctx.GetLogger().Warn("failed to create machine types client, will skip instance type details", "error", err)
		machineTypesClient = nil
	} else {
		defer func() {
			if cerr := machineTypesClient.Close(); cerr != nil {
				ctx.GetLogger().Error("failed to close machine types client", "error", cerr)
			}
		}()
	}

	// Create a cache for machine types to avoid N+1 API calls
	// Key: zone/machineTypeName, Value: machineType details
	machineTypeCache := make(map[string]*computepb.MachineType)

	// List instances in each zone
zoneLoop:
	for _, zone := range zones {
		req := &computepb.ListInstancesRequest{
			Project: session.ProjectId,
			Zone:    zone,
		}

		it := client.List(ctx.GetContext(), req)
		for {
			instance, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				RecordGCPPermissionError(ctx, err)
				if isGCPPermissionOrNotFoundError(err) {
					ctx.GetLogger().Warn("skipping Compute instances — API disabled or permission denied", "error", err, "zone", zone)
					break zoneLoop
				}
				ctx.GetLogger().Error("failed to list instances", "error", err, "zone", zone)
				break
			}

			// Convert instance to Resource
			resource := s.instanceToResource(ctx, instance, session.ProjectId, zone, machineTypesClient, machineTypeCache)
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (s *computeEngineService) instanceToResource(ctx providers.CloudProviderContext, instance *computepb.Instance, projectId, zone string, machineTypesClient *compute.MachineTypesClient, machineTypeCache map[string]*computepb.MachineType) providers.Resource {
	// Extract region from zone (e.g., us-central1-a -> us-central1)
	region := zone
	if lastHyphen := strings.LastIndex(zone, "-"); lastHyphen > 0 {
		region = zone[:lastHyphen]
	}

	// Use numeric instance ID as resource ID (matches GCP Monitoring API format)
	// This is consistent with how AWS uses instance IDs (e.g., i-0abc123)
	// GCP Monitoring API uses the numeric instance.Id (e.g., 1020227973377891447)
	// NOT the selfLink URL that was previously stored
	resourceId := fmt.Sprintf("%d", *instance.Id)

	// Extract tags
	tags := make(map[string][]string)
	if instance.Labels != nil {
		for key, value := range instance.Labels {
			tags[key] = []string{value}
		}
	}

	// Determine status
	status := gcpComputeStatusToNbStatus(instance.Status)

	// Extract creation timestamp
	createdAt := time.Now()
	if instance.CreationTimestamp != nil {
		if parsed, err := time.Parse(time.RFC3339, *instance.CreationTimestamp); err == nil {
			createdAt = parsed
		}
	}

	// Convert instance to map for Meta field
	meta := structToMap(instance)

	// Add InstanceTypeDetails using GCP MachineTypes API data
	// This matches AWS/Azure format for UI compatibility
	if instance.MachineType != nil && machineTypesClient != nil {
		instanceTypeDetails := fetchAndParseMachineType(ctx, machineTypesClient, projectId, zone, *instance.MachineType, machineTypeCache)
		if instanceTypeDetails != nil {
			meta["InstanceTypeDetails"] = instanceTypeDetails
		}
	}

	// Store selfLink in metadata for reference (not used as primary ID anymore)
	// The primary ID is now the numeric instance.Id to match GCP Monitoring API
	selfLink := fmt.Sprintf("projects/%s/zones/%s/instances/%s", projectId, zone, *instance.Name)
	if instance.SelfLink != nil {
		selfLink = *instance.SelfLink
	}
	meta["selfLink"] = selfLink

	return providers.Resource{
		Id:          resourceId,     // Numeric ID: "1020227973377891447"
		Name:        *instance.Name, // Instance name: "my-instance"
		Type:        "compute.googleapis.com/Instance",
		Arn:         selfLink, // selfLink for ARN: "https://..."
		ServiceName: ServiceNameCompute,
		Status:      status,
		Region:      region,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

func gcpComputeStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}

	switch *status {
	case "RUNNING":
		return providers.ResourceStatusActive
	case "STOPPED", "SUSPENDED", "TERMINATED":
		return providers.ResourceStatusInactive
	case "STOPPING", "SUSPENDING":
		return providers.ResourceStatusInactive
	case "PROVISIONING", "STAGING":
		return providers.ResourceStatusActive
	case "REPAIRING":
		return providers.ResourceStatusActive
	default:
		return providers.ResourceStatusUnknown
	}
}

func (s *computeEngineService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	// Load GCP alarm templates
	computeAlarmTemplates, err := LoadGCPAlarmTemplates("Compute Engine")
	if err != nil {
		ctx.GetLogger().Warn("Failed to load GCP Compute Engine alarm templates", "error", err)
		computeAlarmTemplates = []providers.AlarmTemplate{} // Continue with other recommendations
	}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameCompute {
			continue
		}

		// Check for missing Cloud Monitoring alert policies
		for _, template := range computeAlarmTemplates {
			// Check if alarm is missing
			// GCP uses numeric instance_id (not the instance name) for gce_instance resources
			instanceID := ""
			if id, ok := resource.Meta["id"]; ok {
				instanceID = fmt.Sprintf("%.0f", id)
			} else {
				instanceID = resource.Name
			}
			resourceFilter := fmt.Sprintf("resource.type=\"gce_instance\" AND resource.labels.instance_id=\"%s\"", instanceID)
			isMissing, err := IsAlarmMissing(resource, template, resourceFilter)
			if err != nil {
				ctx.GetLogger().Warn("Failed to check if alarm is missing", "error", err, "template", template.Name)
				continue
			}

			if !isMissing {
				// Alarm already exists, skip
				continue
			}

			// Calculate threshold based on machine type
			threshold := calculateGCPThreshold(resource, template)

			// Build alarm configuration for the recommendation data
			alarmConfig := buildGCPAlarmConfig(resource, template, threshold, []providers.AlarmDimension{
				{Name: "instance_id", Value: instanceID},
			})

			// Create recommendation
			recommendation := providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     template.Name,
				Severity:     providers.RecommendationSeverityFromString(template.Severity),
				Savings:      0,
				Data: map[string]any{
					"instance_id":     resource.Id,
					"instance_name":   resource.Name,
					"instance_region": resource.Region,
					"machine_type":    resource.Meta["machineType"],
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
			}
			recommendations = append(recommendations, recommendation)
		}

		// Recommendation 1: Check for instances without labels/tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_compute_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"instance_id":   resource.Id,
					"instance_name": resource.Name,
					"region":        resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check for stopped instances
		if resource.Status == providers.ResourceStatusInactive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing,
				RuleName:     "gcp_compute_stopped_instance",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"instance_id":   resource.Id,
					"instance_name": resource.Name,
					"region":        resource.Region,
					"status":        resource.Status,
				},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 3: Check for old instances (potential upgrade candidates)
		if time.Since(resource.CreatedAt) > 90*24*time.Hour {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryInfraUpgrade,
				RuleName:     "gcp_compute_old_instance",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"instance_id":   resource.Id,
					"instance_name": resource.Name,
					"region":        resource.Region,
					"age_days":      int(time.Since(resource.CreatedAt).Hours() / 24),
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

func (s *computeEngineService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/alert policy recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("gcp: applying alarm recommendation for compute instance",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateGCPAlertPolicyFromRecommendation(ctx, account, recommendation)
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := compute.NewInstancesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close compute client", "error", cerr)
		}
	}()

	// Parse instance information from resource ID
	instanceId, ok := recommendation.Data["instance_id"].(string)
	if !ok || instanceId == "" {
		return fmt.Errorf("instance_id not found in recommendation data")
	}

	switch recommendation.RuleName {
	case "gcp_compute_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gcloud CLI")

	case "gcp_compute_stopped_instance":
		// Delete the stopped instance
		instanceName, ok := recommendation.Data["instance_name"].(string)
		if !ok || instanceName == "" {
			return fmt.Errorf("instance_name not found in recommendation data")
		}

		zone := extractZoneFromResourceId(instanceId)
		if zone == "" {
			return fmt.Errorf("could not extract zone from instance ID: %s", instanceId)
		}

		req := &computepb.DeleteInstanceRequest{
			Project:  session.ProjectId,
			Zone:     zone,
			Instance: instanceName,
		}

		op, err := client.Delete(ctx.GetContext(), req)
		if err != nil {
			return fmt.Errorf("failed to delete instance: %w", err)
		}

		err = op.Wait(ctx.GetContext())
		if err != nil {
			return fmt.Errorf("failed to wait for instance deletion: %w", err)
		}

		ctx.GetLogger().Info("successfully deleted stopped instance", "instance", instanceName, "zone", zone)
		return nil

	case "gcp_compute_old_instance":
		return fmt.Errorf("rightsizing requires manual review and cannot be automatically applied")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

// resolveInstanceByID returns (name, zone) for the given numeric instance id.
// Frontend resources store the canonical numeric instance id (matches GCP
// Monitoring API), but the Compute Engine SDK Start/Stop/Reset/Delete calls
// require name + zone. Callers may also pass instance_name/zone via Args to
// skip the lookup.
func resolveInstanceByID(ctx providers.CloudProviderContext, client *compute.InstancesClient, projectId, instanceId string) (name, zone string, err error) {
	filter := fmt.Sprintf("id eq %s", instanceId)
	req := &computepb.AggregatedListInstancesRequest{
		Project: projectId,
		Filter:  &filter,
	}
	it := client.AggregatedList(ctx.GetContext(), req)
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("failed to list instances: %w", err)
		}
		if resp.Value == nil || resp.Value.Instances == nil {
			continue
		}
		for _, inst := range resp.Value.Instances {
			if inst.Id == nil || inst.Name == nil || inst.Zone == nil {
				continue
			}
			if fmt.Sprintf("%d", *inst.Id) != instanceId {
				continue
			}
			// Zone is returned as a full URL: ".../zones/us-central1-a"
			zoneURL := *inst.Zone
			if idx := strings.LastIndex(zoneURL, "/"); idx >= 0 {
				zoneURL = zoneURL[idx+1:]
			}
			return *inst.Name, zoneURL, nil
		}
	}
	return "", "", fmt.Errorf("instance not found: %s", instanceId)
}

func resolveInstanceTarget(ctx providers.CloudProviderContext, client *compute.InstancesClient, projectId string, command providers.ApplyCommandRequest) (name, zone string, err error) {
	if n, ok := command.Args["instance_name"].(string); ok && n != "" {
		if z, ok := command.Args["zone"].(string); ok && z != "" {
			return n, z, nil
		}
	}
	if command.ResourceId == "" {
		return "", "", fmt.Errorf("resource_id required")
	}
	return resolveInstanceByID(ctx, client, projectId, command.ResourceId)
}

func (s *computeEngineService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	var resultMessage string
	var resultErr error

	// Always audit, including early-return / failure paths. Mirrors the
	// pattern in aws_ec2.go / aws_rds.go so the UI's Action History tab
	// reflects every attempted action, not just successes.
	defer func() {
		status := "SUCCESS"
		msg := resultMessage
		if resultErr != nil {
			status = "FAILURE"
			if msg == "" {
				msg = resultErr.Error()
			}
		}
		if auditErr := logResourceActionAudit(ctx, command, account, status, msg); auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
	}()

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		resultErr = fmt.Errorf("failed to get gcloud session: %w", err)
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{Success: false, Message: resultMessage}, resultErr
	}

	client, err := compute.NewInstancesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		resultErr = fmt.Errorf("failed to create compute client: %w", err)
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{Success: false, Message: resultMessage}, resultErr
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close compute client", "error", cerr)
		}
	}()

	instanceName, zone, err := resolveInstanceTarget(ctx, client, session.ProjectId, command)
	if err != nil {
		resultErr = err
		resultMessage = err.Error()
		return providers.ApplyCommandResponse{Success: false, Message: resultMessage}, resultErr
	}

	switch command.Command {
	case "start":
		op, err := client.Start(ctx.GetContext(), &computepb.StartInstanceRequest{
			Project:  session.ProjectId,
			Zone:     zone,
			Instance: instanceName,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to start instance: %w", err)
			resultMessage = resultErr.Error()
		} else if err = op.Wait(ctx.GetContext()); err != nil {
			resultErr = fmt.Errorf("failed to wait for instance start: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully started instance %s in zone %s", instanceName, zone)
		}

	case "stop":
		op, err := client.Stop(ctx.GetContext(), &computepb.StopInstanceRequest{
			Project:  session.ProjectId,
			Zone:     zone,
			Instance: instanceName,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to stop instance: %w", err)
			resultMessage = resultErr.Error()
		} else if err = op.Wait(ctx.GetContext()); err != nil {
			resultErr = fmt.Errorf("failed to wait for instance stop: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully stopped instance %s in zone %s", instanceName, zone)
		}

	case "reboot", "reset":
		op, err := client.Reset(ctx.GetContext(), &computepb.ResetInstanceRequest{
			Project:  session.ProjectId,
			Zone:     zone,
			Instance: instanceName,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to reset instance: %w", err)
			resultMessage = resultErr.Error()
		} else if err = op.Wait(ctx.GetContext()); err != nil {
			resultErr = fmt.Errorf("failed to wait for instance reset: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully reset instance %s in zone %s", instanceName, zone)
		}

	case "delete":
		op, err := client.Delete(ctx.GetContext(), &computepb.DeleteInstanceRequest{
			Project:  session.ProjectId,
			Zone:     zone,
			Instance: instanceName,
		})
		if err != nil {
			resultErr = fmt.Errorf("failed to delete instance: %w", err)
			resultMessage = resultErr.Error()
		} else if err = op.Wait(ctx.GetContext()); err != nil {
			resultErr = fmt.Errorf("failed to wait for instance deletion: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully deleted instance %s in zone %s", instanceName, zone)
		}

	default:
		resultErr = fmt.Errorf("unsupported command: %s", command.Command)
		resultMessage = resultErr.Error()
	}

	if resultErr != nil {
		return providers.ApplyCommandResponse{Success: false, Message: resultMessage}, resultErr
	}
	return providers.ApplyCommandResponse{Success: true, Message: resultMessage}, nil
}

// extractZoneFromResourceId extracts the zone from a GCP resource ID
// Format: projects/{project}/zones/{zone}/instances/{instance}
func extractZoneFromResourceId(resourceId string) string {
	parts := strings.Split(resourceId, "/")
	for i, part := range parts {
		if part == "zones" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func (s *computeEngineService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, resourceId string) string {
	if resourceId == "" {
		return `resource.type="gce_instance"`
	}
	return fmt.Sprintf(`resource.type="gce_instance" resource.labels.instance_id="%s"`, resourceId)
}

// Helper function to convert struct to map for Meta field
func structToMap(data interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	jsonData, err := json.Marshal(data)
	if err != nil {
		return result
	}
	if err := json.Unmarshal(jsonData, &result); err != nil {
		return make(map[string]interface{})
	}
	return result
}

// fetchAndParseMachineType fetches machine type details from GCP API and converts to AWS/Azure compatible format
// Uses a cache to avoid N+1 API calls when multiple instances use the same machine type
func fetchAndParseMachineType(ctx providers.CloudProviderContext, client *compute.MachineTypesClient, projectId, zone, machineTypeURL string, cache map[string]*computepb.MachineType) map[string]interface{} {
	// Extract machine type name from URL
	// URL format: zones/us-central1-a/machineTypes/n1-standard-4
	// or full URL: https://www.googleapis.com/compute/v1/projects/project/zones/zone/machineTypes/n1-standard-4
	machineTypeName := extractMachineTypeName(machineTypeURL)
	if machineTypeName == "" {
		ctx.GetLogger().Warn("failed to extract machine type name from URL", "url", machineTypeURL)
		return nil
	}

	// Create cache key (zone/machineTypeName) since machine types are zone-specific
	cacheKey := fmt.Sprintf("%s/%s", zone, machineTypeName)

	// Check cache first to avoid redundant API calls
	machineType, exists := cache[cacheKey]
	if !exists {
		// Fetch machine type details from GCP API
		req := &computepb.GetMachineTypeRequest{
			Project:     projectId,
			Zone:        zone,
			MachineType: machineTypeName,
		}

		var err error
		machineType, err = client.Get(ctx.GetContext(), req)
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch machine type details", "machineType", machineTypeName, "error", err)
			return nil
		}

		// Store in cache for future use
		cache[cacheKey] = machineType
	}

	return parseMachineTypeToInstanceTypeDetails(machineTypeName, machineType)
}

// extractMachineTypeName extracts the machine type name from a GCP machine type URL
// Examples:
// - "zones/us-central1-a/machineTypes/n1-standard-4" -> "n1-standard-4"
// - "https://www.googleapis.com/compute/v1/projects/project/zones/zone/machineTypes/n1-standard-4" -> "n1-standard-4"
func extractMachineTypeName(machineTypeURL string) string {
	const marker = "/machineTypes/"
	if i := strings.LastIndex(machineTypeURL, marker); i != -1 {
		return machineTypeURL[i+len(marker):]
	}
	return ""
}

// parseMachineTypeToInstanceTypeDetails converts GCP MachineType to AWS/Azure compatible InstanceTypeDetails format
// This ensures UI compatibility without requiring UI changes
func parseMachineTypeToInstanceTypeDetails(machineTypeName string, machineType *computepb.MachineType) map[string]interface{} {
	if machineType == nil {
		return nil
	}

	// Extract vCPUs (GuestCpus)
	vcpuCount := 0
	if machineType.GuestCpus != nil {
		vcpuCount = int(*machineType.GuestCpus)
	}

	// Extract memory (MemoryMb -> convert to MiB)
	// Note: GCP returns MemoryMb which is actually MiB (1024-based), not MB (1000-based)
	memoryMiB := 0
	if machineType.MemoryMb != nil {
		memoryMiB = int(*machineType.MemoryMb)
	}

	// GCP doesn't expose physical cores vs threads per core directly
	// We'll use the same approach as Azure: assume 2 threads per core (hyperthreading) for standard instances
	// Exception: shared-core instances (e2-micro, e2-small, e2-medium, f1-micro, g1-small) have 1 thread per core
	threadsPerCore := 2
	if machineType.IsSharedCpu != nil && *machineType.IsSharedCpu {
		// Shared-core instances don't have hyperthreading
		threadsPerCore = 1
	}

	// Calculate cores from vCPUs and threads per core
	cores := 0
	if vcpuCount > 0 {
		cores = vcpuCount / threadsPerCore
		if cores < 1 {
			cores = 1
			threadsPerCore = vcpuCount
		}
	} else {
		threadsPerCore = 0
	}
	// Build InstanceTypeDetails matching AWS/Azure format exactly
	instanceTypeDetails := map[string]interface{}{
		"VCpuInfo": map[string]interface{}{
			"DefaultVCpus":          vcpuCount,
			"DefaultCores":          cores,
			"DefaultThreadsPerCore": threadsPerCore,
			"ValidCores":            []int{cores},
			"ValidThreadsPerCore":   []int{1, 2},
		},
		"MemoryInfo": map[string]interface{}{
			"SizeInMiB": memoryMiB,
		},
		"InstanceType": machineTypeName,
	}

	// Add GCP-specific fields (bonus data, similar to Azure's approach)
	if machineType.IsSharedCpu != nil {
		instanceTypeDetails["IsSharedCpu"] = *machineType.IsSharedCpu
	}
	if machineType.MaximumPersistentDisks != nil {
		instanceTypeDetails["MaximumPersistentDisks"] = *machineType.MaximumPersistentDisks
	}
	if machineType.MaximumPersistentDisksSizeGb != nil {
		instanceTypeDetails["MaximumPersistentDisksSizeGb"] = *machineType.MaximumPersistentDisksSizeGb
	}

	return instanceTypeDetails
}

// calculateGCPThreshold calculates the appropriate threshold for an alarm based on resource properties
func calculateGCPThreshold(resource providers.Resource, template providers.AlarmTemplate) float64 {
	// Get default threshold from struct
	defaultThreshold := template.ThresholdRules.Default
	if defaultThreshold == 0 {
		return 0.80 // Fallback to 80%
	}

	// Check for machine family specific thresholds
	machineType, ok := resource.Meta["machineType"].(string)
	if !ok || machineType == "" {
		return defaultThreshold
	}

	// Extract machine type name from URL if needed
	// machineType could be a full URL like "https://.../machineTypes/n1-standard-4"
	machineTypeName := extractMachineTypeName(machineType)
	if machineTypeName == "" {
		machineTypeName = machineType
	}

	// Extract machine family from machine type name
	// Example: "n1-standard-4" -> "n1", "e2-medium" -> "e2"
	parts := strings.Split(machineTypeName, "-")
	if len(parts) < 2 {
		return defaultThreshold
	}
	machineFamily := parts[0]

	// Check for family-specific threshold in ByInstanceFamily map
	if template.ThresholdRules.ByInstanceFamily != nil {
		if familyThreshold, ok := template.ThresholdRules.ByInstanceFamily[machineFamily]; ok {
			return familyThreshold
		}
	}

	return defaultThreshold
}
