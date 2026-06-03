package aws

import (
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
)

const ServiceNameComputeOptimizer = "ComputeOptimizer"

type awsComputeOptimizer struct {
	DefaultAwsServiceImpl
}

func (a *awsComputeOptimizer) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (a *awsComputeOptimizer) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (a *awsComputeOptimizer) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for compute optimizer", "error", err)
		return nil, err
	}

	client := computeoptimizer.NewFromConfig(cfg)

	// Check enrollment status first
	enrollmentOutput, err := client.GetEnrollmentStatus(ctx.GetContext(), &computeoptimizer.GetEnrollmentStatusInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to check compute optimizer enrollment status", "error", err)
		return recommendations, nil
	}

	if enrollmentOutput.Status != cotypes.StatusActive {
		ctx.GetLogger().Info("compute optimizer not active, skipping", "status", enrollmentOutput.Status)
		return recommendations, nil
	}

	ec2Recs := a.getEC2Recommendations(ctx, client, account)
	recommendations = append(recommendations, ec2Recs...)

	lambdaRecs := a.getLambdaRecommendations(ctx, client, account)
	recommendations = append(recommendations, lambdaRecs...)

	ebsRecs := a.getEBSRecommendations(ctx, client, account)
	recommendations = append(recommendations, ebsRecs...)

	ecsRecs := a.getECSRecommendations(ctx, client, account)
	recommendations = append(recommendations, ecsRecs...)

	ctx.GetLogger().Info("fetched compute optimizer recommendations", "count", len(recommendations))
	return recommendations, nil
}

func (a *awsComputeOptimizer) getEC2Recommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	output, err := client.GetEC2InstanceRecommendations(ctx.GetContext(), &computeoptimizer.GetEC2InstanceRecommendationsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to get compute optimizer EC2 recommendations", "error", err)
		return recommendations
	}

	for _, rec := range output.InstanceRecommendations {
		if rec.Finding == cotypes.FindingOptimized {
			continue
		}

		instanceId := ""
		if rec.InstanceArn != nil {
			instanceId = *rec.InstanceArn
		}

		region := ""
		if rec.InstanceArn != nil {
			region = extractRegionFromARN(*rec.InstanceArn)
		}

		data := map[string]any{
			"source":  "aws",
			"finding": string(rec.Finding),
		}

		if rec.InstanceName != nil {
			data["instance_name"] = *rec.InstanceName
		}
		if rec.CurrentInstanceType != nil {
			data["current_instance_type"] = *rec.CurrentInstanceType
		}

		savings := 0.0
		if len(rec.RecommendationOptions) > 0 {
			topOption := rec.RecommendationOptions[0]
			if topOption.InstanceType != nil {
				data["recommended_instance_type"] = *topOption.InstanceType
			}
			if topOption.SavingsOpportunity != nil && topOption.SavingsOpportunity.EstimatedMonthlySavings != nil {
				savings = topOption.SavingsOpportunity.EstimatedMonthlySavings.Value
				data["estimated_monthly_savings"] = savings
				data["currency"] = string(topOption.SavingsOpportunity.EstimatedMonthlySavings.Currency)
			}
			data["performance_risk"] = topOption.PerformanceRisk
			if topOption.MigrationEffort != "" {
				data["migration_effort"] = string(topOption.MigrationEffort)
			}
		}

		severity := mapCOFindingToSeverity(rec.Finding)

		externalResourceId := ""
		if shortId := extractArnTrailingId(instanceId); shortId != "" {
			externalResourceId = common.BuildExternalResourceId("aws", account.AccountNumber, region, "AmazonEC2", "compute-instance", shortId, "")
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            "aws_native_rightsize",
			Severity:            severity,
			Savings:             savings,
			Action:              providers.RecommendationActionModify,
			Data:                data,
			ResourceServiceName: ServiceNameComputeOptimizer,
			ResourceId:          instanceId,
			ResourceType:        "ec2-instance",
			ResourceRegion:      region,
			ExternalResourceId:  externalResourceId,
		})
	}

	return recommendations
}

func (a *awsComputeOptimizer) getLambdaRecommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	output, err := client.GetLambdaFunctionRecommendations(ctx.GetContext(), &computeoptimizer.GetLambdaFunctionRecommendationsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to get compute optimizer Lambda recommendations", "error", err)
		return recommendations
	}

	for _, rec := range output.LambdaFunctionRecommendations {
		if rec.Finding == cotypes.LambdaFunctionRecommendationFindingOptimized {
			continue
		}

		functionArn := ""
		if rec.FunctionArn != nil {
			functionArn = *rec.FunctionArn
		}

		region := ""
		if rec.FunctionArn != nil {
			region = extractRegionFromARN(*rec.FunctionArn)
		}

		data := map[string]any{
			"source":              "aws",
			"finding":             string(rec.Finding),
			"current_memory_size": rec.CurrentMemorySize,
		}

		if rec.FunctionArn != nil {
			data["function_arn"] = *rec.FunctionArn
		}

		savings := 0.0
		if len(rec.MemorySizeRecommendationOptions) > 0 {
			topOption := rec.MemorySizeRecommendationOptions[0]
			data["recommended_memory_size"] = topOption.MemorySize
			if topOption.SavingsOpportunity != nil && topOption.SavingsOpportunity.EstimatedMonthlySavings != nil {
				savings = topOption.SavingsOpportunity.EstimatedMonthlySavings.Value
				data["estimated_monthly_savings"] = savings
			}
		}

		externalResourceId := ""
		if name := extractLambdaFunctionName(functionArn); name != "" {
			externalResourceId = common.BuildExternalResourceId("aws", account.AccountNumber, region, "AWSLambda", "function", name, "")
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            "aws_native_co_lambda_rightsize",
			Severity:            mapSavingsToSeverity(&savings),
			Savings:             savings,
			Action:              providers.RecommendationActionModify,
			Data:                data,
			ResourceServiceName: ServiceNameComputeOptimizer,
			ResourceId:          functionArn,
			ResourceType:        "lambda-function",
			ResourceRegion:      region,
			ExternalResourceId:  externalResourceId,
		})
	}

	return recommendations
}

func (a *awsComputeOptimizer) getEBSRecommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	output, err := client.GetEBSVolumeRecommendations(ctx.GetContext(), &computeoptimizer.GetEBSVolumeRecommendationsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to get compute optimizer EBS recommendations", "error", err)
		return recommendations
	}

	for _, rec := range output.VolumeRecommendations {
		if rec.Finding == cotypes.EBSFindingOptimized {
			continue
		}

		volumeArn := ""
		if rec.VolumeArn != nil {
			volumeArn = *rec.VolumeArn
		}

		region := ""
		if rec.VolumeArn != nil {
			region = extractRegionFromARN(*rec.VolumeArn)
		}

		data := map[string]any{
			"source":  "aws",
			"finding": string(rec.Finding),
		}

		if rec.CurrentConfiguration != nil {
			if rec.CurrentConfiguration.VolumeType != nil {
				data["current_volume_type"] = *rec.CurrentConfiguration.VolumeType
			}
			data["current_volume_size"] = rec.CurrentConfiguration.VolumeSize
			data["current_baseline_iops"] = rec.CurrentConfiguration.VolumeBaselineIOPS
		}

		savings := 0.0
		if len(rec.VolumeRecommendationOptions) > 0 {
			topOption := rec.VolumeRecommendationOptions[0]
			if topOption.Configuration != nil {
				if topOption.Configuration.VolumeType != nil {
					data["recommended_volume_type"] = *topOption.Configuration.VolumeType
				}
				data["recommended_volume_size"] = topOption.Configuration.VolumeSize
			}
			if topOption.SavingsOpportunity != nil && topOption.SavingsOpportunity.EstimatedMonthlySavings != nil {
				savings = topOption.SavingsOpportunity.EstimatedMonthlySavings.Value
				data["estimated_monthly_savings"] = savings
			}
		}

		externalResourceId := ""
		if shortId := extractArnTrailingId(volumeArn); shortId != "" {
			externalResourceId = common.BuildExternalResourceId("aws", account.AccountNumber, region, "AmazonEC2", "storage", shortId, "")
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            "aws_native_co_ebs_rightsize",
			Severity:            mapSavingsToSeverity(&savings),
			Savings:             savings,
			Action:              providers.RecommendationActionModify,
			Data:                data,
			ResourceServiceName: ServiceNameComputeOptimizer,
			ResourceId:          volumeArn,
			ResourceType:        "ebs-volume",
			ResourceRegion:      region,
			ExternalResourceId:  externalResourceId,
		})
	}

	return recommendations
}

func (a *awsComputeOptimizer) getECSRecommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	output, err := client.GetECSServiceRecommendations(ctx.GetContext(), &computeoptimizer.GetECSServiceRecommendationsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to get compute optimizer ECS recommendations", "error", err)
		return recommendations
	}

	for _, rec := range output.EcsServiceRecommendations {
		if rec.Finding == cotypes.ECSServiceRecommendationFindingOptimized {
			continue
		}

		serviceArn := ""
		if rec.ServiceArn != nil {
			serviceArn = *rec.ServiceArn
		}

		region := ""
		if rec.ServiceArn != nil {
			region = extractRegionFromARN(*rec.ServiceArn)
		}

		data := map[string]any{
			"source":  "aws",
			"finding": string(rec.Finding),
		}

		if rec.ServiceArn != nil {
			data["service_arn"] = *rec.ServiceArn
		}
		if rec.CurrentServiceConfiguration != nil {
			if rec.CurrentServiceConfiguration.Cpu != nil {
				data["current_cpu"] = *rec.CurrentServiceConfiguration.Cpu
			}
			if rec.CurrentServiceConfiguration.Memory != nil {
				data["current_memory"] = *rec.CurrentServiceConfiguration.Memory
			}
			if rec.CurrentServiceConfiguration.TaskDefinitionArn != nil {
				data["task_definition_arn"] = *rec.CurrentServiceConfiguration.TaskDefinitionArn
			}
		}

		savings := 0.0
		if len(rec.ServiceRecommendationOptions) > 0 {
			topOption := rec.ServiceRecommendationOptions[0]
			if topOption.Cpu != nil {
				data["recommended_cpu"] = *topOption.Cpu
			}
			if topOption.Memory != nil {
				data["recommended_memory"] = *topOption.Memory
			}
			if topOption.SavingsOpportunity != nil && topOption.SavingsOpportunity.EstimatedMonthlySavings != nil {
				savings = topOption.SavingsOpportunity.EstimatedMonthlySavings.Value
				data["estimated_monthly_savings"] = savings
			}
		}

		externalResourceId := ""
		if shortId := extractArnTrailingId(serviceArn); shortId != "" {
			externalResourceId = common.BuildExternalResourceId("aws", account.AccountNumber, region, "AmazonECS", "service", shortId, "")
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            "aws_native_co_ecs_rightsize",
			Severity:            mapSavingsToSeverity(&savings),
			Savings:             savings,
			Action:              providers.RecommendationActionModify,
			Data:                data,
			ResourceServiceName: ServiceNameComputeOptimizer,
			ResourceId:          serviceArn,
			ResourceType:        "ecs-service",
			ResourceRegion:      region,
			ExternalResourceId:  externalResourceId,
		})
	}

	return recommendations
}

func mapCOFindingToSeverity(finding cotypes.Finding) providers.RecommendationSeverity {
	switch finding {
	case cotypes.FindingOverProvisioned:
		return providers.RecommendationSeverityMedium
	case cotypes.FindingUnderProvisioned:
		return providers.RecommendationSeverityHigh
	default:
		return providers.RecommendationSeverityLow
	}
}

func extractRegionFromARN(arn string) string {
	// ARN format: arn:aws:service:region:account-id:resource
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) >= 4 {
		return parts[3]
	}
	return ""
}

// extractArnTrailingId returns the resource ID portion of an AWS ARN whose
// resource section uses a slash separator (e.g. "instance/i-xxx",
// "volume/vol-xxx", "service/cluster/name"). Returns empty string for empty
// input.
func extractArnTrailingId(arn string) string {
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// extractLambdaFunctionName returns the function name from a Lambda ARN of the
// form arn:aws:lambda:<region>:<account>:function:<name>[:<qualifier>]. The
// qualifier (alias or version) is intentionally dropped so recommendations
// link back to the underlying function row regardless of which version the
// recommendation was emitted for.
func extractLambdaFunctionName(arn string) string {
	if arn == "" {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) >= 7 && parts[5] == "function" {
		return parts[6]
	}
	return extractArnTrailingId(arn)
}

func (a *awsComputeOptimizer) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return nil
}

func (a *awsComputeOptimizer) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (a *awsComputeOptimizer) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (a *awsComputeOptimizer) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}
