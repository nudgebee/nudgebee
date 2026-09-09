package aws

import (
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/computeoptimizer"
	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
	"github.com/samber/lo"
)

const ServiceNameComputeOptimizer = "ComputeOptimizer"

// coPageSize is the per-call ceiling Compute Optimizer accepts on every
// recommendation API. Without it the SDK asks for one default-sized page and
// silently drops the rest of the account.
const coPageSize = 100

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

	// Enrollment is an account-wide opt-in, so the account's own region answers
	// for every region we go on to query.
	enrollmentOutput, err := computeoptimizer.NewFromConfig(cfg).GetEnrollmentStatus(ctx.GetContext(), &computeoptimizer.GetEnrollmentStatusInput{})
	if err != nil {
		// Not being permitted to ask is a settled answer — the account is not
		// using Compute Optimizer. Any other failure leaves enrollment unknown,
		// and answering "no recommendations" to an unknown would archive every
		// Compute Optimizer recommendation the account has.
		if isAccessDeniedError(err) {
			ctx.GetLogger().Info("compute optimizer enrollment not readable, skipping")
			return recommendations, nil
		}
		return nil, fmt.Errorf("check compute optimizer enrollment status: %w", err)
	}

	if enrollmentOutput.Status != cotypes.StatusActive {
		ctx.GetLogger().Info("compute optimizer not active, skipping", "status", enrollmentOutput.Status)
		return recommendations, nil
	}

	// Compute Optimizer is a regional service: a client built from the account's
	// own region only ever answers for that region. Querying every region the
	// account has resources in is what the RDS producer already does.
	for _, region := range regionsFromResources(existingResources, cfg.Region) {
		regionalCfg := cfg.Copy()
		regionalCfg.Region = region
		client := computeoptimizer.NewFromConfig(regionalCfg)

		for _, get := range []func(providers.CloudProviderContext, *computeoptimizer.Client, providers.Account) ([]providers.Recommendation, error){
			a.getEC2Recommendations,
			a.getLambdaRecommendations,
			a.getEBSRecommendations,
			a.getECSRecommendations,
		} {
			recs, err := get(ctx, client, account)
			if err != nil {
				// A region the account opted into but where Compute Optimizer has
				// no working endpoint carries no recommendations to lose, and the
				// condition is permanent — skip it rather than failing the sync
				// forever. Everything else means we cannot tell whether this
				// region's recommendations still apply, and returning the partial
				// set would read as "they no longer do": the sync archives every
				// recommendation for this service that the scan did not return.
				if isRegionEndpointMissing(err) || isRegionUnreachable(err) || isServiceUnavailableInRegion(err) {
					ctx.GetLogger().Info("compute optimizer not available in region, skipping", "region", region)
					break
				}
				return nil, fmt.Errorf("compute optimizer in %s: %w", region, err)
			}
			recommendations = append(recommendations, recs...)
		}
	}

	ctx.GetLogger().Info("fetched compute optimizer recommendations", "count", len(recommendations))
	return recommendations, nil
}

// regionsFromResources returns the distinct regions the account has resources
// in, always including defaultRegion. Keeping the default in the set means this
// can only ever query more than the single-region behaviour it replaces, even
// when discovery has not run or does not record the resource type in question.
func regionsFromResources(existingResources []providers.Resource, defaultRegion string) []string {
	regions := []string{}
	if defaultRegion != "" {
		regions = append(regions, defaultRegion)
	}
	for _, resource := range existingResources {
		if resource.Region != "" {
			regions = append(regions, resource.Region)
		}
	}
	return lo.Uniq(regions)
}

func (a *awsComputeOptimizer) getEC2Recommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	instanceRecommendations := []cotypes.InstanceRecommendation{}
	var nextToken *string
	for {
		output, err := client.GetEC2InstanceRecommendations(ctx.GetContext(), &computeoptimizer.GetEC2InstanceRecommendationsInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(coPageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("get EC2 instance recommendations: %w", err)
		}
		instanceRecommendations = append(instanceRecommendations, output.InstanceRecommendations...)
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = output.NextToken
	}

	for _, rec := range instanceRecommendations {
		if normCOFinding(rec.Finding) == normFindingOptimized {
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

	return recommendations, nil
}

func (a *awsComputeOptimizer) getLambdaRecommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	functionRecommendations := []cotypes.LambdaFunctionRecommendation{}
	var nextToken *string
	for {
		output, err := client.GetLambdaFunctionRecommendations(ctx.GetContext(), &computeoptimizer.GetLambdaFunctionRecommendationsInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(coPageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("get Lambda function recommendations: %w", err)
		}
		functionRecommendations = append(functionRecommendations, output.LambdaFunctionRecommendations...)
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = output.NextToken
	}

	for _, rec := range functionRecommendations {
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

	return recommendations, nil
}

func (a *awsComputeOptimizer) getEBSRecommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	volumeRecommendations := []cotypes.VolumeRecommendation{}
	var nextToken *string
	for {
		output, err := client.GetEBSVolumeRecommendations(ctx.GetContext(), &computeoptimizer.GetEBSVolumeRecommendationsInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(coPageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("get EBS volume recommendations: %w", err)
		}
		volumeRecommendations = append(volumeRecommendations, output.VolumeRecommendations...)
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = output.NextToken
	}

	for _, rec := range volumeRecommendations {
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

	return recommendations, nil
}

func (a *awsComputeOptimizer) getECSRecommendations(ctx providers.CloudProviderContext, client *computeoptimizer.Client, account providers.Account) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	serviceRecommendations := []cotypes.ECSServiceRecommendation{}
	var nextToken *string
	for {
		output, err := client.GetECSServiceRecommendations(ctx.GetContext(), &computeoptimizer.GetECSServiceRecommendationsInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(coPageSize),
		})
		if err != nil {
			return nil, fmt.Errorf("get ECS service recommendations: %w", err)
		}
		serviceRecommendations = append(serviceRecommendations, output.EcsServiceRecommendations...)
		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		nextToken = output.NextToken
	}

	for _, rec := range serviceRecommendations {
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

	return recommendations, nil
}

func mapCOFindingToSeverity(finding cotypes.Finding) providers.RecommendationSeverity {
	switch normCOFinding(finding) {
	case normFindingOverProvisioned:
		return providers.RecommendationSeverityMedium
	case normFindingUnderProvisioned:
		return providers.RecommendationSeverityHigh
	default:
		return providers.RecommendationSeverityLow
	}
}

// Normalized SDK finding constants, pre-computed once so the per-recommendation
// comparisons in getEC2Recommendations and mapCOFindingToSeverity avoid
// redundant string allocations.
var (
	normFindingOptimized        = normCOFinding(cotypes.FindingOptimized)
	normFindingOverProvisioned  = normCOFinding(cotypes.FindingOverProvisioned)
	normFindingUnderProvisioned = normCOFinding(cotypes.FindingUnderProvisioned)
)

// normCOFinding normalizes an EC2 Compute Optimizer finding for comparison.
// The GetEC2InstanceRecommendations API returns findings in
// SCREAMING_SNAKE_CASE ("OPTIMIZED", "OVER_PROVISIONED", "UNDER_PROVISIONED"),
// whereas the aws-sdk-go-v2 enum constants are mixed-case ("Optimized",
// "Overprovisioned", "Underprovisioned"). Comparing the raw values silently
// fails, so already-optimized instances leak through as bogus rightsize
// recommendations (current == recommended, $0 savings). Uppercasing and
// stripping the underscore separators makes both wire forms comparable.
func normCOFinding(f cotypes.Finding) string {
	return strings.ReplaceAll(strings.ToUpper(string(f)), "_", "")
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
