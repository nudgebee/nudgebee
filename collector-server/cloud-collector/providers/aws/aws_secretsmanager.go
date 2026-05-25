package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type awsSecretsManager struct {
	DefaultAwsServiceImpl
}

func (a *awsSecretsManager) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsSecretsManager) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsSecretsManager) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsSecretsManager) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameSecretsManager)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := secretsmanager.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := secretsmanager.NewListSecretsPaginator(svc, &secretsmanager.ListSecretsInput{})
	for paginator.HasMorePages() {
		secretsOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch secretsmanager resources", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		for _, secret := range secretsOutput.SecretList {
			// Skip secrets marked for deletion
			if secret.DeletedDate != nil {
				continue
			}

			tags := make(map[string][]string)
			// Tags are included in ListSecrets response
			for _, tag := range secret.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(secret) // Use list entry info for Meta

			// Determine creation time (use LastChangedDate as proxy if CreationDate isn't available)
			createdAt := time.Now() // Fallback
			if secret.CreatedDate != nil {
				createdAt = *secret.CreatedDate
			} else if secret.LastChangedDate != nil {
				createdAt = *secret.LastChangedDate // Use LastChangedDate if CreatedDate is nil
			}

			resource := providers.Resource{
				Id:          *secret.Name,
				ServiceName: ServiceNameSecretsManager,
				Name:        *secret.Name,
				Status:      providers.ResourceStatusActive, // Assume active if listed and not deleted
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *secret.ARN,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameSecretsManager, "secret"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *awsSecretsManager) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	_, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameSecretsManager)
		return recommendations, err
	}

	// Define threshold for unused secrets (e.g., 90 days)
	unusedThreshold := time.Now().AddDate(0, 0, -90)

	for _, resource := range existingResources {
		// Ensure we are looking at Secrets Manager secrets
		if resource.Type != getAwsServiceResourceType(ServiceNameSecretsManager, "secret") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: Unused Secret
		lastAccessedDate := time.Time{} // Zero time
		if accessedDatePtr, ok := meta["LastAccessedDate"].(*time.Time); ok && accessedDatePtr != nil {
			lastAccessedDate = *accessedDatePtr
		}

		// If LastAccessedDate is available and before the threshold, recommend deletion
		if !lastAccessedDate.IsZero() && lastAccessedDate.Before(unusedThreshold) {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryRightSizing,
				RuleName:            "aws_secretsmanager_unused_secret",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0.40, // Base cost per secret per month (approx)
				Data:                map[string]any{"secret_name": resource.Name, "secret_arn": resource.Arn, "last_accessed": lastAccessedDate.Format(time.RFC3339), "reason": "Secret has not been accessed recently."},
				Action:              providers.RecommendationActionDelete,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		} // else if lastAccessedDate.IsZero() {
		// If LastAccessedDate is nil (might happen for newly created or never accessed secrets),
		// consider checking CreatedAt/LastChangedDate as a fallback, but be cautious.
		// For now, we'll only recommend based on a non-zero LastAccessedDate.
		// ctx.GetLogger().Debug("LastAccessedDate is nil for secret", "secret_name", resource.Name)
		// }

		// Check 2: Rotation Disabled
		rotationEnabled := false
		if enabled, ok := meta["RotationEnabled"].(bool); ok {
			rotationEnabled = enabled
		}
		if !rotationEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_secretsmanager_rotation_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"secret_name": resource.Name, "secret_arn": resource.Arn, "reason": "Automatic rotation is not enabled for this secret."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Encryption with KMS CMK
		// Recommend if KmsKeyId is not specified (implying default AWS managed key)
		kmsKeyId := ""
		if keyId, ok := meta["KmsKeyId"].(string); ok {
			kmsKeyId = keyId
		}
		if kmsKeyId == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_secretsmanager_encryption_cmk",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"secret_name": resource.Name, "secret_arn": resource.Arn, "reason": "Secret is not encrypted with a Customer Managed Key (CMK)."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 4: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"secret_name": resource.Name, "secret_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
	}

	return recommendations, nil
}

func (a *awsSecretsManager) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *awsSecretsManager) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "secretsmanager",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	smSvc := secretsmanager.NewFromConfig(cfg)

	describeSecretOutput, err := smSvc.DescribeSecret(context.TODO(), &secretsmanager.DescribeSecretInput{SecretId: aws.String(resourceId)})
	if err != nil {
		return app, err
	}
	app.Id.Name = *describeSecretOutput.ARN
	app.Status = "Active"
	if describeSecretOutput.DeletedDate != nil {
		app.Status = "Deleted"
	}
	if describeSecretOutput.KmsKeyId != nil {
		app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *describeSecretOutput.KmsKeyId, Kind: "kms", Namespace: region}}.ToDownstreamLink())
	}

	return app, nil
}
