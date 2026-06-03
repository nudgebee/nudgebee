package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	//"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func route53HealthCheckStatusToNbStatus(status string) providers.ResourceStatus {
	switch strings.ToLower(status) {
	case "healthy":
		return providers.ResourceStatusActive
	case "unhealthy":
		return providers.ResourceStatusInactive
	default:
		return providers.ResourceStatusUnknown
	}
}

type awsRoute53 struct {
	DefaultAwsServiceImpl
}

func (a *awsRoute53) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *awsRoute53) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsRoute53) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsRoute53) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region string, resourceId string) (string, error) {
	// Route 53 query logs go to CloudWatch Logs
	return "/aws/route53/" + resourceId, nil
}

// IsGlobal reports Route 53 as a global service. ListHostedZones returns
// account-wide results; the central ListResources short-circuit calls
// GetResources exactly once.
func (a *awsRoute53) IsGlobal() bool {
	return true
}

func (a *awsRoute53) GetResources(ctx providers.CloudProviderContext, account providers.Account, _ string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return []providers.Resource{}, err
	}

	// Route 53 is global, no need to set region
	svc := route53.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// List all hosted zones
	paginator := route53.NewListHostedZonesPaginator(svc, &route53.ListHostedZonesInput{})
	for paginator.HasMorePages() {
		result, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch route53 hosted zones", "error", err, "accountNumber", account.AccountNumber)
			return resources, err
		}

		for _, zone := range result.HostedZones {
			if zone.Id == nil || zone.Name == nil {
				ctx.GetLogger().Warn("Skipping Route 53 hosted zone due to missing ID or name")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the hosted zone
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &route53.ListTagsForResourceInput{
				ResourceType: types.TagResourceTypeHostedzone,
				ResourceId:   zone.Id,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch route53 tags", "error", err, "zoneId", *zone.Id)
			} else if tagsResult.ResourceTagSet != nil {
				for _, tag := range tagsResult.ResourceTagSet.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(zone)

			// Get record sets for this hosted zone
			recordSets := []map[string]any{}
			recordPaginator := route53.NewListResourceRecordSetsPaginator(svc, &route53.ListResourceRecordSetsInput{
				HostedZoneId: zone.Id,
			})

			recordCount := 0
			for recordPaginator.HasMorePages() && recordCount < 100 { // Limit to first 100 records
				recordsResult, err := recordPaginator.NextPage(context.TODO())
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch route53 record sets", "error", err, "zoneId", *zone.Id)
					break
				}

				for _, record := range recordsResult.ResourceRecordSets {
					recordSets = append(recordSets, structToMap(record))
					recordCount++
					if recordCount >= 100 {
						break
					}
				}
			}
			metaMap["RecordSets"] = recordSets
			metaMap["RecordSetCount"] = recordCount

			// Get query logging configuration if exists
			queryLogResult, err := svc.ListQueryLoggingConfigs(context.TODO(), &route53.ListQueryLoggingConfigsInput{
				HostedZoneId: zone.Id,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch route53 query logging config", "error", err, "zoneId", *zone.Id)
			} else if len(queryLogResult.QueryLoggingConfigs) > 0 {
				metaMap["QueryLoggingConfigs"] = queryLogResult.QueryLoggingConfigs
			}

			// Get DNSSEC status
			dnssecResult, err := svc.GetDNSSEC(context.TODO(), &route53.GetDNSSECInput{
				HostedZoneId: zone.Id,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch route53 DNSSEC status", "error", err, "zoneId", *zone.Id)
			} else if dnssecResult != nil {
				metaMap["DNSSEC"] = structToMap(dnssecResult)
			}

			createdAt := time.Time{}
			status := providers.ResourceStatusActive // Hosted zones are generally active if they exist

			// Extract zone ID without the /hostedzone/ prefix for cleaner ID
			zoneId := strings.TrimPrefix(*zone.Id, "/hostedzone/")

			resource := providers.Resource{
				Id:          zoneId,
				ServiceName: ServiceNameRoute53,
				Name:        strings.TrimSuffix(*zone.Name, "."), // Remove trailing dot
				Status:      status,
				Region:      "global", // Route 53 is a global service
				Tags:        tags,
				Meta:        metaMap,
				Arn:         *zone.Id, // Hosted zone ID acts as ARN
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameRoute53, "hostedzone"),
			}
			resources = append(resources, resource)
		}
	}

	// Also get health checks
	healthCheckPaginator := route53.NewListHealthChecksPaginator(svc, &route53.ListHealthChecksInput{})
	for healthCheckPaginator.HasMorePages() {
		result, err := healthCheckPaginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch route53 health checks", "error", err, "accountNumber", account.AccountNumber)
			break
		}

		for _, hc := range result.HealthChecks {
			if hc.Id == nil {
				ctx.GetLogger().Warn("Skipping Route 53 health check due to missing ID")
				continue
			}

			tags := make(map[string][]string)

			// Get tags for the health check
			tagsResult, err := svc.ListTagsForResource(context.TODO(), &route53.ListTagsForResourceInput{
				ResourceType: types.TagResourceTypeHealthcheck,
				ResourceId:   hc.Id,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch route53 health check tags", "error", err, "healthCheckId", *hc.Id)
			} else if tagsResult.ResourceTagSet != nil {
				for _, tag := range tagsResult.ResourceTagSet.Tags {
					if tag.Key != nil && tag.Value != nil {
						tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
					}
				}
			}

			metaMap := structToMap(hc)

			// Get health check status
			statusResult, err := svc.GetHealthCheckStatus(context.TODO(), &route53.GetHealthCheckStatusInput{
				HealthCheckId: hc.Id,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch route53 health check status", "error", err, "healthCheckId", *hc.Id)
			} else if statusResult.HealthCheckObservations != nil {
				metaMap["HealthCheckObservations"] = statusResult.HealthCheckObservations

				// Determine overall health status
				healthyCount := 0
				for _, obs := range statusResult.HealthCheckObservations {
					if obs.StatusReport != nil && obs.StatusReport.Status != nil {
						if strings.ToLower(*obs.StatusReport.Status) == "success" {
							healthyCount++
						}
					}
				}
				metaMap["HealthyCheckers"] = healthyCount
				metaMap["TotalCheckers"] = len(statusResult.HealthCheckObservations)
			}

			status := providers.ResourceStatusActive
			if metaMap["HealthyCheckers"] != nil && metaMap["TotalCheckers"] != nil {
				healthy := metaMap["HealthyCheckers"].(int)
				total := metaMap["TotalCheckers"].(int)
				if total > 0 && healthy < total/2 { // Less than 50% healthy
					status = providers.ResourceStatusInactive
				}
			}

			healthCheckName := *hc.Id
			if hc.HealthCheckConfig != nil && hc.HealthCheckConfig.FullyQualifiedDomainName != nil {
				healthCheckName = *hc.HealthCheckConfig.FullyQualifiedDomainName
			}

			resource := providers.Resource{
				Id:          *hc.Id,
				ServiceName: ServiceNameRoute53,
				Name:        healthCheckName,
				Status:      status,
				Region:      "global",
				Tags:        tags,
				Meta:        metaMap,
				Arn:         *hc.Id,
				CreatedAt:   time.Time{},
				Type:        getAwsServiceResourceType(ServiceNameRoute53, "healthcheck"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *awsRoute53) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			continue
		}

		// Check if query logging is disabled for hosted zones
		if resource.Type == getAwsServiceResourceType(ServiceNameRoute53, "hostedzone") {
			if _, hasQueryLogging := meta["QueryLoggingConfigs"]; !hasQueryLogging {
				recommendation := providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "aws_route53_query_logging_disabled",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"message":        "Query logging is not enabled for this hosted zone",
						"recommendation": "Enable query logging to CloudWatch Logs for security and troubleshooting",
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				}
				recommendations = append(recommendations, recommendation)
			}

			// Check if DNSSEC is disabled
			if dnssec, ok := meta["DNSSEC"].(map[string]any); ok {
				if status, ok := dnssec["Status"].(map[string]any); ok {
					if serveSignature, ok := status["ServeSignature"].(string); ok {
						if strings.ToUpper(serveSignature) != "SIGNING" {
							recommendation := providers.Recommendation{
								CategoryName: providers.RecommendationCategorySecurity,
								RuleName:     "aws_route53_dnssec_disabled",
								Severity:     providers.RecommendationSeverityMedium,
								Savings:      0,
								Data: map[string]any{
									"message":        "DNSSEC is not enabled for this hosted zone",
									"recommendation": "Enable DNSSEC to protect against DNS spoofing attacks",
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

			// Check for hosted zones with no records (except NS and SOA)
			if recordCount, ok := meta["RecordSetCount"].(int); ok {
				if recordCount <= 2 { // Only NS and SOA records
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "aws_route53_empty_hosted_zone",
						Severity:     providers.RecommendationSeverityLow,
						Savings:      0.50, // $0.50 per month per hosted zone
						Data: map[string]any{
							"message":        "Hosted zone has no user-defined DNS records",
							"recommendation": "Consider deleting unused hosted zones to save costs",
							"recordCount":    recordCount,
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

		// Check health check status
		if resource.Type == getAwsServiceResourceType(ServiceNameRoute53, "healthcheck") {
			if healthy, ok := meta["HealthyCheckers"].(int); ok {
				if total, ok := meta["TotalCheckers"].(int); ok && total > 0 {
					healthPercentage := float64(healthy) / float64(total)
					if healthPercentage < 0.5 {
						recommendation := providers.Recommendation{
							CategoryName: providers.RecommendationCategoryConfiguration,
							RuleName:     "aws_route53_unhealthy_health_check",
							Severity:     providers.RecommendationSeverityHigh,
							Savings:      0,
							Data: map[string]any{
								"message":          "Health check is failing on majority of checkers",
								"healthyCheckers":  healthy,
								"totalCheckers":    total,
								"healthPercentage": healthPercentage * 100,
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

			// Check for health checks without alarm configuration
			if hcConfig, ok := meta["HealthCheckConfig"].(map[string]any); ok {
				if alarmIdentifier, ok := hcConfig["AlarmIdentifier"].(map[string]any); !ok || alarmIdentifier == nil {
					recommendation := providers.Recommendation{
						CategoryName: providers.RecommendationCategoryConfiguration,
						RuleName:     "aws_route53_health_check_no_alarm",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"message":        "Health check does not have a CloudWatch alarm configured",
							"recommendation": "Configure CloudWatch alarms to get notified of health check failures",
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

	return recommendations, nil
}

func (a *awsRoute53) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "route53",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}

	// Placeholder for fetching service map details.
	// add logic here to describe the Route 53 resource and its relationships.

	return app, nil
}
