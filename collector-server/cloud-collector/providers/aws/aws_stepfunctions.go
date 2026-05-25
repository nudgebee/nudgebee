package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	//	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

func stepFunctionsStatusToNbStatus(status types.StateMachineStatus) providers.ResourceStatus {
	switch status {
	case types.StateMachineStatusActive:
		return providers.ResourceStatusActive
	case types.StateMachineStatusDeleting:
		return providers.ResourceStatusDeleted
	default:
		return providers.ResourceStatusUnknown
	}
}

func executionStatusToNbStatus(status types.ExecutionStatus) providers.ResourceStatus {
	switch status {
	case types.ExecutionStatusRunning:
		return providers.ResourceStatusActive
	case types.ExecutionStatusSucceeded:
		return providers.ResourceStatusActive
	case types.ExecutionStatusFailed, types.ExecutionStatusTimedOut, types.ExecutionStatusAborted:
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsStepFunctions struct {
	DefaultAwsServiceImpl
}

func (a *awsStepFunctions) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsStepFunctions) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsStepFunctions) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsStepFunctions) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Step Functions uses CloudWatch Logs for execution history
	return fmt.Sprintf("/aws/vendedlogs/states/%s", resourceId), nil
}

func (a *awsStepFunctions) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := sfn.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List all state machines
	paginator := sfn.NewListStateMachinesPaginator(svc, &sfn.ListStateMachinesInput{})
	for paginator.HasMorePages() {
		result, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch step functions state machines", "error", err, "accountNumber", account.AccountNumber, "region", region)
			return resources, err
		}

		for _, sm := range result.StateMachines {
			if sm.StateMachineArn == nil || sm.Name == nil {
				ctx.GetLogger().Warn("Skipping Step Functions state machine due to missing ARN or name")
				continue
			}

			// Get detailed information about the state machine
			describeResult, err := svc.DescribeStateMachine(context.TODO(), &sfn.DescribeStateMachineInput{
				StateMachineArn: sm.StateMachineArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe state machine", "error", err, "stateMachineArn", *sm.StateMachineArn)
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the state machine
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &sfn.ListTagsForResourceInput{
				ResourceArn: sm.StateMachineArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch step functions tags", "error", err, "stateMachineArn", *sm.StateMachineArn)
			} else if tagsResult.Tags != nil {
				for _, tag := range tagsResult.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(describeResult)

			// Get recent executions for this state machine
			executionsResult, err := svc.ListExecutions(context.TODO(), &sfn.ListExecutionsInput{
				StateMachineArn: sm.StateMachineArn,
				MaxResults:      10, // Last 10 executions
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch step functions executions", "error", err, "stateMachineArn", *sm.StateMachineArn)
			} else {
				executions := []map[string]any{}
				failedCount := 0
				succeededCount := 0
				runningCount := 0

				for _, exec := range executionsResult.Executions {
					execMap := structToMap(exec)
					executions = append(executions, execMap)

					switch exec.Status {
					case types.ExecutionStatusFailed, types.ExecutionStatusTimedOut, types.ExecutionStatusAborted:
						failedCount++
					case types.ExecutionStatusSucceeded:
						succeededCount++
					case types.ExecutionStatusRunning:
						runningCount++
					}
				}

				metaMap["RecentExecutions"] = executions
				metaMap["ExecutionStats"] = map[string]int{
					"Failed":    failedCount,
					"Succeeded": succeededCount,
					"Running":   runningCount,
					"Total":     len(executionsResult.Executions),
				}
			}

			createdAt := time.Time{}
			if describeResult.CreationDate != nil {
				createdAt = *describeResult.CreationDate
			}

			status := providers.ResourceStatusUnknown
			if describeResult.Status != "" {
				status = stepFunctionsStatusToNbStatus(describeResult.Status)
			}

			resource := providers.Resource{
				Id:          *sm.StateMachineArn,
				ServiceName: ServiceNameStepFunctions,
				Name:        *sm.Name,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         *sm.StateMachineArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameStepFunctions, "statemachine"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsStepFunctions) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for high failure rate in recent executions
		if execStats, ok := meta["ExecutionStats"].(map[string]int); ok {
			total := execStats["Total"]
			failed := execStats["Failed"]

			if total > 0 {
				failureRate := float64(failed) / float64(total)
				if failureRate > 0.5 { // More than 50% failure rate
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_stepfunctions_high_failure_rate",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"message":     fmt.Sprintf("State machine has high failure rate: %d/%d executions failed", failed, total),
							"failureRate": fmt.Sprintf("%.2f%%", failureRate*100),
							"failed":      failed,
							"total":       total,
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

		// Check for logging configuration
		if loggingConfig, ok := meta["LoggingConfiguration"].(map[string]any); ok {
			if level, ok := loggingConfig["Level"].(string); ok {
				if strings.ToUpper(level) == "OFF" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_stepfunctions_logging_disabled",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "CloudWatch Logs logging is disabled",
							"recommendation": "Enable logging for better troubleshooting and monitoring",
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
		} else {
			// No logging configuration found
			recommendation := providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "aws_stepfunctions_logging_not_configured",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"message":        "CloudWatch Logs logging is not configured",
					"recommendation": "Configure logging for better troubleshooting and monitoring",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			}
			recommendations = append(recommendations, recommendation)
		}

		// Check for tracing configuration
		if tracingConfig, ok := meta["TracingConfiguration"].(map[string]any); ok {
			if enabled, ok := tracingConfig["Enabled"].(bool); !ok || !enabled {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_stepfunctions_xray_tracing_disabled",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"message":        "X-Ray tracing is disabled",
						"recommendation": "Enable X-Ray tracing for better performance monitoring and debugging",
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

		// Check for state machine type (Express vs Standard)
		if smType, ok := meta["Type"].(string); ok {
			if strings.ToUpper(smType) == "EXPRESS" {
				// Express workflows have limitations
				if execStats, ok := meta["ExecutionStats"].(map[string]int); ok {
					if execStats["Total"] == 0 {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "aws_stepfunctions_express_not_utilized",
							Severity:     providers.RecommendationSeverityLow,
							Savings:      0,
							Data: map[string]any{
								"message":        "Express workflow has no recent executions",
								"recommendation": "Consider deleting unused state machines or switch to Standard type for infrequent executions",
							},
							Action:              providers.RecommendationActionDelete,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						}
						recommendations = append(recommendations, recommendation)
					}
				}
			}
		}

		// Check for definition size (large definitions can be hard to maintain)
		if definition, ok := meta["Definition"].(string); ok {
			if len(definition) > 100000 { // > 100KB
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_stepfunctions_large_definition",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"message":        fmt.Sprintf("State machine definition is very large (%d bytes)", len(definition)),
						"recommendation": "Consider breaking down the workflow into smaller, more manageable state machines",
						"definitionSize": len(definition),
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

func (a *awsStepFunctions) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "stepfunctions",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
