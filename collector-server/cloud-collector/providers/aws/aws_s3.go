package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type amazonS3 struct {
	DefaultAwsServiceImpl
}

// IsGlobal reports S3 as a global service: ListBuckets returns every bucket in
// the account in a single account-wide call. Per-region iteration would either
// be wasteful (N×ListBuckets) or — as it used to — silently drop buckets whose
// region was not in the caller's iteration set.
func (a *amazonS3) IsGlobal() bool {
	return true
}

func (a *amazonS3) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonS3) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonS3) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

// GetResources lists every S3 bucket in the account regardless of the `region`
// argument (S3 is a global service — see IsGlobal). Each returned Resource has
// Region set to the bucket's actual location.
func (a *amazonS3) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return []providers.Resource{}, err
	}
	svc := s3.NewFromConfig(cfg)

	// Paginate ListBuckets — a single response is capped (10k per page in v2),
	// and accounts with thousands of buckets would otherwise be truncated.
	var buckets []types.Bucket
	paginator := s3.NewListBucketsPaginator(svc, &s3.ListBucketsInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch s3 resources", "error", err, "accountNumber", account.AccountNumber)
			return nil, err
		}
		buckets = append(buckets, page.Buckets...)
	}

	resources := []providers.Resource{}

	// Lazily fetch CloudWatch alarms per region so we hit DescribeAlarms once
	// per bucket-region across the whole account, not once per bucket.
	alarmsByRegion := map[string]map[string][]any{}
	getAlarms := func(region string) map[string][]any {
		if region == "" {
			return nil
		}
		if cached, ok := alarmsByRegion[region]; ok {
			return cached
		}
		fetched := fetchAlarmsByResource(ctx, account, region)
		alarmsByRegion[region] = fetched
		return fetched
	}

	for _, instance := range buckets {
		if instance.Name == nil || instance.CreationDate == nil {
			ctx.GetLogger().Warn("skipping s3 bucket with missing required fields", "accountNumber", account.AccountNumber)
			continue
		}

		// Get Bucket Location
		bucketLocation, err := svc.GetBucketLocation(ctx.GetContext(), &s3.GetBucketLocationInput{
			Bucket:              instance.Name,
			ExpectedBucketOwner: aws.String(account.AccountNumber),
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch s3 bucket location, skipping bucket", "error", err, "bucketName", *instance.Name, "accountNumber", account.AccountNumber)
			continue // Skip this bucket and proceed to the next one
		}

		// For "us-east-1", LocationConstraint is empty. For other regions, it's the region string.
		bucketRegion := "us-east-1"
		if bucketLocation.LocationConstraint != "" {
			bucketRegion = string(bucketLocation.LocationConstraint)
		}

		// Create a new S3 client for the specific bucket region
		regionalCfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
		if err != nil {
			ctx.GetLogger().Error("failed to create aws session for bucket region", "error", err, "accountNumber", account.AccountNumber, "bucketRegion", bucketRegion)
			continue
		}
		regionalCfg.Region = bucketRegion
		regionalSvc := s3.NewFromConfig(regionalCfg)

		// Fetch Tags
		tags := make(map[string][]string)
		taggingOutput, err := regionalSvc.GetBucketTagging(ctx.GetContext(), &s3.GetBucketTaggingInput{
			Bucket:              instance.Name,
			ExpectedBucketOwner: aws.String(account.AccountNumber),
		})

		if err != nil {
			var apiErr *smithy.GenericAPIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchTagSet" {
				ctx.GetLogger().Info("no tags found for s3 bucket", "bucketName", *instance.Name, "region", bucketRegion)
			} else {
				ctx.GetLogger().Warn("failed to fetch s3 tags", "error", err, "bucketName", *instance.Name, "region", bucketRegion)
			}
		} else if taggingOutput != nil {
			for _, tag := range taggingOutput.TagSet {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
		}

		instanceMap := structToMap(instance)

		// Fetch Public Access Block Configuration
		publicAccessBlockOutput, err := regionalSvc.GetPublicAccessBlock(ctx.GetContext(), &s3.GetPublicAccessBlockInput{
			Bucket: instance.Name,
		})
		if err != nil {
			var apiErr *smithy.GenericAPIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchPublicAccessBlockConfiguration" {
				ctx.GetLogger().Info("no public access block configuration found for s3 bucket", "bucketName", *instance.Name, "region", bucketRegion)
				instanceMap["PublicAccessBlockConfiguration"] = nil // Explicitly set to nil if not found
			} else {
				ctx.GetLogger().Warn("failed to fetch public access block configuration for s3 bucket", "error", err, "bucketName", *instance.Name, "region", bucketRegion)
				instanceMap["PublicAccessBlockConfiguration"] = nil // Set to nil on other errors
			}
		} else {
			instanceMap["PublicAccessBlockConfiguration"] = publicAccessBlockOutput.PublicAccessBlockConfiguration
		}

		// Fetch Server Side Encryption Configuration
		encryptionOutput, err := regionalSvc.GetBucketEncryption(ctx.GetContext(), &s3.GetBucketEncryptionInput{
			Bucket: instance.Name,
		})
		if err != nil {
			var apiErr *smithy.GenericAPIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ServerSideEncryptionConfigurationNotFoundError" {
				ctx.GetLogger().Info("no server side encryption configuration found for s3 bucket", "bucketName", *instance.Name, "region", bucketRegion)
				instanceMap["ServerSideEncryptionConfiguration"] = nil
			} else {
				ctx.GetLogger().Warn("failed to fetch server side encryption configuration for s3 bucket", "error", err, "bucketName", *instance.Name, "region", bucketRegion)
				instanceMap["ServerSideEncryptionConfiguration"] = nil
			}
		} else {
			instanceMap["ServerSideEncryptionConfiguration"] = encryptionOutput.ServerSideEncryptionConfiguration
		}

		// Fetch Versioning Configuration
		versioningOutput, err := regionalSvc.GetBucketVersioning(ctx.GetContext(), &s3.GetBucketVersioningInput{
			Bucket: instance.Name,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch versioning configuration for s3 bucket", "error", err, "bucketName", *instance.Name, "region", bucketRegion)
			instanceMap["VersioningConfiguration"] = nil
		} else {
			instanceMap["VersioningConfiguration"] = versioningOutput.Status
		}

		// Fetch Logging Configuration
		loggingOutput, err := regionalSvc.GetBucketLogging(ctx.GetContext(), &s3.GetBucketLoggingInput{
			Bucket: instance.Name,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch logging configuration for s3 bucket", "error", err, "bucketName", *instance.Name, "region", bucketRegion)
			instanceMap["LoggingEnabled"] = nil
		} else {
			instanceMap["LoggingEnabled"] = loggingOutput.LoggingEnabled
		}

		instanceMap["AlarmDetails"] = getAlarms(bucketRegion)[*instance.Name]

		resource := providers.Resource{
			Id:          *instance.Name,
			ServiceName: ServiceNameS3,
			Name:        *instance.Name,
			Status:      providers.ResourceStatusActive,
			Region:      bucketRegion,
			Tags:        tags,
			Meta:        instanceMap,
			CreatedAt:   *instance.CreationDate,
			Type:        getAwsServiceResourceType(ServiceNameS3, "storage"),
		}
		resources = append(resources, resource)

	}
	return resources, nil
}

// / https://www.trendmicro.com/cloudoneconformity-staging/knowledge-base/aws/S3/
func (a *amazonS3) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {

	recommendations := []providers.Recommendation{}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return nil, err
	}
	for _, resource := range existingResources {

		if resource.Type == "storage" && resource.Meta != nil && strings.ToLower(resource.ServiceName) == "amazons3" {
			regionalCfg := cfg.Copy()
			regionalCfg.Region = resource.Region
			svc := s3.NewFromConfig(regionalCfg)
			acl, err := svc.GetBucketAcl(context.TODO(), &s3.GetBucketAclInput{
				Bucket: &resource.Name,
			})
			if err != nil {
				ctx.GetLogger().Error("error getting bucket acl", "error", err)
			}
			if acl != nil && len(acl.Grants) > 0 {
				for _, grant := range acl.Grants {
					// check for public read
					if grant.Grantee != nil && grant.Grantee.URI != nil && *grant.Grantee.URI == "http://acs.amazonaws.com/groups/global/AllUsers" {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     "aws_s3_public_access_acl",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"bucket_name": resource.Name,
								"bucket_arn":  resource.Arn,
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

			policy, err := svc.GetBucketPolicyStatus(context.TODO(), &s3.GetBucketPolicyStatusInput{
				Bucket: &resource.Name,
			})
			if err != nil {
				var apiErr *smithy.GenericAPIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchBucketPolicy" {
					ctx.GetLogger().Error("error getting bucket policy status", "error", err, "bucketName", resource.Name)
				}
				// If there was an error (and it wasn't NoSuchBucketPolicy),
				// and policy or policy.PolicyStatus is nil, skip this recommendation.
				// This check is crucial if the error implies policy object might be indeterminate.
				if policy == nil || policy.PolicyStatus == nil {
					ctx.GetLogger().Warn("skipping public_access_policy check due to nil policy/policyStatus after error or for NoSuchBucketPolicy", "bucketName", resource.Name)
					goto SkipPublicAccessPolicyCheck // Using goto to clearly jump past this specific recommendation block
				}
			}

			// Proceed if no error OR if error was NoSuchBucketPolicy (in which case policy.PolicyStatus might still be informative or nil)
			// Add explicit nil check for policy.PolicyStatus.IsPublic itself.
			if policy != nil && policy.PolicyStatus != nil && *policy.PolicyStatus.IsPublic {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_s3_public_access_policy",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"bucket_name": resource.Name,
						"bucket_arn":  resource.Arn,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}
		SkipPublicAccessPolicyCheck: // Label for goto

			// check for bucket versioning
			versioning, err := svc.GetBucketVersioning(context.TODO(), &s3.GetBucketVersioningInput{
				Bucket: &resource.Name,
			})
			if err != nil {
				ctx.GetLogger().Error("error getting bucket versioning", "error", err)
			}
			if versioning != nil && (versioning.Status != types.BucketVersioningStatusEnabled) {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_s3_versioning",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_name": resource.Name,
						"bucket_arn":  resource.Arn,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// check for bucket tags
			if len(resource.Tags) == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_tags",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_name": resource.Name,
						"bucket_arn":  resource.Arn,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// check for lifecycle
			lifecycle, err := svc.GetBucketLifecycleConfiguration(context.TODO(), &s3.GetBucketLifecycleConfigurationInput{
				Bucket:              &resource.Name,
				ExpectedBucketOwner: &account.AccountNumber,
			})
			if err != nil {
				var apiErr *smithy.GenericAPIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchLifecycleConfiguration" {
					ctx.GetLogger().Error("error getting bucket lifecycle", "error", err)
					continue
				}
			}
			if (err != nil && strings.Contains(err.Error(), "NoSuchLifecycleConfiguration")) || (lifecycle != nil && (len(lifecycle.Rules) == 0)) {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "aws_s3_lifecycle",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"bucket_name": resource.Name,
						"bucket_arn":  resource.Arn,
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

func (a *amazonS3) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonS3) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "s3",
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
