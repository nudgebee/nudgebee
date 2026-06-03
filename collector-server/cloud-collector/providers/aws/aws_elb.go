// This file implements the AWS provider for Classic Load Balancers (ELB),
// Application Load Balancers (ALB), and Network Load Balancers (NLB).
package aws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

var regionMap = map[string]string{
	"us-east-1":      "US East (N. Virginia)",
	"us-east-2":      "US East (Ohio)",
	"us-west-1":      "US West (N. California)",
	"us-west-2":      "US West (Oregon)",
	"ca-central-1":   "Canada (Central)",
	"eu-central-1":   "EU (Frankfurt)",
	"eu-west-1":      "EU (Ireland)",
	"eu-west-2":      "EU (London)",
	"eu-west-3":      "EU (Paris)",
	"eu-north-1":     "EU (Stockholm)",
	"ap-northeast-1": "Asia Pacific (Tokyo)",
	"ap-northeast-2": "Asia Pacific (Seoul)",
	"ap-northeast-3": "Asia Pacific (Osaka-Local)",
	"ap-southeast-1": "Asia Pacific (Singapore)",
	"ap-southeast-2": "Asia Pacific (Sydney)",
	"ap-south-1":     "Asia Pacific (Mumbai)",
	"sa-east-1":      "South America (Sao Paulo)",
}

func getElbPrice(cfg aws.Config, region string) (float64, error) {
	location, ok := regionMap[region]
	if !ok {
		slog.Debug("elb pricing: region not in regionMap, skipping", "region", region)
		return 0, nil
	}

	filters := map[string]string{
		"location":        location,
		"productFamily":   "Load Balancer",
		"operatingSystem": "", // Explicitly exclude OS filter - load balancers don't have an OS
	}

	// The service code for classic ELB is AWSELB.
	priceList, err := getAvailableInstancesFromPricing(cfg, "AWSELB", filters)
	if err != nil {
		return 0, fmt.Errorf("failed to query pricing API: %w", err)
	}

	if len(priceList) == 0 {
		return 0, fmt.Errorf("pricing API returned no results for region %s (filters: location=%s, productFamily=%s)", region, location, "Load Balancer")
	}

	// Debug: log the number of pricing items found
	// (In production, this could be removed or changed to Debug level)
	var usageTypes []string
	for _, priceItem := range priceList {
		product, ok := priceItem["product"].(map[string]any)
		if !ok {
			continue
		}
		attributes, ok := product["attributes"].(map[string]any)
		if !ok {
			continue
		}
		if usageType, ok := attributes["usagetype"].(string); ok {
			usageTypes = append(usageTypes, usageType)
			if strings.HasSuffix(usageType, "LoadBalancerUsage") {
				price, err := getPricingValue(priceItem)
				if err == nil {
					return price, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("could not find pricing for ELB in region %s (found %d pricing items with usage types: %v, expected suffix: LoadBalancerUsage)",
		region, len(priceList), usageTypes)
}

type awsElb struct {
	DefaultAwsServiceImpl
}

func (a *awsElb) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an ELB alarm recommendation (ALB, NLB, or CLB)
	if (strings.HasPrefix(recommendation.RuleName, "aws_alb_") ||
		strings.HasPrefix(recommendation.RuleName, "aws_nlb_") ||
		strings.HasPrefix(recommendation.RuleName, "aws_clb_")) &&
		strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {
		// This is an alarm recommendation - create the CloudWatch alarm
		err := CreateCloudWatchAlarmFromRecommendation(ctx.GetContext(), account, recommendation)
		if err != nil {
			ctx.GetLogger().Error("Failed to create CloudWatch alarm", "error", err, "ruleName", recommendation.RuleName, "resourceId", recommendation.ResourceId)
			return fmt.Errorf("failed to create CloudWatch alarm: %w", err)
		}
		ctx.GetLogger().Info("Successfully created CloudWatch alarm", "ruleName", recommendation.RuleName, "resourceId", recommendation.ResourceId)
		return nil
	}

	// Other recommendations not yet supported
	return errors.ErrUnsupported
}

func (a *awsElb) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *awsElb) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *awsElb) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameELB)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := elb.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := elb.NewDescribeLoadBalancersPaginator(svc, &elb.DescribeLoadBalancersInput{})

	for paginator.HasMorePages() {
		loadBalancersOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch elb resources", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err // Return partial or fail completely
		}

		for _, loadBalancer := range loadBalancersOutput.LoadBalancerDescriptions {
			if loadBalancer.LoadBalancerName == nil {
				ctx.GetLogger().Warn("Encountered nil loadBalancer in LoadBalancerDescriptions list", "region", regionName)
				continue
			}
			if loadBalancer.LoadBalancerName == nil {
				ctx.GetLogger().Warn("Skipping ELB due to missing LoadBalancerName", "loadBalancer", loadBalancer, "region", regionName)
				continue
			}
			// Log if CreatedTime is nil but proceed, CreatedAt will be zero time.
			if loadBalancer.CreatedTime == nil {
				ctx.GetLogger().Warn("CreatedTime is nil for ELB", "loadBalancerName", *loadBalancer.LoadBalancerName, "region", regionName)
			}

			tags := make(map[string][]string)
			tagsOutput, err := svc.DescribeTags(context.TODO(), &elb.DescribeTagsInput{
				LoadBalancerNames: []string{*loadBalancer.LoadBalancerName}, // LoadBalancerName checked not nil
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch tags for elb", "error", err, "lbName", *loadBalancer.LoadBalancerName, "region", regionName)
			} else {
				if len(tagsOutput.TagDescriptions) > 0 { // Check if TagDescriptions slice is nil
					for _, tagDesc := range tagsOutput.TagDescriptions {
						// Ensure LoadBalancerName in tagDesc matches the current loadBalancer before processing tags
						if tagDesc.LoadBalancerName != nil && loadBalancer.LoadBalancerName != nil && *tagDesc.LoadBalancerName == *loadBalancer.LoadBalancerName {
							if len(tagDesc.Tags) > 0 { // Check if Tags slice in tagDesc is nil
								for _, tag := range tagDesc.Tags {
									if tag.Key != nil && tag.Value != nil { // Check individual tag and its fields
										tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
									}
								}
							} else {
								ctx.GetLogger().Info("tagDesc.Tags is nil for ELB", "loadBalancerName", *loadBalancer.LoadBalancerName, "tagDescLbName", *tagDesc.LoadBalancerName, "region", regionName)
							}
							break // Found tags for the current load balancer
						}
					}
				} else {
					ctx.GetLogger().Info("tagsOutput.TagDescriptions is nil for ELB", "loadBalancerName", *loadBalancer.LoadBalancerName, "region", regionName)
				}
			}

			attributesOutput, err := svc.DescribeLoadBalancerAttributes(context.TODO(), &elb.DescribeLoadBalancerAttributesInput{
				LoadBalancerName: loadBalancer.LoadBalancerName, // LoadBalancerName checked not nil
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch attributes for elb", "error", err, "lbName", *loadBalancer.LoadBalancerName, "region", regionName)
			}

			meta := structToMap(loadBalancer) // loadBalancer checked not nil
			if attributesOutput != nil && attributesOutput.LoadBalancerAttributes != nil {
				meta["Attributes"] = structToMap(attributesOutput.LoadBalancerAttributes)
			}

			createdAt := time.Time{} // Default zero time
			if loadBalancer.CreatedTime != nil {
				createdAt = *loadBalancer.CreatedTime
			}

			arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", regionName, account.AccountNumber, *loadBalancer.LoadBalancerName)
			resource := providers.Resource{
				Id:          *loadBalancer.LoadBalancerName, // LoadBalancerName checked not nil
				Arn:         arn,
				ServiceName: ServiceNameELB,
				Name:        *loadBalancer.LoadBalancerName,
				Status:      providers.ResourceStatusActive,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameELB, "loadbalancer"),
			}
			resources = append(resources, resource)
		}
	}

	// Handle Application and Network Load Balancers (elbv2)
	elbv2Svc := elbv2.NewFromConfig(cfg)
	paginatorV2 := elbv2.NewDescribeLoadBalancersPaginator(elbv2Svc, &elbv2.DescribeLoadBalancersInput{})

	for paginatorV2.HasMorePages() {
		loadBalancersOutputV2, err := paginatorV2.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch elbv2 resources", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		if len(loadBalancersOutputV2.LoadBalancers) > 0 {
			var lbArns []string
			for _, lb := range loadBalancersOutputV2.LoadBalancers {
				if lb.LoadBalancerArn != nil {
					lbArns = append(lbArns, *lb.LoadBalancerArn)
				}
			}

			tagsMap := make(map[string]map[string][]string)
			if len(lbArns) > 0 {
				// DescribeTags can handle a maximum of 20 ARNs at a time.
				const chunkSize = 20
				for i := 0; i < len(lbArns); i += chunkSize {
					end := i + chunkSize
					if end > len(lbArns) {
						end = len(lbArns)
					}
					chunk := lbArns[i:end]

					tagsOutput, err := elbv2Svc.DescribeTags(context.TODO(), &elbv2.DescribeTagsInput{
						ResourceArns: chunk,
					})
					if err != nil {
						ctx.GetLogger().Warn("failed to fetch tags for elbv2 chunk", "error", err, "region", regionName)
						continue
					}

					for _, tagDesc := range tagsOutput.TagDescriptions {
						if tagDesc.ResourceArn != nil {
							tags := make(map[string][]string)
							for _, tag := range tagDesc.Tags {
								if tag.Key != nil && tag.Value != nil {
									tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
								}
							}
							tagsMap[*tagDesc.ResourceArn] = tags
						}
					}
				}
			}

			for _, loadBalancer := range loadBalancersOutputV2.LoadBalancers {
				if loadBalancer.LoadBalancerArn == nil || loadBalancer.LoadBalancerName == nil {
					ctx.GetLogger().Warn("Skipping elbv2 due to missing information", "loadBalancer", loadBalancer, "region", regionName)
					continue
				}

				attributesOutput, err := elbv2Svc.DescribeLoadBalancerAttributes(context.TODO(), &elbv2.DescribeLoadBalancerAttributesInput{
					LoadBalancerArn: loadBalancer.LoadBalancerArn,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch attributes for elbv2", "error", err, "lbArn", *loadBalancer.LoadBalancerArn, "region", regionName)
				}

				// Fetch target groups for this load balancer
				targetGroupsOutput, err := elbv2Svc.DescribeTargetGroups(context.TODO(), &elbv2.DescribeTargetGroupsInput{
					LoadBalancerArn: loadBalancer.LoadBalancerArn,
				})
				if err != nil {
					ctx.GetLogger().Warn("failed to fetch target groups for elbv2", "error", err, "lbArn", *loadBalancer.LoadBalancerArn, "region", regionName)
				}

				meta := structToMap(loadBalancer)
				if attributesOutput != nil && len(attributesOutput.Attributes) > 0 {
					attrs := make(map[string]string)
					for _, attr := range attributesOutput.Attributes {
						if attr.Key != nil && attr.Value != nil {
							attrs[*attr.Key] = *attr.Value
						}
					}
					meta["Attributes"] = attrs
				}

				// Store target group details with health information
				if targetGroupsOutput != nil && len(targetGroupsOutput.TargetGroups) > 0 {
					targetGroupList := []map[string]any{}
					for _, tg := range targetGroupsOutput.TargetGroups {
						tgMap := structToMap(tg)

						// Fetch target health for this target group
						if tg.TargetGroupArn != nil {
							targetHealthOutput, healthErr := elbv2Svc.DescribeTargetHealth(context.TODO(), &elbv2.DescribeTargetHealthInput{
								TargetGroupArn: tg.TargetGroupArn,
							})
							if healthErr != nil {
								ctx.GetLogger().Warn("failed to fetch target health", "error", healthErr, "targetGroupArn", *tg.TargetGroupArn, "region", regionName)
							} else if targetHealthOutput != nil && len(targetHealthOutput.TargetHealthDescriptions) > 0 {
								healthList := []map[string]any{}
								for _, health := range targetHealthOutput.TargetHealthDescriptions {
									healthList = append(healthList, structToMap(health))
								}
								tgMap["TargetHealthDescriptions"] = healthList
							}
						}

						targetGroupList = append(targetGroupList, tgMap)
					}
					meta["TargetGroups"] = targetGroupList
				} else {
					meta["TargetGroups"] = []any{}
				}

				createdAt := time.Time{}
				if loadBalancer.CreatedTime != nil {
					createdAt = *loadBalancer.CreatedTime
				}

				var lbType elbv2types.LoadBalancerTypeEnum
				if loadBalancer.Type != "" {
					lbType = loadBalancer.Type
				} else {
					lbType = "unknown"
				}

				resource := providers.Resource{
					Id:          *loadBalancer.LoadBalancerArn,
					Arn:         *loadBalancer.LoadBalancerArn,
					ServiceName: ServiceNameELB,
					Name:        *loadBalancer.LoadBalancerName,
					Status:      providers.ResourceStatusActive,
					Region:      regionName,
					Tags:        tagsMap[*loadBalancer.LoadBalancerArn],
					Meta:        meta,
					CreatedAt:   createdAt,
					Type:        getAwsServiceResourceType(ServiceNameELB, string(lbType)+"_loadbalancer"),
				}
				resources = append(resources, resource)
			}
		}
	}

	return resources, nil
}

func (a *awsElb) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameELB)
		return recommendations, err
	}

	elbPrices := make(map[string]float64)

	// Single loop over all resources - process each load balancer type
	for _, resource := range existingResources {
		switch resource.Type {
		case getAwsServiceResourceType(ServiceNameELB, "loadbalancer"):
			// Classic Load Balancer - general recommendations and alarm recommendations
			clbRecs := a.processClassicLoadBalancerRecommendations(ctx, cfg, resource, &elbPrices)
			recommendations = append(recommendations, clbRecs...)

		case getAwsServiceResourceType(ServiceNameELB, "application_loadbalancer"):
			// Application Load Balancer - alarm recommendations
			albRecs := a.processApplicationLoadBalancerAlarms(ctx, resource)
			recommendations = append(recommendations, albRecs...)

		case getAwsServiceResourceType(ServiceNameELB, "network_loadbalancer"):
			// Network Load Balancer - alarm recommendations
			nlbRecs := a.processNetworkLoadBalancerAlarms(ctx, resource)
			recommendations = append(recommendations, nlbRecs...)

		default:
			continue
		}
	}

	return recommendations, nil
}

// processClassicLoadBalancerRecommendations generates both general and alarm recommendations for Classic Load Balancers
func (a *awsElb) processClassicLoadBalancerRecommendations(ctx providers.CloudProviderContext, cfg aws.Config, resource providers.Resource, elbPrices *map[string]float64) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	// Part 1: General CLB recommendations (unused, tags, cross-zone, etc.)
	var monthlySavings float64
	price, ok := (*elbPrices)[resource.Region]
	if !ok {
		newPrice, err := getElbPrice(cfg, resource.Region)
		if err != nil {
			ctx.GetLogger().Warn("failed to get elb price", "error", err, "region", resource.Region)
			// continue with savings = 0
		} else {
			price = newPrice
		}
		(*elbPrices)[resource.Region] = price
	}
	monthlySavings = price * 24 * 30

	meta := resource.Meta
	if len(meta) == 0 {
		ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for CLB", "elbName", resource.Name, "region", resource.Region)
		return recommendations
	}

	// Check 1: Unused Load Balancer (No registered instances)
	hasInstances := false
	instancesAny, instancesOk := meta["Instances"]
	if instancesOk {
		if instancesSlice, typeOk := instancesAny.([]any); typeOk {
			if len(instancesSlice) > 0 {
				hasInstances = true
			}
		} else {
			ctx.GetLogger().Warn("meta[Instances] is not of expected type []any for CLB", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", instancesAny))
		}
	}

	if !hasInstances {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            "aws_elb_unused",
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             monthlySavings,
			Data:                map[string]any{"load_balancer_name": resource.Name, "load_balancer_arn": resource.Arn, "reason": "No instances registered with the load balancer or Instances field missing/invalid."},
			Action:              providers.RecommendationActionDelete,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check 2: Missing Tags
	if len(resource.Tags) == 0 {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "aws_tags",
			Severity:            providers.RecommendationSeverityLow,
			Savings:             0,
			Data:                map[string]any{"load_balancer_name": resource.Name, "load_balancer_arn": resource.Arn},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check 3, 4, 5 using Attributes from Meta
	crossZoneEnabled := false
	connectionDrainingEnabled := false
	accessLogEnabled := false

	attributesAny, attrOk := meta["Attributes"]
	if !attrOk {
		ctx.GetLogger().Warn("Attributes field missing in CLB meta", "elbName", resource.Name, "region", resource.Region)
	} else {
		attributesMap, attrMapOk := attributesAny.(map[string]any)
		if !attrMapOk {
			ctx.GetLogger().Warn("Attributes field in CLB meta is not a map", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", attributesAny))
		} else {
			// CrossZoneLoadBalancing
			czAny, czOk := attributesMap["CrossZoneLoadBalancing"]
			if czOk {
				czMap, czMapOk := czAny.(map[string]any)
				if czMapOk {
					enabledBool, typeOk := czMap["Enabled"].(bool)
					if typeOk {
						crossZoneEnabled = enabledBool
					} else {
						ctx.GetLogger().Warn("Attributes.CrossZoneLoadBalancing.Enabled is not a boolean", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", czMap["Enabled"]))
					}
				} else {
					ctx.GetLogger().Warn("Attributes.CrossZoneLoadBalancing is not a map", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", czAny))
				}
			}

			// ConnectionDraining
			cdAny, cdOk := attributesMap["ConnectionDraining"]
			if cdOk {
				cdMap, cdMapOk := cdAny.(map[string]any)
				if cdMapOk {
					enabledBool, typeOk := cdMap["Enabled"].(bool)
					if typeOk {
						connectionDrainingEnabled = enabledBool
					} else {
						ctx.GetLogger().Warn("Attributes.ConnectionDraining.Enabled is not a boolean", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", cdMap["Enabled"]))
					}
				} else {
					ctx.GetLogger().Warn("Attributes.ConnectionDraining is not a map", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", cdAny))
				}
			}

			// AccessLog
			alAny, alOk := attributesMap["AccessLog"]
			if alOk {
				alMap, alMapOk := alAny.(map[string]any)
				if alMapOk {
					enabledBool, typeOk := alMap["Enabled"].(bool)
					if typeOk {
						accessLogEnabled = enabledBool
					} else {
						ctx.GetLogger().Warn("Attributes.AccessLog.Enabled is not a boolean", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", alMap["Enabled"]))
					}
				} else {
					ctx.GetLogger().Warn("Attributes.AccessLog is not a map", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", alAny))
				}
			}
		}
	}

	// Check 3: Cross-Zone Load Balancing Disabled
	if !crossZoneEnabled {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "aws_elb_cross_zone_balancing",
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             0,
			Data:                map[string]any{"load_balancer_name": resource.Name, "load_balancer_arn": resource.Arn, "reason": "Cross-zone load balancing is disabled."},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check 4: Connection Draining Disabled
	if !connectionDrainingEnabled {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "aws_elb_connection_draining",
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             0,
			Data:                map[string]any{"load_balancer_name": resource.Name, "load_balancer_arn": resource.Arn, "reason": "Connection draining is disabled."},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check 5: Access Logs Disabled
	if !accessLogEnabled {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryConfiguration,
			RuleName:            "aws_elb_access_logs",
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             0,
			Data:                map[string]any{"load_balancer_name": resource.Name, "load_balancer_arn": resource.Arn, "reason": "Access logging is disabled."},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Check 6: No Listeners Configured
	hasListeners := false
	listenersAny, listenersOk := meta["ListenerDescriptions"]
	if listenersOk {
		if listenersSlice, typeOk := listenersAny.([]any); typeOk {
			if len(listenersSlice) > 0 {
				hasListeners = true
			}
		} else {
			ctx.GetLogger().Warn("meta[ListenerDescriptions] is not of expected type []any for CLB", "elbName", resource.Name, "region", resource.Region, "actualType", fmt.Sprintf("%T", listenersAny))
		}
	}

	if !hasListeners {
		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            "aws_elb_no_listeners",
			Severity:            providers.RecommendationSeverityHigh,
			Savings:             monthlySavings,
			Data:                map[string]any{"load_balancer_name": resource.Name, "load_balancer_arn": resource.Arn, "reason": "Load balancer has no listeners configured or ListenerDescriptions field missing/invalid."},
			Action:              providers.RecommendationActionDelete,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}

	// Part 2: CLB CloudWatch Alarm Recommendations
	clbAlarmTemplates, err := LoadAlarmTemplates("clb")
	if err != nil {
		ctx.GetLogger().Warn("Failed to load CLB alarm templates", "error", err)
		return recommendations
	}
	ctx.GetLogger().Info("Processing CLB alarm recommendations", "resourceName", resource.Name, "resourceId", resource.Id, "templateCount", len(clbAlarmTemplates))

	// Classic LB uses LoadBalancerName dimension
	loadBalancerName := resource.Name

	for _, template := range clbAlarmTemplates {
		if !ShouldRecommendAlarm(resource, template) {
			ctx.GetLogger().Debug("Skipping alarm - should not recommend", "template", template.Name, "resourceName", resource.Name)
			continue
		}

		isMissing, err := IsAlarmMissing(resource, template, loadBalancerName)
		if err != nil {
			ctx.GetLogger().Warn("Error checking if alarm is missing", "error", err, "template", template.Name, "resourceName", resource.Name)
			continue
		}
		if !isMissing {
			ctx.GetLogger().Debug("Skipping alarm - already exists", "template", template.Name, "resourceName", resource.Name)
			continue
		}

		threshold, err := CalculateThreshold(resource, template)
		if err != nil {
			ctx.GetLogger().Warn("Failed to calculate threshold", "error", err, "template", template.Name)
			continue
		}
		ctx.GetLogger().Info("Creating CLB alarm recommendation", "template", template.Name, "resourceName", resource.Name, "threshold", threshold)

		// CLB alarms use simple metrics only
		alarmConfig := providers.AlarmCreationConfig{
			AlarmName:          fmt.Sprintf("%s-%s", template.Name, resource.Id),
			MetricName:         template.Configuration.MetricName,
			Namespace:          template.Configuration.Namespace,
			Statistic:          template.Configuration.Statistic,
			Period:             template.Configuration.Period,
			EvaluationPeriods:  template.Configuration.EvaluationPeriods,
			DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
			Threshold:          threshold,
			ComparisonOperator: template.Configuration.ComparisonOperator,
			TreatMissingData:   template.Configuration.TreatMissingData,
			Dimensions: []providers.AlarmDimension{
				{Name: "LoadBalancerName", Value: loadBalancerName},
			},
		}

		recommendation := providers.Recommendation{
			CategoryName: providers.RecommendationCategoryConfiguration,
			RuleName:     template.Name,
			Severity:     providers.RecommendationSeverityFromString(template.Severity),
			Savings:      0,
			Data: map[string]any{
				"load_balancer_name": loadBalancerName,
				"load_balancer_type": "classic",
				"metric_name":        template.Configuration.MetricName,
				"threshold":          threshold,
				"alarm_config":       alarmConfig,
				"alarm_type":         template.AlarmType,
				"reason":             template.Description,
			},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		}
		recommendations = append(recommendations, recommendation)
	}

	return recommendations
}

// processApplicationLoadBalancerAlarms generates alarm recommendations for Application Load Balancers
func (a *awsElb) processApplicationLoadBalancerAlarms(ctx providers.CloudProviderContext, resource providers.Resource) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	// Load ALB alarm templates
	albAlarmTemplates, err := LoadAlarmTemplates("alb")
	if err != nil {
		ctx.GetLogger().Warn("Failed to load ALB alarm templates", "error", err)
		return recommendations
	}

	// Get load balancer ARN for alarms
	loadBalancerArn := resource.Arn
	if loadBalancerArn == "" {
		ctx.GetLogger().Warn("ALB ARN is empty, skipping alarm recommendations", "resourceId", resource.Id)
		return recommendations
	}

	// Extract LoadBalancer dimension value from ARN
	// ARN format: arn:aws:elasticloadbalancing:region:account-id:loadbalancer/app/my-load-balancer/50dc6c495c0c9188
	// Dimension value: app/my-load-balancer/50dc6c495c0c9188
	loadBalancerDimension := extractLoadBalancerNameFromArn(loadBalancerArn)
	if loadBalancerDimension == "" {
		ctx.GetLogger().Warn("Failed to extract LoadBalancer dimension from ARN", "arn", loadBalancerArn)
		return recommendations
	}

	for _, template := range albAlarmTemplates {
		// Check if should recommend this alarm
		if !ShouldRecommendAlarm(resource, template) {
			continue
		}

		// Check if alarm is missing
		isMissing, err := IsAlarmMissing(resource, template, loadBalancerDimension)
		if err != nil {
			ctx.GetLogger().Warn("Error checking if alarm is missing", "error", err, "template", template.Name)
			continue
		}
		if !isMissing {
			continue
		}

		// Calculate threshold
		threshold, err := CalculateThreshold(resource, template)
		if err != nil {
			ctx.GetLogger().Warn("Error calculating threshold", "error", err, "template", template.Name)
			continue
		}

		// Build alarm configuration
		var alarmConfig providers.AlarmCreationConfig

		if len(template.Configuration.Metrics) > 0 {
			// Metric math alarm (HTTPErrorRate)
			metricQueries := make([]providers.MetricQueryConfig, len(template.Configuration.Metrics))
			for i, mq := range template.Configuration.Metrics {
				query := providers.MetricQueryConfig{
					Id:         mq.Id,
					ReturnData: mq.ReturnData,
					Label:      mq.Label,
					Expression: mq.Expression,
				}

				if mq.MetricStat != nil {
					query.MetricStat = &providers.MetricStatConfig{
						Metric: providers.MetricInfoConfig{
							Namespace:  mq.MetricStat.Metric.Namespace,
							MetricName: mq.MetricStat.Metric.MetricName,
							Dimensions: []providers.AlarmDimension{
								{Name: "LoadBalancer", Value: loadBalancerDimension},
							},
						},
						Period: mq.MetricStat.Period,
						Stat:   mq.MetricStat.Stat,
					}
				}

				metricQueries[i] = query
			}

			alarmConfig = providers.AlarmCreationConfig{
				AlarmName:          fmt.Sprintf("%s-%s", template.Name, resource.Id),
				Metrics:            metricQueries,
				Period:             template.Configuration.Period,
				EvaluationPeriods:  template.Configuration.EvaluationPeriods,
				DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
				Threshold:          threshold,
				ComparisonOperator: template.Configuration.ComparisonOperator,
				TreatMissingData:   template.Configuration.TreatMissingData,
			}
		} else {
			// Simple metric alarm (RejectedConnectionCount, TargetResponseTime)
			alarmConfig = providers.AlarmCreationConfig{
				AlarmName:          fmt.Sprintf("%s-%s", template.Name, resource.Id),
				MetricName:         template.Configuration.MetricName,
				Namespace:          template.Configuration.Namespace,
				Statistic:          template.Configuration.Statistic,
				Period:             template.Configuration.Period,
				EvaluationPeriods:  template.Configuration.EvaluationPeriods,
				DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
				Threshold:          threshold,
				ComparisonOperator: template.Configuration.ComparisonOperator,
				TreatMissingData:   template.Configuration.TreatMissingData,
				Dimensions: []providers.AlarmDimension{
					{Name: "LoadBalancer", Value: loadBalancerDimension},
				},
			}
		}

		// Create recommendation
		recommendation := providers.Recommendation{
			CategoryName: providers.RecommendationCategoryConfiguration,
			RuleName:     template.Name,
			Severity:     providers.RecommendationSeverityFromString(template.Severity),
			Savings:      0,
			Data: map[string]any{
				"load_balancer_arn":  loadBalancerArn,
				"load_balancer_name": resource.Name,
				"load_balancer_type": "application",
				"metric_name":        template.Configuration.MetricName,
				"threshold":          threshold,
				"alarm_config":       alarmConfig,
				"alarm_type":         template.AlarmType,
				"reason":             template.Description,
			},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		}
		recommendations = append(recommendations, recommendation)
	}

	return recommendations
}

// processNetworkLoadBalancerAlarms generates alarm recommendations for Network Load Balancers
func (a *awsElb) processNetworkLoadBalancerAlarms(ctx providers.CloudProviderContext, resource providers.Resource) []providers.Recommendation {
	recommendations := []providers.Recommendation{}

	// Load NLB alarm templates
	nlbAlarmTemplates, err := LoadAlarmTemplates("nlb")
	if err != nil {
		ctx.GetLogger().Warn("Failed to load NLB alarm templates", "error", err)
		return recommendations
	}

	// Extract LoadBalancer dimension value from ARN (same format as ALB)
	loadBalancerArn := resource.Arn
	if loadBalancerArn == "" {
		ctx.GetLogger().Warn("NLB ARN is empty, skipping alarm recommendations", "resourceId", resource.Id)
		return recommendations
	}

	loadBalancerDimension := extractLoadBalancerNameFromArn(loadBalancerArn)
	if loadBalancerDimension == "" {
		ctx.GetLogger().Warn("Failed to extract LoadBalancer dimension from ARN", "arn", loadBalancerArn)
		return recommendations
	}

	for _, template := range nlbAlarmTemplates {
		if !ShouldRecommendAlarm(resource, template) {
			continue
		}

		isMissing, err := IsAlarmMissing(resource, template, loadBalancerDimension)
		if err != nil || !isMissing {
			continue
		}

		threshold, err := CalculateThreshold(resource, template)
		if err != nil {
			ctx.GetLogger().Warn("Failed to calculate threshold", "error", err, "template", template.Name)
			continue
		}

		// NLB alarms use simple metrics only
		alarmConfig := providers.AlarmCreationConfig{
			AlarmName:          fmt.Sprintf("%s-%s", template.Name, resource.Id),
			MetricName:         template.Configuration.MetricName,
			Namespace:          template.Configuration.Namespace,
			Statistic:          template.Configuration.Statistic,
			Period:             template.Configuration.Period,
			EvaluationPeriods:  template.Configuration.EvaluationPeriods,
			DatapointsToAlarm:  template.Configuration.DatapointsToAlarm,
			Threshold:          threshold,
			ComparisonOperator: template.Configuration.ComparisonOperator,
			TreatMissingData:   template.Configuration.TreatMissingData,
			Dimensions: []providers.AlarmDimension{
				{Name: "LoadBalancer", Value: loadBalancerDimension},
			},
		}

		recommendation := providers.Recommendation{
			CategoryName: providers.RecommendationCategoryConfiguration,
			RuleName:     template.Name,
			Severity:     providers.RecommendationSeverityFromString(template.Severity),
			Savings:      0,
			Data: map[string]any{
				"load_balancer_arn":  loadBalancerArn,
				"load_balancer_name": resource.Name,
				"load_balancer_type": "network",
				"metric_name":        template.Configuration.MetricName,
				"threshold":          threshold,
				"alarm_config":       alarmConfig,
				"alarm_type":         template.AlarmType,
				"reason":             template.Description,
			},
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		}
		recommendations = append(recommendations, recommendation)
	}

	return recommendations
}

// extractLoadBalancerNameFromArn extracts the LoadBalancer dimension value from an ALB/NLB ARN
// ARN format: arn:aws:elasticloadbalancing:region:account-id:loadbalancer/app/my-load-balancer/50dc6c495c0c9188
// Returns: app/my-load-balancer/50dc6c495c0c9188
func extractLoadBalancerNameFromArn(arn string) string {
	parts := strings.Split(arn, "loadbalancer/")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func (a *awsElb) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return "", err
	}
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	logsSvc := cloudwatchlogs.NewFromConfig(regionalCfg)
	elbv2Svc := elbv2.NewFromConfig(regionalCfg)

	// For ELBv2 (ALB/NLB), there's no direct CloudWatch Logs integration for access logs.
	// They go to S3. However, a common convention for forwarded logs is to use a name
	// derived from the load balancer's ARN.
	// This attempts to find a log group based on that convention.

	// Try to find the load balancer by name or ARN to get a canonical ARN.
	var lbArn string
	if strings.HasPrefix(resourceId, "arn:aws:elasticloadbalancing:") {
		lbArn = resourceId
	} else {
		if strings.HasPrefix(resourceId, "app/") {
			albSplits := strings.Split(resourceId, "/")
			resourceId = albSplits[1]
		}
		// Assume resourceId is a name and try to describe it.
		descLBs, err := elbv2Svc.DescribeLoadBalancers(context.TODO(), &elbv2.DescribeLoadBalancersInput{
			Names: []string{resourceId},
		})
		if err == nil && len(descLBs.LoadBalancers) > 0 {
			lbArn = *descLBs.LoadBalancers[0].LoadBalancerArn
		}
	}

	if lbArn != "" {
		// ARN format: arn:aws:elasticloadbalancing:<region>:<account>:loadbalancer/<suffix>
		// e.g., arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/1a2b3c4d5e6f
		parts := strings.Split(lbArn, ":")
		if len(parts) >= 6 {
			lbPart := parts[5] // e.g., loadbalancer/app/my-lb/1a2b3c4d5e6f
			lbSuffix := strings.TrimPrefix(lbPart, "loadbalancer/")
			if lbSuffix != "" {
				// A plausible convention for the log group name.
				logGroupName := fmt.Sprintf("/aws/elasticloadbalancing/%s", lbSuffix)

				// Verify the log group exists.
				_, err := logsSvc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
					LogGroupName: &logGroupName,
					Limit:        aws.Int32(1),
				})
				if err == nil {
					return logGroupName, nil
				}
			}
		}
	}

	return "", nil
}

func (a *awsElb) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber)
		return providers.ServiceMapApplication{}, err
	}
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "elb",
			Namespace: region,
		},
		Upstreams:   []providers.UpstreamLink{},
		Downstreams: []providers.DownstreamLink{},
		Status:      "Unknown",
	}
	var describeLbOutput *elbv2.DescribeLoadBalancersOutput
	elbv2Svc := elbv2.NewFromConfig(cfg)
	if strings.HasPrefix(resourceId, "arn:") {
		describeLbOutput, err = elbv2Svc.DescribeLoadBalancers(context.TODO(), &elbv2.DescribeLoadBalancersInput{LoadBalancerArns: []string{resourceId}})
	} else {
		if strings.HasPrefix(resourceId, "app/") {
			resourceSpits := strings.Split(resourceId, "/")
			resourceId = resourceSpits[1]
		}

		describeLbOutput, err = elbv2Svc.DescribeLoadBalancers(context.TODO(), &elbv2.DescribeLoadBalancersInput{Names: []string{resourceId}})
	}
	if err != nil {
		ctx.GetLogger().Error("failed to describe load balancer", "error", err, "id", resourceId)
		return app, err
	}
	if len(describeLbOutput.LoadBalancers) > 0 {
		lb := describeLbOutput.LoadBalancers[0]
		app.Id.Name = *lb.LoadBalancerArn
		app.Status = string(lb.State.Code)
		for _, sg := range lb.SecurityGroups {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: sg, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
		}
		if lb.VpcId != nil {
			app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *lb.VpcId, Kind: "ec2", Namespace: region}}.ToDownstreamLink())
		}
		attrOutput, err := elbv2Svc.DescribeLoadBalancerAttributes(context.TODO(), &elbv2.DescribeLoadBalancerAttributesInput{LoadBalancerArn: lb.LoadBalancerArn})
		if err == nil {
			s3Bucket := ""
			s3Enabled := false
			for _, attr := range attrOutput.Attributes {
				if *attr.Key == "access_logs.s3.enabled" && *attr.Value == "true" {
					s3Enabled = true
				}
				if *attr.Key == "access_logs.s3.bucket" {
					s3Bucket = *attr.Value
				}
			}
			if s3Enabled && s3Bucket != "" {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: s3Bucket, Kind: "s3", Namespace: ""}}.ToDownstreamLink())
			}
		}
		describeListenersOutput, err := elbv2Svc.DescribeListeners(context.TODO(), &elbv2.DescribeListenersInput{LoadBalancerArn: lb.LoadBalancerArn})
		if err == nil {
			for _, listener := range describeListenersOutput.Listeners {
				app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: *listener.ListenerArn, Kind: "elbv2", Namespace: region}}.ToDownstreamLink())
				for _, action := range listener.DefaultActions {
					if action.TargetGroupArn != nil {
						tgArn := *action.TargetGroupArn
						app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: tgArn, Kind: "elbv2", Namespace: region}}.ToDownstreamLink())
						healthOutput, err := elbv2Svc.DescribeTargetHealth(context.TODO(), &elbv2.DescribeTargetHealthInput{TargetGroupArn: &tgArn})
						if err == nil {
							for _, thd := range healthOutput.TargetHealthDescriptions {
								if thd.Target != nil && thd.Target.Id != nil {
									targetId := *thd.Target.Id
									targetService := "unknown"
									if strings.HasPrefix(targetId, "i-") {
										targetService = "ec2"
									} else if strings.Contains(targetId, ":task/") {
										targetService = "ecs"
									}
									app.Downstreams = append(app.Downstreams, providers.ServiceApplicationLink{Id: providers.ServiceApplicationId{Name: targetId, Kind: targetService, Namespace: region}}.ToDownstreamLink())
								}
							}
						}
					}
				}
			}
		}
	}
	return app, nil
}

func (a *awsElb) DescribeResource(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (*ResourceMetadata, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		return nil, fmt.Errorf("failed to create aws session: %w", err)
	}

	elbv2Svc := elbv2.NewFromConfig(cfg)

	// Handle both ARN and name formats
	var describeLbOutput *elbv2.DescribeLoadBalancersOutput
	if strings.HasPrefix(resourceId, "arn:") {
		describeLbOutput, err = elbv2Svc.DescribeLoadBalancers(context.TODO(),
			&elbv2.DescribeLoadBalancersInput{LoadBalancerArns: []string{resourceId}})
	} else {
		// Extract name if format is "app/name/id" or "net/name/id"
		name := resourceId
		if strings.HasPrefix(resourceId, "app/") || strings.HasPrefix(resourceId, "net/") {
			parts := strings.Split(resourceId, "/")
			if len(parts) > 1 {
				name = parts[1]
			}
		}
		describeLbOutput, err = elbv2Svc.DescribeLoadBalancers(context.TODO(),
			&elbv2.DescribeLoadBalancersInput{Names: []string{name}})
	}

	if err != nil {
		return nil, fmt.Errorf("failed to describe load balancer: %w", err)
	}

	if len(describeLbOutput.LoadBalancers) == 0 {
		return nil, fmt.Errorf("load balancer not found: %s", resourceId)
	}

	lb := describeLbOutput.LoadBalancers[0]

	metadata := &ResourceMetadata{
		ResourceID:     *lb.LoadBalancerArn,
		ResourceARN:    *lb.LoadBalancerArn,
		SecurityGroups: lb.SecurityGroups,
		Subnets:        []string{},
		Tags:           make(map[string]string),
		Metadata:       make(map[string]any),
		Status:         string(lb.State.Code),
	}

	// VPC ID
	if lb.VpcId != nil {
		metadata.VpcID = *lb.VpcId
	}

	// Subnets from availability zones
	for _, az := range lb.AvailabilityZones {
		if az.SubnetId != nil {
			metadata.Subnets = append(metadata.Subnets, *az.SubnetId)
		}
	}

	// Get private IPs from ENIs (Elastic Network Interfaces)
	// ALB/NLB have ENIs in each subnet - query EC2 to get IPs
	if metadata.VpcID != "" {
		ec2Svc := ec2.NewFromConfig(cfg)

		// Extract load balancer name from ARN for ENI description filter
		lbName := extractLoadBalancerNameFromArn(*lb.LoadBalancerArn)

		// Filter ENIs by VPC and description pattern
		// ENI descriptions follow pattern: "ELB app/my-lb/xxx" or "ELB net/my-lb/xxx"
		eniOutput, err := ec2Svc.DescribeNetworkInterfaces(context.TODO(),
			&ec2.DescribeNetworkInterfacesInput{
				Filters: []ec2types.Filter{
					{
						Name:   aws.String("vpc-id"),
						Values: []string{metadata.VpcID},
					},
					{
						Name:   aws.String("description"),
						Values: []string{fmt.Sprintf("ELB %s*", lbName)},
					},
				},
			})

		if err == nil && len(eniOutput.NetworkInterfaces) > 0 {
			// Use first ENI's primary IP as the main private IP
			if eniOutput.NetworkInterfaces[0].PrivateIpAddress != nil {
				metadata.PrivateIP = *eniOutput.NetworkInterfaces[0].PrivateIpAddress
			}

			// Store all IPs in metadata for VPC Flow Logs queries
			// (ALB/NLB have one ENI per subnet/AZ)
			var allIPs []string
			for _, eni := range eniOutput.NetworkInterfaces {
				if eni.PrivateIpAddress != nil {
					allIPs = append(allIPs, *eni.PrivateIpAddress)
				}
			}
			metadata.Metadata["all_ips"] = allIPs

			ctx.GetLogger().Debug("found load balancer ENIs",
				"lbArn", *lb.LoadBalancerArn,
				"eniCount", len(eniOutput.NetworkInterfaces),
				"primaryIP", metadata.PrivateIP,
				"allIPs", allIPs)
		} else if err != nil {
			ctx.GetLogger().Debug("failed to query ENIs for load balancer",
				"lbArn", *lb.LoadBalancerArn,
				"error", err)
		}
	}

	// Get listener port(s)
	describeListenersOutput, err := elbv2Svc.DescribeListeners(context.TODO(),
		&elbv2.DescribeListenersInput{LoadBalancerArn: lb.LoadBalancerArn})
	if err == nil && len(describeListenersOutput.Listeners) > 0 {
		// Use first listener port as primary port
		if describeListenersOutput.Listeners[0].Port != nil {
			metadata.Port = int(*describeListenersOutput.Listeners[0].Port)
		}

		// Store all listener ports in metadata
		var allPorts []int
		for _, listener := range describeListenersOutput.Listeners {
			if listener.Port != nil {
				allPorts = append(allPorts, int(*listener.Port))
			}
		}
		metadata.Metadata["all_ports"] = allPorts
	} else if err != nil {
		ctx.GetLogger().Debug("failed to query listeners for load balancer",
			"lbArn", *lb.LoadBalancerArn,
			"error", err)
	}

	// Get tags
	tagsOutput, err := elbv2Svc.DescribeTags(context.TODO(),
		&elbv2.DescribeTagsInput{ResourceArns: []string{*lb.LoadBalancerArn}})
	if err == nil && len(tagsOutput.TagDescriptions) > 0 {
		for _, tagDesc := range tagsOutput.TagDescriptions {
			for _, tag := range tagDesc.Tags {
				if tag.Key != nil && tag.Value != nil {
					metadata.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	return metadata, nil
}
