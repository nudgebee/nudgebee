package aws

import (
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// Helper function to map CloudFront status strings to provider status
func cloudfrontStatusToNbStatus(status *string) providers.ResourceStatus {
	if status == nil {
		return providers.ResourceStatusUnknown
	}
	s := strings.ToLower(*status)
	switch s {
	case "deployed":
		return providers.ResourceStatusActive
	case "inprogress":
		return providers.ResourceStatusActive
	default:
		return providers.ResourceStatusUnknown
	}
}

type amazonCloudFront struct {
	DefaultAwsServiceImpl
}

func (a *amazonCloudFront) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for CloudFront yet.
	return errors.ErrUnsupported
}

func (a *amazonCloudFront) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for CloudFront yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonCloudFront) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

// IsGlobal reports CloudFront as a global service. ListDistributions is
// account-wide and the API endpoint lives in us-east-1; the central
// ListResources short-circuit calls GetResources exactly once.
func (a *amazonCloudFront) IsGlobal() bool {
	return true
}

func (a *amazonCloudFront) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameCloudFront)
		return []providers.Resource{}, err
	}
	cfg.Region = "us-east-1"
	svc := cloudfront.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := cloudfront.NewListDistributionsPaginator(svc, &cloudfront.ListDistributionsInput{})
	for paginator.HasMorePages() {
		distributionsOutput, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to list cloudfront distributions", "error", err, "accountNumber", account.AccountNumber)
			return resources, err
		}

		if distributionsOutput.DistributionList == nil {
			continue
		}

		for _, summary := range distributionsOutput.DistributionList.Items {
			if summary.Id == nil || summary.ARN == nil {
				ctx.GetLogger().Warn("Skipping CloudFront distribution due to missing Id or ARN", "summary", summary)
				continue
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.ListTagsForResource(ctx.GetContext(), &cloudfront.ListTagsForResourceInput{
				Resource: summary.ARN,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for cloudfront distribution", "error", err, "distributionArn", *summary.ARN)
			} else if tagsOutput.Tags != nil {
				for _, tag := range tagsOutput.Tags.Items {
					tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
				}
			}

			meta := structToMap(summary)
			details, err := svc.GetDistribution(ctx.GetContext(), &cloudfront.GetDistributionInput{
				Id: summary.Id,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to get cloudfront distribution details", "error", err, "distributionId", *summary.Id)
			} else if details.Distribution != nil {
				meta = structToMap(details.Distribution)
				if details.ETag != nil {
					meta["ETag"] = *details.ETag
				}
			}

			name := aws.ToString(summary.Id)
			if summary.Aliases != nil && summary.Aliases.Quantity != nil && *summary.Aliases.Quantity > 0 && len(summary.Aliases.Items) > 0 {
				name = summary.Aliases.Items[0]
			}

			resource := providers.Resource{
				Id:          aws.ToString(summary.Id),
				ServiceName: ServiceNameCloudFront,
				Name:        name,
				Status:      cloudfrontStatusToNbStatus(summary.Status),
				Region:      "global",
				Tags:        tags,
				Meta:        meta,
				Arn:         aws.ToString(summary.ARN),
				CreatedAt:   aws.ToTime(summary.LastModifiedTime),
				Type:        getAwsServiceResourceType(ServiceNameCloudFront, "distribution"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonCloudFront) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	for _, resource := range existingResources {
		if resource.Type != getAwsServiceResourceType(ServiceNameCloudFront, "distribution") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is empty, skipping recommendations", "distributionArn", resource.Arn)
			continue
		}

		distConfigAny, dcOk := meta["DistributionConfig"]
		if !dcOk {
			ctx.GetLogger().Warn("DistributionConfig missing in metadata", "distributionId", resource.Id)
			continue
		}
		distConfig, dcMapOk := distConfigAny.(map[string]interface{})
		if !dcMapOk {
			ctx.GetLogger().Warn("DistributionConfig is not a map", "distributionId", resource.Id)
			continue
		}

		if webACLIdStr, _ := ToString(distConfig["WebACLId"], "WebACLId", resource.Arn, resource.Region, ctx); webACLIdStr == "" {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_cloudfront_waf_integration",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"distribution_id": resource.Id, "reason": "Not associated with a WAF WebACL."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		loggingEnabled := false
		if loggingConfigAny, logOk := distConfig["Logging"]; logOk {
			if loggingConfig, logMapOk := loggingConfigAny.(map[string]interface{}); logMapOk {
				if enabledBool, enabledTypeOk := loggingConfig["Enabled"].(bool); enabledTypeOk {
					loggingEnabled = enabledBool
				}
			}
		}
		if !loggingEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_cloudfront_access_logging",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"distribution_id": resource.Id, "reason": "Access logging not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		recommendHTTPS := false
		if defaultBehaviorAny, defBehOk := distConfig["DefaultCacheBehavior"]; defBehOk {
			if defaultBehavior, defBehMapOk := defaultBehaviorAny.(map[string]interface{}); defBehMapOk {
				if policyStr, policyOk := ToString(defaultBehavior["ViewerProtocolPolicy"], "ViewerProtocolPolicy", resource.Arn, resource.Region, ctx); policyOk && policyStr == string(types.ViewerProtocolPolicyAllowAll) {
					recommendHTTPS = true
				}
			}
		}
		if !recommendHTTPS {
			if cacheBehaviorsAny, cacheBehOk := distConfig["CacheBehaviors"]; cacheBehOk {
				if cacheBehaviors, cacheBehMapOk := cacheBehaviorsAny.(map[string]interface{}); cacheBehMapOk {
					if itemsAny, itemsOk := cacheBehaviors["Items"]; itemsOk {
						if itemsSlice, itemsSliceOk := itemsAny.([]interface{}); itemsSliceOk {
							for _, itemAny := range itemsSlice {
								if itemMap, itemMapOk := itemAny.(map[string]interface{}); itemMapOk {
									if policyStr, policyOk := ToString(itemMap["ViewerProtocolPolicy"], "ViewerProtocolPolicy", resource.Arn, resource.Region, ctx); policyOk && policyStr == string(types.ViewerProtocolPolicyAllowAll) {
										recommendHTTPS = true
										break
									}
								}
							}
						}
					}
				}
			}
		}
		if recommendHTTPS {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_cloudfront_viewer_protocol_https",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"distribution_id": resource.Id, "reason": "Allows HTTP traffic."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		recommendOAC := false
		if originsAny, originsOk := distConfig["Origins"]; originsOk {
			if origins, originsMapOk := originsAny.(map[string]interface{}); originsMapOk {
				if itemsAny, itemsOk := origins["Items"]; itemsOk {
					if itemsSlice, itemsSliceOk := itemsAny.([]interface{}); itemsSliceOk {
						for _, itemAny := range itemsSlice {
							if itemMap, itemMapOk := itemAny.(map[string]interface{}); itemMapOk {
								if s3OriginConfigAny, s3ConfigExists := itemMap["S3OriginConfig"]; s3ConfigExists {
									if s3OriginConfig, s3MapOk := s3OriginConfigAny.(map[string]interface{}); s3MapOk {
										if oaiStr, _ := ToString(s3OriginConfig["OriginAccessIdentity"], "OriginAccessIdentity", resource.Arn, resource.Region, ctx); oaiStr == "" {
											if oacIDStr, _ := ToString(itemMap["OriginAccessControlId"], "OriginAccessControlId", resource.Arn, resource.Region, ctx); oacIDStr == "" {
												recommendOAC = true
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
		}
		if recommendOAC {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_cloudfront_origin_access_control",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"distribution_id": resource.Id, "reason": "S3 origin does not use OAC or OAI."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		geoRestricted := false
		if restrictionsAny, restriccionesOk := distConfig["Restrictions"]; restriccionesOk {
			if restrictions, restMapOk := restrictionsAny.(map[string]interface{}); restMapOk {
				if geoRestrictionAny, geoOk := restrictions["GeoRestriction"]; geoOk {
					if geoRestriction, geoMapOk := geoRestrictionAny.(map[string]interface{}); geoMapOk {
						if restrictionTypeStr, typeOk := ToString(geoRestriction["RestrictionType"], "RestrictionType", resource.Arn, resource.Region, ctx); typeOk && restrictionTypeStr != string(types.GeoRestrictionTypeNone) {
							geoRestricted = true
						}
					}
				}
			}
		}
		if !geoRestricted {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_cloudfront_geo_restriction",
				Severity:            providers.RecommendationSeverityLow,
				Data:                map[string]any{"distribution_id": resource.Id, "reason": "Geo-restriction not enabled."},
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
				Data:                map[string]any{"distribution_id": resource.Id},
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

func (a *amazonCloudFront) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	return "", nil
}

func (a *amazonCloudFront) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "cloudfront",
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
