package aws

import (
	"errors"
	"fmt"
	"math"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Helper function to map DynamoDB status strings to provider status
func dynamoDBStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	// Common statuses: CREATING, UPDATING, DELETING, ACTIVE, ARCHIVING, ARCHIVED, INACCESSIBLE_ENCRYPTION_CREDENTIALS
	s := strings.ToLower(*status)
	switch s {
	case "active":
		return providers.ResourceStatusActive
	case "creating", "updating", "archiving":
		return providers.ResourceStatusUnknown
	case "deleting":
		return providers.ResourceStatusDeleted // Or Inactive
	case "archived", "inaccessible_encryption_credentials":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type amazonDynamoDB struct {
	DefaultAwsServiceImpl
}

func (a *amazonDynamoDB) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonDynamoDB) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonDynamoDB) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonDynamoDB) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameDynamoDB)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := dynamodb.NewFromConfig(cfg)
	resources := []providers.Resource{}
	paginator := dynamodb.NewListTablesPaginator(svc, &dynamodb.ListTablesInput{})

	for paginator.HasMorePages() {
		tablesOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list dynamodb tables", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, tableName := range tablesOutput.TableNames {
			tags := make(map[string][]string)
			var meta map[string]any

			describeOutput, err := svc.DescribeTable(ctx.GetContext(), &dynamodb.DescribeTableInput{
				TableName: &tableName,
			})
			if err != nil {
				ctx.GetLogger().Info("failed to describe dynamodb table", "error", err, "tableName", tableName, "accountNumber", account.AccountNumber, "region", regionName)
				continue
			}

			if describeOutput.Table == nil {
				ctx.GetLogger().Info("DescribeTable output.Table is nil, skipping DynamoDB table", "tableName", tableName, "region", regionName)
				continue
			}
			tableDetails := describeOutput.Table

			if tableDetails.TableArn == nil {
				ctx.GetLogger().Info("Skipping DynamoDB table due to missing TableArn in describe response", "tableName", tableName, "region", regionName)
				continue
			}
			if tableDetails.TableStatus == "" {
				ctx.GetLogger().Info("TableStatus is nil for DynamoDB table, will default to Unknown", "tableName", tableName, "tableArn", *tableDetails.TableArn, "region", regionName)
			}
			if tableDetails.CreationDateTime == nil {
				ctx.GetLogger().Info("CreationDateTime is nil for DynamoDB table, will default to zero time", "tableName", tableName, "tableArn", *tableDetails.TableArn, "region", regionName)
			}

			meta = structToMap(tableDetails)

			pitrOutput, err := svc.DescribeContinuousBackups(ctx.GetContext(), &dynamodb.DescribeContinuousBackupsInput{
				TableName: &tableName,
			})
			if err != nil {
				ctx.GetLogger().Info("failed to describe dynamodb continuous backups", "error", err, "tableName", tableName, "region", regionName)
			} else if pitrOutput != nil && pitrOutput.ContinuousBackupsDescription != nil {
				meta["ContinuousBackupsDescription"] = structToMap(pitrOutput.ContinuousBackupsDescription)
			} else {
				ctx.GetLogger().Info("ContinuousBackupsDescription is nil in pitrOutput or pitrOutput itself is nil", "tableName", tableName, "region", regionName)
			}

			// Fetch TTL description
			ttlOutput, err := svc.DescribeTimeToLive(ctx.GetContext(), &dynamodb.DescribeTimeToLiveInput{
				TableName: &tableName,
			})
			if err != nil {
				ctx.GetLogger().Info("failed to describe dynamodb ttl", "error", err, "tableName", tableName, "region", regionName)
			} else if ttlOutput != nil && ttlOutput.TimeToLiveDescription != nil {
				meta["TimeToLiveDescription"] = structToMap(ttlOutput.TimeToLiveDescription)
			}

			// Fetch auto-scaling configuration (for provisioned mode)
			appAutoScalingSvc := applicationautoscaling.NewFromConfig(cfg)
			scalableTargetsOutput, err := appAutoScalingSvc.DescribeScalableTargets(ctx.GetContext(), &applicationautoscaling.DescribeScalableTargetsInput{
				ServiceNamespace:  "dynamodb",
				ResourceIds:       []string{fmt.Sprintf("table/%s", tableName)},
				ScalableDimension: "dynamodb:table:ReadCapacityUnits",
			})
			if err != nil {
				ctx.GetLogger().Info("failed to describe dynamodb auto-scaling targets (read)", "error", err, "tableName", tableName, "region", regionName)
			} else if scalableTargetsOutput != nil && len(scalableTargetsOutput.ScalableTargets) > 0 {
				meta["AutoScalingReadTargets"] = structToMap(scalableTargetsOutput.ScalableTargets)
			}

			scalableTargetsOutputWrite, err := appAutoScalingSvc.DescribeScalableTargets(ctx.GetContext(), &applicationautoscaling.DescribeScalableTargetsInput{
				ServiceNamespace:  "dynamodb",
				ResourceIds:       []string{fmt.Sprintf("table/%s", tableName)},
				ScalableDimension: "dynamodb:table:WriteCapacityUnits",
			})
			if err != nil {
				ctx.GetLogger().Info("failed to describe dynamodb auto-scaling targets (write)", "error", err, "tableName", tableName, "region", regionName)
			} else if scalableTargetsOutputWrite != nil && len(scalableTargetsOutputWrite.ScalableTargets) > 0 {
				meta["AutoScalingWriteTargets"] = structToMap(scalableTargetsOutputWrite.ScalableTargets)
			}

			tagsOutput, err := svc.ListTagsOfResource(ctx.GetContext(), &dynamodb.ListTagsOfResourceInput{
				ResourceArn: tableDetails.TableArn,
			})
			if err != nil {
				ctx.GetLogger().Info("failed to fetch tags for dynamodb table", "error", err, "tableArn", *tableDetails.TableArn, "region", regionName)
			} else {
				if tagsOutput.Tags != nil {
					for _, tag := range tagsOutput.Tags {
						if tag.Key != nil && tag.Value != nil {
							tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
						}
					}
				} else {
					ctx.GetLogger().Info("tagsOutput.Tags is nil for table", "tableArn", *tableDetails.TableArn, "region", regionName)
				}
			}

			createdAt := time.Time{}
			if tableDetails.CreationDateTime != nil {
				createdAt = *tableDetails.CreationDateTime
			}

			resource := providers.Resource{
				Id:          tableName,
				ServiceName: ServiceNameDynamoDB,
				Name:        tableName,
				Status:      dynamoDBStatusToNbStatus((*string)(&tableDetails.TableStatus)),
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *tableDetails.TableArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameDynamoDB, "table"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonDynamoDB) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameDynamoDB)
	// 	return recommendations, err
	// }

	for _, resource := range existingResources {
		// Ensure we are looking at DynamoDB Tables
		if resource.Type != getAwsServiceResourceType(ServiceNameDynamoDB, "table") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for DynamoDB table", "tableArn", resource.Arn, "region", resource.Region)
			continue
		}

		// Check 1: Point-in-Time Recovery (PITR) Enabled
		pitrEnabled := false
		pitrDescAny, pitrDescOk := meta["ContinuousBackupsDescription"]
		if pitrDescOk {
			pitrDesc, pitrDescMapOk := pitrDescAny.(map[string]any)
			if pitrDescMapOk {
				statusAny, statusOk := pitrDesc["PointInTimeRecoveryDescription"]
				if statusOk {
					statusMap, statusMapOk := statusAny.(map[string]any)
					if statusMapOk {
						pitrStatusStr, pitrStatusTypeOk := statusMap["PointInTimeRecoveryStatus"].(string)
						if pitrStatusTypeOk && pitrStatusStr == string(types.PointInTimeRecoveryStatusEnabled) {
							pitrEnabled = true
						} else if !pitrStatusTypeOk && statusMap["PointInTimeRecoveryStatus"] != nil {
							ctx.GetLogger().Info("PointInTimeRecoveryStatus is not a string", "tableArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", statusMap["PointInTimeRecoveryStatus"]))
						}
					} else {
						ctx.GetLogger().Info("PointInTimeRecoveryDescription is not a map", "tableArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", statusAny))
					}
				} // If statusOk is false, PointInTimeRecoveryDescription key is missing.
			} else {
				ctx.GetLogger().Info("ContinuousBackupsDescription is not a map", "tableArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", pitrDescAny))
			}
		} // If !pitrDescOk, ContinuousBackupsDescription key is missing.

		if !pitrEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_dynamodb_pitr_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"table_name": resource.Name, "table_arn": resource.Arn, "reason": "Point-in-time recovery (PITR) is not enabled or status could not be verified."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Server-Side Encryption (SSE) with KMS CMK
		sseEnabledOverall := false
		sseUsingCMK := false
		sseDescAny, sseDescOk := meta["SSEDescription"]
		if sseDescOk {
			sseDesc, sseDescMapOk := sseDescAny.(map[string]any)
			if sseDescMapOk {
				statusAny, statusOk := sseDesc["Status"]
				if statusOk {
					statusStr, statusTypeOk := statusAny.(string)
					if statusTypeOk && statusStr == string(types.SSEStatusEnabled) {
						sseEnabledOverall = true
						kmsMasterKeyArnAny, kmsKeyOk := sseDesc["KMSMasterKeyArn"]
						if kmsKeyOk {
							kmsMasterKeyArnStr, kmsKeyTypeOk := kmsMasterKeyArnAny.(string)
							if kmsKeyTypeOk && kmsMasterKeyArnStr != "" {
								// Could further validate ARN format or check if it's an AWS managed key like 'alias/aws/dynamodb'
								if !strings.HasPrefix(kmsMasterKeyArnStr, "alias/aws/dynamodb") { // Basic check
									sseUsingCMK = true
								}
							} else if !kmsKeyTypeOk && kmsMasterKeyArnAny != nil {
								ctx.GetLogger().Info("SSEDescription.KMSMasterKeyArn is not a string", "tableArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", kmsMasterKeyArnAny))
							}
						} // If !kmsKeyOk, KMSMasterKeyArn is missing, implies AWS owned key if SSE is ENABLED.
					} else if !statusTypeOk && statusAny != nil {
						ctx.GetLogger().Info("SSEDescription.Status is not a string", "tableArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", statusAny))
					}
				} // If !statusOk, Status is missing.
			} else {
				ctx.GetLogger().Info("SSEDescription is not a map", "tableArn", resource.Arn, "region", resource.Region, "actualType", fmt.Sprintf("%T", sseDescAny))
			}
		} // If !sseDescOk, SSEDescription is missing.

		if !sseEnabledOverall || !sseUsingCMK {
			reason := "Server-side encryption (SSE) with a Customer Managed Key (CMK) is not enabled."
			if !sseEnabledOverall {
				reason = "Server-side encryption (SSE) does not appear to be enabled."
			} else if sseEnabledOverall && !sseUsingCMK { // SSE is enabled, but with AWS owned key
				reason = "Server-side encryption (SSE) is enabled but not using a Customer Managed Key (CMK). Using default AWS owned key."
			}
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_dynamodb_sse_cmk",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"table_name": resource.Name, "table_arn": resource.Arn, "reason": reason},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Billing Mode (On-Demand vs Provisioned)
		billingMode := ""
		if billingSummary, ok := meta["BillingModeSummary"].(map[string]any); ok {
			if mode, ok := billingSummary["BillingMode"].(string); ok {
				billingMode = mode
			}
		}

		// Analyze capacity mode based on metrics (7-day window)
		startDate := time.Now().Add(-time.Hour * 24 * 7)
		endDate := time.Now()

		if billingMode == string(types.BillingModeProvisioned) {
			// Fetch metrics for provisioned tables
			metricsRequest := providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{
					"ConsumedReadCapacityUnits",
					"ConsumedWriteCapacityUnits",
					"ProvisionedReadCapacityUnits",
					"ProvisionedWriteCapacityUnits",
				},
				Step:       3600 * time.Second, // 1-hour intervals
				Statistics: []string{"Average"},
			}

			metrics, err := a.QueryMetrices(ctx, account, metricsRequest)
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch DynamoDB capacity metrics", "error", err, "tableName", resource.Name, "region", resource.Region)
			} else if len(metrics.Items) >= 4 {
				// Calculate average utilization for read and write
				var consumedRead, consumedWrite, provisionedRead, provisionedWrite []float64

				for _, item := range metrics.Items {
					switch item.Name {
					case "ConsumedReadCapacityUnits":
						consumedRead = item.Values
					case "ConsumedWriteCapacityUnits":
						consumedWrite = item.Values
					case "ProvisionedReadCapacityUnits":
						provisionedRead = item.Values
					case "ProvisionedWriteCapacityUnits":
						provisionedWrite = item.Values
					}
				}

				if len(consumedRead) > 0 && len(provisionedRead) > 0 && len(consumedWrite) > 0 && len(provisionedWrite) > 0 {
					// Calculate average utilization
					avgReadUtil := calculateAverageUtilization(consumedRead, provisionedRead)
					avgWriteUtil := calculateAverageUtilization(consumedWrite, provisionedWrite)
					overallUtil := (avgReadUtil + avgWriteUtil) / 2

					// AWS recommendation: Switch to on-demand if utilization < 18%
					if overallUtil < 18.0 {
						// Calculate savings
						// NOTE: Prices are for us-east-1 region. Other regions may be 25-50% more expensive.
						// Provisioned cost: $0.00065 (us-east-1)/hour per RCU, $0.00065/hour per WCU
						// On-demand cost: $0.25 (us-east-1) per million reads, $1.25 (us-east-1) per million writes
						avgProvisionedRead := averageFloat64(provisionedRead)
						avgProvisionedWrite := averageFloat64(provisionedWrite)
						avgConsumedRead := averageFloat64(consumedRead)
						avgConsumedWrite := averageFloat64(consumedWrite)

						// Provisioned monthly cost
						provisionedCost := (avgProvisionedRead * 0.00065 * 730) + (avgProvisionedWrite * 0.00065 * 730)

						// On-demand monthly cost (approximation)
						// 1 RCU = 1 strongly consistent read/sec = 2.628M reads/month
						// 1 WCU = 1 write/sec = 2.628M writes/month
						onDemandCost := (avgConsumedRead * 2.628 * 0.25) + (avgConsumedWrite * 2.628 * 1.25)

						savings := provisionedCost - onDemandCost

						if savings > 0 {
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName: providers.RecommendationCategoryInfraUpgrade,
								RuleName:     "aws_dynamodb_capacity_mode",
								Severity:     providers.RecommendationSeverityMedium,
								Savings:      savings,
								Data: map[string]any{
									"table_name":               resource.Name,
									"table_arn":                resource.Arn,
									"current_mode":             "PROVISIONED",
									"recommended_mode":         "PAY_PER_REQUEST",
									"avg_read_utilization":     avgReadUtil,
									"avg_write_utilization":    avgWriteUtil,
									"overall_utilization":      overallUtil,
									"reason":                   "This table has low and predictable utilization. Switching to on-demand can save costs while still meeting performance needs.",
									"provisioned_monthly_cost": provisionedCost,
									"ondemand_monthly_cost":    onDemandCost,
									"startDate":                startDate.Format(time.RFC3339),
									"endDate":                  endDate.Format(time.RFC3339),
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
			}
		} else if billingMode == string(types.BillingModePayPerRequest) {
			// Fetch metrics for on-demand tables
			metricsRequest := providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id},
				ServiceName: resource.ServiceName,
				StartDate:   &startDate,
				EndDate:     &endDate,
				Region:      resource.Region,
				MetricNames: []string{
					"ConsumedReadCapacityUnits",
					"ConsumedWriteCapacityUnits",
				},
				Step:       3600 * time.Second,
				Statistics: []string{"Average"},
			}

			metrics, err := a.QueryMetrices(ctx, account, metricsRequest)
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch DynamoDB consumption metrics", "error", err, "tableName", resource.Name, "region", resource.Region)
			} else if len(metrics.Items) >= 2 {
				var consumedRead, consumedWrite []float64

				for _, item := range metrics.Items {
					switch item.Name {
					case "ConsumedReadCapacityUnits":
						consumedRead = item.Values
					case "ConsumedWriteCapacityUnits":
						consumedWrite = item.Values
					}
				}

				if len(consumedRead) > 0 && len(consumedWrite) > 0 {
					// Check if usage is steady (low coefficient of variation)
					readCV := coefficientOfVariation(consumedRead)
					writeCV := coefficientOfVariation(consumedWrite)
					avgConsumedRead := averageFloat64(consumedRead)
					avgConsumedWrite := averageFloat64(consumedWrite)

					// If CV < 0.3 (30%), usage is considered predictable
					if readCV < 0.3 && writeCV < 0.3 {
						// Calculate on-demand cost
						onDemandCost := (avgConsumedRead * 2.628 * 0.25) + (avgConsumedWrite * 2.628 * 1.25)

						// Calculate provisioned cost (with some buffer)
						// Add 20% buffer to average consumption
						recommendedRead := avgConsumedRead * 1.2
						recommendedWrite := avgConsumedWrite * 1.2
						provisionedCost := (recommendedRead * 0.00065 * 730) + (recommendedWrite * 0.00065 * 730)

						savings := onDemandCost - provisionedCost

						if savings > 10 { // Only recommend if savings > $10/month
							recommendations = append(recommendations, providers.Recommendation{
								CategoryName: providers.RecommendationCategoryInfraUpgrade,
								RuleName:     "aws_dynamodb_capacity_mode",
								Severity:     providers.RecommendationSeverityMedium,
								Savings:      savings,
								Data: map[string]any{
									"table_name":               resource.Name,
									"table_arn":                resource.Arn,
									"current_mode":             "PAY_PER_REQUEST",
									"recommended_mode":         "PROVISIONED",
									"read_cv":                  readCV,
									"write_cv":                 writeCV,
									"avg_read_consumption":     avgConsumedRead,
									"avg_write_consumption":    avgConsumedWrite,
									"reason":                   "This table has steady and predictable usage. Switching to provisioned with appropriate capacity can save costs while still meeting performance needs.",
									"recommended_read_units":   recommendedRead,
									"recommended_write_units":  recommendedWrite,
									"ondemand_monthly_cost":    onDemandCost,
									"provisioned_monthly_cost": provisionedCost,
									"startDate":                startDate.Format(time.RFC3339),
									"endDate":                  endDate.Format(time.RFC3339),
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
			}
		}

		// Check 4: Auto-scaling disabled for provisioned tables
		if billingMode == string(types.BillingModeProvisioned) {
			hasReadAutoScaling := false
			hasWriteAutoScaling := false

			if autoScalingRead, ok := meta["AutoScalingReadTargets"].([]any); ok && len(autoScalingRead) > 0 {
				hasReadAutoScaling = true
			}
			if autoScalingWrite, ok := meta["AutoScalingWriteTargets"].([]any); ok && len(autoScalingWrite) > 0 {
				hasWriteAutoScaling = true
			}

			if !hasReadAutoScaling || !hasWriteAutoScaling {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_dynamodb_autoscaling_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"table_name":            resource.Name,
						"table_arn":             resource.Arn,
						"has_read_autoscaling":  hasReadAutoScaling,
						"has_write_autoscaling": hasWriteAutoScaling,
						"reason":                "Auto-scaling is not configured for this provisioned table, which can lead to throttling or over-provisioning.",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check 5: TTL disabled
		ttlEnabled := false
		if ttlDesc, ok := meta["TimeToLiveDescription"].(map[string]any); ok {
			if status, ok := ttlDesc["TimeToLiveStatus"].(string); ok && status == string(types.TimeToLiveStatusEnabled) {
				ttlEnabled = true
			}
		}

		if !ttlEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "aws_dynamodb_ttl_disabled",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"table_name": resource.Name,
					"table_arn":  resource.Arn,
					"reason":     "Time To Live (TTL) is not enabled. Enabling TTL can automatically delete expired items and reduce storage costs.",
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 6: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"table_name": resource.Name, "table_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Auto Scaling configuration for Provisioned tables
		// - Global Table usage
		// - TTL (TimeToLiveDescription) configuration
		// - Contributor Insights enabled
		// - Kinesis Data Stream destination enabled
	}

	return recommendations, nil
}

func (a *amazonDynamoDB) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	cfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(cfg)

	paginator := cloudwatchlogs.NewDescribeLogGroupsPaginator(logsSvc, &cloudwatchlogs.DescribeLogGroupsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			return "", err
		}
		for _, lg := range page.LogGroups {
			logGroupName := *lg.LogGroupName
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        &logGroupName,
				LogStreamNamePrefix: &resourceId,
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				return logGroupName, nil
			}
		}
	}
	return "", nil
}

func (a *amazonDynamoDB) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "dynamodb",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}

	return app, nil
}

// Helper functions for DynamoDB capacity analysis

// calculateAverageUtilization calculates the average utilization percentage
func calculateAverageUtilization(consumed, provisioned []float64) float64 {
	if len(consumed) == 0 || len(provisioned) == 0 {
		return 0
	}

	totalUtil := 0.0
	count := 0

	minLen := len(consumed)
	if len(provisioned) < minLen {
		minLen = len(provisioned)
	}

	for i := 0; i < minLen; i++ {
		if provisioned[i] > 0 {
			util := (consumed[i] / provisioned[i]) * 100
			totalUtil += util
			count++
		}
	}

	if count == 0 {
		return 0
	}
	return totalUtil / float64(count)
}

// average calculates the average of a slice of float64

// coefficientOfVariation calculates the coefficient of variation (CV)
// CV = standard deviation / mean
// Lower CV indicates more predictable usage
func coefficientOfVariation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	mean := averageFloat64(values)
	if mean == 0 {
		return 0
	}

	// Calculate standard deviation
	sumSquaredDiff := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquaredDiff += diff * diff
	}
	variance := sumSquaredDiff / float64(len(values))
	stdDev := math.Sqrt(variance)

	return stdDev / mean
}
