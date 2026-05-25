package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/efs/types"
)

type amazonEFS struct {
	DefaultAwsServiceImpl
}

func (a *amazonEFS) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for EFS yet.
	return errors.ErrUnsupported
}

func (a *amazonEFS) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for EFS yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonEFS) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonEFS) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameEFS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := efs.NewFromConfig(cfg)
	resources := []providers.Resource{}
	paginator := efs.NewDescribeFileSystemsPaginator(svc, &efs.DescribeFileSystemsInput{})

	for paginator.HasMorePages() {
		fsOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch EFS file systems", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, fs := range fsOutput.FileSystems {
			tags := make(map[string][]string)
			for _, tag := range fs.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			lifecycleConfig, err := svc.DescribeLifecycleConfiguration(ctx.GetContext(), &efs.DescribeLifecycleConfigurationInput{
				FileSystemId: fs.FileSystemId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch lifecycle configuration for EFS", "error", err, "fileSystemId", *fs.FileSystemId, "accountNumber", account.AccountNumber, "region", regionName)
			}

			// Fetch mount targets (IP addresses, subnets, VPC associations)
			mountTargetsOutput, err := svc.DescribeMountTargets(ctx.GetContext(), &efs.DescribeMountTargetsInput{
				FileSystemId: fs.FileSystemId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch mount targets for EFS", "error", err, "fileSystemId", *fs.FileSystemId, "accountNumber", account.AccountNumber, "region", regionName)
			}

			meta := structToMap(fs)
			if lifecycleConfig != nil && len(lifecycleConfig.LifecyclePolicies) > 0 {
				policyMap := []map[string]any{}
				for _, policy := range lifecycleConfig.LifecyclePolicies {
					policyMap = append(policyMap, structToMap(policy))
				}
				meta["LifecyclePolicies"] = policyMap
			} else {
				meta["LifecyclePolicies"] = nil
			}

			// Store mount target details (IP addresses, subnets, VPC, availability zones)
			if mountTargetsOutput != nil && len(mountTargetsOutput.MountTargets) > 0 {
				mountTargetList := []map[string]any{}
				for _, mt := range mountTargetsOutput.MountTargets {
					mountTargetList = append(mountTargetList, structToMap(mt))
				}
				meta["MountTargets"] = mountTargetList
			} else {
				meta["MountTargets"] = []any{}
			}

			name := *fs.FileSystemId
			if nameTag, ok := tags["Name"]; ok && len(nameTag) > 0 {
				name = nameTag[0]
			}

			status := providers.ResourceStatusUnknown
			if fs.LifeCycleState != "" {
				switch fs.LifeCycleState {
				case types.LifeCycleStateAvailable, types.LifeCycleStateCreating, types.LifeCycleStateUpdating:
					status = providers.ResourceStatusActive
				case types.LifeCycleStateDeleting, types.LifeCycleStateDeleted:
					status = providers.ResourceStatusDeleted
				default:
					status = providers.ResourceStatusUnknown
				}
			}

			resource := providers.Resource{
				Id:          *fs.FileSystemId,
				ServiceName: ServiceNameEFS,
				Name:        name,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *fs.FileSystemArn,
				CreatedAt:   *fs.CreationTime,
				Type:        getAwsServiceResourceType(ServiceNameEFS, "file-system"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonEFS) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameEFS)
	// 	return recommendations, err
	// }

	for _, resource := range existingResources {
		// Ensure we are looking at EFS File Systems
		if resource.Type != getAwsServiceResourceType(ServiceNameEFS, "filesystem") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: EFS Encryption at Rest
		encrypted := false
		if enc, ok := meta["Encrypted"].(bool); ok {
			encrypted = enc
		}
		if !encrypted {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_efs_encryption_at_rest",
				Severity:            providers.RecommendationSeverityMedium, // Or High depending on compliance needs
				Savings:             0,
				Data:                map[string]any{"filesystem_id": resource.Id, "filesystem_arn": resource.Arn, "reason": "File system is not encrypted at rest."},
				Action:              providers.RecommendationActionModify, // Note: Encryption must be enabled at creation time. Action might be 'Recreate'.
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: EFS Lifecycle Policy
		hasLifecyclePolicy := false
		if policies, ok := meta["LifecyclePolicies"]; ok && policies != nil {
			// Check if the slice/map representation is non-empty
			if policiesSlice, ok := policies.([]any); ok && len(policiesSlice) > 0 {
				hasLifecyclePolicy = true
			} else if policiesMap, ok := policies.(map[string]any); ok && len(policiesMap) > 0 {
				// Handle case where structToMap might produce a map for a single policy
				hasLifecyclePolicy = true
			}
		}
		if !hasLifecyclePolicy {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing,
				RuleName:            "aws_efs_lifecycle_policy",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0, // Potential savings, hard to calculate
				Data:                map[string]any{"filesystem_id": resource.Id, "filesystem_arn": resource.Arn, "reason": "File system does not have a lifecycle policy configured."},
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
				Data:                map[string]any{"filesystem_id": resource.Id, "filesystem_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Check Throughput Mode (Bursting vs Provisioned) - recommend Provisioned if BurstCreditBalance is consistently low (requires metrics)
		// - Check Performance Mode (General Purpose vs Max I/O)
		// - Check Backup Policy (requires DescribeBackupPolicy API)
	}

	return recommendations, nil
}

func (a *amazonEFS) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonEFS) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "efs",
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
