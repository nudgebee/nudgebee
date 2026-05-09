package aws

import (
	"context"
	"encoding/json"
	"errors"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type awsKms struct {
	DefaultAwsServiceImpl
}

func (a *awsKms) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsKms) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsKms) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsKms) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameKMS)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := kms.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := kms.NewListKeysPaginator(svc, &kms.ListKeysInput{})

	for paginator.HasMorePages() {
		listOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to list kms keys", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		for _, key := range listOutput.Keys {
			tags := make(map[string][]string)
			var keyDetails *types.KeyMetadata

			// Describe the key to get full details (State, KeyManager, CreationDate, etc.)
			describeOutput, err := svc.DescribeKey(context.TODO(), &kms.DescribeKeyInput{
				KeyId: key.KeyId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe kms key", "error", err, "keyId", *key.KeyId, "accountNumber", account.AccountNumber, "region", regionName)
				// Continue with basic info if describe fails? Or skip? Let's skip for now.
				continue
			}
			keyDetails = describeOutput.KeyMetadata

			// Skip keys that are pending deletion or already deleted during resource gathering
			if keyDetails.KeyState != "" && (keyDetails.KeyState == types.KeyStatePendingDeletion || keyDetails.KeyState == types.KeyStateDisabled) {
				continue
			}

			// Get Tags for the key
			tagsOutput, err := svc.ListResourceTags(context.TODO(), &kms.ListResourceTagsInput{
				KeyId: key.KeyId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for kms key", "error", err, "keyId", *key.KeyId, "accountNumber", account.AccountNumber, "region", regionName)
			} else {
				for _, tag := range tagsOutput.Tags {
					if tag.TagKey != nil && tag.TagValue != nil {
						tags[*tag.TagKey] = append(tags[*tag.TagKey], *tag.TagValue)
					}
				}
			}

			meta := structToMap(keyDetails) // Use detailed key metadata for Meta

			// Determine status based on KeyState
			status := providers.ResourceStatusUnknown
			if keyDetails.KeyState != "" {
				switch keyDetails.KeyState {
				case types.KeyStateEnabled, types.KeyStateUpdating, types.KeyStateCreating, types.KeyStatePendingImport:
					status = providers.ResourceStatusActive
				case types.KeyStateDisabled, types.KeyStateUnavailable, types.KeyStatePendingDeletion: // PendingDeletion handled above, but include for completeness
					status = providers.ResourceStatusInactive
				default:
					status = providers.ResourceStatusUnknown
				}
			}

			// Use creation date from details if available
			createdAt := time.Now() // Fallback
			if keyDetails.CreationDate != nil {
				createdAt = *keyDetails.CreationDate
			}

			resource := providers.Resource{
				Id:          *keyDetails.KeyId,
				ServiceName: ServiceNameKMS,
				Name:        *keyDetails.KeyId, // Consider using Alias if available/preferred
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         *keyDetails.Arn,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameKMS, "key"),
			}
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func (a *awsKms) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session needed for GetKeyRotationStatus API call
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameKMS)
		return recommendations, err // Can't proceed without a session
	}

	for _, resource := range existingResources {
		// Ensure we are looking at KMS Keys
		if resource.Type != getAwsServiceResourceType(ServiceNameKMS, "key") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Re-create client with specific region for potential API calls
		cfg.Region = resource.Region
		svc := kms.NewFromConfig(cfg)

		keyManager := ""

		if manager, ok := meta["KeyManager"].(string); ok {
			keyManager = manager
		}

		// Check 2: Key Rotation for Customer Managed Keys (CMKs)
		if keyManager == string(types.KeyManagerTypeCustomer) {
			rotationStatus, err := svc.GetKeyRotationStatus(context.TODO(), &kms.GetKeyRotationStatusInput{
				KeyId: aws.String(resource.Id),
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get key rotation status", "error", err, "keyId", resource.Id)
				// Continue to next checks even if this fails
			} else if !rotationStatus.KeyRotationEnabled {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_kms_key_rotation_enabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"key_id": resource.Id, "key_arn": resource.Arn, "reason": "Automatic key rotation is not enabled for this Customer Managed Key."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Check 3: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"key_id": resource.Id, "key_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 4: Key Policy Public Access
		policyOutput, err := svc.GetKeyPolicy(context.TODO(), &kms.GetKeyPolicyInput{
			KeyId:      aws.String(resource.Id),
			PolicyName: aws.String("default"),
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to get key policy", "error", err, "keyId", resource.Id)
		} else if policyOutput.Policy != nil {
			var policyMap map[string]any
			if err := json.Unmarshal([]byte(*policyOutput.Policy), &policyMap); err == nil {
				if statements, ok := policyMap["Statement"].([]any); ok {
					for _, stmt := range statements {
						if stmtMap, ok := stmt.(map[string]any); ok {
							if effect, ok := stmtMap["Effect"].(string); ok && effect == "Allow" {
								if principal, ok := stmtMap["Principal"]; ok {
									isPublic := false
									if pStr, ok := principal.(string); ok && pStr == "*" {
										isPublic = true
									} else if pMap, ok := principal.(map[string]any); ok {
										if awsP, ok := pMap["AWS"]; ok {
											if awsStr, ok := awsP.(string); ok && awsStr == "*" {
												isPublic = true
											}
										}
									}

									if isPublic {
										if _, hasCondition := stmtMap["Condition"]; !hasCondition {
											recommendations = append(recommendations, providers.Recommendation{
												CategoryName:        providers.RecommendationCategorySecurity,
												RuleName:            "aws_kms_key_policy_public_access",
												Severity:            providers.RecommendationSeverityHigh,
												Savings:             0,
												Data:                map[string]any{"key_id": resource.Id, "key_arn": resource.Arn, "reason": "Key policy allows public access without conditions."},
												Action:              providers.RecommendationActionModify,
												ResourceServiceName: resource.ServiceName,
												ResourceId:          resource.Id,
												ResourceType:        resource.Type,
												ResourceRegion:      resource.Region,
											})
											break // Found one public statement, no need to check others
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// Check 5: Missing Description
		description := ""
		if desc, ok := meta["Description"].(string); ok {
			description = desc
		}
		if description == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_kms_key_missing_description",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"key_id": resource.Id, "key_arn": resource.Arn, "reason": "KMS key does not have a description."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Unused Keys (check LastUsedDate from DescribeKey, might need CloudTrail for more accuracy)
		// - Key Policy Analysis (complex, requires parsing policy JSON)
	}

	return recommendations, nil
}

func (a *awsKms) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *awsKms) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "kms",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	kmsSvc := kms.NewFromConfig(cfg)

	describeKeyOutput, err := kmsSvc.DescribeKey(context.TODO(), &kms.DescribeKeyInput{KeyId: aws.String(resourceId)})
	if err != nil {
		return app, err
	}
	if describeKeyOutput.KeyMetadata != nil {
		key := describeKeyOutput.KeyMetadata
		app.Id.Name = *key.Arn
		app.Status = string(key.KeyState)
	}

	return app, nil
}
