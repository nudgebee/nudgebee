package aws

import (
	"errors"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costoptimizationhub"
	cohtypes "github.com/aws/aws-sdk-go-v2/service/costoptimizationhub/types"
	"github.com/aws/smithy-go"
)

const ServiceNameCostOptimizationHub = "CostOptimizationHub"

// cohResourceTypeServiceMap maps Cost Optimization Hub resource types to cloud_resourses service_name values
var cohResourceTypeServiceMap = map[string]string{
	"Ec2Instance":                  "AmazonEC2",
	"Ec2InstanceSavingsPlans":      "AmazonEC2",
	"Ec2ReservedInstances":         "AmazonEC2",
	"Ec2AutoScalingGroup":          "AmazonEC2",
	"EbsVolume":                    "AmazonEC2",
	"ComputeSavingsPlans":          "AmazonEC2",
	"LambdaFunction":               "AWSLambda",
	"EcsService":                   "AmazonECS",
	"RdsDbInstance":                "AmazonRDS",
	"RdsReservedInstances":         "AmazonRDS",
	"RdsDbInstanceStorage":         "AmazonRDS",
	"OpenSearchReservedInstances":  "AmazonOpenSearchService",
	"ElastiCacheReservedInstances": "AmazonElastiCache",
	"RedshiftReservedInstances":    "AmazonRedshift",
	"SageMakerSavingsPlans":        "AmazonSageMaker",
}

// cohResourceTypeCanonicalMap maps Cost Optimization Hub resource types to the
// type value used in cloud_resourses, so per-resource COH recommendations can
// be linked back to their actual resource row via external_resource_id.
var cohResourceTypeCanonicalMap = map[string]string{
	"Ec2Instance":    "compute-instance",
	"EbsVolume":      "storage",
	"LambdaFunction": "function",
	"EcsService":     "service",
	"RdsDbInstance":  "db",
}

type awsCostOptimizationHub struct {
	DefaultAwsServiceImpl
}

func (a *awsCostOptimizationHub) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return providers.QueryMetricsResponse{}, nil
}

func (a *awsCostOptimizationHub) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	return nil, nil
}

func (a *awsCostOptimizationHub) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for cost optimization hub", "error", err)
		return nil, err
	}

	// Cost Optimization Hub is only available in us-east-1
	cfg, err = awsconfig.LoadDefaultConfig(ctx.GetContext(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(cfg.Credentials),
	)
	if err != nil {
		ctx.GetLogger().Error("failed to create us-east-1 config for cost optimization hub", "error", err)
		return nil, err
	}

	client := costoptimizationhub.NewFromConfig(cfg)

	var nextToken *string
	for {
		output, err := client.ListRecommendations(ctx.GetContext(), &costoptimizationhub.ListRecommendationsInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "AccessDeniedException" &&
				strings.Contains(apiErr.ErrorMessage(), "not enrolled") {
				ctx.GetLogger().Info("Cost Optimization Hub not enrolled for account, skipping",
					"accountNumber", account.AccountNumber)
				return recommendations, nil
			}
			ctx.GetLogger().Error("failed to list cost optimization hub recommendations", "error", err)
			return recommendations, nil
		}

		for _, item := range output.Items {
			rec := mapCostOptimizationHubRecommendation(item, account)
			recommendations = append(recommendations, rec)
		}

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	ctx.GetLogger().Info("fetched cost optimization hub recommendations", "count", len(recommendations))
	return recommendations, nil
}

func mapCostOptimizationHubRecommendation(item cohtypes.Recommendation, account providers.Account) providers.Recommendation {
	actionType := ""
	if item.ActionType != nil {
		actionType = *item.ActionType
	}
	ruleName := mapCOHActionToRuleName(actionType)
	severity := mapSavingsToSeverity(item.EstimatedMonthlySavings)

	region := "global"
	if item.Region != nil {
		region = *item.Region
	}

	resourceType := ""
	if item.CurrentResourceType != nil {
		resourceType = *item.CurrentResourceType
	}

	resourceId := ""
	if item.RecommendationId != nil {
		resourceId = *item.RecommendationId
	}

	data := map[string]any{
		"source": "aws",
	}
	if item.RecommendationId != nil {
		data["recommendation_id"] = *item.RecommendationId
	}
	if item.CurrentResourceType != nil {
		data["current_resource_type"] = *item.CurrentResourceType
	}
	if item.CurrentResourceSummary != nil {
		data["current_resource_summary"] = *item.CurrentResourceSummary
	}
	if item.RecommendedResourceType != nil {
		data["recommended_resource_type"] = *item.RecommendedResourceType
	}
	if item.RecommendedResourceSummary != nil {
		data["recommended_resource_summary"] = *item.RecommendedResourceSummary
		data["description"] = *item.RecommendedResourceSummary
	}
	if item.ResourceArn != nil {
		data["resource_arn"] = *item.ResourceArn
	}
	if item.ResourceId != nil {
		data["resource_id"] = *item.ResourceId
	}
	if item.EstimatedMonthlySavings != nil {
		data["estimated_monthly_savings"] = *item.EstimatedMonthlySavings
	}
	if item.EstimatedSavingsPercentage != nil {
		data["estimated_savings_percentage"] = *item.EstimatedSavingsPercentage
	}
	if item.EstimatedMonthlyCost != nil {
		data["estimated_monthly_cost"] = *item.EstimatedMonthlyCost
	}
	if item.ImplementationEffort != nil {
		data["implementation_effort"] = string(*item.ImplementationEffort)
	}
	if item.CurrencyCode != nil {
		data["currency_code"] = *item.CurrencyCode
	}
	if item.AccountId != nil {
		data["account_id"] = *item.AccountId
	}
	data["action_type"] = actionType
	data["restart_needed"] = item.RestartNeeded
	data["rollback_possible"] = item.RollbackPossible

	savings := 0.0
	if item.EstimatedMonthlySavings != nil {
		savings = *item.EstimatedMonthlySavings
	}

	serviceName := ServiceNameCostOptimizationHub
	if mapped, ok := cohResourceTypeServiceMap[resourceType]; ok {
		serviceName = mapped
	}

	// For commitment-style actions (PurchaseSavingsPlans / PurchaseReservedInstances),
	// COH and Cost Explorer return overlapping recommendations against the same
	// underlying spend. Tag with a stable group so they collapse in the aggregator.
	dedupeGroup := ""
	switch cohtypes.ActionType(actionType) {
	case cohtypes.ActionTypePurchaseSavingsPlans, cohtypes.ActionTypePurchaseReservedInstances:
		if serviceName != ServiceNameCostOptimizationHub {
			dedupeGroup = fmt.Sprintf("aws_commitment:%s:%s", account.AccountNumber, serviceName)
		}
	}

	// For per-resource actions (Rightsize/Stop/Upgrade/Delete/MigrateToGraviton),
	// resolve to the canonical cloud_resourses lookup key so the row links back
	// to its actual resource and dedupes against Compute Optimizer recommendations
	// for the same resource via the existing resource_id partition.
	externalResourceId := ""
	if canonicalType, ok := cohResourceTypeCanonicalMap[resourceType]; ok && item.ResourceId != nil && *item.ResourceId != "" {
		externalResourceId = common.BuildExternalResourceId("aws", account.AccountNumber, region, serviceName, canonicalType, *item.ResourceId, "")
	}

	return providers.Recommendation{
		CategoryName:        providers.RecommendationCategoryRightSizing,
		RuleName:            ruleName,
		Severity:            severity,
		Savings:             savings,
		Action:              mapCOHActionToRecommendationAction(actionType),
		Data:                data,
		ResourceServiceName: serviceName,
		ResourceId:          resourceId,
		ResourceType:        resourceType,
		ResourceRegion:      region,
		ExternalResourceId:  externalResourceId,
		DedupeGroup:         dedupeGroup,
	}
}

func mapCOHActionToRuleName(actionType string) string {
	switch cohtypes.ActionType(actionType) {
	case cohtypes.ActionTypePurchaseSavingsPlans:
		return "aws_native_purchase_savings_plans"
	case cohtypes.ActionTypePurchaseReservedInstances:
		return "aws_native_purchase_reserved_instances"
	case cohtypes.ActionTypeRightsize:
		return "aws_native_rightsize"
	case cohtypes.ActionTypeStop:
		return "aws_native_stop"
	case cohtypes.ActionTypeUpgrade:
		return "aws_native_upgrade"
	case cohtypes.ActionTypeDelete:
		return "aws_native_delete"
	case cohtypes.ActionTypeMigrateToGraviton:
		return "aws_native_migrate_graviton"
	default:
		return fmt.Sprintf("aws_native_coh_%s", actionType)
	}
}

func mapCOHActionToRecommendationAction(actionType string) providers.RecommendationAction {
	switch cohtypes.ActionType(actionType) {
	case cohtypes.ActionTypeDelete, cohtypes.ActionTypeStop:
		return providers.RecommendationActionDelete
	default:
		return providers.RecommendationActionModify
	}
}

func mapSavingsToSeverity(savings *float64) providers.RecommendationSeverity {
	if savings == nil {
		return providers.RecommendationSeverityLow
	}
	switch {
	case *savings >= 100:
		return providers.RecommendationSeverityHigh
	case *savings >= 20:
		return providers.RecommendationSeverityMedium
	default:
		return providers.RecommendationSeverityLow
	}
}

func (a *awsCostOptimizationHub) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return nil
}

func (a *awsCostOptimizationHub) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, nil
}

func (a *awsCostOptimizationHub) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (a *awsCostOptimizationHub) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	return providers.ServiceMapApplication{}, nil
}
