package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func ssmInstanceStatusToNbStatus(pingStatus types.PingStatus) providers.ResourceStatus {
	switch pingStatus {
	case types.PingStatusOnline, types.PingStatusConnectionLost:
		return providers.ResourceStatusActive
	case types.PingStatusInactive:
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

func ssmComplianceStatusToNbStatus(status types.ComplianceStatus) providers.ResourceStatus {
	switch status {
	case types.ComplianceStatusCompliant:
		return providers.ResourceStatusActive
	case types.ComplianceStatusNonCompliant:
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsSystemsManager struct {
	DefaultAwsServiceImpl
}

func (a *awsSystemsManager) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsSystemsManager) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	var resultMessage string
	var resultErr error
	var instanceIDs []string

	// Always audit, even on early returns
	defer func() {
		status := "SUCCESS"
		if resultErr != nil {
			status = "FAILURE"
		}

		// Use batch audit logging for multi-instance commands
		var auditErr error
		if len(instanceIDs) > 1 {
			auditErr = logResourceActionAuditBatch(ctx, command, account, status, resultMessage, instanceIDs)
		} else {
			auditErr = logResourceActionAudit(ctx, command, account, status, resultMessage)
		}

		if auditErr != nil {
			ctx.GetLogger().Warn("failed to log audit record", "error", auditErr)
		}
	}()

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		resultErr = fmt.Errorf("failed to get AWS config: %w", err)
		resultMessage = resultErr.Error()
		return providers.ApplyCommandResponse{}, resultErr
	}

	// Override region if specified in command
	if command.Region != "" {
		cfg.Region = command.Region
	}

	switch command.Command {
	case "run_command":
		// Get template ID
		templateID, ok := command.Args["template_id"].(string)
		if !ok || templateID == "" {
			resultErr = fmt.Errorf("template_id required for run_command")
			resultMessage = resultErr.Error()
			break
		}

		// Get instance IDs
		if command.ResourceId != "" {
			instanceIDs = []string{command.ResourceId}
		} else if ids, ok := command.Args["instance_ids"].([]interface{}); ok {
			for _, id := range ids {
				if idStr, ok := id.(string); ok {
					instanceIDs = append(instanceIDs, idStr)
				}
			}
		} else if ids, ok := command.Args["instance_ids"].([]string); ok {
			instanceIDs = ids
		}

		if len(instanceIDs) == 0 {
			resultErr = fmt.Errorf("instance_id(s) required for run_command")
			resultMessage = resultErr.Error()
			break
		}

		// Check SSM agent status before sending command
		statuses, err := CheckSSMAgentStatus(ctx.GetContext(), cfg, instanceIDs)
		if err != nil {
			resultErr = fmt.Errorf("failed to check SSM agent status: %w", err)
			resultMessage = resultErr.Error()
			break
		}

		// Filter out instances that are not online
		var onlineInstances []string
		var offlineStatuses []string
		for instanceID, status := range statuses {
			if status == "Online" {
				onlineInstances = append(onlineInstances, instanceID)
			} else {
				offlineStatuses = append(offlineStatuses, fmt.Sprintf("%s:%s", instanceID, status))
			}
		}

		if len(onlineInstances) == 0 {
			resultErr = fmt.Errorf("no instances have SSM agent online (statuses: %s). SSM agent must be installed and running on instances", strings.Join(offlineStatuses, ", "))
			resultMessage = resultErr.Error()
			break
		}

		// Log warning if some instances are offline
		if len(offlineStatuses) > 0 {
			ctx.GetLogger().Warn("some instances are not online, skipping them", "offline", offlineStatuses)
		}

		// Get custom parameters (will be validated against template allowlist)
		customParams := make(map[string]interface{})
		if params, ok := command.Args["parameters"].(map[string]interface{}); ok {
			customParams = params
		}

		// Send command
		commandID, err := RunSSMCommand(ctx.GetContext(), cfg, templateID, onlineInstances, customParams)
		if err != nil {
			resultErr = fmt.Errorf("failed to run SSM command: %w", err)
			resultMessage = resultErr.Error()
			break
		}

		// Check if we should wait for results
		waitForResults := false
		if wait, ok := command.Args["wait_for_results"].(bool); ok {
			waitForResults = wait
		}

		if waitForResults {
			// Poll for results (wait up to 60 seconds)
			timeout := 60 * time.Second
			if timeoutVal, ok := command.Args["timeout"].(float64); ok {
				timeout = time.Duration(timeoutVal) * time.Second
			}

			// Poll all instances
			results, err := PollSSMCommandStatusMulti(ctx.GetContext(), cfg, commandID, onlineInstances, timeout)
			if err != nil {
				resultErr = fmt.Errorf("failed to poll command results: %w", err)
				resultMessage = resultErr.Error()
				break
			}

			// Aggregate results
			successCount := 0
			failedCount := 0
			var outputs []string

			for instanceID, result := range results {
				if result.Status == "Success" {
					successCount++
				} else {
					failedCount++
				}

				outputs = append(outputs, fmt.Sprintf("Instance %s: %s\n%s", instanceID, result.Status, result.Output))
			}

			// If any instance failed, mark the overall command as failed
			if failedCount > 0 {
				resultErr = fmt.Errorf("%d instance(s) failed command execution", failedCount)
			}

			resultMessage = fmt.Sprintf("Command completed on %d instance(s): %d succeeded, %d failed\n\n%s",
				len(onlineInstances), successCount, failedCount, strings.Join(outputs, "\n---\n"))
		} else {
			resultMessage = fmt.Sprintf("SSM command sent successfully (Command ID: %s) to %d instance(s)", commandID, len(onlineInstances))
			if len(offlineStatuses) > 0 {
				resultMessage += fmt.Sprintf(". %d instance(s) offline and skipped", len(offlineStatuses))
			}
		}

	default:
		resultErr = fmt.Errorf("unsupported command: %s", command.Command)
		resultMessage = resultErr.Error()
	}

	if resultErr != nil {
		return providers.ApplyCommandResponse{Success: false, Message: resultMessage}, resultErr
	}

	return providers.ApplyCommandResponse{Success: true, Message: resultMessage}, nil
}

func (a *awsSystemsManager) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsSystemsManager) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// SSM Session Manager logs go to CloudWatch Logs
	return fmt.Sprintf("/aws/ssm/%s", resourceId), nil
}

func (a *awsSystemsManager) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := ssm.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// Get Managed Instances (EC2 instances with SSM agent)
	instancePaginator := ssm.NewDescribeInstanceInformationPaginator(svc, &ssm.DescribeInstanceInformationInput{})
	for instancePaginator.HasMorePages() {
		result, err := instancePaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch SSM managed instances", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, instance := range result.InstanceInformationList {
			if instance.InstanceId == nil {
				ctx.GetLogger().Warn("Skipping SSM instance due to missing ID")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the instance
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &ssm.ListTagsForResourceInput{
				ResourceType: types.ResourceTypeForTaggingManagedInstance,
				ResourceId:   instance.InstanceId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM instance tags", "error", err, "instanceId", *instance.InstanceId)
			} else if tagsResult.TagList != nil {
				for _, tag := range tagsResult.TagList {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(instance)

			// Get instance patches compliance
			patchResult, err := svc.DescribeInstancePatches(context.TODO(), &ssm.DescribeInstancePatchesInput{
				InstanceId: instance.InstanceId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM instance patches", "error", err, "instanceId", *instance.InstanceId)
			} else {
				installedCount := 0
				missingCount := 0
				failedCount := 0

				for _, patch := range patchResult.Patches {
					switch patch.State {
					case "Installed", "InstalledOther":
						installedCount++
					case "Missing":
						missingCount++
					case "Failed", "NotApplicable":
						failedCount++
					}
				}

				metaMap["PatchCompliance"] = map[string]int{
					"Installed": installedCount,
					"Missing":   missingCount,
					"Failed":    failedCount,
				}
			}

			// Get associations for this instance
			associationsResult, err := svc.ListAssociations(context.TODO(), &ssm.ListAssociationsInput{
				AssociationFilterList: []types.AssociationFilter{
					{
						Key:   types.AssociationFilterKeyInstanceId,
						Value: instance.InstanceId,
					},
				},
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM associations", "error", err, "instanceId", *instance.InstanceId)
			} else {
				metaMap["AssociationCount"] = len(associationsResult.Associations)
			}

			status := providers.ResourceStatusUnknown
			if instance.PingStatus != "" {
				status = ssmInstanceStatusToNbStatus(instance.PingStatus)
			}

			instanceArn := fmt.Sprintf("arn:aws:ssm:%s:%s:managed-instance/%s", region, account.AccountNumber, *instance.InstanceId)

			instanceName := *instance.InstanceId
			if instance.Name != nil {
				instanceName = *instance.Name
			}

			createdAt := time.Time{}
			if instance.RegistrationDate != nil {
				createdAt = *instance.RegistrationDate
			}

			resource := providers.Resource{
				Id:          *instance.InstanceId,
				ServiceName: ServiceNameSystemsManager,
				Name:        instanceName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         instanceArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameSystemsManager, "managedinstance"),
			}
			resources = append(resources, resource)
		}
	}

	// Get SSM Documents (automation runbooks, command documents)
	docPaginator := ssm.NewListDocumentsPaginator(svc, &ssm.ListDocumentsInput{
		Filters: []types.DocumentKeyValuesFilter{
			{
				Key:    aws.String("Owner"),
				Values: []string{"Self"}, // Only custom documents, not AWS-managed
			},
		},
	})

	for docPaginator.HasMorePages() {
		result, err := docPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch SSM documents", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, doc := range result.DocumentIdentifiers {
			if doc.Name == nil {
				ctx.GetLogger().Warn("Skipping SSM document due to missing name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the document
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &ssm.ListTagsForResourceInput{
				ResourceType: types.ResourceTypeForTaggingDocument,
				ResourceId:   doc.Name,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM document tags", "error", err, "documentName", *doc.Name)
			} else if tagsResult.TagList != nil {
				for _, tag := range tagsResult.TagList {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			// Get detailed document information
			describeResult, err := svc.DescribeDocument(context.TODO(), &ssm.DescribeDocumentInput{
				Name: doc.Name,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe SSM document", "error", err, "documentName", *doc.Name)
				continue
			}

			metaMap := structToMap(describeResult.Document)

			docArn := ""
			if doc.Name != nil {
				docArn = fmt.Sprintf("arn:aws:ssm:%s:%s:document/%s", region, account.AccountNumber, *doc.Name)
			}

			createdAt := time.Time{}
			if describeResult.Document != nil && describeResult.Document.CreatedDate != nil {
				createdAt = *describeResult.Document.CreatedDate
			}

			resource := providers.Resource{
				Id:          *doc.Name,
				ServiceName: ServiceNameSystemsManager,
				Name:        *doc.Name,
				Status:      providers.ResourceStatusActive,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         docArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameSystemsManager, "document"),
			}
			resources = append(resources, resource)
		}
	}

	// Get Parameter Store parameters
	paramPaginator := ssm.NewDescribeParametersPaginator(svc, &ssm.DescribeParametersInput{})
	paramCount := 0
	for paramPaginator.HasMorePages() && paramCount < 100 { // Limit to 100 parameters
		result, err := paramPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch SSM parameters", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, param := range result.Parameters {
			if param.Name == nil {
				ctx.GetLogger().Warn("Skipping SSM parameter due to missing name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the parameter
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &ssm.ListTagsForResourceInput{
				ResourceType: types.ResourceTypeForTaggingParameter,
				ResourceId:   param.Name,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM parameter tags", "error", err, "parameterName", *param.Name)
			} else if tagsResult.TagList != nil {
				for _, tag := range tagsResult.TagList {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(param)

			paramArn := ""
			if param.ARN != nil {
				paramArn = *param.ARN
			}

			createdAt := time.Time{}
			if param.LastModifiedDate != nil {
				createdAt = *param.LastModifiedDate
			}

			resource := providers.Resource{
				Id:          *param.Name,
				ServiceName: ServiceNameSystemsManager,
				Name:        *param.Name,
				Status:      providers.ResourceStatusActive,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         paramArn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameSystemsManager, "parameter"),
			}
			resources = append(resources, resource)
			paramCount++

			if paramCount >= 100 {
				break
			}
		}
	}

	// Get Maintenance Windows
	mwPaginator := ssm.NewDescribeMaintenanceWindowsPaginator(svc, &ssm.DescribeMaintenanceWindowsInput{})
	for mwPaginator.HasMorePages() {
		result, err := mwPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch SSM maintenance windows", "error", err, "accountNumber", account.AccountNumber, "region", region)
			break
		}

		for _, window := range result.WindowIdentities {
			if window.WindowId == nil {
				ctx.GetLogger().Warn("Skipping SSM maintenance window due to missing ID")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the maintenance window
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &ssm.ListTagsForResourceInput{
				ResourceType: types.ResourceTypeForTaggingMaintenanceWindow,
				ResourceId:   window.WindowId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM maintenance window tags", "error", err, "windowId", *window.WindowId)
			} else if tagsResult.TagList != nil {
				for _, tag := range tagsResult.TagList {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(window)

			windowArn := fmt.Sprintf("arn:aws:ssm:%s:%s:maintenancewindow/%s", region, account.AccountNumber, *window.WindowId)

			windowName := *window.WindowId
			if window.Name != nil {
				windowName = *window.Name
			}

			status := providers.ResourceStatusActive
			if !window.Enabled {
				status = providers.ResourceStatusInactive
			}

			resource := providers.Resource{
				Id:          *window.WindowId,
				ServiceName: ServiceNameSystemsManager,
				Name:        windowName,
				Status:      status,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         windowArn,
				CreatedAt:   time.Time{},
				Type:        getAwsServiceResourceType(ServiceNameSystemsManager, "maintenancewindow"),
			}
			resources = append(resources, resource)
		}
	}

	// Get Patch Baselines
	baselineResult, err := svc.DescribePatchBaselines(context.TODO(), &ssm.DescribePatchBaselinesInput{
		Filters: []types.PatchOrchestratorFilter{
			{
				Key:    aws.String("OWNER"),
				Values: []string{"Self"}, // Only custom baselines
			},
		},
	})
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch SSM patch baselines", "error", err, "accountNumber", account.AccountNumber, "region", region)
	} else {
		for _, baseline := range baselineResult.BaselineIdentities {
			if baseline.BaselineId == nil {
				ctx.GetLogger().Warn("Skipping SSM patch baseline due to missing ID")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the patch baseline
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &ssm.ListTagsForResourceInput{
				ResourceType: types.ResourceTypeForTaggingPatchBaseline,
				ResourceId:   baseline.BaselineId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch SSM patch baseline tags", "error", err, "baselineId", *baseline.BaselineId)
			} else if tagsResult.TagList != nil {
				for _, tag := range tagsResult.TagList {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(baseline)

			baselineArn := fmt.Sprintf("arn:aws:ssm:%s:%s:patchbaseline/%s", region, account.AccountNumber, *baseline.BaselineId)

			baselineName := *baseline.BaselineId
			if baseline.BaselineName != nil {
				baselineName = *baseline.BaselineName
			}

			resource := providers.Resource{
				Id:          *baseline.BaselineId,
				ServiceName: ServiceNameSystemsManager,
				Name:        baselineName,
				Status:      providers.ResourceStatusActive,
				Region:      region,
				Tags:        tags,
				Meta:        metaMap,
				Arn:         baselineArn,
				CreatedAt:   time.Time{},
				Type:        getAwsServiceResourceType(ServiceNameSystemsManager, "patchbaseline"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsSystemsManager) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for managed instances that are offline
		if resource.Type == getAwsServiceResourceType(ServiceNameSystemsManager, "managedinstance") {
			if pingStatus, ok := meta["PingStatus"].(string); ok {
				if strings.ToUpper(pingStatus) == "CONNECTIONLOST" || strings.ToUpper(pingStatus) == "INACTIVE" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_ssm_instance_offline",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      0,
						Data: map[string]any{
							"message":        "SSM managed instance is offline or connection lost",
							"pingStatus":     pingStatus,
							"recommendation": "Check SSM agent status and network connectivity",
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

			// Check for instances with missing patches
			if patchCompliance, ok := meta["PatchCompliance"].(map[string]int); ok {
				missingCount := patchCompliance["Missing"]
				if missingCount > 0 {
					severity := providers.RecommendationSeverityMedium
					if missingCount > 10 {
						severity = providers.RecommendationSeverityHigh
					}

					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "aws_ssm_missing_patches",
						Severity:     severity,
						Savings:      0,
						Data: map[string]any{
							"message":        fmt.Sprintf("Instance has %d missing patches", missingCount),
							"missingCount":   missingCount,
							"recommendation": "Apply missing patches through SSM Patch Manager",
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

			// Check for instances with no associations
			if assocCount, ok := meta["AssociationCount"].(int); ok && assocCount == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_ssm_no_associations",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"message":        "Managed instance has no SSM associations",
						"recommendation": "Configure associations for automated management tasks",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check for outdated SSM agent
			if agentVersion, ok := meta["AgentVersion"].(string); ok && agentVersion != "" {
				if !strings.HasPrefix(agentVersion, "3.") { // SSM agent v3.x is current
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryInfraUpgrade,
						RuleName:     "aws_ssm_outdated_agent",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "SSM agent version is outdated",
							"agentVersion":   agentVersion,
							"recommendation": "Update SSM agent to the latest version",
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

		// Check for parameters without encryption
		if resource.Type == getAwsServiceResourceType(ServiceNameSystemsManager, "parameter") {
			if paramType, ok := meta["Type"].(string); ok {
				if strings.ToUpper(paramType) == "STRING" {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "aws_ssm_parameter_not_encrypted",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "Parameter is stored as plaintext",
							"recommendation": "Use SecureString type for sensitive parameters",
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

		// Check for disabled maintenance windows
		if resource.Type == getAwsServiceResourceType(ServiceNameSystemsManager, "maintenancewindow") {
			if enabled, ok := meta["Enabled"].(bool); ok && !enabled {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_ssm_maintenance_window_disabled",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"message":        "Maintenance window is disabled",
						"recommendation": "Enable or delete unused maintenance windows",
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

func (a *awsSystemsManager) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "ssm",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// Placeholder for fetching service map details.
	// add logic here to describe the SSM resource and its relationships.

	return app, nil
}
