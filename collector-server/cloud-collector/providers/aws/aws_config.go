package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	//"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/aws/aws-sdk-go-v2/service/configservice/types"
)

func configRecorderStatusToNbStatus(recording bool) providers.ResourceStatus {
	if recording {
		return providers.ResourceStatusActive
	}
	return providers.ResourceStatusInactive
}

func configRuleComplianceToNbStatus(compliance types.ComplianceType) providers.ResourceStatus {
	switch compliance {
	case types.ComplianceTypeCompliant:
		return providers.ResourceStatusActive
	case types.ComplianceTypeNonCompliant:
		return providers.ResourceStatusInactive
	case types.ComplianceTypeNotApplicable, types.ComplianceTypeInsufficientData:
		return providers.ResourceStatusUnknown
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsConfig struct {
	DefaultAwsServiceImpl
}

func (a *awsConfig) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsConfig) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsConfig) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsConfig) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Config delivers configuration snapshots and history to S3, not CloudWatch Logs
	return "", errors.New("AWS Config does not use CloudWatch Logs")
}

func (a *awsConfig) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := configservice.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// Get Configuration Recorders
	recordersResult, err := svc.DescribeConfigurationRecorders(context.TODO(), &configservice.DescribeConfigurationRecordersInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to fetch Config recorders", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return resources, err
	}

	for _, recorder := range recordersResult.ConfigurationRecorders {
		if recorder.Name == nil {
			ctx.GetLogger().Warn("Skipping Config recorder due to missing name")
			continue
		}

		tags := make(map[string][]string)

		// Get recorder status
		statusResult, err := svc.DescribeConfigurationRecorderStatus(context.TODO(), &configservice.DescribeConfigurationRecorderStatusInput{
			ConfigurationRecorderNames: []string{*recorder.Name},
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch Config recorder status", "error", err, "recorderName", *recorder.Name)
		}

		metaMap := structToMap(recorder)

		status := providers.ResourceStatusInactive
		if statusResult != nil && len(statusResult.ConfigurationRecordersStatus) > 0 {
			recorderStatus := statusResult.ConfigurationRecordersStatus[0]
			metaMap["RecorderStatus"] = structToMap(recorderStatus)
			status = configRecorderStatusToNbStatus(recorderStatus.Recording)
		}

		// Get delivery channel info
		channelsResult, err := svc.DescribeDeliveryChannels(context.TODO(), &configservice.DescribeDeliveryChannelsInput{})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch Config delivery channels", "error", err, "recorderName", *recorder.Name)
		} else if channelsResult.DeliveryChannels != nil {
			metaMap["DeliveryChannels"] = channelsResult.DeliveryChannels
		}

		recorderArn := fmt.Sprintf("arn:aws:config:%s:%s:config-recorder/%s", region, account.AccountNumber, *recorder.Name)

		resource := providers.Resource{
			Id:          *recorder.Name,
			ServiceName: ServiceNameConfig,
			Name:        *recorder.Name,
			Status:      status,
			Region:      region,
			Tags:        tags,
			Meta:        metaMap,
			Arn:         recorderArn,
			CreatedAt:   time.Time{},
			Type:        getAwsServiceResourceType(ServiceNameConfig, "recorder"),
		}
		resources = append(resources, resource)
	}

	// Get Config Rules
	rulesPaginator := configservice.NewDescribeConfigRulesPaginator(svc, &configservice.DescribeConfigRulesInput{})
	for rulesPaginator.HasMorePages() {
		result, err := rulesPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch Config rules", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, rule := range result.ConfigRules {
			if rule.ConfigRuleName == nil {
				ctx.GetLogger().Warn("Skipping Config rule due to missing name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the rule
			if rule.ConfigRuleArn != nil {
				tagsResult, err := svc.ListTagsForResource(context.TODO(), &configservice.ListTagsForResourceInput{
					ResourceArn: rule.ConfigRuleArn,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch Config rule tags", "error", err, "ruleArn", *rule.ConfigRuleArn)
				} else if tagsResult.Tags != nil {
					for _, tag := range tagsResult.Tags {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
						}
					}
				}
			}

			metaMap := structToMap(rule)

			// Get compliance information
			complianceResult, err := svc.DescribeComplianceByConfigRule(context.TODO(), &configservice.DescribeComplianceByConfigRuleInput{
				ConfigRuleNames: []string{*rule.ConfigRuleName},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch Config rule compliance", "error", err, "ruleName", *rule.ConfigRuleName)
			} else if len(complianceResult.ComplianceByConfigRules) > 0 {
				compliance := complianceResult.ComplianceByConfigRules[0]
				metaMap["Compliance"] = structToMap(compliance)
			}

			// Get rule evaluation status
			evalStatusResult, err := svc.DescribeConfigRuleEvaluationStatus(context.TODO(), &configservice.DescribeConfigRuleEvaluationStatusInput{
				ConfigRuleNames: []string{*rule.ConfigRuleName},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch Config rule evaluation status", "error", err, "ruleName", *rule.ConfigRuleName)
			} else if len(evalStatusResult.ConfigRulesEvaluationStatus) > 0 {
				evalStatus := evalStatusResult.ConfigRulesEvaluationStatus[0]
				metaMap["EvaluationStatus"] = structToMap(evalStatus)
			}

			status := providers.ResourceStatusActive
			if rule.ConfigRuleState == types.ConfigRuleStateDeleting {
				status = providers.ResourceStatusDeleted
			}

			// Override status based on compliance
			if compliance, ok := metaMap["Compliance"].(map[string]any); ok {
				if complianceType, ok := compliance["ComplianceType"].(string); ok {
					status = configRuleComplianceToNbStatus(types.ComplianceType(complianceType))
				}
			}

			ruleArn := ""
			if rule.ConfigRuleArn != nil {
				ruleArn = *rule.ConfigRuleArn
			}

			resource := providers.Resource{
				Id:          *rule.ConfigRuleName,
				ServiceName: ServiceNameConfig,
				Name:        *rule.ConfigRuleName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         ruleArn,
				CreatedAt:   time.Time{},
				Type:        getAwsServiceResourceType(ServiceNameConfig, "rule"),
			}
			resources = append(resources, resource)
		}
	}

	// Get Aggregators
	aggregatorsResult, err := svc.DescribeConfigurationAggregators(context.TODO(), &configservice.DescribeConfigurationAggregatorsInput{})
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch Config aggregators", "error", err, "accountNumber", account.AccountNumber, "region", region)
	} else {
		for _, aggregator := range aggregatorsResult.ConfigurationAggregators {
			if aggregator.ConfigurationAggregatorName == nil {
				ctx.GetLogger().Warn("Skipping Config aggregator due to missing name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the aggregator
			if aggregator.ConfigurationAggregatorArn != nil {
				tagsResult, err := svc.ListTagsForResource(context.TODO(), &configservice.ListTagsForResourceInput{
					ResourceArn: aggregator.ConfigurationAggregatorArn,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch Config aggregator tags", "error", err, "aggregatorArn", *aggregator.ConfigurationAggregatorArn)
				} else if tagsResult.Tags != nil {
					for _, tag := range tagsResult.Tags {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
						}
					}
				}
			}

			metaMap := structToMap(aggregator)

			aggregatorArn := ""
			if aggregator.ConfigurationAggregatorArn != nil {
				aggregatorArn = *aggregator.ConfigurationAggregatorArn
			}

			createdAt := time.Time{}
			if aggregator.CreationTime != nil {
				createdAt = *aggregator.CreationTime
			}

			resource := providers.Resource{
				Id:          *aggregator.ConfigurationAggregatorName,
				ServiceName: ServiceNameConfig,
				Name:        *aggregator.ConfigurationAggregatorName,
				Status:      providers.ResourceStatusActive,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         aggregatorArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameConfig, "aggregator"),
			}
			resources = append(resources, resource)
		}
	}

	// Get Conformance Packs
	conformancePacksPaginator := configservice.NewDescribeConformancePacksPaginator(svc, &configservice.DescribeConformancePacksInput{})
	for conformancePacksPaginator.HasMorePages() {
		result, err := conformancePacksPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch Config conformance packs", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, pack := range result.ConformancePackDetails {
			if pack.ConformancePackName == nil {
				ctx.GetLogger().Warn("Skipping Config conformance pack due to missing name")
				continue
			}

			tags := make(map[string][]string)

			metaMap := structToMap(pack)

			// Get conformance pack compliance
			complianceResult, err := svc.DescribeConformancePackCompliance(context.TODO(), &configservice.DescribeConformancePackComplianceInput{
				ConformancePackName: pack.ConformancePackName,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch conformance pack compliance", "error", err, "packName", *pack.ConformancePackName)
			} else if complianceResult.ConformancePackRuleComplianceList != nil {
				metaMap["RuleCompliance"] = complianceResult.ConformancePackRuleComplianceList

				// Count compliant vs non-compliant rules
				compliantCount := 0
				nonCompliantCount := 0
				for _, ruleCompliance := range complianceResult.ConformancePackRuleComplianceList {
					switch ruleCompliance.ComplianceType {
					case types.ConformancePackComplianceTypeCompliant:
						compliantCount++
					case types.ConformancePackComplianceTypeNonCompliant:
						nonCompliantCount++
					}
				}
				metaMap["CompliantRulesCount"] = compliantCount
				metaMap["NonCompliantRulesCount"] = nonCompliantCount
			}

			packArn := ""
			if pack.ConformancePackArn != nil {
				packArn = *pack.ConformancePackArn
			}

			createdAt := time.Time{}
			if pack.CreatedBy != nil {
				// ConformancePacks don't have CreatedAt, using placeholder
				createdAt = time.Time{}
			}

			resource := providers.Resource{
				Id:          *pack.ConformancePackName,
				ServiceName: ServiceNameConfig,
				Name:        *pack.ConformancePackName,
				Status:      providers.ResourceStatusActive,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         packArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameConfig, "conformancepack"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsConfig) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	hasRecorder := false
	recorderRecording := false

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if Config recorder is not enabled or not recording
		if resource.Type == getAwsServiceResourceType(ServiceNameConfig, "recorder") {
			hasRecorder = true

			if recorderStatus, ok := meta["RecorderStatus"].(map[string]any); ok {
				if recording, ok := recorderStatus["Recording"].(bool); ok && recording {
					recorderRecording = true
				}
			}

			if !recorderRecording {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_config_recorder_not_recording",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":        "AWS Config recorder is not actively recording",
						"recommendation": "Enable Config recorder to track resource configuration changes",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check if delivery channel is configured
			if _, hasChannel := meta["DeliveryChannels"]; !hasChannel {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_config_no_delivery_channel",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":        "Config recorder has no delivery channel configured",
						"recommendation": "Configure a delivery channel to store configuration snapshots",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check if recording all resource types
			if recordingGroup, ok := meta["RecordingGroup"].(map[string]any); ok {
				if allSupported, ok := recordingGroup["AllSupported"].(bool); ok && !allSupported {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "aws_config_not_recording_all_resources",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "Config is not recording all supported resource types",
							"recommendation": "Enable recording of all resource types for comprehensive compliance monitoring",
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

		// Check for non-compliant Config rules
		if resource.Type == getAwsServiceResourceType(ServiceNameConfig, "rule") {
			if compliance, ok := meta["Compliance"].(map[string]any); ok {
				if complianceType, ok := compliance["ComplianceType"].(string); ok {
					if strings.ToUpper(complianceType) == "NON_COMPLIANT" {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     "aws_config_rule_non_compliant",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"message":        fmt.Sprintf("Config rule '%s' is non-compliant", resource.Name),
								"recommendation": "Review and remediate the non-compliant resources",
								"ruleName":       resource.Name,
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

			// Check for rules with evaluation errors
			if evalStatus, ok := meta["EvaluationStatus"].(map[string]any); ok {
				if lastErrorCode, ok := evalStatus["LastErrorCode"].(string); ok && lastErrorCode != "" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_config_rule_evaluation_error",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        fmt.Sprintf("Config rule '%s' has evaluation errors", resource.Name),
							"errorCode":      lastErrorCode,
							"errorMessage":   evalStatus["LastErrorMessage"],
							"recommendation": "Fix the configuration or permissions for this rule",
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

		// Check conformance pack compliance
		if resource.Type == getAwsServiceResourceType(ServiceNameConfig, "conformancepack") {
			if nonCompliantCount, ok := meta["NonCompliantRulesCount"].(int); ok && nonCompliantCount > 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_config_conformance_pack_non_compliant",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":           fmt.Sprintf("Conformance pack '%s' has %d non-compliant rules", resource.Name, nonCompliantCount),
						"nonCompliantCount": nonCompliantCount,
						"recommendation":    "Review and remediate non-compliant rules in the conformance pack",
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

	// If no Config recorder found at all
	if !hasRecorder {
		recommendation := providers.Recommendation{
			CategoryName: providers.RecommendationCategorySecurity,
			RuleName:     "aws_config_not_enabled",
			Severity:     providers.RecommendationSeverityCritical,
			Savings:      0,
			Data: map[string]any{
				"message":        "AWS Config is not enabled in this region",
				"recommendation": "Enable AWS Config to track resource configurations and ensure compliance",
			},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: ServiceNameConfig,
			ResourceId:          "config-" + account.AccountNumber,
			ResourceType:        getAwsServiceResourceType(ServiceNameConfig, "recorder"),
			ResourceRegion:      filter.ServiceName,
		}
		recommendations = append(recommendations, recommendation)
	}

	return recommendations, nil
}

func (a *awsConfig) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "config",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// Placeholder for fetching service map details.
	// You can add logic here to describe the config resource and its relationships.

	return app, nil
}
