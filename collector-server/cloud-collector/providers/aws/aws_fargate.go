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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/samber/lo"
)

// Helper function to map Fargate task status strings to provider status
func fargateTaskStatusToNbStatus(lastStatus *string) providers.ResourceStatus {
	if lastStatus == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*lastStatus)
	switch s {
	case "running", "activating":
		return providers.ResourceStatusActive
	case "provisioning", "pending":
		return providers.ResourceStatusActive
	case "stopped":
		return providers.ResourceStatusInactive
	case "deprovisioned":
		return providers.ResourceStatusDeleted
	case "stopping", "deactivating":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

// Helper function to map Fargate service status strings to provider status
func fargateServiceStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*status)
	switch s {
	case "active":
		return providers.ResourceStatusActive
	case "provisioning":
		return providers.ResourceStatusUnknown
	case "deprovisioning":
		return providers.ResourceStatusInactive
	case "inactive", "failed":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

// Regex to check for ':latest' tag or no tag (which implies latest)
var fargateLatestTagRegex = regexp.MustCompile(`(:latest|[^:]+)$`)

type amazonFargate struct {
	DefaultAwsServiceImpl
}

func (a *amazonFargate) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonFargate) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonFargate) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonFargate) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameFargate)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := ecs.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List all clusters
	clustersPaginator := ecs.NewListClustersPaginator(svc, &ecs.ListClustersInput{})
	var clusterArns []string
	for clustersPaginator.HasMorePages() {
		clustersOutput, err := clustersPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list ecs clusters for fargate", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
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
			ctx.GetLogger().Error("failed to describe ecs clusters for fargate", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			continue
		}

		for _, cluster := range describedClustersOutput.Clusters {
			if cluster.ClusterName == nil || cluster.ClusterArn == nil || cluster.Status == nil {
				ctx.GetLogger().Warn("Skipping Fargate cluster due to missing essential fields", "cluster", cluster)
				continue
			}

			// List services in the cluster and filter for Fargate launch type
			servicesPaginator := ecs.NewListServicesPaginator(svc, &ecs.ListServicesInput{
				Cluster:    cluster.ClusterArn,
				LaunchType: types.LaunchTypeFargate,
			})

			for servicesPaginator.HasMorePages() {
				servicesOutput, err := servicesPaginator.NextPage(ctx.GetContext())
				if err != nil {
					ctx.GetLogger().Error("failed to list fargate services for cluster", "error", err, "clusterArn", *cluster.ClusterArn, "region", regionName)
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
						ctx.GetLogger().Error("failed to describe fargate services", "error", err, "clusterArn", *cluster.ClusterArn, "region", regionName)
						continue
					}

					for _, service := range describedServicesOutput.Services {
						// Filter to only Fargate services
						if service.LaunchType != types.LaunchTypeFargate {
							continue
						}

						if service.ServiceName == nil || service.ServiceArn == nil || service.Status == nil || service.CreatedAt == nil {
							ctx.GetLogger().Warn("Skipping Fargate service due to missing essential fields", "service", service)
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
							ServiceName: ServiceNameFargate,
							Name:        *service.ServiceName,
							Status:      fargateServiceStatusToNbStatus(service.Status),
							Region:      regionName,
							Tags:        serviceTags,
							Meta:        serviceMeta,
							Arn:         *service.ServiceArn,
							CreatedAt:   *service.CreatedAt,
							Type:        getAwsServiceResourceType(ServiceNameFargate, "service"),
						}
						resources = append(resources, serviceResource)

						// Get tasks for this Fargate service
						tasksPaginator := ecs.NewListTasksPaginator(svc, &ecs.ListTasksInput{
							Cluster:     cluster.ClusterArn,
							ServiceName: service.ServiceName,
							LaunchType:  types.LaunchTypeFargate,
						})

						for tasksPaginator.HasMorePages() {
							tasksOutput, err := tasksPaginator.NextPage(ctx.GetContext())
							if err != nil {
								ctx.GetLogger().Error("failed to list fargate tasks for service", "error", err, "serviceArn", *service.ServiceArn, "clusterArn", *cluster.ClusterArn)
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
									ctx.GetLogger().Error("failed to describe fargate tasks", "error", err, "clusterArn", *cluster.ClusterArn)
									continue
								}

								for _, task := range describedTasksOutput.Tasks {
									// Filter to only Fargate tasks
									if task.LaunchType != types.LaunchTypeFargate {
										continue
									}

									if task.TaskArn == nil || task.LastStatus == nil || task.CreatedAt == nil {
										ctx.GetLogger().Warn("Skipping Fargate task due to missing essential fields", "task", task)
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
										ServiceName: ServiceNameFargate,
										Name:        taskID,
										Status:      fargateTaskStatusToNbStatus(task.LastStatus),
										Region:      regionName,
										Tags:        taskTags,
										Meta:        taskMeta,
										Arn:         *task.TaskArn,
										CreatedAt:   *task.CreatedAt,
										Type:        getAwsServiceResourceType(ServiceNameFargate, "task"),
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
func getFargateTaskDefinitionDetails(ctx context.Context, taskDefArn string, svc *ecs.Client) (*types.TaskDefinition, error) {
	input := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &taskDefArn,
		Include:        []types.TaskDefinitionField{types.TaskDefinitionFieldTags},
	}
	result, err := svc.DescribeTaskDefinition(ctx, input)
	if err != nil {
		return nil, err
	}
	return result.TaskDefinition, nil
}

func getFargatePrices(cfg aws.Config, region string, cpu float64, memoryGB float64) (float64, float64, error) {
	// Fargate pricing is based on vCPU and Memory per hour.
	// We need to make two calls to the pricing API.

	// Get vCPU price
	vcpuFilters := map[string]string{
		"regionCode":      region,
		"productFamily":   "Compute",
		"usagetype":       fmt.Sprintf("%s-vCPU-Hours:perCPU", strings.ToUpper(region)),
		"operatingSystem": "Linux",
	}
	vcpuPriceList, err := getAvailableInstancesFromPricing(cfg, "AmazonECS", vcpuFilters)
	if err != nil || len(vcpuPriceList) == 0 {
		return 0, 0, fmt.Errorf("could not get Fargate vCPU pricing: %w", err)
	}
	vcpuPrice, err := getPricingValue(vcpuPriceList[0])
	if err != nil {
		return 0, 0, fmt.Errorf("could not parse Fargate vCPU price: %w", err)
	}

	// Get Memory price
	memFilters := map[string]string{
		"regionCode":      region,
		"productFamily":   "Compute",
		"usagetype":       fmt.Sprintf("%s-Memory-Hours:perGB", strings.ToUpper(region)),
		"operatingSystem": "Linux",
	}
	memPriceList, err := getAvailableInstancesFromPricing(cfg, "AmazonECS", memFilters)
	if err != nil || len(memPriceList) == 0 {
		return 0, 0, fmt.Errorf("could not get Fargate Memory pricing: %w", err)
	}
	memPrice, err := getPricingValue(memPriceList[0])
	if err != nil {
		return 0, 0, fmt.Errorf("could not parse Fargate Memory price: %w", err)
	}

	return vcpuPrice, memPrice, nil
}

func (a *amazonFargate) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config for fargate recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameFargate)
		return recommendations, err
	}

	startDate := time.Now().Add(-time.Hour * 24 * 7)
	endDate := time.Now()

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// --- Recommendations for Fargate Services ---
		if resource.Type == getAwsServiceResourceType(ServiceNameFargate, "service") {
			platformVersion := ""
			desiredCount := 0.0
			minHealthyPercent := 100.0
			maxPercent := 200.0
			healthCheckGracePeriod := 0.0
			enableExecuteCommand := false
			taskDefinitionArn := ""
			taskDefinitionCPU := 0.0
			taskDefinitionMemory := 0.0

			if pv, ok := meta["PlatformVersion"].(string); ok {
				platformVersion = pv
			}
			if dc, ok := meta["DesiredCount"].(float64); ok {
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
			if tdArn, ok := meta["TaskDefinition"].(string); ok {
				taskDefinitionArn = tdArn
			}

			// Check 1: Fargate Latest Platform Version
			if platformVersion != "LATEST" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryInfraUpgrade,
					RuleName:            "aws_fargate_latest_platform_version",
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

			// Check 2: Deployment Configuration - Minimum Healthy Percent
			if desiredCount > 1 && minHealthyPercent < 50 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_fargate_service_min_healthy_percent_low",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "current_min_healthy_percent": minHealthyPercent, "desired_count": desiredCount, "reason": "Minimum healthy percent is below 50% which might impact availability during deployments."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			} else if desiredCount == 1 && minHealthyPercent < 100 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_fargate_service_min_healthy_percent_too_low_for_single_task",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "current_min_healthy_percent": minHealthyPercent, "desired_count": desiredCount, "reason": "For a single-task service, minimum healthy percent less than 100% can cause downtime during updates."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 3: Deployment Configuration - Maximum Percent
			if maxPercent != 200 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_fargate_service_max_percent_non_standard",
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
			hasLoadBalancers := false
			if lbs, ok := meta["LoadBalancers"].([]any); ok && len(lbs) > 0 {
				hasLoadBalancers = true
			}
			if hasLoadBalancers && healthCheckGracePeriod == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_fargate_service_health_check_grace_period_zero",
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

			// Check 5: ECS Exec Logging
			if enableExecuteCommand {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_fargate_service_exec_enabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"service_name": resource.Name, "service_arn": resource.Arn, "reason": "ECS Exec is enabled for the service. Ensure audit logging is configured and access is properly restricted."},
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
				taskDefDetails, tdErr := getFargateTaskDefinitionDetails(ctx.GetContext(), taskDefinitionArn, ecsSvc)
				if tdErr == nil && taskDefDetails != nil {
					// Extract CPU and Memory from Task Definition
					if taskDefDetails.Cpu != nil && *taskDefDetails.Cpu != "" {
						if cpuInt, parseErr := strconv.ParseFloat(*taskDefDetails.Cpu, 64); parseErr == nil {
							taskDefinitionCPU = cpuInt / 1024.0
						}
					}
					if taskDefDetails.Memory != nil && *taskDefDetails.Memory != "" {
						if memInt, parseErr := strconv.ParseFloat(*taskDefDetails.Memory, 64); parseErr == nil {
							taskDefinitionMemory = memInt / 1024.0
						}
					}

					if taskDefDetails.Cpu == nil || *taskDefDetails.Cpu == "" || *taskDefDetails.Cpu == "0" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategoryConfiguration,
							RuleName:            "aws_fargate_task_definition_cpu_undefined",
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
							RuleName:            "aws_fargate_task_definition_memory_undefined",
							Severity:            providers.RecommendationSeverityMedium,
							Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "reason": "Fargate task definition does not specify memory."},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}

					// Check: Task Role ARN defined
					if taskDefDetails.TaskRoleArn == nil || *taskDefDetails.TaskRoleArn == "" {
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName:        providers.RecommendationCategorySecurity,
							RuleName:            "aws_fargate_task_definition_missing_task_role",
							Severity:            providers.RecommendationSeverityMedium,
							Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "reason": "Task definition does not have an IAM task role defined."},
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
						if containerDef.Image != nil && (!strings.Contains(*containerDef.Image, ":") || strings.HasSuffix(*containerDef.Image, ":latest")) {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_fargate_task_definition_image_latest_tag",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "image": *containerDef.Image, "reason": "Container image uses 'latest' tag or no tag."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Secrets Management
						if (len(containerDef.Secrets) == 0) && len(containerDef.Environment) > 0 {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategorySecurity,
								RuleName:            "aws_fargate_task_definition_secrets_not_used",
								Severity:            providers.RecommendationSeverityLow,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container defines environment variables. Review if any are sensitive."},
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
								RuleName:            "aws_fargate_task_definition_privileged_container",
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
								RuleName:            "aws_fargate_task_definition_readonly_root_fs_disabled",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container does not have a read-only root filesystem."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Container Logging Configuration
						if containerDef.LogConfiguration == nil {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_fargate_task_definition_logging_not_configured",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container definition does not have log configuration."},
								Action:              providers.RecommendationActionModify,
								ResourceServiceName: resource.ServiceName,
								ResourceId:          resource.Id,
								ResourceType:        resource.Type,
								ResourceRegion:      resource.Region,
							})
						}

						// Container Health Check
						if containerDef.HealthCheck == nil {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName:        providers.RecommendationCategoryConfiguration,
								RuleName:            "aws_fargate_task_definition_health_check_not_configured",
								Severity:            providers.RecommendationSeverityMedium,
								Data:                map[string]any{"service_name": resource.Name, "task_definition_arn": taskDefinitionArn, "container_name": containerName, "reason": "Container definition does not have a health check configured."},
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
			if desiredCount > 0 && taskDefinitionCPU > 0 && taskDefinitionMemory > 0 {
				metrics, metricsErr := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
					ResourceIds:  []string{resource.Name},
					ServiceName:  ServiceNameFargate,
					ResourceType: "service",
					Region:       resource.Region,
					StartDate:    &startDate,
					EndDate:      &endDate,
					MetricNames:  []string{"CPUUtilization", "MemoryUtilization"},
					Step:         3600 * time.Second,
					Statistics:   []string{"Maximum"},
				})

				if metricsErr != nil {
					ctx.GetLogger().Warn("failed to get metrics for Fargate service", "error", metricsErr, "serviceArn", resource.Arn)
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

					underutilizedCPUThreshold := 20.0
					underutilizedMemoryThreshold := 50.0
					overutilizedCPUThreshold := 80.0
					overutilizedMemoryThreshold := 90.0

					// Check for Underutilization
					if maxCPUUtil < underutilizedCPUThreshold && maxMemoryUtil < underutilizedMemoryThreshold {
						recommendedCPU := math.Max(0.25, taskDefinitionCPU*0.5)
						recommendedMemory := math.Max(0.5, taskDefinitionMemory*0.5)

						estimatedSavingsPerMonth := 0.0
						vcpuPrice, memPrice, priceErr := getFargatePrices(cfg, resource.Region, taskDefinitionCPU, taskDefinitionMemory)
						if priceErr != nil {
							ctx.GetLogger().Warn("could not calculate savings for Fargate service", "error", priceErr, "serviceArn", resource.Arn)
						} else {
							// Calculate current and recommended costs
							currentCostPerHour := (taskDefinitionCPU * vcpuPrice) + (taskDefinitionMemory * memPrice)
							recommendedCostPerHour := (recommendedCPU * vcpuPrice) + (recommendedMemory * memPrice)
							// Savings per task per hour
							savingsPerHour := currentCostPerHour - recommendedCostPerHour
							// Total monthly savings for all tasks
							estimatedSavingsPerMonth = savingsPerHour * 24 * 30 * desiredCount
						}
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "aws_fargate_service_underutilized",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      estimatedSavingsPerMonth,
							Data: map[string]any{
								"service_name":          resource.Name,
								"service_arn":           resource.Arn,
								"current_cpu_vcpu":      taskDefinitionCPU,
								"current_memory_gb":     taskDefinitionMemory,
								"max_cpu_util":          maxCPUUtil,
								"max_memory_util":       maxMemoryUtil,
								"recommended_cpu_vcpu":  recommendedCPU,
								"recommended_memory_gb": recommendedMemory,
								"reason":                fmt.Sprintf("Service appears underutilized (Max CPU: %.2f%%, Max Memory: %.2f%%).", maxCPUUtil, maxMemoryUtil),
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}

					// Check for Overutilization
					if maxCPUUtil > overutilizedCPUThreshold || maxMemoryUtil > overutilizedMemoryThreshold {
						recommendedCPU := taskDefinitionCPU * 1.5
						recommendedMemory := taskDefinitionMemory * 1.5
						estimatedCostIncreasePerMonth := 0.0
						vcpuPrice, memPrice, priceErr := getFargatePrices(cfg, resource.Region, taskDefinitionCPU, taskDefinitionMemory)
						if priceErr != nil {
							ctx.GetLogger().Warn("could not calculate cost increase for Fargate service", "error", priceErr, "serviceArn", resource.Arn)
						} else {
							currentCostPerHour := (taskDefinitionCPU * vcpuPrice) + (taskDefinitionMemory * memPrice)
							recommendedCostPerHour := (recommendedCPU * vcpuPrice) + (recommendedMemory * memPrice)
							costIncreasePerHour := recommendedCostPerHour - currentCostPerHour
							estimatedCostIncreasePerMonth = costIncreasePerHour * 24 * 30 * desiredCount
						}

						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "aws_fargate_service_overutilized",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      -estimatedCostIncreasePerMonth,
							Data: map[string]any{
								"service_name":          resource.Name,
								"service_arn":           resource.Arn,
								"current_cpu_vcpu":      taskDefinitionCPU,
								"current_memory_gb":     taskDefinitionMemory,
								"max_cpu_util":          maxCPUUtil,
								"max_memory_util":       maxMemoryUtil,
								"recommended_cpu_vcpu":  recommendedCPU,
								"recommended_memory_gb": recommendedMemory,
								"reason":                fmt.Sprintf("Service appears overutilized (Max CPU: %.2f%%, Max Memory: %.2f%%).", maxCPUUtil, maxMemoryUtil),
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

			// Check: Missing Tags
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags",
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
		}
	}

	return recommendations, nil
}

func (a *amazonFargate) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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
		describeServiceOutput, err := ecsSvc.DescribeServices(ctx.GetContext(), &ecs.DescribeServicesInput{
			Cluster:  &cluster,
			Services: []string{service},
		})
		if err != nil || len(describeServiceOutput.Services) == 0 {
			return "", nil
		}

		// Filter for Fargate services
		if describeServiceOutput.Services[0].LaunchType != types.LaunchTypeFargate {
			return "", nil
		}

		taskDefArn := describeServiceOutput.Services[0].TaskDefinition
		describeTdOutput1, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{
			TaskDefinition: taskDefArn,
		})
		if err != nil || describeTdOutput1.TaskDefinition == nil {
			return "", nil
		}
		describeTdOutput = describeTdOutput1
	} else if cluster != "" && taskDef != "" {
		describeTdOutput1, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{
			TaskDefinition: &taskDef,
		})
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

func (a *amazonFargate) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
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
			Kind:      "fargate",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	if !strings.HasPrefix(resourceId, "arn:") {
		if strings.Contains(resourceId, "/") {
			resourceIdSplits := strings.Split(resourceId, "/")
			if len(resourceIdSplits) == 3 {
				resourceId = resourceIdSplits[1]
			}
		}
		as := awsProvider{}
		resources, err := as.ListResources(ctx, account, providers.ListResourceRequest{
			ServiceName: "fargate",
			Regions:     []string{region},
			ResourceIds: []string{resourceId},
		})
		if err != nil {
			ctx.GetLogger().Error("aws: failed to identify fargate resources", "error", err)
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

		describeServiceOutput, err := ecsSvc.DescribeServices(ctx.GetContext(), &ecs.DescribeServicesInput{
			Cluster:  &cluster,
			Services: []string{service},
		})
		if err != nil {
			return app, err
		}

		if len(describeServiceOutput.Services) > 0 {
			s := describeServiceOutput.Services[0]
			// Filter for Fargate services
			if s.LaunchType != types.LaunchTypeFargate {
				return app, fmt.Errorf("service is not a Fargate service")
			}

			app.Id.Name = *s.ServiceArn
			app.Status = *s.Status
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
				Id: providers.ServiceApplicationId{Name: *s.TaskDefinition, Kind: "ecs", Namespace: region},
			}.ToDownstreamLink())

			for _, lb := range s.LoadBalancers {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
					Id: providers.ServiceApplicationId{Name: *lb.TargetGroupArn, Kind: "elbv2", Namespace: region},
				}.ToDownstreamLink())
			}

			if s.NetworkConfiguration != nil && s.NetworkConfiguration.AwsvpcConfiguration != nil {
				for _, subnetId := range s.NetworkConfiguration.AwsvpcConfiguration.Subnets {
					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
						Id: providers.ServiceApplicationId{Name: subnetId, Kind: "ec2", Namespace: region},
					}.ToDownstreamLink())
				}
				for _, sgId := range s.NetworkConfiguration.AwsvpcConfiguration.SecurityGroups {
					app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
						Id: providers.ServiceApplicationId{Name: sgId, Kind: "ec2", Namespace: region},
					}.ToDownstreamLink())
				}
			}

			describeTdOutput, err := ecsSvc.DescribeTaskDefinition(ctx.GetContext(), &ecs.DescribeTaskDefinitionInput{
				TaskDefinition: s.TaskDefinition,
			})
			if err == nil && describeTdOutput.TaskDefinition != nil {
				for _, cd := range describeTdOutput.TaskDefinition.ContainerDefinitions {
					if cd.LogConfiguration != nil && cd.LogConfiguration.LogDriver == "awslogs" {
						if logGroup, ok := cd.LogConfiguration.Options["awslogs-group"]; ok {
							app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{
								Id: providers.ServiceApplicationId{Name: logGroup, Kind: "cloudwatchlogs", Namespace: region},
							}.ToDownstreamLink())
						}
					}
				}
			}
		}
	}

	return app, nil
}
