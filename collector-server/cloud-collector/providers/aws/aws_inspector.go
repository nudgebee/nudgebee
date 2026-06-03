package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	"github.com/aws/aws-sdk-go-v2/service/inspector2/types"
)

func inspectorFindingStatusToNbStatus(status types.FindingStatus) providers.ResourceStatus {
	switch status {
	case types.FindingStatusActive:
		return providers.ResourceStatusActive
	case types.FindingStatusClosed, types.FindingStatusSuppressed:
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsInspector struct {
	DefaultAwsServiceImpl
}

func (a *awsInspector) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsInspector) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsInspector) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsInspector) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Inspector doesn't have direct CloudWatch Logs integration
	return "", errors.New("inspector does not have CloudWatch Logs integration")
}

func (a *awsInspector) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := inspector2.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// Check if Inspector is enabled in this account
	statusResult, err := svc.BatchGetAccountStatus(context.TODO(), &inspector2.BatchGetAccountStatusInput{
		AccountIds: []string{account.AccountNumber},
	})
	if err != nil {
		// Account-level "Inspector isn't enabled / IAM doesn't grant access" responses
		// surface as AccessDeniedException. Treat as a benign empty result so we don't
		// double-log at the umbrella site or page someone for a known account config.
		if _, _, _, isPermErr := IsAWSPermissionError(err); isPermErr {
			ctx.GetLogger().Info("Inspector not accessible in this region, skipping", "accountNumber", account.AccountNumber, "region", region)
			return resources, nil
		}
		ctx.GetLogger().Error("failed to get Inspector status", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return resources, err
	}

	inspectorEnabled := false
	accountMeta := map[string]any{
		"InspectorEnabled": inspectorEnabled,
	}

	if len(statusResult.Accounts) > 0 {
		accountState := statusResult.Accounts[0]
		if accountState.ResourceState != nil && accountState.ResourceState.Ec2.Status == types.StatusEnabled {
			inspectorEnabled = true
		}
		if accountState.ResourceState != nil {
			accountMeta["AccountStatus"] = "enabled"
		}
		if accountState.ResourceState != nil {
			accountMeta["Ec2Status"] = string(accountState.ResourceState.Ec2.Status)
			accountMeta["EcrStatus"] = string(accountState.ResourceState.Ecr.Status)
			accountMeta["LambdaStatus"] = string(accountState.ResourceState.Lambda.Status)
			accountMeta["LambdaCodeStatus"] = string(accountState.ResourceState.LambdaCode.Status)
		}
		accountMeta["InspectorEnabled"] = inspectorEnabled
	}

	// Store account status as a resource
	accountResource := providers.Resource{
		Id:          "inspector-account-" + account.AccountNumber,
		ServiceName: ServiceNameInspector,
		Name:        "Inspector Account Status",
		Status:      providers.ResourceStatusActive,
		Region:      region,
		Tags:        make(map[string][]string),
		Meta:        accountMeta,
		Arn:         "",
		CreatedAt:   time.Time{},
		Type:        getAwsServiceResourceType(ServiceNameInspector, "account"),
	}
	resources = append(resources, accountResource)

	if !inspectorEnabled {
		ctx.GetLogger().Warn("Inspector is not enabled in this account", "accountNumber", account.AccountNumber, "region", region)
		return resources, nil
	}

	// Get findings - use paginator for large result sets
	paginator := inspector2.NewListFindingsPaginator(svc, &inspector2.ListFindingsInput{
		MaxResults: aws.Int32(100),
		FilterCriteria: &types.FilterCriteria{
			FindingStatus: []types.StringFilter{
				{
					Comparison: types.StringComparisonEquals,
					Value:      aws.String("ACTIVE"),
				},
			},
		},
	})

	findingCount := 0
	for paginator.HasMorePages() && findingCount < 1000 { // Limit to 1000 findings
		result, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to list Inspector findings", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, finding := range result.Findings {
			if finding.FindingArn == nil {
				ctx.GetLogger().Warn("Skipping Inspector finding due to missing ARN")
				continue
			}

			tags := make(map[string][]string)

			metaMap := structToMap(finding)

			// Extract severity information
			severity := "INFORMATIONAL"
			if finding.Severity != "" {
				severity = string(finding.Severity)
			}

			// Extract resource information
			resourceType := "unknown"
			if len(finding.Resources) > 0 {
				foundResource := finding.Resources[0]
				if foundResource.Type != "" {
					resourceType = string(foundResource.Type)
				}

				// Get resource tags if available
				if foundResource.Tags != nil {
					for k, v := range foundResource.Tags {
						tags[k] = append(tags[k], v)
					}
				}
			}

			metaMap["Severity"] = severity
			metaMap["ResourceType"] = resourceType

			status := providers.ResourceStatusActive
			if finding.Status != "" {
				status = inspectorFindingStatusToNbStatus(finding.Status)
			}

			createdAt := time.Time{}
			if finding.FirstObservedAt != nil {
				createdAt = *finding.FirstObservedAt
			}

			findingName := *finding.FindingArn
			if finding.Title != nil {
				findingName = *finding.Title
			}

			resource := providers.Resource{
				Id:          *finding.FindingArn,
				ServiceName: ServiceNameInspector,
				Name:        findingName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         *finding.FindingArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameInspector, "finding"),
			}
			resources = append(resources, resource)
			findingCount++

			if findingCount >= 1000 {
				break
			}
		}
	}

	// Get coverage statistics
	coverageResult, err := svc.ListCoverage(context.TODO(), &inspector2.ListCoverageInput{
		MaxResults: aws.Int32(100),
	})
	if err != nil {
		ctx.GetLogger().Warn("failed to get Inspector coverage", "error", err, "accountNumber", account.AccountNumber, "region", region)
	} else {
		// Create a summary resource for coverage
		ec2Count := 0
		ecrCount := 0
		lambdaCount := 0

		for _, coverage := range coverageResult.CoveredResources {
			switch coverage.ResourceType {
			case types.CoverageResourceTypeAwsEc2Instance:
				ec2Count++
			case types.CoverageResourceTypeAwsEcrRepository:
				ecrCount++
			case types.CoverageResourceTypeAwsLambdaFunction:
				lambdaCount++
			}
		}

		coverageResource := providers.Resource{
			Id:          "inspector-coverage-" + account.AccountNumber,
			ServiceName: ServiceNameInspector,
			Name:        "Inspector Coverage Summary",
			Status:      providers.ResourceStatusActive,
			Region:      region,
			Tags:        make(map[string][]string),
			Meta: map[string]any{
				"Ec2InstancesCovered":    ec2Count,
				"EcrRepositoriesCovered": ecrCount,
				"LambdaFunctionsCovered": lambdaCount,
				"TotalResourcesCovered":  len(coverageResult.CoveredResources),
			},
			Arn:       "",
			CreatedAt: time.Time{},
			Type:      getAwsServiceResourceType(ServiceNameInspector, "coverage"),
		}
		resources = append(resources, coverageResource)
	}

	return resources, nil
}

func (a *awsInspector) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if Inspector is not enabled
		if resource.Type == getAwsServiceResourceType(ServiceNameInspector, "account") {
			if enabled, ok := meta["InspectorEnabled"].(bool); ok && !enabled {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_inspector_not_enabled",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":        "Amazon Inspector is not enabled",
						"recommendation": "Enable Inspector to continuously scan for software vulnerabilities and network exposure",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check if specific resource types are not enabled
			if ec2Status, ok := meta["Ec2Status"].(string); ok && strings.ToUpper(ec2Status) != "ENABLED" {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_inspector_ec2_not_enabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Inspector EC2 scanning is not enabled",
						"recommendation": "Enable EC2 scanning to detect vulnerabilities in EC2 instances",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			if ecrStatus, ok := meta["EcrStatus"].(string); ok && strings.ToUpper(ecrStatus) != "ENABLED" {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_inspector_ecr_not_enabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Inspector ECR scanning is not enabled",
						"recommendation": "Enable ECR scanning to detect vulnerabilities in container images",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			if lambdaStatus, ok := meta["LambdaStatus"].(string); ok && strings.ToUpper(lambdaStatus) != "ENABLED" {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_inspector_lambda_not_enabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Inspector Lambda scanning is not enabled",
						"recommendation": "Enable Lambda scanning to detect vulnerabilities in Lambda functions",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}

		// Check for active critical/high severity findings
		if resource.Type == getAwsServiceResourceType(ServiceNameInspector, "finding") {
			if severity, ok := meta["Severity"].(string); ok {
				severityUpper := strings.ToUpper(severity)
				if severityUpper == "CRITICAL" || severityUpper == "HIGH" {
					recommendationSeverity := providers.RecommendationSeverityHigh
					if severityUpper == "CRITICAL" {
						recommendationSeverity = providers.RecommendationSeverityCritical
					}

					description := "Security vulnerability found"
					if desc, ok := meta["Description"].(string); ok {
						description = desc
					}

					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "aws_inspector_critical_finding",
						Severity:     recommendationSeverity,
						Savings:      0,
						Data: map[string]any{
							"message":         description,
							"findingSeverity": severity,
							"recommendation":  "Review and remediate this security finding as soon as possible",
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					}
					recommendations = append(recommendations, recommendation)
				}
			}

			// Check for long-standing findings (older than 30 days)
			if resource.CreatedAt.Before(time.Now().AddDate(0, 0, -30)) {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_inspector_old_finding",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Security finding has been active for more than 30 days",
						"firstObserved":  resource.CreatedAt.Format(time.RFC3339),
						"recommendation": "Prioritize remediation of long-standing security findings",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}

		// Check coverage statistics
		if resource.Type == getAwsServiceResourceType(ServiceNameInspector, "coverage") {
			if totalCovered, ok := meta["TotalResourcesCovered"].(int); ok && totalCovered == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_inspector_no_coverage",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Inspector is enabled but not covering any resources",
						"recommendation": "Ensure resources are tagged or configured correctly for Inspector scanning",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		}
	}

	return recommendations, nil
}

func (a *awsInspector) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "inspector",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
