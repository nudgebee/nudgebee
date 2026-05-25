package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	"github.com/aws/smithy-go"
)

// Helper function to map GuardDuty status strings to provider status
func guardDutyDetectorStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*status)
	switch s {
	case "enabled":
		return providers.ResourceStatusActive
	case "disabled": // Or Stopping/Stopped if those states exist
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsGuardDuty struct {
	DefaultAwsServiceImpl
}

func (a *awsGuardDuty) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for GuardDuty yet.
	return errors.ErrUnsupported
}

func (a *awsGuardDuty) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for GuardDuty yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsGuardDuty) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsGuardDuty) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameGuardDuty)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := guardduty.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get Detectors ---
	paginator := guardduty.NewListDetectorsPaginator(svc, &guardduty.ListDetectorsInput{})
	for paginator.HasMorePages() {
		detectorsOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			var apiErr *smithy.GenericAPIError
			if errors.As(err, &apiErr) {
				if apiErr.ErrorCode() == "BadRequestException" && strings.Contains(apiErr.ErrorMessage(), "The request is rejected because GuardDuty is not enabled") {
					ctx.GetLogger().Info("GuardDuty is not enabled in this region", "region", regionName, "accountNumber", account.AccountNumber)
					return resources, nil // Not an error, just no resources
				}
				if apiErr.ErrorCode() == "AuthFailure" || apiErr.ErrorCode() == "UnrecognizedClientException" || strings.Contains(apiErr.ErrorMessage(), "is not supported in this region") {
					ctx.GetLogger().Warn("GuardDuty API might not be available or fully supported in this region", "region", regionName, "error", apiErr.ErrorCode())
					return resources, nil // Return empty list if region not supported
				}
			}
			// Log other errors
			ctx.GetLogger().Error("failed to list guardduty detectors", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely for other errors
		}

		if len(detectorsOutput.DetectorIds) == 0 {
			break // No more detectors
		}

		for _, detectorId := range detectorsOutput.DetectorIds {
			// Basic nil check
			if detectorId == "" {
				continue
			}

			// Get Detector Details
			detectorDetails, err := svc.GetDetector(context.TODO(), &guardduty.GetDetectorInput{
				DetectorId: &detectorId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get guardduty detector details", "error", err, "detectorId", detectorId, "accountNumber", account.AccountNumber, "region", regionName)
				continue // Skip if details cannot be fetched
			}

			meta := structToMap(detectorDetails)

			createdAt := time.Now() // Default
			if detectorDetails.CreatedAt != nil {
				// GuardDuty CreatedAt is a string like "2023-10-27T10:30:00.123Z"
				// Attempt to parse it
				parsedTime, parseErr := time.Parse(time.RFC3339Nano, *detectorDetails.CreatedAt)
				if parseErr == nil {
					createdAt = parsedTime
				} else {
					ctx.GetLogger().Warn("failed to parse CreatedAt time for GuardDuty detector", "error", parseErr, "createdAt", *detectorDetails.CreatedAt, "detectorId", detectorId)
				}
			}

			resource := providers.Resource{
				Id:          detectorId,
				ServiceName: ServiceNameGuardDuty,
				Name:        detectorId, // GuardDuty detectors don't have separate names
				Status:      guardDutyDetectorStatusToNbStatus((*string)(&detectorDetails.Status)),
				Region:      regionName,
				Tags:        map[string][]string{},
				Meta:        meta,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameGuardDuty, "detector"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Potentially Get Findings (Optional - can be very numerous, consider separate handling) ---
	// If findings are needed as resources, use ListFindings and GetFindings APIs.
	// This is often better handled by recommendation logic or dedicated event processing.

	// --- Potentially Get IP Sets / Threat Intel Sets (Optional) ---
	// Use ListIPSets, GetIPSet, ListThreatIntelSets, GetThreatIntelSet if needed as resources.

	return resources, nil
}

func (a *awsGuardDuty) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameGuardDuty)
	// 	return recommendations, err
	// }

	// --- Region/Account Level Checks ---
	// Check if GuardDuty is enabled at all in the regions where resources were found (or all regions)
	regionsWithDetectors := make(map[string]bool)
	detectorFound := false
	for _, resource := range existingResources {
		if resource.Type == getAwsServiceResourceType(ServiceNameGuardDuty, "detector") {
			regionsWithDetectors[resource.Region] = true
			detectorFound = true
			break // Found at least one detector, no need to check further for this specific recommendation
		}
	}

	// If no detectors were found in the list of resources (implying GuardDuty might be disabled)
	// Note: This assumes GetResources was called for the relevant regions.
	// A more robust check might involve iterating through all desired regions and calling ListDetectors.
	if !detectorFound && len(existingResources) > 0 {
		// Create a single recommendation (or one per region checked by GetResources)
		region := existingResources[0].Region // Use region from the (empty) resource list context
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategorySecurity,
			RuleName:            "aws_guardduty_enabled",
			Severity:            providers.RecommendationSeverityHigh,
			Savings:             0,
			Data:                map[string]any{"region": region, "reason": "GuardDuty detector is not enabled in this region."},
			Action:              providers.RecommendationActionModify, // Action is to enable GuardDuty
			ResourceServiceName: ServiceNameGuardDuty,
			ResourceId:          account.AccountNumber, // Account/Region-level setting
			ResourceType:        getAwsServiceResourceType(ServiceNameGuardDuty, "detector"),
			ResourceRegion:      region,
		})
	}

	// --- Detector Level Checks ---
	for _, resource := range existingResources {
		// Ensure we are looking at GuardDuty Detectors
		if resource.Type != getAwsServiceResourceType(ServiceNameGuardDuty, "detector") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: Detector Status (already checked above, but can confirm here)
		if resource.Status != providers.ResourceStatusActive {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_guardduty_detector_active",
				Severity:            providers.RecommendationSeverityHigh, // If it exists but isn't active
				Savings:             0,
				Data:                map[string]any{"detector_id": resource.Id, "detector_arn": resource.Arn, "current_status": resource.Status, "reason": "GuardDuty detector is not in an active state."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Data Sources Enabled (e.g., S3 Logs, Kubernetes Audit Logs)
		// Requires parsing the 'DataSources' field within the Meta map
		s3LogsEnabled := false
		k8sAuditLogsEnabled := false
		// Add checks for other data sources as needed (Malware Protection, EKS Runtime Monitoring, etc.)

		if dataSources, ok := meta["DataSources"].(map[string]any); ok {
			if s3Logs, ok := dataSources["S3Logs"].(map[string]any); ok {
				if status, ok := s3Logs["Status"].(string); ok && strings.ToLower(status) == "enabled" {
					s3LogsEnabled = true
				}
			}
			if k8sAudit, ok := dataSources["Kubernetes"].(map[string]any); ok {
				if auditLogs, ok := k8sAudit["AuditLogs"].(map[string]any); ok {
					if status, ok := auditLogs["Status"].(string); ok && strings.ToLower(status) == "enabled" {
						k8sAuditLogsEnabled = true
					}
				}
			}
			// Add checks for MalwareProtection, EKS Runtime Monitoring similarly
		}

		if !s3LogsEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_guardduty_s3_logs_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"detector_id": resource.Id, "detector_arn": resource.Arn, "reason": "GuardDuty S3 data source is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
		if !k8sAuditLogsEnabled {
			// Only recommend if EKS clusters exist in the account/region (requires cross-service check)
			// For now, recommend generally.
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_guardduty_k8s_audit_logs_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"detector_id": resource.Id, "detector_arn": resource.Arn, "reason": "GuardDuty Kubernetes Audit Logs data source is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"detector_id": resource.Id, "detector_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Findings Analysis (High/Medium severity findings present?) -> Requires GetFindings
		// - Publishing Findings to S3/EventBridge configured? (Check PublishingDestination)
		// - Member accounts status (if master account) -> Requires ListMembers, GetMemberDetectors
	}

	return recommendations, nil
}

func (a *awsGuardDuty) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *awsGuardDuty) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "guardduty",
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
