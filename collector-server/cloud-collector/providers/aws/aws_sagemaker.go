package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	// Ensure other necessary imports like "github.com/samber/lo" are present if used
)

// Helper function to map SageMaker status strings to provider status
func sagemakerStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*status)
	switch s {
	case "inservice", "completed", "succeeded", "ready": // Common success/active states
		return providers.ResourceStatusActive
	case "pending", "creating", "updating", "starting", "inprogress": // Common pending states
		return providers.ResourceStatusActive
	case "stopping", "deleting":
		return providers.ResourceStatusInactive // Or Deleting
	case "stopped", "failed", "delete_failed": // Common inactive/error states
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type amazonSagemaker struct {
	DefaultAwsServiceImpl
}

func (a *amazonSagemaker) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for SageMaker yet.
	return errors.ErrUnsupported
}

func (a *amazonSagemaker) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for SageMaker yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonSagemaker) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonSagemaker) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameSageMaker)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := sagemaker.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get Notebook Instances ---
	notebookPaginator := sagemaker.NewListNotebookInstancesPaginator(svc, &sagemaker.ListNotebookInstancesInput{})
	for notebookPaginator.HasMorePages() {
		notebooksOutput, err := notebookPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to list sagemaker notebook instances", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		for _, summary := range notebooksOutput.NotebookInstances {
			tags := make(map[string][]string)
			// Describe Notebook Instance to get full details
			details, err := svc.DescribeNotebookInstance(context.TODO(), &sagemaker.DescribeNotebookInstanceInput{
				NotebookInstanceName: summary.NotebookInstanceName,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe sagemaker notebook instance", "error", err, "notebookName", *summary.NotebookInstanceName, "accountNumber", account.AccountNumber, "region", regionName)
				// Continue with summary info if describe fails? Or skip? Let's skip.
				continue
			}

			// Get Tags for the notebook instance
			tagsOutput, err := svc.ListTags(context.TODO(), &sagemaker.ListTagsInput{
				ResourceArn: details.NotebookInstanceArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for sagemaker notebook instance", "error", err, "notebookArn", *details.NotebookInstanceArn, "accountNumber", account.AccountNumber, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			meta := structToMap(details) // Use detailed info for Meta

			resource := providers.Resource{
				Id:          *details.NotebookInstanceName,
				ServiceName: ServiceNameSageMaker,
				Name:        *details.NotebookInstanceName,
				Status:      sagemakerStatusToNbStatus((*string)(&details.NotebookInstanceStatus)),
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *details.NotebookInstanceArn,
				CreatedAt:   *details.CreationTime,
				Type:        getAwsServiceResourceType(ServiceNameSageMaker, "notebook-instance"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Get Endpoints ---
	endpointPaginator := sagemaker.NewListEndpointsPaginator(svc, &sagemaker.ListEndpointsInput{})
	for endpointPaginator.HasMorePages() {
		endpointsOutput, err := endpointPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to list sagemaker endpoints", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		for _, summary := range endpointsOutput.Endpoints {
			tags := make(map[string][]string)
			// Describe Endpoint to get full details
			details, err := svc.DescribeEndpoint(context.TODO(), &sagemaker.DescribeEndpointInput{
				EndpointName: summary.EndpointName,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe sagemaker endpoint", "error", err, "endpointName", *summary.EndpointName, "accountNumber", account.AccountNumber, "region", regionName)
				// Skip if describe fails
				continue
			}

			// Get Tags for the endpoint
			tagsOutput, err := svc.ListTags(context.TODO(), &sagemaker.ListTagsInput{
				ResourceArn: details.EndpointArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for sagemaker endpoint", "error", err, "endpointArn", *details.EndpointArn, "accountNumber", account.AccountNumber, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			meta := structToMap(details) // Use detailed info for Meta

			resource := providers.Resource{
				Id:          *details.EndpointName,
				ServiceName: ServiceNameSageMaker,
				Name:        *details.EndpointName,
				Status:      sagemakerStatusToNbStatus((*string)(&details.EndpointStatus)),
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *details.EndpointArn,
				CreatedAt:   *details.CreationTime,
				Type:        getAwsServiceResourceType(ServiceNameSageMaker, "endpoint"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Get Training Jobs (Optional - can be numerous) ---
	// Consider adding if needed, similar structure using ListTrainingJobs and DescribeTrainingJob

	// --- Get Models (Optional) ---
	// Consider adding if needed, using ListModels and DescribeModel

	return resources, nil
}

func (a *amazonSagemaker) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameSageMaker)
	// 	return recommendations, err
	// }

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// --- Recommendations for Notebook Instances ---
		if resource.Type == getAwsServiceResourceType(ServiceNameSageMaker, "notebook-instance") {
			// Check 1: Direct Internet Access Disabled
			directInternetDisabled := false
			if directAccess, ok := meta["DirectInternetAccess"].(string); ok && directAccess == string(types.DirectInternetAccessDisabled) {
				directInternetDisabled = true
			}
			if !directInternetDisabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_sagemaker_notebook_no_direct_internet",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"notebook_name": resource.Name, "notebook_arn": resource.Arn, "reason": "Direct internet access is enabled."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 2: Root Access Disabled
			rootAccessDisabled := false
			if rootAccess, ok := meta["RootAccess"].(string); ok && rootAccess == string(types.RootAccessDisabled) {
				rootAccessDisabled = true
			}
			if !rootAccessDisabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_sagemaker_notebook_root_access_disabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"notebook_name": resource.Name, "notebook_arn": resource.Arn, "reason": "Root access is enabled."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 3: Encryption with KMS CMK
			kmsKeyId := ""
			if keyId, ok := meta["KmsKeyId"].(string); ok {
				kmsKeyId = keyId
			}
			if kmsKeyId == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_sagemaker_notebook_encryption_cmk",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"notebook_name": resource.Name, "notebook_arn": resource.Arn, "reason": "Notebook instance is not encrypted with a Customer Managed Key (CMK)."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 4: Idle Notebook Instance (Requires Metrics)
			// Placeholder: Fetch CPUUtilization, check if consistently low (e.g., < 5% for 7 days)
			/*
				metrics, err := a.GetMetrices(ctx, account, providers.ListMetricsRequest{
					ResourceIds: []string{resource.Id},
					ServiceName: ServiceNameSageMaker,
					ResourceType: "notebook-instance",
					Region: resource.Region,
					// Define StartDate, EndDate, MetricNames=["CPUUtilization"], Statistics=["Maximum"]
				})
				if err == nil && len(metrics.Items) > 0 {
					// Analyze metrics.Items[0].Values
					// If idle... create recommendation (Stop or Delete)
				} else if err != nil {
					ctx.GetLogger().Warn("failed to get metrics for notebook instance", "error", err, "arn", resource.Id)
				}
			*/

			// Check 5: Missing Tags
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags", // Use generic tag rule name
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"notebook_name": resource.Name, "notebook_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// --- Recommendations for Endpoints ---
		if resource.Type == getAwsServiceResourceType(ServiceNameSageMaker, "endpoint") {
			// Check 1: Data Capture Enabled (for monitoring)
			dataCaptureEnabled := false
			if dcConfigSummary, ok := meta["DataCaptureConfig"].(map[string]any); ok { // Check key name from DescribeEndpoint response
				if enabled, ok := dcConfigSummary["EnableCapture"].(bool); ok && enabled {
					dataCaptureEnabled = true
				}
			}
			if !dataCaptureEnabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration, // Or Monitoring
					RuleName:            "aws_sagemaker_endpoint_data_capture",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"endpoint_name": resource.Name, "endpoint_arn": resource.Arn, "reason": "Data capture is not enabled for monitoring model quality or data drift."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			// Check 2: Idle Endpoint (Requires Metrics - Invocations)
			// Placeholder: Fetch Invocations metric, check if sum is 0 over a period.
			/*
				metrics, err := a.GetMetrices(ctx, account, providers.ListMetricsRequest{
					ResourceIds: []string{resource.Id}, // Might need VariantName as well depending on metric
					ServiceName: ServiceNameSageMaker,
					ResourceType: "endpoint-variant", // Or endpoint, check CloudWatch namespace/dimensions
					Region: resource.Region,
					// Define StartDate, EndDate, MetricNames=["Invocations"], Statistics=["Sum"]
				})
				if err == nil && len(metrics.Items) > 0 {
					// Analyze metrics.Items[0].Values
					// If idle... create recommendation (Delete)
				} else if err != nil {
					ctx.GetLogger().Warn("failed to get metrics for endpoint", "error", err, "arn", resource.Id)
				}
			*/

			// Check 3: Missing Tags
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags", // Use generic tag rule name
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"endpoint_name": resource.Name, "endpoint_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// --- Recommendations for Training Jobs (if fetched) ---
		// if resource.Type == getAwsServiceResourceType(ServiceNameSageMaker, "training-job") {
		// Check 1: InterContainerTrafficEncryptionEnabled
		// Check 2: VolumeKmsKeyId (Encryption)
		// Check 3: Missing Tags
		// }
	}

	return recommendations, nil
}

func (a *amazonSagemaker) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)

	var foundLogGroup string
	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        &logGroupName,
				LogStreamNamePrefix: &resourceId,
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				foundLogGroup = logGroupName
				return foundLogGroup, nil
			}
		}
	}
	return foundLogGroup, nil
}

func (a *amazonSagemaker) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "sagemaker",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}

	return app, nil
}
