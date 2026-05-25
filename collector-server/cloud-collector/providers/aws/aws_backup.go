package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

type awsBackup struct {
	DefaultAwsServiceImpl
}

func (a *awsBackup) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for AWS Backup yet.
	return errors.New("Unsupported")
}

func (a *awsBackup) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for AWS Backup yet.
	return providers.ApplyCommandResponse{}, errors.New("Unsupported")
}

func (a *awsBackup) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsBackup) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameBackup)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := backup.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// --- Get Backup Vaults ---
	vaultsPaginator := backup.NewListBackupVaultsPaginator(svc, &backup.ListBackupVaultsInput{})
	for vaultsPaginator.HasMorePages() {
		vaultsOutput, err := vaultsPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch backup vaults", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Decide if we should return partial results or fail completely
			return resources, err
		}

		for _, vault := range vaultsOutput.BackupVaultList {
			// Redundant check, but good practice
			if vault.BackupVaultName == nil || vault.BackupVaultArn == nil {
				ctx.GetLogger().Warn("Backup vault Name or ARN is nil, skipping", "vault", vault, "region", regionName)
				continue
			}
			if vault.CreationDate == nil {
				// Log and proceed, CreatedAt will be zero time
				ctx.GetLogger().Warn("Backup vault CreationDate is nil", "vaultArn", *vault.BackupVaultArn, "region", regionName)
			}

			tags := make(map[string][]string)
			descVaultOutput, err := svc.DescribeBackupVault(ctx.GetContext(), &backup.DescribeBackupVaultInput{
				BackupVaultName: vault.BackupVaultName,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to describe backup vault", "error", err, "vaultName", *vault.BackupVaultName, "region", regionName)
			}

			tagsOutput, err := svc.ListTags(ctx.GetContext(), &backup.ListTagsInput{
				ResourceArn: vault.BackupVaultArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for backup vault", "error", err, "vaultArn", *vault.BackupVaultArn, "region", regionName)
			} else {
				for k, v := range tagsOutput.Tags {
					tags[k] = append(tags[k], v)
				}
			}

			meta := structToMap(vault)
			if descVaultOutput != nil {
				meta["DescriptionDetails"] = structToMap(descVaultOutput)
			}

			resource := providers.Resource{
				Id:          aws.ToString(vault.BackupVaultName),
				ServiceName: ServiceNameBackup,
				Name:        aws.ToString(vault.BackupVaultName),
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         aws.ToString(vault.BackupVaultArn),
				CreatedAt:   aws.ToTime(vault.CreationDate),
				Type:        getAwsServiceResourceType(ServiceNameBackup, "backup-vault"),
			}
			resources = append(resources, resource)
		}
	}

	// --- Get Backup Plans ---
	plansPaginator := backup.NewListBackupPlansPaginator(svc, &backup.ListBackupPlansInput{})
	for plansPaginator.HasMorePages() {
		plansOutput, err := plansPaginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch backup plans", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, plan := range plansOutput.BackupPlansList {
			if plan.BackupPlanId == nil || plan.BackupPlanArn == nil {
				ctx.GetLogger().Warn("Backup plan ID or ARN is nil, skipping", "plan", plan, "region", regionName)
				continue
			}
			if plan.BackupPlanName == nil || plan.CreationDate == nil {
				ctx.GetLogger().Warn("Backup plan Name or CreationDate is nil", "planArn", *plan.BackupPlanArn, "region", regionName)
			}

			tags := make(map[string][]string)
			planDetails, err := svc.GetBackupPlan(ctx.GetContext(), &backup.GetBackupPlanInput{
				BackupPlanId: plan.BackupPlanId,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get backup plan details", "error", err, "planId", *plan.BackupPlanId, "region", regionName)
			}

			tagsOutput, err := svc.ListTags(ctx.GetContext(), &backup.ListTagsInput{
				ResourceArn: plan.BackupPlanArn,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for backup plan", "error", err, "planArn", *plan.BackupPlanArn, "region", regionName)
			} else {
				for k, v := range tagsOutput.Tags {
					tags[k] = append(tags[k], v)
				}
			}

			meta := structToMap(plan)
			if planDetails != nil && planDetails.BackupPlan != nil {
				meta["PlanDetails"] = structToMap(planDetails.BackupPlan)
			}

			name := aws.ToString(plan.BackupPlanId) // Fallback name
			if plan.BackupPlanName != nil {
				name = aws.ToString(plan.BackupPlanName)
			}

			resource := providers.Resource{
				Id:          aws.ToString(plan.BackupPlanId),
				ServiceName: ServiceNameBackup,
				Name:        name,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				Arn:         aws.ToString(plan.BackupPlanArn),
				CreatedAt:   aws.ToTime(plan.CreationDate),
				Type:        getAwsServiceResourceType(ServiceNameBackup, "backup-plan"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsBackup) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameBackup)
		return recommendations, err
	}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for this Backup resource", "resourceArn", resource.Arn, "region", resource.Region)
			continue
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameBackup, "backup-vault") {
			kmsKeyArn := ""
			if descDetailsAny, descOk := meta["DescriptionDetails"]; descOk {
				if descDetails, descMapOk := descDetailsAny.(map[string]any); descMapOk {
					if encryptionKeyArnAny, encKeyOk := descDetails["EncryptionKeyArn"]; encKeyOk {
						if keyArnStr, typeOk := encryptionKeyArnAny.(string); typeOk {
							kmsKeyArn = keyArnStr
						}
					}
				}
			}

			if kmsKeyArn == "" {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_backup_vault_encryption_cmk",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"vault_name": resource.Name, "vault_arn": resource.Arn, "reason": "Vault is not encrypted with a Customer Managed Key (CMK)."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}

			cfg.Region = resource.Region
			svc := backup.NewFromConfig(cfg)
			_, errPolicy := svc.GetBackupVaultAccessPolicy(ctx.GetContext(), &backup.GetBackupVaultAccessPolicyInput{
				BackupVaultName: aws.String(resource.Name),
			})
			if errPolicy != nil {
				var rnfe *types.ResourceNotFoundException
				if errors.As(errPolicy, &rnfe) {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName:        providers.RecommendationCategorySecurity,
						RuleName:            "aws_backup_vault_access_policy_exists",
						Severity:            providers.RecommendationSeverityLow,
						Savings:             0,
						Data:                map[string]any{"vault_name": resource.Name, "vault_arn": resource.Arn, "reason": "No access policy found for Backup Vault."},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				} else {
					ctx.GetLogger().Warn("failed to get backup vault access policy", "error", errPolicy, "vaultName", resource.Name, "region", resource.Region)
				}
			}

			lockConfigured := false
			if descDetailsAny, descOk := meta["DescriptionDetails"]; descOk {
				if descDetails, descMapOk := descDetailsAny.(map[string]any); descMapOk {
					if locked, _ := descDetails["Locked"].(bool); locked {
						lockConfigured = true
					}
				}
			}

			if !lockConfigured {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategorySecurity,
					RuleName:            "aws_backup_vault_lock_enabled",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"vault_name": resource.Name, "vault_arn": resource.Arn, "reason": "Backup Vault Lock is not configured or not active."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		if resource.Type == getAwsServiceResourceType(ServiceNameBackup, "backup-plan") {
			hasRules := false
			hasLifecycle := false
			if planDetailsAny, pdOk := meta["PlanDetails"]; pdOk {
				if planDetails, pdMapOk := planDetailsAny.(map[string]any); pdMapOk {
					if rulesAny, rulesOk := planDetails["Rules"]; rulesOk {
						if rulesSlice, rulesSliceOk := rulesAny.([]any); rulesSliceOk && len(rulesSlice) > 0 {
							hasRules = true
							for _, ruleAny := range rulesSlice {
								if ruleMap, ruleMapOk := ruleAny.(map[string]any); ruleMapOk {
									if lc, lcOk := ruleMap["Lifecycle"]; lcOk {
										if lifecycleMap, lifecycleMapOk := lc.(map[string]any); lifecycleMapOk {
											if _, ctaOk := lifecycleMap["MoveToColdStorageAfterDays"]; ctaOk {
												hasLifecycle = true
												break
											}
											if _, dadOk := lifecycleMap["DeleteAfterDays"]; dadOk {
												hasLifecycle = true
												break
											}
										}
									}
								}
							}
						}
					}
				}
			}

			if hasRules && !hasLifecycle {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "aws_backup_plan_rule_lifecycle",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             0,
					Data:                map[string]any{"plan_name": resource.Name, "plan_arn": resource.Arn, "reason": "No backup rule has a lifecycle policy (cold storage or deletion)."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
			if !hasRules {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_backup_plan_has_rules",
					Severity:            providers.RecommendationSeverityHigh,
					Savings:             0,
					Data:                map[string]any{"plan_name": resource.Name, "plan_arn": resource.Arn, "reason": "Backup plan has no rules defined."},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
			if len(resource.Tags) == 0 {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryConfiguration,
					RuleName:            "aws_tags",
					Severity:            providers.RecommendationSeverityLow,
					Savings:             0,
					Data:                map[string]any{"plan_name": resource.Name, "plan_arn": resource.Arn},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}
	}

	return recommendations, nil
}

func (a *awsBackup) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
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
			logGroupName := aws.ToString(lg.LogGroupName)
			describeLogStreamsOutput, err := logsSvc.DescribeLogStreams(ctx.GetContext(), &cloudwatchlogs.DescribeLogStreamsInput{
				LogGroupName:        aws.String(logGroupName),
				LogStreamNamePrefix: aws.String(resourceId),
				Limit:               aws.Int32(1),
			})
			if err == nil && len(describeLogStreamsOutput.LogStreams) > 0 {
				return logGroupName, nil
			}
		}
	}
	return "", nil
}

func (a *awsBackup) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "backup",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
	// 	return providers.ServiceMapApplication{}, err
	// }

	return app, nil
}
