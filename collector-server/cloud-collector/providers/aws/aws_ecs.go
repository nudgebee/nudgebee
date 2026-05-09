package aws

import (
	"context"
	"errors"
	"fmt"
	"math"
	"nudgebee/collector/cloud/providers"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/samber/lo"
)

// Helper function to map ECS status strings to provider status
func ecsStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	// Common statuses: ACTIVE, PROVISIONING, DEPROVISIONING, INACTIVE, FAILED
	s := strings.ToLower(*status)
	switch s {
	case "active":
		return providers.ResourceStatusActive
	case "provisioning":
		return providers.ResourceStatusUnknown
	case "deprovisioning":
		return providers.ResourceStatusInactive // Or Deleting
	case "inactive", "failed":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

// Helper function to map ECS Task status strings to provider status
func ecsTaskStatusToNbStatus(lastStatus *string) providers.ResourceStatus {
	if lastStatus == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*lastStatus)
	// Based on https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-lifecycle.html
	// LastStatus can be: PROVISIONING, PENDING, ACTIVATING, RUNNING, DEACTIVATING, STOPPING, DEPROVISIONED, STOPPED.
	switch s {
	case "running", "activating":
		return providers.ResourceStatusActive
	case "provisioning", "pending": // These are transient states leading to active
		return providers.ResourceStatusActive
	case "stopped":
		return providers.ResourceStatusInactive
	case "deprovisioned": // Task is permanently gone from active listings, effectively deleted for resource tracking
		return providers.ResourceStatusDeleted
	case "stopping", "deactivating": // Transient states leading to inactive/stopped
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type amazonEcs struct {
	DefaultAwsServiceImpl
}

func (a *amazonEcs) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonEcs) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	var resultMessage string
	var resultErr error

	// Always audit, even on early returns
	defer func() {
		status := "SUCCESS"
		if resultErr != nil {
			status = "FAILURE"
		}

		auditErr := logResourceActionAudit(ctx, command, account, status, resultMessage)
		if auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
	}()

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		resultErr = fmt.Errorf("failed to get AWS config: %w", err)
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	// Override region if specified in command
	if command.Region != "" {
		cfg.Region = command.Region
	}

	client := ecs.NewFromConfig(cfg)

	// Get cluster and service names
	clusterName, _ := command.Args["cluster"].(string)
	if clusterName == "" {
		resultErr = fmt.Errorf("cluster name required")
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	serviceName := command.ResourceId
	if serviceName == "" {
		serviceName, _ = command.Args["service"].(string)
	}

	if serviceName == "" {
		resultErr = fmt.Errorf("service name required")
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	switch command.Command {
	case "redeploy", "force_redeploy":
		// Force new deployment without changing the task definition
		input := &ecs.UpdateServiceInput{
			Cluster:            &clusterName,
			Service:            &serviceName,
			ForceNewDeployment: true,
		}

		_, err := client.UpdateService(ctx.GetContext(), input)
		if err != nil {
			resultErr = fmt.Errorf("failed to force redeploy ECS service: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully initiated force redeployment for ECS service %s", serviceName)
		}

	case "scale":
		// Scale service by updating desired count
		desiredCount, ok := command.Args["desired_count"].(float64)
		if !ok {
			// Try int
			if count, ok := command.Args["desired_count"].(int); ok {
				desiredCount = float64(count)
			} else {
				resultErr = fmt.Errorf("desired_count argument required for scale command")
				resultMessage = resultErr.Error()
				break
			}
		}

		// Validate desired_count bounds
		if desiredCount < 0 {
			resultErr = fmt.Errorf("desired_count must be non-negative, got: %.0f", desiredCount)
			resultMessage = resultErr.Error()
			break
		}

		// Check for fractional values
		if desiredCount != float64(int32(desiredCount)) {
			resultErr = fmt.Errorf("desired_count must be a whole number, got: %.2f", desiredCount)
			resultMessage = resultErr.Error()
			break
		}

		// Reasonable upper limit (ECS services typically don't exceed 10000 tasks)
		if desiredCount > 10000 {
			resultErr = fmt.Errorf("desired_count exceeds reasonable limit (10000), got: %.0f", desiredCount)
			resultMessage = resultErr.Error()
			break
		}

		input := &ecs.UpdateServiceInput{
			Cluster:      &clusterName,
			Service:      &serviceName,
			DesiredCount: lo.ToPtr(int32(desiredCount)),
		}

		_, err := client.UpdateService(ctx.GetContext(), input)
		if err != nil {
			resultErr = fmt.Errorf("failed to scale ECS service: %w", err)
			resultMessage = resultErr.Error()
		} else {
			resultMessage = fmt.Sprintf("Successfully scaled ECS service %s to %d tasks", serviceName, int32(desiredCount))
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

func (a *amazonEcs) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Ensure aws_cloudwatch_metrices_common.go has an entry for AWS/ECS
	// Note: Dimension names vary, common function might need adjustments.
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonEcs) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameECS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := ecs.NewFromConfig(cfg)
	resources := []providers.Resource{}

	clustersPaginator := ecs.NewListClustersPaginator(svc, &ecs.ListClustersInput{})
	var clusterArns []string
	for clustersPaginator.HasMorePages() {
		clustersOutput, err := clustersPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list ecs clusters", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}
		clusterArns = append(clusterArns, clustersOutput.ClusterArns...)
	}

	clusterChunks := lo.Chunk(clusterArns, 100)
	for _, chunk := range clusterChunks {
		describeClustersInput := &ecs.DescribeClustersInput{
			Clusters: chunk,
			Include:  []types.ClusterField{types.ClusterFieldTags, types.ClusterFieldSettings, types.ClusterFieldConfigurations},
		}
		describedClustersOutput, err := svc.DescribeClusters(ctx.GetContext(), describeClustersInput)
		if err != nil {
			ctx.GetLogger().Error("failed to describe ecs clusters", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			continue
		}

		for _, cluster := range describedClustersOutput.Clusters {
			if cluster.ClusterName == nil || cluster.ClusterArn == nil || cluster.Status == nil {
				ctx.GetLogger().Warn("Skipping ECS cluster due to missing essential fields", "cluster", cluster)
				continue
			}

			tags := make(map[string][]string)
			for _, tag := range cluster.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(cluster)
			createdAt := time.Time{}
			resource := providers.Resource{
				Id:          *cluster.ClusterName,
				ServiceName: ServiceNameECS,
				Name:        *cluster.ClusterName,
				Status:      ecsStatusToNbStatus(cluster.Status),
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *cluster.ClusterArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameECS, "cluster"),
			}
			resources = append(resources, resource)

			servicesPaginator := ecs.NewListServicesPaginator(svc, &ecs.ListServicesInput{Cluster: cluster.ClusterArn})
			for servicesPaginator.HasMorePages() {
				servicesOutput, err := servicesPaginator.NextPage(ctx.GetContext())
				if err != nil {
					ctx.GetLogger().Error("failed to list ecs services for cluster", "error", err, "clusterArn", *cluster.ClusterArn, "region", regionName)
					break
				}

				serviceChunks := lo.Chunk(servicesOutput.ServiceArns, 10)
				for _, serviceChunk := range serviceChunks {
					describeServicesInput := &ecs.DescribeServicesInput{
						Cluster:  cluster.ClusterArn,
						Services: serviceChunk,
						Include:  []types.ServiceField{types.ServiceFieldTags},
					}
					describedServicesOutput, err := svc.DescribeServices(ctx.GetContext(), describeServicesInput)
					if err != nil {
						ctx.GetLogger().Error("failed to describe ecs services", "error", err, "clusterArn", *cluster.ClusterArn, "region", regionName)
						continue
					}

					for _, service := range describedServicesOutput.Services {
						if service.ServiceName == nil || service.ServiceArn == nil || service.Status == nil || service.CreatedAt == nil {
							ctx.GetLogger().Warn("Skipping ECS service due to missing essential fields", "service", service)
							continue
						}

						serviceTags := make(map[string][]string)
						for _, tag := range service.Tags {
							if tag.Key != nil && tag.Value != nil {
								serviceTags[*tag.Key] = append(serviceTags[*tag.Key], *tag.Value)
							}
						}

						serviceMeta := structToMap(service)
						serviceMeta["ClusterArn"] = *cluster.ClusterArn
						serviceMeta["ClusterName"] = *cluster.ClusterName

						serviceResource := providers.Resource{
							Id:          *service.ServiceName,
							ServiceName: ServiceNameECS,
							Name:        *service.ServiceName,
							Status:      ecsStatusToNbStatus(service.Status),
							Region:      regionName,
							Tags:        serviceTags,
							Meta:        serviceMeta,
							Arn:         *service.ServiceArn,
							CreatedAt:   *service.CreatedAt,
							Type:        getAwsServiceResourceType(ServiceNameECS, "service"),
						}
						resources = append(resources, serviceResource)

						tasksPaginator := ecs.NewListTasksPaginator(svc, &ecs.ListTasksInput{
							Cluster:     cluster.ClusterArn,
							ServiceName: service.ServiceName,
						})
						for tasksPaginator.HasMorePages() {
							tasksOutput, err := tasksPaginator.NextPage(ctx.GetContext())
							if err != nil {
								ctx.GetLogger().Error("failed to list ecs tasks for service", "error", err, "serviceArn", *service.ServiceArn, "clusterArn", *cluster.ClusterArn)
								break
							}

							taskChunks := lo.Chunk(tasksOutput.TaskArns, 100)
							for _, taskChunk := range taskChunks {
								describeTasksInput := &ecs.DescribeTasksInput{
									Cluster: cluster.ClusterArn,
									Tasks:   taskChunk,
									Include: []types.TaskField{types.TaskFieldTags},
								}
								describedTasksOutput, err := svc.DescribeTasks(ctx.GetContext(), describeTasksInput)
								if err != nil {
									ctx.GetLogger().Error("failed to describe ecs tasks", "error", err, "clusterArn", *cluster.ClusterArn)
									continue
								}

								for _, task := range describedTasksOutput.Tasks {
									if task.TaskArn == nil || task.LastStatus == nil || task.CreatedAt == nil {
										ctx.GetLogger().Warn("Skipping ECS task due to missing essential fields", "task", task)
										continue
									}
									taskArnSplits := strings.Split(*task.TaskArn, "/")
									taskID := taskArnSplits[len(taskArnSplits)-1]
									taskTags := make(map[string][]string)
									for _, tag := range task.Tags {
										if tag.Key != nil && tag.Value != nil {
											taskTags[*tag.Key] = append(taskTags[*tag.Key], *tag.Value)
										}
									}
									taskMeta := structToMap(task)
									taskMeta["ClusterArn"] = *cluster.ClusterArn
									taskMeta["ServiceArn"] = *service.ServiceArn

									taskResource := providers.Resource{
										Id:          taskID,
										ServiceName: ServiceNameECS,
										Name:        taskID,
										Status:      ecsTaskStatusToNbStatus(task.LastStatus),
										Region:      regionName,
										Tags:        taskTags,
										Meta:        taskMeta,
										Arn:         *task.TaskArn,
										CreatedAt:   *task.CreatedAt,
										Type:        getAwsServiceResourceType(ServiceNameECS, "task"),
									}
									resources = append(resources, taskResource)
								}
							}
						}
					}
				}
			}
		}
	}
	return resources, nil
}

// Helper function to get task definition details
func getTaskDefinitionDetails(ctx context.Context, taskDefArn string, svc *ecs.Client) (*types.TaskDefinition, error) {
	input := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &taskDefArn,
		Include:        []types.TaskDefinitionField{types.TaskDefinitionFieldTags},
	}
	result, err := svc.DescribeTaskDefinition(ctx, input)
	if err != nil {
		// ctx.GetLogger().Warn("failed to describe task definition", "arn", taskDefArn, "error", err)
		return nil, err
	}
	return result.TaskDefinition, nil
}

// Regex to check for ':latest' tag or no tag (which implies latest)
var latestTagRegex = regexp.MustCompile(`(:latest|[^:]+)$`)

func (a *amazonEcs) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameECS)
		return recommendations, err
	}

	startDate := time.Now().Add(-time.Hour * 24 * 7) // Metrics period: last 7 days
	endDate := time.Now()
	// Helper to find cluster details from existingResources
	findClusterDetails := func(clusterArn string) map[string]any {
		for _, r := range existingResources {
			if r.Type == getAwsServiceResourceType(ServiceNameECS, "cluster") && r.Arn == clusterArn {
				return r.Meta
			}
		}
		return nil
	}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// --- Recommendations for Clusters ---
		if resource.Type == getAwsServiceResourceType(ServiceNameECS, "cluster") {
			// Check 1: Container Insights Enabled
			insightsEnabled := false
			if settings, ok := meta["Settings"].([]any); ok {
				for _, settingAny := range settings {
					if setting, ok := settingAny.(map[string]any); ok {
						if name, nameOK := setting["Name"].(string); nameOK && name == "containerInsights" {
							if value, valueOK := setting["Value"].(string); valueOK && value == "enabled" {
								insightsEnabled = true
								break
							}
						}
					}
				}
			}
			if !insightsEnabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration, // Or Monitoring
					RuleName:            "aws_ecs_container_insights_disabled",         // Renamed for clarity
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Container Insights is not enabled for the cluster."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 2: Avoid using the "default" cluster
			if resource.Name == "default" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ecs_avoid_default_cluster",
					Severity:            providers.RecommendationSeverityLow, // Can be Medium depending on usage
					Savings:             0,
					Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Using the 'default' cluster is not recommended for production workloads. Create custom clusters for better control and isolation."},
					Action:              providers.RecommendationActionModify, // Suggest migrating workloads
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 3: Fargate FIPS compliance (if applicable)
			fargateFipsEnabled := false
			if settings, ok := meta["Settings"].([]any); ok {
				for _, settingAny := range settings {
					if setting, ok := settingAny.(map[string]any); ok {
						if name, nameOK := setting["Name"].(string); nameOK && name == "fargateFIPSMode" {
							if value, valueOK := setting["Value"].(string); valueOK && value == "enabled" {
								fargateFipsEnabled = true
								break
							}
						}
					}
				}
			}
			// This recommendation is more relevant if FIPS is a requirement.
			// For now, let's assume it's a general good practice to be aware of.
			// If not enabled, it's not necessarily "wrong" unless FIPS is required.
			// We can make this a low severity informational recommendation or skip if not explicitly required.
			// Let's add it as low for awareness.
			if !fargateFipsEnabled { // Example: Recommend enabling if FIPS is a general concern
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_ecs_cluster_fargate_fips_disabled",
					Severity:            providers.RecommendationSeverityLow, // Severity depends on compliance needs
					Savings:             0,
					Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Fargate FIPS compliance mode is not enabled for the cluster. Enable if FIPS 140-2 is required."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 4: Missing Tags
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags", // Use generic tag rule name
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"cluster_name": resource.Name, "cluster_arn": resource.Arn, "reason": "Cluster is missing tags."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// --- Recommendations for Services ---
		if resource.Type == getAwsServiceResourceType(ServiceNameECS, "service") {
			launchType := ""
			platformVersion := ""
			desiredCount := 0.0        // float64 due to structToMap
			minHealthyPercent := 100.0 // Default for ECS if not specified in DeploymentConfiguration
			maxPercent := 200.0        // Default for ECS if not specified in DeploymentConfiguration
			healthCheckGracePeriod := 0.0
			enableExecuteCommand := false
			clusterArn := ""
			taskDefinitionArn := ""
			taskDefinitionCPU := 0.0    // In vCPU units (e.g., 0.25, 0.5, 1, 2...)
			taskDefinitionMemory := 0.0 // In GB (e.g., 0.5, 1, 2, 4...)
			serviceConnectEnabled := false

			if lt, ok := meta["LaunchType"].(string); ok {
				launchType = lt
			}
			if pv, ok := meta["PlatformVersion"].(string); ok {
				platformVersion = pv
			}
			if dc, ok := meta["DesiredCount"].(float64); ok { // Assuming DesiredCount is float64 after structToMap
				desiredCount = dc
			}
			if depConfig, ok := meta["DeploymentConfiguration"].(map[string]any); ok {
				if mhp, ok := depConfig["MinimumHealthyPercent"].(float64); ok {
					minHealthyPercent = mhp
				}
				if mp, ok := depConfig["MaximumPercent"].(float64); ok {
					maxPercent = mp
				}
			}
			if hcgp, ok := meta["HealthCheckGracePeriodSeconds"].(float64); ok {
				healthCheckGracePeriod = hcgp
			}
			if eec, ok := meta["EnableExecuteCommand"].(bool); ok {
				enableExecuteCommand = eec
			}
			if ca, ok := meta["ClusterArn"].(string); ok {
				clusterArn = ca
			}
			if tdArn, ok := meta["TaskDefinition"].(string); ok {
				taskDefinitionArn = tdArn
			}
			if scConfig, ok := meta["ServiceConnectConfiguration"].(map[string]any); ok {
				if enabled, ok := scConfig["Enabled"].(bool); ok {
					serviceConnectEnabled = enabled
				}
			}

			// Check 1: Fargate Latest Platform Version (if Fargate)
			if launchType == string(types.LaunchTypeFargate) && platformVersion != "LATEST" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryInfraUpgrade,
					RuleName:            "aws_ecs_fargate_latest_platform_version", // Renamed for clarity
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "current_version": platformVersion, "reason": "Fargate service is not configured to use the LATEST platform version."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 2: Service Auto Scaling (Placeholder - Requires Application Auto Scaling API)
			// This is a significant recommendation and would require new API calls.
			// For now, we'll add a placeholder comment in the code and mention it here.
			// RuleName: "aws_ecs_service_autoscaling_disabled"
			// Severity: Medium

			// Check 3: Deployment Configuration - Minimum Healthy Percent
			// Recommend if less than 50% for services with more than 1 desired task.
			// For desiredCount=1, 0% might be acceptable for some rolling update strategies, but 50%/100% is safer.
			if desiredCount > 1 && minHealthyPercent < 50 {
				recommendations = append(recommendations, providers.Recommendation{ // TODO: This should be Availability
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ecs_service_min_healthy_percent_low",
					Severity:            providers.RecommendationSeverityMedium, // TODO: This should be Medium
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "current_min_healthy_percent": minHealthyPercent, "desired_count": desiredCount, "reason": "Minimum healthy percent is below 50% which might impact availability during deployments."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			} else if desiredCount == 1 && minHealthyPercent < 100 { // For single task services, 0% means downtime during update.
				recommendations = append(recommendations, providers.Recommendation{ // TODO: This should be Availability
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ecs_service_min_healthy_percent_too_low_for_single_task",
					Severity:            providers.RecommendationSeverityMedium, // TODO: This should be Medium
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "current_min_healthy_percent": minHealthyPercent, "desired_count": desiredCount, "reason": "For a single-task service, minimum healthy percent less than 100% can cause downtime during updates. Consider 100% or blue/green deployments."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check: Deployment Configuration - Maximum Percent
			// Common default is 200%. Recommend if significantly different, e.g., not 200%.
			if maxPercent != 200 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ecs_service_max_percent_non_standard",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "current_max_percent": maxPercent, "reason": fmt.Sprintf("Maximum percent is %v%%, standard is 200%%. Review if this is intended.", maxPercent)},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 4: Health Check Grace Period
			// Recommend if it's 0 and the service has load balancers (common scenario).
			hasLoadBalancers := false
			if lbs, ok := meta["LoadBalancers"].([]any); ok && len(lbs) > 0 {
				hasLoadBalancers = true
			}
			if hasLoadBalancers && healthCheckGracePeriod == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ecs_service_health_check_grace_period_zero",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "reason": "Service uses a load balancer but has a health check grace period of 0 seconds. This can lead to premature task termination."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 5: ECS Exec and Logging
			if enableExecuteCommand {
				execLoggingConfigured := false
				execLoggingState := "UNKNOWN" // Default if cluster info not found or config missing

				if clusterArn != "" {
					clusterMeta := findClusterDetails(clusterArn)
					if clusterMeta != nil {
						if config, ok := clusterMeta["Configuration"].(map[string]any); ok {
							if execConfig, ok := config["ExecuteCommandConfiguration"].(map[string]any); ok {
								if logging, ok := execConfig["Logging"].(string); ok {
									execLoggingState = logging
									if logging != string(types.ExecuteCommandLoggingNone) {
										execLoggingConfigured = true
									}
								}
							} else {
								execLoggingState = string(types.ExecuteCommandLoggingNone)
							}
						} else {
							execLoggingState = string(types.ExecuteCommandLoggingNone)
						}
					}
				}

				if !execLoggingConfigured {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "aws_ecs_service_exec_logging_disabled",
						Severity:            providers.RecommendationSeverityMedium,
						Savings:             0,
						Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "cluster_arn": clusterArn, "exec_logging_state": execLoggingState, "reason": "ECS Exec is enabled for the service, but audit logging for Exec sessions is not configured (or set to NONE) on the cluster."},
						Action:              providers.RecommendationActionModify, // Modify cluster execute command configuration
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id, // Could also be cluster ID/ARN for action target
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}

			// Check: Service Connect Usage
			if !serviceConnectEnabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_ecs_service_connect_disabled",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "reason": "Service Connect is not enabled. Consider enabling for simplified service discovery and traffic management."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// --- Task Definition Checks ---
			if taskDefinitionArn != "" {
				cfg.Region = resource.Region
				ecsSvc := ecs.NewFromConfig(cfg)
				taskDefDetails, tdErr := getTaskDefinitionDetails(ctx.GetContext(), taskDefinitionArn, ecsSvc)
				if tdErr == nil && taskDefDetails != nil {
					// Fargate: CPU/Memory defined in Task Definition
					if launchType == string(types.LaunchTypeFargate) {
						// Extract CPU and Memory from Task Definition
						if taskDefDetails.Cpu != nil && *taskDefDetails.Cpu != "" {
							// CPU is in 1024 units, convert to vCPU
							if cpuInt, parseErr := strconv.ParseFloat(*taskDefDetails.Cpu, 64); parseErr == nil {
								taskDefinitionCPU = cpuInt / 1024.0
							}
						}
						// Memory is in MiB, convert to GB
						if taskDefDetails.Memory != nil && *taskDefDetails.Memory != "" {
							if memInt, parseErr := strconv.ParseFloat(*taskDefDetails.Memory, 64); parseErr == nil {
								taskDefinitionMemory = memInt / 1024.0
							}
						}

						if taskDefDetails.Cpu == nil || *taskDefDetails.Cpu == "" || *taskDefDetails.Cpu == "0" {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_ecs_fargate_task_definition_cpu_undefined",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "reason": "Fargate task definition does not specify CPU units."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}
						if taskDefDetails.Memory == nil || *taskDefDetails.Memory == "" || *taskDefDetails.Memory == "0" {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_ecs_fargate_task_definition_memory_undefined",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "reason": "Fargate task definition does not specify memory."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}
					}

					// Check: Task Role ARN defined
					if taskDefDetails.TaskRoleArn == nil || *taskDefDetails.TaskRoleArn == "" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategorySecurity,
							RuleName:            "aws_ecs_task_definition_missing_task_role",
							Severity:            providers.RecommendationSeverityMedium,
							Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "reason": "Task definition does not have an IAM task role defined. Assigning a task role with least privilege is recommended."},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}

					// --- Container Definition Checks ---
					for _, containerDef := range taskDefDetails.ContainerDefinitions {
						containerName := "unknown"
						if containerDef.Name != nil {
							containerName = *containerDef.Name
						}

						// Image Tag Usage (:latest or no tag)
						if containerDef.Image != nil && latestTagRegex.MatchString(*containerDef.Image) {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_ecs_task_definition_image_latest_tag",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "image": *containerDef.Image, "reason": "Container image uses 'latest' tag or no tag, which is not recommended for production."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Secrets Management (using environment variables instead of 'secrets' field)
						if (len(containerDef.Secrets) == 0) && len(containerDef.Environment) > 0 {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategorySecurity,
								RuleName:            "aws_ecs_task_definition_secrets_not_used",
								Severity:            providers.RecommendationSeverityLow,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container defines environment variables. Review if any are sensitive and should be managed via the 'secrets' configuration using AWS Secrets Manager or Parameter Store."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Privileged Containers
						if containerDef.Privileged != nil && *containerDef.Privileged {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategorySecurity,
								RuleName:            "aws_ecs_task_definition_privileged_container",
								Severity:            providers.RecommendationSeverityHigh,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container is running in privileged mode."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Read-Only Root Filesystem
						if containerDef.ReadonlyRootFilesystem == nil || !*containerDef.ReadonlyRootFilesystem {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategorySecurity,
								RuleName:            "aws_ecs_task_definition_readonly_root_fs_disabled",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container does not have a read-only root filesystem."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Check: Container Logging Configuration
						if containerDef.LogConfiguration == nil {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_ecs_task_definition_logging_not_configured",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container definition does not have log configuration. Configure logging (e.g., to CloudWatch Logs) for monitoring and debugging."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Check: Container Health Check
						if containerDef.HealthCheck == nil {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration, // Or Availability
								RuleName:            "aws_ecs_task_definition_health_check_not_configured",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container definition does not have a health check configured. Health checks help ECS manage task health and load balancer target health."},
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

			// --- Rightsizing Checks (Fargate) ---
			// Only apply rightsizing checks to Fargate services with a desired count > 0
			if launchType == string(types.LaunchTypeFargate) && desiredCount > 0 && taskDefinitionCPU > 0 && taskDefinitionMemory > 0 {
				// Fetch CPU and Memory utilization metrics for the service
				metrics, metricsErr := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
					ResourceIds:  []string{resource.Name}, // Use ServiceName as ResourceId for ECS service metrics
					ServiceName:  ServiceNameECS,
					ResourceType: "service", // Specify resource type for correct dimensions
					Region:       resource.Region,
					StartDate:    &startDate,
					EndDate:      &endDate,
					MetricNames:  []string{"CPUUtilization", "MemoryUtilization"},
					Step:         3600 * time.Second,  // Hourly average/max
					Statistics:   []string{"Maximum"}, // Use Maximum to see peak usage
				})

				if metricsErr != nil {
					ctx.GetLogger().Warn("failed to get metrics for ECS service", "error", metricsErr, "serviceArn", resource.Arn)
					// Continue processing other recommendations even if metrics fail
				} else if len(metrics.Items) > 0 {
					cpuMetrics := lo.Filter(metrics.Items, func(item providers.MetricItem, _ int) bool { return item.Name == "CPUUtilization" })
					memoryMetrics := lo.Filter(metrics.Items, func(item providers.MetricItem, _ int) bool { return item.Name == "MemoryUtilization" })

					maxCPUUtil := 0.0
					if len(cpuMetrics) > 0 && len(cpuMetrics[0].Values) > 0 {
						maxCPUUtil = lo.Max(cpuMetrics[0].Values)
					}

					maxMemoryUtil := 0.0
					if len(memoryMetrics) > 0 && len(memoryMetrics[0].Values) > 0 {
						maxMemoryUtil = lo.Max(memoryMetrics[0].Values)
					}

					// Rightsizing thresholds (example heuristics)
					underutilizedCPUThreshold := 20.0    // Max CPU < 20%
					underutilizedMemoryThreshold := 50.0 // Max Memory < 50%
					overutilizedCPUThreshold := 80.0     // Max CPU > 80%
					overutilizedMemoryThreshold := 90.0  // Max Memory > 90%

					// Check for Underutilization
					// Consider underutilized if BOTH CPU and Memory are below thresholds
					if maxCPUUtil < underutilizedCPUThreshold && maxMemoryUtil < underutilizedMemoryThreshold {
						// Recommend reducing CPU and Memory (e.g., by 50%)
						recommendedCPU := math.Max(0.0625, taskDefinitionCPU*0.5)       // Smallest Fargate CPU is 0.25, but allow smaller steps? No, stick to Fargate increments.
						recommendedMemory := math.Max(0.0625, taskDefinitionMemory*0.5) // Smallest Fargate Memory is 0.5GB for 0.25vCPU

						// TODO: Need to find valid Fargate CPU/Memory combinations and estimate cost savings.
						// This requires Fargate pricing data lookup, similar to EC2/RDS, but for Fargate.
						// Placeholder savings calculation: Assume 50% reduction in cost.
						estimatedCurrentCostPerHourPerTask := 0.0                                                     // Placeholder
						estimatedSavingsPerMonth := estimatedCurrentCostPerHourPerTask * 24 * 30 * desiredCount * 0.5 // Placeholder

						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "aws_ecs_fargate_service_underutilized",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      estimatedSavingsPerMonth, // Placeholder
							Data: map[string]any{
								"service_name":          resource.Name,
								"service_arn":           resource.Arn,
								"current_cpu_vcpu":      taskDefinitionCPU,
								"current_memory_gb":     taskDefinitionMemory,
								"max_cpu_util":          maxCPUUtil,
								"max_memory_util":       maxMemoryUtil,
								"recommended_cpu_vcpu":  recommendedCPU,    // Placeholder values
								"recommended_memory_gb": recommendedMemory, // Placeholder values
								"reason":                fmt.Sprintf("Service appears underutilized (Max CPU: %.2f%%, Max Memory: %.2f%%). Consider reducing task CPU/Memory.", maxCPUUtil, maxMemoryUtil),
							},
							Action:              providers.RecommendationActionModify, // Modify Task Definition
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}

					// Check for Overutilization
					// Consider overutilized if EITHER CPU or Memory is above thresholds
					if maxCPUUtil > overutilizedCPUThreshold || maxMemoryUtil > overutilizedMemoryThreshold {
						// Recommend increasing CPU or Memory
						// TODO: Need to find valid Fargate CPU/Memory combinations and estimate cost increase.
						// Placeholder savings calculation: Assume 50% increase in cost (negative savings).
						estimatedCurrentCostPerHourPerTask := 0.0                                                          // Placeholder
						estimatedCostIncreasePerMonth := estimatedCurrentCostPerHourPerTask * 24 * 30 * desiredCount * 0.5 // Placeholder

						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "aws_ecs_fargate_service_overutilized",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      -estimatedCostIncreasePerMonth, // Negative savings indicates cost increase
							Data: map[string]any{
								"service_name":      resource.Name,
								"service_arn":       resource.Arn,
								"current_cpu_vcpu":  taskDefinitionCPU,
								"current_memory_gb": taskDefinitionMemory,
								"max_cpu_util":      maxCPUUtil,
								"max_memory_util":   maxMemoryUtil,
								"reason":            fmt.Sprintf("Service appears overutilized (Max CPU: %.2f%%, Max Memory: %.2f%%). Consider increasing task CPU/Memory.", maxCPUUtil, maxMemoryUtil),
							},
							Action:              providers.RecommendationActionModify, // Modify Task Definition
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}

			// Check 6: Missing Tags
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags", // Use generic tag rule name
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "reason": "Service is missing tags."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Placeholder/Reminder for Service Auto Scaling
			// Actual check requires Application Auto Scaling API calls.
			// For now, this is a general recommendation to review.
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing, // Or Availability
				RuleName:            "aws_ecs_service_review_autoscaling",
				Severity:            providers.RecommendationSeverityLow, // Informational, as we can't check it directly here
				Savings:             0,                                   // Potential savings if implemented correctly
				Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "reason": "Review if service auto scaling (task count) is configured appropriately for the workload via Application Auto Scaling."},
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

func (a *amazonEcs) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	cfg.Region = region
	ecsSvc := ecs.NewFromConfig(cfg)
	var cluster, service, taskDef string
	if strings.HasPrefix(resourceId, "arn:") {
		cluster, service = getClusterAndServiceNameFromArn(resourceId)
	} else if strings.Contains(resourceId, "/") {
		parts := strings.Split(resourceId, "/")
		if len(parts) == 2 {
			cluster = parts[0]
			service = parts[1]
		} else if len(parts) == 3 {
			cluster = parts[0]
			taskDef = parts[1]
		}
	} else {
		return "", nil
	}

	if cluster == "" && service == "" && taskDef == "" {
		return "", nil
	}
	var describeTdOutput *ecs.DescribeTaskDefinitionOutput
	if cluster != "" && service != "" {
		describeServiceOutput, err := ecsSvc.DescribeServices(ctx.GetContext(), &ecs.DescribeServicesInput{Cluster: &cluster, Services: []string{service}})
		if err != nil || len(describeServiceOutput.Services) == 0 {
			return "", nil
		}

		taskDefArn := describeServiceOutput.Services[0].TaskDefinition
		describeTdOutput1, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{TaskDefinition: taskDefArn})
		if err != nil || describeTdOutput1.TaskDefinition == nil {
			return "", nil
		}
		describeTdOutput = describeTdOutput1
	} else if cluster != "" && taskDef != "" {
		describeTdOutput1, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{TaskDefinition: &taskDef})
		if err != nil || describeTdOutput1.TaskDefinition == nil {
			return "", nil
		}
		describeTdOutput = describeTdOutput1
	}
	if describeTdOutput == nil || describeTdOutput.TaskDefinition == nil || describeTdOutput.TaskDefinition.ContainerDefinitions == nil {
		return "", nil
	}

	for _, cd := range describeTdOutput.TaskDefinition.ContainerDefinitions {
		if cd.LogConfiguration != nil && cd.LogConfiguration.LogDriver == "awslogs" {
			if logGroup, ok := cd.LogConfiguration.Options["awslogs-group"]; ok {
				return logGroup, nil
			}
		}
	}

	return "", nil
}

func (a *amazonEcs) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	cfg.Region = region
	ecsSvc := ecs.NewFromConfig(cfg)
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "ecs",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	app.Id.Kind = "ecs"
	if !strings.HasPrefix(resourceId, "arn:") {
		if strings.Contains(resourceId, "/") {
			resourceIdSplits := strings.Split(resourceId, "/")
			if len(resourceIdSplits) == 3 {
				resourceId = resourceIdSplits[1]
			}
		}
		as := awsProvider{}
		resources, err := as.ListResources(ctx, account, providers.ListResourceRequest{
			ServiceName: "amazonecs",
			Regions:     []string{region},
			ResourceIds: []string{resourceId},
		})
		if err != nil {
			ctx.GetLogger().Error("aws: failed to identify resources", "error", err)
			return app, err
		}
		if len(resources.Items) == 0 {
			return app, err
		}
		resourceId = resources.Items[0].Arn
	}

	if strings.Contains(resourceId, ":service/") {
		var cluster, service string
		if strings.HasPrefix(resourceId, "arn:") {
			cluster, service = getClusterAndServiceNameFromArn(resourceId)
		} else if strings.Contains(resourceId, "/") {
			parts := strings.Split(resourceId, "/")
			cluster = parts[0]
			service = parts[1]
		} else {
			cluster = "default"
			service = resourceId
		}
		if cluster == "" || service == "" {
			return app, nil
		}
		describeServiceOutput, err := ecsSvc.DescribeServices(ctx.GetContext(), &ecs.DescribeServicesInput{Cluster: &cluster, Services: []string{service}})
		if err != nil {
			return app, err
		}
		if len(describeServiceOutput.Services) > 0 {
			s := describeServiceOutput.Services[0]
			app.Id.Name = *s.ServiceArn
			app.Status = *s.Status
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *s.TaskDefinition, Kind: "ecs", Namespace: region}}.ToDownstreamLink())
			for _, lb := range s.LoadBalancers {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *lb.TargetGroupArn, Kind: "elbv2", Namespace: region}}.ToDownstreamLink())
			}
			if s.NetworkConfiguration != nil && s.NetworkConfiguration.AwsvpcConfiguration != nil {
				for _, subnetId := range s.NetworkConfiguration.AwsvpcConfiguration.Subnets {
					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: subnetId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
				}
				for _, sgId := range s.NetworkConfiguration.AwsvpcConfiguration.SecurityGroups {
					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: sgId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
				}
			}
			describeTdOutput, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{TaskDefinition: s.TaskDefinition})
			if err == nil && describeTdOutput.TaskDefinition != nil {
				for _, cd := range describeTdOutput.TaskDefinition.ContainerDefinitions {
					if cd.LogConfiguration != nil && cd.LogConfiguration.LogDriver == "awslogs" {
						if logGroup, ok := cd.LogConfiguration.Options["awslogs-group"]; ok {
							app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: logGroup, Kind: "cloudwatchlogs", Namespace: region}}.ToDownstreamLink())
						}
					}
				}
			}
		}
	} else if strings.Contains(resourceId, ":task-definition/") {
		describeTdOutput, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{TaskDefinition: &resourceId})
		if err != nil {
			return app, err
		}
		if td := describeTdOutput.TaskDefinition; td != nil {
			app.Status = string(td.Status)
			if td.TaskRoleArn != nil {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *td.TaskRoleArn, Kind: "iam", Namespace: ""}}.ToDownstreamLink())
			}
			if td.ExecutionRoleArn != nil {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *td.ExecutionRoleArn, Kind: "iam", Namespace: ""}}.ToDownstreamLink())
			}
			for _, cd := range td.ContainerDefinitions {
				if cd.Image != nil {
					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *cd.Image, Kind: "ecr", Namespace: region}}.ToDownstreamLink())
				}
			}
		}
	} else {
		describeClustersOutput, err := ecsSvc.DescribeClusters(ctx.GetContext(), &ecs.DescribeClustersInput{
			Clusters: []string{resourceId},
			Include:  []types.ClusterField{types.ClusterFieldSettings, types.ClusterFieldTags},
		})
		if err != nil || len(describeClustersOutput.Clusters) == 0 {
			ctx.GetLogger().Error("failed to describe ecs cluster", "error", err, "id", resourceId)
			return app, err
		}

		cluster := describeClustersOutput.Clusters[0]
		app.Id.Name = *cluster.ClusterArn
		app.Status = *cluster.Status

		servicesPaginator := ecs.NewListServicesPaginator(ecsSvc, &ecs.ListServicesInput{Cluster: cluster.ClusterArn})
		for servicesPaginator.HasMorePages() {
			page, err := servicesPaginator.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("failed to list services for cluster", "error", err, "cluster", app.Id.Name)
				break
			}
			for _, serviceArn := range page.ServiceArns {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: serviceArn, Kind: "ecs", Namespace: region}}.ToDownstreamLink())
			}
		}

		tasksPaginator := ecs.NewListTasksPaginator(ecsSvc, &ecs.ListTasksInput{Cluster: cluster.ClusterArn})
		for tasksPaginator.HasMorePages() {
			page, err := tasksPaginator.NextPage(ctx.GetContext())
			if err != nil {
				ctx.GetLogger().Warn("failed to list tasks for cluster", "error", err, "cluster", app.Id.Name)
				break
			}
			for _, taskArn := range page.TaskArns {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: taskArn, Kind: "ecs", Namespace: region}}.ToDownstreamLink())
			}
		}

		for _, cpName := range cluster.CapacityProviders {
			describeCpOutput, err := ecsSvc.DescribeCapacityProviders(ctx.GetContext(), &ecs.DescribeCapacityProvidersInput{CapacityProviders: []string{cpName}})
			if err == nil && len(describeCpOutput.CapacityProviders) > 0 {
				cp := describeCpOutput.CapacityProviders[0]
				if cp.AutoScalingGroupProvider != nil && cp.AutoScalingGroupProvider.AutoScalingGroupArn != nil {
					app.Upstreams = append(app.Upstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *cp.AutoScalingGroupProvider.AutoScalingGroupArn, Kind: "autoscaling", Namespace: region}}.ToUpstreamLink())
				}
			}
		}
	}

	return app, nil
}
