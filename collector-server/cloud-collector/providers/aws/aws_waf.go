package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	"github.com/aws/aws-sdk-go-v2/service/wafv2/types"
)

type awsWAF struct {
	DefaultAwsServiceImpl
}

func (a *awsWAF) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsWAF) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsWAF) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsWAF) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// WAF logs go to CloudWatch Logs or S3
	return fmt.Sprintf("aws-waf-logs-%s", resourceId), nil
}

func (a *awsWAF) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", region)
		return []providers.Resource{}, err
	}
	cfg.Region = region
	svc := wafv2.NewFromConfig(cfg)

	// CLOUDFRONT-scope WebACLs live behind a us-east-1 endpoint regardless of
	// what regions the customer iterates. The previous implementation gated
	// CLOUDFRONT scope on `region == "us-east-1"` — accounts whose synced
	// regions list lacked us-east-1 silently lost every CloudFront WAF. We now
	// always include CLOUDFRONT scope using a dedicated us-east-1 client; the
	// downstream resourceMap dedupes by external_resource_id so concurrent
	// per-region goroutines coalesce to one row per resource.
	cfClient := svc
	if region != "us-east-1" {
		cfCfg := cfg.Copy()
		cfCfg.Region = "us-east-1"
		cfClient = wafv2.NewFromConfig(cfCfg)
	}

	type scopeClient struct {
		scope  types.Scope
		client *wafv2.Client
	}
	scopes := []scopeClient{
		{types.ScopeRegional, svc},
		{types.ScopeCloudfront, cfClient},
	}
	resources := []providers.Resource{}

	for _, sc := range scopes {
		scope := sc.scope
		svc := sc.client
		// List Web ACLs
		var nextMarker *string
		for {
			result, err := svc.ListWebACLs(context.TODO(), &wafv2.ListWebACLsInput{
				Scope:      scope,
				NextMarker: nextMarker,
				Limit:      aws.Int32(100),
			})
			if err != nil {
				ctx.GetLogger().Error("failed to fetch WAF web ACLs", "error", err, "accountNumber", account.AccountNumber, "region", region, "scope", scope)
				break
			}

			for _, webACL := range result.WebACLs {
				if webACL.Id == nil || webACL.Name == nil {
					ctx.GetLogger().Warn("Skipping WAF Web ACL due to missing ID or name")
					continue
				}

				// Get detailed information about the Web ACL
				detailResult, err := svc.GetWebACL(context.TODO(), &wafv2.GetWebACLInput{
					Id:    webACL.Id,
					Name:  webACL.Name,
					Scope: scope,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to describe web ACL", "error", err, "webACLId", *webACL.Id)
					continue
				}

				tags := make(map[string][]string)

				// Get tags for the Web ACL
				if webACL.ARN != nil {
					tagsResult, err := svc.ListTagsForResource(context.TODO(), &wafv2.ListTagsForResourceInput{
						ResourceARN: webACL.ARN,
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch WAF tags", "error", err, "webACLArn", *webACL.ARN)
					} else if tagsResult.TagInfoForResource != nil && tagsResult.TagInfoForResource.TagList != nil {
						for _, tag := range tagsResult.TagInfoForResource.TagList {
							if tag.Key != nil && tag.Value != nil {
								tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
							}
						}
					}
				}

				metaMap := structToMap(detailResult.WebACL)

				// Get logging configuration
				if webACL.ARN != nil {
					loggingResult, err := svc.GetLoggingConfiguration(context.TODO(), &wafv2.GetLoggingConfigurationInput{
						ResourceArn: webACL.ARN,
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch WAF logging configuration", "error", err, "webACLArn", *webACL.ARN)
					} else if loggingResult.LoggingConfiguration != nil {
						metaMap["LoggingConfiguration"] = structToMap(loggingResult.LoggingConfiguration)
					}
				}

				// Get associated resources
				if webACL.ARN != nil {
					resourcesResult, err := svc.ListResourcesForWebACL(context.TODO(), &wafv2.ListResourcesForWebACLInput{
						WebACLArn: webACL.ARN,
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch associated resources", "error", err, "webACLArn", *webACL.ARN)
					} else {
						metaMap["AssociatedResources"] = resourcesResult.ResourceArns
						metaMap["AssociatedResourceCount"] = len(resourcesResult.ResourceArns)
					}
				}

				// Get managed rule group usage
				if detailResult.WebACL != nil && detailResult.WebACL.Rules != nil {
					managedRuleCount := 0
					customRuleCount := 0

					for _, rule := range detailResult.WebACL.Rules {
						if rule.Statement != nil && rule.Statement.ManagedRuleGroupStatement != nil {
							managedRuleCount++
						} else {
							customRuleCount++
						}
					}

					metaMap["ManagedRuleCount"] = managedRuleCount
					metaMap["CustomRuleCount"] = customRuleCount
				}

				createdAt := time.Time{}
				status := providers.ResourceStatusActive // WAF Web ACLs are active if they exist

				scopeStr := string(scope)
				effectiveRegion := region
				if scope == types.ScopeCloudfront {
					effectiveRegion = "global"
				}

				resource := providers.Resource{
					Id:          *webACL.Id,
					ServiceName: ServiceNameWAF,
					Name:        *webACL.Name,
					Status:      status,
					Region:      effectiveRegion,
					Tags:        tags,
					Meta:        metaMap,
					Arn:         *webACL.ARN,
					CreatedAt:   createdAt,
					Type:        getAwsServiceResourceType(ServiceNameWAF, "webacl") + ":" + scopeStr,
				}
				resources = append(resources, resource)
			}

			if result.NextMarker == nil {
				break
			}
			nextMarker = result.NextMarker
		}

		// List IP Sets
		nextMarker = nil
		for {
			result, err := svc.ListIPSets(context.TODO(), &wafv2.ListIPSetsInput{
				Scope:      scope,
				NextMarker: nextMarker,
				Limit:      aws.Int32(100),
			})
			if err != nil {
				ctx.GetLogger().Error("failed to fetch WAF IP sets", "error", err, "accountNumber", account.AccountNumber, "region", region, "scope", scope)
				break
			}

			for _, ipSet := range result.IPSets {
				if ipSet.Id == nil || ipSet.Name == nil {
					ctx.GetLogger().Warn("Skipping WAF IP Set due to missing ID or name")
					continue
				}

				// Get detailed information
				detailResult, err := svc.GetIPSet(context.TODO(), &wafv2.GetIPSetInput{
					Id:    ipSet.Id,
					Name:  ipSet.Name,
					Scope: scope,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to describe IP set", "error", err, "ipSetId", *ipSet.Id)
					continue
				}

				tags := make(map[string][]string)

				// Get tags for the IP Set
				if ipSet.ARN != nil {
					tagsResult, err := svc.ListTagsForResource(context.TODO(), &wafv2.ListTagsForResourceInput{
						ResourceARN: ipSet.ARN,
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch WAF IP set tags", "error", err, "ipSetArn", *ipSet.ARN)
					} else if tagsResult.TagInfoForResource != nil && tagsResult.TagInfoForResource.TagList != nil {
						for _, tag := range tagsResult.TagInfoForResource.TagList {
							if tag.Key != nil && tag.Value != nil {
								tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
							}
						}
					}
				}

				metaMap := structToMap(detailResult.IPSet)

				effectiveRegion := region
				if scope == types.ScopeCloudfront {
					effectiveRegion = "global"
				}

				resource := providers.Resource{
					Id:          *ipSet.Id,
					ServiceName: ServiceNameWAF,
					Name:        *ipSet.Name,
					Status:      providers.ResourceStatusActive,
					Region:      effectiveRegion,
					Tags:        tags,
					Meta:        metaMap,
					Arn:         *ipSet.ARN,
					CreatedAt:   time.Time{},
					Type:        getAwsServiceResourceType(ServiceNameWAF, "ipset"),
				}
				resources = append(resources, resource)
			}

			if result.NextMarker == nil {
				break
			}
			nextMarker = result.NextMarker
		}

		// List Regex Pattern Sets
		nextMarker = nil
		for {
			result, err := svc.ListRegexPatternSets(context.TODO(), &wafv2.ListRegexPatternSetsInput{
				Scope:      scope,
				NextMarker: nextMarker,
				Limit:      aws.Int32(100),
			})
			if err != nil {
				ctx.GetLogger().Error("failed to fetch WAF regex pattern sets", "error", err, "accountNumber", account.AccountNumber, "region", region, "scope", scope)
				break
			}

			for _, regexSet := range result.RegexPatternSets {
				if regexSet.Id == nil || regexSet.Name == nil {
					ctx.GetLogger().Warn("Skipping WAF Regex Pattern Set due to missing ID or name")
					continue
				}

				tags := make(map[string][]string)

				// Get tags
				if regexSet.ARN != nil {
					tagsResult, err := svc.ListTagsForResource(context.TODO(), &wafv2.ListTagsForResourceInput{
						ResourceARN: regexSet.ARN,
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch WAF regex set tags", "error", err, "regexSetArn", *regexSet.ARN)
					} else if tagsResult.TagInfoForResource != nil && tagsResult.TagInfoForResource.TagList != nil {
						for _, tag := range tagsResult.TagInfoForResource.TagList {
							if tag.Key != nil && tag.Value != nil {
								tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
							}
						}
					}
				}

				metaMap := structToMap(regexSet)

				effectiveRegion := region
				if scope == types.ScopeCloudfront {
					effectiveRegion = "global"
				}

				resource := providers.Resource{
					Id:          *regexSet.Id,
					ServiceName: ServiceNameWAF,
					Name:        *regexSet.Name,
					Status:      providers.ResourceStatusActive,
					Region:      effectiveRegion,
					Tags:        tags,
					Meta:        metaMap,
					Arn:         *regexSet.ARN,
					CreatedAt:   time.Time{},
					Type:        getAwsServiceResourceType(ServiceNameWAF, "regexpatternset"),
				}
				resources = append(resources, resource)
			}

			if result.NextMarker == nil {
				break
			}
			nextMarker = result.NextMarker
		}
	}

	return resources, nil
}

func (a *awsWAF) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check for Web ACLs without logging enabled
		if resource.Type == getAwsServiceResourceType(ServiceNameWAF, "webacl") ||
			resource.Type == getAwsServiceResourceType(ServiceNameWAF, "webacl")+":REGIONAL" ||
			resource.Type == getAwsServiceResourceType(ServiceNameWAF, "webacl")+":CLOUDFRONT" {

			if _, hasLogging := meta["LoggingConfiguration"]; !hasLogging {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_waf_logging_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "WAF Web ACL does not have logging enabled",
						"recommendation": "Enable logging to CloudWatch Logs or S3 for security monitoring",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check for Web ACLs with no associated resources
			if resourceCount, ok := meta["AssociatedResourceCount"].(int); ok && resourceCount == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_waf_webacl_not_associated",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "WAF Web ACL is not associated with any resources",
						"recommendation": "Delete unused Web ACLs to save costs",
					},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check for Web ACLs with no rules
			if rules, ok := meta["Rules"].([]any); ok && len(rules) == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_waf_no_rules",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      0,
					Data: map[string]any{
						"message":        "WAF Web ACL has no rules defined",
						"recommendation": "Add rules to protect your resources from web attacks",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check for Web ACLs without rate-based rules
			if rules, ok := meta["Rules"].([]any); ok {
				hasRateBasedRule := false
				for _, ruleAny := range rules {
					if ruleMap, ok := ruleAny.(map[string]any); ok {
						if statement, ok := ruleMap["Statement"].(map[string]any); ok {
							if _, hasRate := statement["RateBasedStatement"]; hasRate {
								hasRateBasedRule = true
								break
							}
						}
					}
				}

				if !hasRateBasedRule && len(rules) > 0 {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategorySecurity,
						RuleName:     "aws_waf_no_rate_limiting",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "WAF Web ACL does not have rate-based rules",
							"recommendation": "Add rate-based rules to protect against DDoS and brute-force attacks",
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

			// Recommend using AWS Managed Rules
			if managedCount, ok := meta["ManagedRuleCount"].(int); ok {
				if customCount, ok := meta["CustomRuleCount"].(int); ok {
					if managedCount == 0 && customCount > 0 {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategorySecurity,
							RuleName:     "aws_waf_no_managed_rules",
							Severity:     providers.RecommendationSeverityLow,
							Savings:      0,
							Data: map[string]any{
								"message":         "WAF Web ACL uses only custom rules",
								"recommendation":  "Consider using AWS Managed Rules for better protection against common threats",
								"customRuleCount": customCount,
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
		}

		// Check for empty IP sets
		if resource.Type == getAwsServiceResourceType(ServiceNameWAF, "ipset") {
			if addresses, ok := meta["Addresses"].([]any); ok && len(addresses) == 0 {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_waf_empty_ipset",
					Severity:     providers.RecommendationSeverityLow,
					Savings:      0,
					Data: map[string]any{
						"message":        "WAF IP Set is empty",
						"recommendation": "Delete unused IP sets",
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

	return recommendations, nil
}

func (a *awsWAF) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "waf",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	return app, nil
}
