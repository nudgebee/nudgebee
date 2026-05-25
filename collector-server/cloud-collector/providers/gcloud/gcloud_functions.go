package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strconv"
	"strings"

	functions "cloud.google.com/go/functions/apiv2"
	"cloud.google.com/go/functions/apiv2/functionspb"
	"google.golang.org/api/iterator"
)

// parseMemoryToMB parses memory strings like "512Mi", "1Gi", "2048Mi" and returns the value in MB
func parseMemoryToMB(memStr string) (float64, error) {
	memStr = strings.TrimSpace(memStr)
	if memStr == "" {
		return 0, fmt.Errorf("empty memory string")
	}

	// Handle different suffixes
	if strings.HasSuffix(memStr, "Gi") {
		val, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Gi"), 64)
		if err != nil {
			return 0, err
		}
		return val * 1024, nil // Convert GB to MB
	} else if strings.HasSuffix(memStr, "Mi") {
		val, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Mi"), 64)
		if err != nil {
			return 0, err
		}
		return val, nil
	} else if strings.HasSuffix(memStr, "M") {
		val, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "M"), 64)
		if err != nil {
			return 0, err
		}
		return val, nil
	}

	// Try to parse as plain number (assume MB)
	val, err := strconv.ParseFloat(memStr, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to parse memory string: %s", memStr)
	}
	return val, nil
}

const ServiceNameFunctions = "Cloud Functions"

type cloudFunctionsService struct{}

func (s *cloudFunctionsService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for Cloud Functions
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *cloudFunctionsService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := functions.NewFunctionClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud Functions client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Cloud Functions client", "error", cerr)
		}
	}()

	var resources []providers.Resource

	// List all Cloud Functions (Gen 2) in the project
	req := &functionspb.ListFunctionsRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", session.ProjectId),
	}

	it := client.ListFunctions(ctx.GetContext(), req)
	for {
		function, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping Cloud Functions — API disabled or permission denied", "error", err)
				break
			}
			ctx.GetLogger().Error("failed to list Cloud Functions", "error", err)
			break
		}

		resource := functionToResource(function, session.ProjectId)
		resources = append(resources, resource)
	}

	ctx.GetLogger().Info("retrieved Cloud Functions", "count", len(resources), "projectId", session.ProjectId)
	return resources, nil
}

func functionToResource(function *functionspb.Function, projectId string) providers.Resource {
	// Extract location from function name (format: projects/{project}/locations/{location}/functions/{function})
	parts := strings.Split(function.Name, "/")
	location := "global"
	functionName := function.Name
	if len(parts) >= 6 {
		location = parts[3]
		functionName = parts[5]
	}

	// Extract runtime and entry point
	runtime := "unknown"
	if function.BuildConfig != nil && function.BuildConfig.Runtime != "" {
		runtime = function.BuildConfig.Runtime
	}

	entryPoint := ""
	if function.BuildConfig != nil && function.BuildConfig.EntryPoint != "" {
		entryPoint = function.BuildConfig.EntryPoint
	}

	// Extract trigger type
	triggerType := "unknown"
	if function.ServiceConfig != nil && function.ServiceConfig.Uri != "" {
		triggerType = "https"
	}

	// Extract memory and CPU
	memory := ""
	cpu := ""
	if function.ServiceConfig != nil {
		memory = function.ServiceConfig.AvailableMemory
		cpu = function.ServiceConfig.AvailableCpu
	}

	// Get state
	state := function.State.String()
	status := providers.ResourceStatusUnknown
	switch state {
	case "ACTIVE":
		status = providers.ResourceStatusActive
	case "FAILED", "DELETING":
		status = providers.ResourceStatusInactive
	}

	// Use function name only as resource ID (matches GCP Monitoring function_name label)
	// The full path is like: projects/my-project/locations/us-central1/functions/my-function
	resourceId := functionName
	selfLink := function.Name

	// Tags/Labels
	tags := make(map[string][]string)
	for key, value := range function.Labels {
		tags[key] = []string{value}
	}

	// Meta information
	meta := map[string]interface{}{
		"runtime":      runtime,
		"entry_point":  entryPoint,
		"trigger_type": triggerType,
		"memory":       memory,
		"cpu":          cpu,
		"state":        state,
		"url":          function.ServiceConfig.GetUri(),
		"environment":  function.Environment.String(),
		"selfLink":     selfLink,
	}

	return providers.Resource{
		Id:          resourceId, // Function name only (matches GCP Monitoring function_name)
		Name:        functionName,
		Type:        "cloudfunctions.googleapis.com/Function",
		Arn:         selfLink, // Full path for ARN
		ServiceName: ServiceNameFunctions,
		Status:      status,
		Region:      location,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   function.CreateTime.AsTime(),
	}
}

func (s *cloudFunctionsService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameFunctions {
			continue
		}

		// Recommendation 1: Check if function has no labels
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "gcp_function_no_labels",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"function_id": resource.Id},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check if function is not in ACTIVE state
		if resource.Status != providers.ResourceStatusActive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "gcp_function_not_active",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"function_id": resource.Id, "state": resource.Status},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 3: Check for excessive memory allocation
		if memStr, ok := resource.Meta["memory"].(string); ok && memStr != "" {
			// Parse memory (e.g., "512Mi", "1Gi") and check if > 1024 MB (1 GB)
			memMB, err := parseMemoryToMB(memStr)
			if err == nil && memMB > 1024 {
				// Functions with > 1GB memory may be over-provisioned
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "gcp_function_high_memory",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             5.0,
					Action:              providers.RecommendationActionModify,
					Data:                map[string]any{"function_id": resource.Id, "current_memory": memStr, "current_memory_mb": memMB},
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 4: Check for public access (HTTPS trigger)
		if triggerType, ok := resource.Meta["trigger_type"].(string); ok && triggerType == "https" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "gcp_function_public_access",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Action:              providers.RecommendationActionModify,
				Data:                map[string]any{"function_id": resource.Id, "trigger_type": triggerType},
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (s *cloudFunctionsService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := functions.NewFunctionClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create Cloud Functions client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Cloud Functions client", "error", cerr)
		}
	}()

	switch recommendation.RuleName {
	case "gcp_function_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or gcloud CLI")

	case "gcp_function_high_memory":
		return fmt.Errorf("automatic memory adjustment not yet implemented - please adjust memory allocation manually via GCP console")

	case "gcp_function_public_access":
		return fmt.Errorf("security configuration changes require manual review - please configure IAM policies via GCP console")

	case "gcp_function_not_active":
		return fmt.Errorf("function state issues require manual investigation - check deployment logs in GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

func (s *cloudFunctionsService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := functions.NewFunctionClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create Cloud Functions client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close Cloud Functions client", "error", cerr)
		}
	}()

	switch command.Command {
	case "delete":
		functionName, ok := command.Args["function_name"].(string)
		if !ok || functionName == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("function_name arg required")
		}

		// Check if location is provided, otherwise use default
		location, ok := command.Args["location"].(string)
		if !ok || location == "" {
			location = "us-central1" // default location
		}

		fullName := fmt.Sprintf("projects/%s/locations/%s/functions/%s", session.ProjectId, location, functionName)

		req := &functionspb.DeleteFunctionRequest{
			Name: fullName,
		}

		op, err := client.DeleteFunction(ctx.GetContext(), req)
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to initiate function deletion: %w", err)
		}

		// Wait for operation to complete
		err = op.Wait(ctx.GetContext())
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("function deletion failed: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted function %s", functionName),
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *cloudFunctionsService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, resourceId string) string {
	if resourceId == "" {
		return `resource.type="cloud_function"`
	}
	return fmt.Sprintf(`resource.type="cloud_function" resource.labels.function_name="%s"`, resourceId)
}
