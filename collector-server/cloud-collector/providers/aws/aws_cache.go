package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// getGravitonCacheInstanceFamily returns the Graviton equivalent cache node type
// Returns empty string if no Graviton equivalent exists
//
// Graviton mapping logic:
// - Instance families ending with 'g' are already Graviton (r7g, m7g, t4g, etc.)
// - For non-Graviton instances, we map to the latest Graviton generation:
//   - r-series (memory-optimized): r5, r6i, r6id, r6in → r7g
//   - m-series (general-purpose): m5, m6i, m6id, m6in → m7g
//   - t-series (burstable): t2, t3, t3a → t4g
//   - c-series (compute-optimized): c5, c6i, c6id, c6in → c7g
//
// This approach is more maintainable than hardcoding as it:
// 1. Automatically handles new instance sizes (e.g., r6i.xlarge, r6i.2xlarge)
// 2. Uses AWS's consistent naming pattern (generation number + 'g' suffix)
// 3. Can be updated by modifying just the generation mappings
func getGravitonCacheInstanceFamily(nodeType string) string {
	return getGravitonInstanceType(nodeType, "cache.")
}

func getAwsElasticCacheEngineLatestVersion(ctx context.Context, cfg aws.Config, engine string) (string, error) {
	svc := elasticache.NewFromConfig(cfg)
	input := &elasticache.DescribeCacheEngineVersionsInput{
		Engine: aws.String(engine),
	}
	result, err := svc.DescribeCacheEngineVersions(ctx, input)
	if err != nil {
		return "", err
	}
	if len(result.CacheEngineVersions) == 0 {
		return "", errors.New("no cache engine versions found in response")
	}
	return aws.ToString(result.CacheEngineVersions[0].EngineVersion), nil
}

func getAvailableCacheInstances(ctx context.Context, cfg aws.Config, region string, engine string, memory int, cpu int, instanceType string) ([]map[string]interface{}, error) {
	if engine != "" && engine == "postgres" {
		engine = "PostgreSQL"
	}
	cfg.Region = region
	svc := pricing.NewFromConfig(cfg)
	filters := []pricingtypes.Filter{}
	if region != "" {
		filters = append(filters, pricingtypes.Filter{
			Field: aws.String("regionCode"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(region),
		})
	}
	if instanceType != "" {
		filters = append(filters, pricingtypes.Filter{
			Field: aws.String("instanceType"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(instanceType),
		})
	}
	if engine != "" {
		filters = append(filters, pricingtypes.Filter{
			Field: aws.String("cacheEngine"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(engine),
		})
	}
	if memory > 0 {
		filters = append(filters, pricingtypes.Filter{
			Field: aws.String("memory"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(fmt.Sprintf("%d GiB", memory)),
		})
	}
	if cpu > 0 {
		filters = append(filters, pricingtypes.Filter{
			Field: aws.String("vcpu"),
			Type:  pricingtypes.FilterTypeTermMatch,
			Value: aws.String(fmt.Sprint(cpu)),
		})
	}

	input := &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonElastiCache"),
		Filters:     filters,
	}
	result, err := svc.GetProducts(ctx, input)
	if err != nil {
		return nil, err
	}
	var priceList []map[string]interface{}
	for _, priceDoc := range result.PriceList {
		var priceData map[string]interface{}
		if err := json.Unmarshal([]byte(priceDoc), &priceData); err != nil {
			return nil, err
		}
		priceList = append(priceList, priceData)
	}
	return priceList, nil
}

type amazonElasticCache struct {
	DefaultAwsServiceImpl
}

func (a *amazonElasticCache) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	return errors.ErrUnsupported
}

func (a *amazonElasticCache) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonElasticCache) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonElasticCache) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := elasticache.NewFromConfig(cfg)
	resources := []providers.Resource{}
	instanceTypeMap := make(map[string]map[string]interface{})

	paginator := elasticache.NewDescribeCacheClustersPaginator(svc, &elasticache.DescribeCacheClustersInput{
		ShowCacheNodeInfo: aws.Bool(true),
	})

	for paginator.HasMorePages() {
		result, err := paginator.NextPage(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch elasticache resources", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			return resources, err
		}

		for _, cluster := range result.CacheClusters {
			if cluster.CacheClusterId == nil || cluster.ARN == nil {
				ctx.GetLogger().Warn("Skipping ElastiCache cluster due to missing CacheClusterId or ARN", "cluster", cluster, "region", regionName)
				continue
			}

			tags := make(map[string][]string)
			resourceTags, err := svc.ListTagsForResource(ctx.GetContext(), &elasticache.ListTagsForResourceInput{
				ResourceName: cluster.ARN,
			})
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch elasticache tags", "error", err, "clusterArn", *cluster.ARN, "region", regionName)
			} else {
				for _, tag := range resourceTags.TagList {
					tags[aws.ToString(tag.Key)] = append(tags[aws.ToString(tag.Key)], aws.ToString(tag.Value))
				}
			}

			metaDetails := structToMap(cluster)
			instanceTypeKey := aws.ToString(cluster.CacheNodeType) + aws.ToString(cluster.Engine)
			if _, ok := instanceTypeMap[instanceTypeKey]; !ok {
				instanceTypes, err := getAvailableCacheInstances(ctx.GetContext(), cfg, regionName, aws.ToString(cluster.Engine), 0, 0, aws.ToString(cluster.CacheNodeType))
				if err != nil {
					ctx.GetLogger().Warn("failed to get available cache instances pricing", "error", err, "nodeType", aws.ToString(cluster.CacheNodeType), "engine", aws.ToString(cluster.Engine), "region", regionName)
					instanceTypeMap[instanceTypeKey] = make(map[string]interface{})
				} else if len(instanceTypes) == 0 {
					ctx.GetLogger().Warn("no pricing instance type found for cache instance", "nodeType", aws.ToString(cluster.CacheNodeType), "engine", aws.ToString(cluster.Engine), "region", regionName)
					instanceTypeMap[instanceTypeKey] = make(map[string]interface{})
				} else {
					instanceTypeMap[instanceTypeKey] = instanceTypes[0]
				}
			}
			metaDetails["InstanceTypeDetails"] = instanceTypeMap[instanceTypeKey]

			var status providers.ResourceStatus
			switch strings.ToLower(aws.ToString(cluster.CacheClusterStatus)) {
			case "available", "modifying":
				status = providers.ResourceStatusActive
			case "deleting":
				status = providers.ResourceStatusDeleted
			case "create-failed", "incompatible-network", "restore-failed":
				status = providers.ResourceStatusInactive
			default:
				status = providers.ResourceStatusUnknown
			}

			resource := providers.Resource{
				Id:          aws.ToString(cluster.CacheClusterId),
				ServiceName: ServiceNameElastiCache,
				Name:        aws.ToString(cluster.CacheClusterId),
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        metaDetails,
				Arn:         aws.ToString(cluster.ARN),
				CreatedAt:   aws.ToTime(cluster.CacheClusterCreateTime),
				Type:        getAwsServiceResourceType(ServiceNameElastiCache, "cluster"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

// https://www.trendmicro.com/cloudoneconformity-staging/knowledge-base/aws/ElastiCache/
func (a *amazonElasticCache) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	startDate := time.Now().Add(-time.Hour * 24 * 7)
	endDate := time.Now()

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return recommendations, err
	}

	engineLatestVersions := make(map[string]string)

	for _, resource := range existingResources {
		meta := resource.Meta
		if len(meta) == 0 {
			ctx.GetLogger().Info("Resource meta is nil or empty, skipping recommendations for ElastiCache resource", "resourceArn", resource.Arn, "region", resource.Region)
			continue
		}

		engineStr, engineOk := ToString(meta["Engine"], "Engine", resource.Arn, resource.Region, ctx)
		if engineOk {
			if _, ok := engineLatestVersions[engineStr]; !ok {
				latestVersion, errLatest := getAwsElasticCacheEngineLatestVersion(ctx.GetContext(), cfg, engineStr)
				if errLatest != nil {
					ctx.GetLogger().Warn("failed to get elasticache engine latest version for engine", "error", errLatest, "engine", engineStr, "resourceArn", resource.Arn, "region", resource.Region)
					engineLatestVersions[engineStr] = "ERROR_FETCHING"
				} else {
					engineLatestVersions[engineStr] = latestVersion
				}
			}

			currentVersionStr, currentVersionOk := ToString(meta["EngineVersion"], "EngineVersion", resource.Arn, resource.Region, ctx)
			if currentVersionOk && engineLatestVersions[engineStr] != "ERROR_FETCHING" && currentVersionStr != engineLatestVersions[engineStr] {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryInfraUpgrade,
					RuleName:     "aws_elasticache_engine_version",
					Severity:     providers.RecommendationSeverityMedium,
					Data: map[string]any{
						"current_version": currentVersionStr,
						"latest_version":  engineLatestVersions[engineStr],
						"engine":          engineStr,
						"cluster_id":      resource.Id,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		if instanceTypeDetailsAny, itdOk := meta["InstanceTypeDetails"]; itdOk {
			if instanceTypeDetails, itdMapOk := instanceTypeDetailsAny.(map[string]any); itdMapOk {
				if productAny, prodOk := instanceTypeDetails["product"]; prodOk {
					if productDetails, prodMapOk := productAny.(map[string]any); prodMapOk {
						if attributesAny, attrOk := productDetails["attributes"]; attrOk {
							if attributes, attrMapOk := attributesAny.(map[string]any); attrMapOk {
								if currentGenAny, cgOk := attributes["currentGeneration"]; cgOk {
									if currentGenStr, cgStrOk := currentGenAny.(string); cgStrOk && currentGenStr != "Yes" {
										cacheNodeTypeStr, _ := ToString(meta["CacheNodeType"], "CacheNodeType", resource.Arn, resource.Region, ctx)
										recommendations = append(recommendations, providers.Recommendation{
											CategoryName: providers.RecommendationCategoryInfraUpgrade, RuleName: "aws_elasticache_instance_generation", Severity: providers.RecommendationSeverityMedium,
											Data:   map[string]any{"cluster_id": resource.Id, "cluster_arn": resource.Arn, "instance_type": cacheNodeTypeStr, "reason": "Not latest generation."},
											Action: providers.RecommendationActionModify, ResourceServiceName: resource.ServiceName, ResourceId: resource.Id, ResourceType: resource.Type, ResourceRegion: resource.Region,
										})
									}
								}
							}
						}
					}
				}
			}
		}

		// Graviton Migration Recommendation
		if cacheNodeType, cntOk := ToString(meta["CacheNodeType"], "CacheNodeType", resource.Arn, resource.Region, ctx); cntOk {
			gravitonNodeType := getGravitonCacheInstanceFamily(cacheNodeType)
			if gravitonNodeType != "" {
				// Try to get pricing for Graviton node type
				engineStr, _ := ToString(meta["Engine"], "Engine", resource.Arn, resource.Region, ctx)

				gravitonInstances, err := getAvailableCacheInstances(ctx.GetContext(), cfg, resource.Region, engineStr, 0, 0, gravitonNodeType)
				if err != nil {
					ctx.GetLogger().Warn("failed to get Graviton cache instance pricing", "error", err, "gravitonNodeType", gravitonNodeType, "region", resource.Region)
				} else if len(gravitonInstances) > 0 {
					// Calculate savings (Graviton typically 10-20% cheaper)
					savings := 0.0
					gravitonPrice := 0.0
					currentPrice := 0.0

					if gravitonPriceVal, err := getPricingValue(gravitonInstances[0]); err == nil {
						gravitonPrice = gravitonPriceVal
					}

					if instanceTypeDetailsAny, itdOk := meta["InstanceTypeDetails"]; itdOk {
						if instanceTypeDetails, itdMapOk := instanceTypeDetailsAny.(map[string]any); itdMapOk {
							if price, err := getPricingValue(instanceTypeDetails); err == nil {
								currentPrice = price
							}
						}
					}

					if currentPrice > 0 && gravitonPrice > 0 && gravitonPrice < currentPrice {
						// Monthly savings
						savings = (currentPrice - gravitonPrice) * 24 * 30
						savingsPercent := ((currentPrice - gravitonPrice) / currentPrice) * 100

						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryInfraUpgrade,
							RuleName:     "aws_elasticache_graviton_migration",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      savings,
							Data: map[string]any{
								"cluster_id":           resource.Id,
								"current_node_type":    cacheNodeType,
								"graviton_node_type":   gravitonNodeType,
								"current_hourly_cost":  currentPrice,
								"graviton_hourly_cost": gravitonPrice,
								"monthly_savings":      savings,
								"savings_percent":      savingsPercent,
								"reason":               fmt.Sprintf("Cache cluster can be migrated from %s to Graviton-based %s for %.1f%% cost savings ($%.2f/month)", cacheNodeType, gravitonNodeType, savingsPercent, savings),
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}
		}

		if atRestEncrypted, atRestOk := ToBool(meta["AtRestEncryptionEnabled"], "AtRestEncryptionEnabled", resource.Arn, resource.Region, ctx); (atRestOk && !atRestEncrypted) || !atRestOk {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity, RuleName: "aws_elasticache_encryption_at_rest", Severity: providers.RecommendationSeverityMedium,
				Data:   map[string]any{"cluster_id": resource.Id, "cluster_arn": resource.Arn, "reason": "AtRestEncryptionEnabled is false or missing."},
				Action: providers.RecommendationActionModify, ResourceServiceName: resource.ServiceName, ResourceId: resource.Id, ResourceType: resource.Type, ResourceRegion: resource.Region,
			})
		}

		if inTransitEncrypted, inTransitOk := ToBool(meta["TransitEncryptionEnabled"], "TransitEncryptionEnabled", resource.Arn, resource.Region, ctx); (inTransitOk && !inTransitEncrypted) || !inTransitOk {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity, RuleName: "aws_elasticache_encryption_in_transit", Severity: providers.RecommendationSeverityMedium,
				Data:   map[string]any{"cluster_id": resource.Id, "cluster_arn": resource.Arn, "reason": "TransitEncryptionEnabled is false or missing."},
				Action: providers.RecommendationActionModify, ResourceServiceName: resource.ServiceName, ResourceId: resource.Id, ResourceType: resource.Type, ResourceRegion: resource.Region,
			})
		}

		if autoMinorUpgrade, autoMinorOk := ToBool(meta["AutoMinorVersionUpgrade"], "AutoMinorVersionUpgrade", resource.Arn, resource.Region, ctx); autoMinorOk && !autoMinorUpgrade {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration, RuleName: "aws_elasticache_auto_minor_upgrade", Severity: providers.RecommendationSeverityMedium,
				Data:   map[string]any{"cluster_id": resource.Id, "cluster_arn": resource.Arn, "reason": "AutoMinorVersionUpgrade is false."},
				Action: providers.RecommendationActionModify, ResourceServiceName: resource.ServiceName, ResourceId: resource.Id, ResourceType: resource.Type, ResourceRegion: resource.Region,
			})
		}

		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags",
				Severity:            providers.RecommendationSeverityMedium,
				Data:                map[string]any{"cluster_id": resource.Id, "cluster_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		isIdle := false
		connectionMetrics, errConn := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id}, ServiceName: resource.ServiceName, StartDate: &startDate, EndDate: &endDate, Region: resource.Region, MetricNames: []string{"CurrConnections"}, Step: 3600 * time.Second, Statistics: []string{"Average"},
		})
		if errConn != nil {
			ctx.GetLogger().Warn("Error getting CurrConnections metrics for idle check", "resourceId", resource.Id, "error", errConn, "region", resource.Region)
		} else if len(connectionMetrics.Items) > 0 && len(connectionMetrics.Items[0].Values) > 0 {
			isIdle = true
			for _, metric := range connectionMetrics.Items[0].Values {
				if metric > 1 {
					isIdle = false
					break
				}
			}
		}

		cpuMetrics := providers.QueryMetricsResponse{}
		if isIdle {
			var errCPU error
			cpuMetrics, errCPU = a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds: []string{resource.Id}, ServiceName: resource.ServiceName, StartDate: &startDate, EndDate: &endDate, Region: resource.Region, MetricNames: []string{"CPUUtilization"}, Step: 3600 * time.Second, Statistics: []string{"Average"},
			})
			if errCPU != nil {
				ctx.GetLogger().Warn("Error getting CPUUtilization metrics for idle check", "resourceId", resource.Id, "error", errCPU, "region", resource.Region)
				isIdle = false
			} else if len(cpuMetrics.Items) > 0 && len(cpuMetrics.Items[0].Values) > 0 {
				for _, metric := range cpuMetrics.Items[0].Values {
					if metric > 1 {
						isIdle = false
						break
					}
				}
			}
		}

		if isIdle {
			savings := 0.0
			if instanceTypeDetailsAny, itdOk := meta["InstanceTypeDetails"]; itdOk {
				if instanceTypeDetails, itdMapOk := instanceTypeDetailsAny.(map[string]any); itdMapOk {
					price, errPrice := getPricingValue(instanceTypeDetails)
					if errPrice == nil && price > 0 {
						savings = price * 24 * 30
					}
				}
			}
			idleData := map[string]any{
				"startDate":  startDate.Format(time.RFC3339),
				"endDate":    endDate.Format(time.RFC3339),
				"cluster_id": resource.Id,
			}
			if len(connectionMetrics.Items) > 0 {
				idleData["connections_metrics"] = connectionMetrics.Items[0]
			}
			if len(cpuMetrics.Items) > 0 {
				idleData["cpu_metrics"] = cpuMetrics.Items[0]
			}
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryRightSizing, RuleName: "aws_elasticache_idle_instance", Severity: providers.RecommendationSeverityMedium,
				Savings: savings, Data: idleData, Action: providers.RecommendationActionDelete, ResourceServiceName: resource.ServiceName, ResourceId: resource.Id, ResourceType: resource.Type, ResourceRegion: resource.Region,
			})
			continue // Skip right-sizing checks for idle instances
		}

		// Right-sizing analysis (only if not idle)
		rightsizingMetrics, errRightsize := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id},
			ServiceName: resource.ServiceName,
			StartDate:   &startDate,
			EndDate:     &endDate,
			Region:      resource.Region,
			MetricNames: []string{"BytesUsedForCache", "Evictions", "CacheHitRate"},
			Step:        3600 * time.Second,
			Statistics:  []string{"Average", "Maximum"},
		})

		if errRightsize != nil {
			ctx.GetLogger().Warn("Error getting right-sizing metrics", "resourceId", resource.Id, "error", errRightsize, "region", resource.Region)
			continue
		}

		if len(rightsizingMetrics.Items) < 3 {
			ctx.GetLogger().Debug("Not enough metrics for right-sizing analysis", "resourceId", resource.Id, "metricsCount", len(rightsizingMetrics.Items))
			continue
		}

		var bytesUsedValues, evictionsValues, cacheHitRateValues []float64
		var maxEvictions float64

		for _, item := range rightsizingMetrics.Items {
			switch item.Name {
			case "BytesUsedForCache":
				switch item.Statistics {
				case "Average":
					bytesUsedValues = item.Values
				}
			case "Evictions":
				switch item.Statistics {
				case "Average":
					evictionsValues = item.Values
				case "Maximum":
					if len(item.Values) > 0 {
						for _, v := range item.Values {
							if v > maxEvictions {
								maxEvictions = v
							}
						}
					}
				}
			case "CacheHitRate":
				switch item.Statistics {
				case "Average":
					cacheHitRateValues = item.Values
				}
			}
		}

		if len(bytesUsedValues) == 0 || len(cacheHitRateValues) == 0 {
			ctx.GetLogger().Debug("Missing required metrics for right-sizing", "resourceId", resource.Id)
			continue
		}

		// Get instance memory capacity from meta
		var totalMemoryBytes float64
		if instanceTypeDetailsAny, itdOk := meta["InstanceTypeDetails"]; itdOk {
			if instanceTypeDetails, itdMapOk := instanceTypeDetailsAny.(map[string]any); itdMapOk {
				if productAny, prodOk := instanceTypeDetails["product"]; prodOk {
					if productDetails, prodMapOk := productAny.(map[string]any); prodMapOk {
						if attributesAny, attrOk := productDetails["attributes"]; attrOk {
							if attributes, attrMapOk := attributesAny.(map[string]any); attrMapOk {
								if memoryAny, memOk := attributes["memory"]; memOk {
									if memoryStr, memStrOk := memoryAny.(string); memStrOk {
										// Parse memory string (e.g., "2.33 GiB" or "6.05 GB")
										var memoryGiB float64
										_, err := fmt.Sscanf(memoryStr, "%f", &memoryGiB)
										if err == nil && memoryGiB > 0 {
											totalMemoryBytes = memoryGiB * 1024 * 1024 * 1024 // Convert GiB to bytes
										}
									}
								}
							}
						}
					}
				}
			}
		}

		if totalMemoryBytes == 0 {
			ctx.GetLogger().Debug("Could not determine total memory capacity", "resourceId", resource.Id)
			continue
		}

		// Calculate averages
		avgBytesUsed := averageFloat64(bytesUsedValues)
		avgCacheHitRate := averageFloat64(cacheHitRateValues)
		avgEvictions := averageFloat64(evictionsValues)
		memoryUtilization := (avgBytesUsed / totalMemoryBytes) * 100

		// Check for oversized cache
		// Criteria: BytesUsedForCache <30% AND Evictions = 0 AND CacheHitRate >95%
		if memoryUtilization < 30 && maxEvictions == 0 && avgCacheHitRate > 95 {
			// Recommend smaller instance
			currentNodeType, _ := ToString(meta["CacheNodeType"], "CacheNodeType", resource.Arn, resource.Region, ctx)
			engineStr, _ := ToString(meta["Engine"], "Engine", resource.Arn, resource.Region, ctx)

			// Get current pricing
			currentPrice := 0.0
			if instanceTypeDetailsAny, itdOk := meta["InstanceTypeDetails"]; itdOk {
				if instanceTypeDetails, itdMapOk := instanceTypeDetailsAny.(map[string]any); itdMapOk {
					price, errPrice := getPricingValue(instanceTypeDetails)
					if errPrice == nil {
						currentPrice = price
					}
				}
			}

			// Find recommended smaller instance (50% memory)
			recommendedMemory := int(totalMemoryBytes / (1024 * 1024 * 1024) / 2) // Half of current memory in GiB
			if recommendedMemory < 1 {
				recommendedMemory = 1
			}

			alternativeInstances, errAlt := getAvailableCacheInstances(ctx.GetContext(), cfg, resource.Region, engineStr, recommendedMemory, 0, "")
			if errAlt == nil && len(alternativeInstances) > 0 {
				// Sort by price to get cheapest alternative
				sort.Slice(alternativeInstances, func(i, j int) bool {
					priceI, errI := getPricingValue(alternativeInstances[i])
					priceJ, errJ := getPricingValue(alternativeInstances[j])
					if errI != nil || errJ != nil {
						return false
					}
					return priceI < priceJ
				})

				// Get cheapest alternative price
				minPrice, errMinPrice := getPricingValue(alternativeInstances[0])
				if errMinPrice == nil && minPrice < currentPrice {
					savings := (currentPrice - minPrice) * 24 * 30
					if savings > 5 { // Only recommend if savings > $5/month
						recommendations = append(recommendations, providers.Recommendation{
							CategoryName: providers.RecommendationCategoryRightSizing,
							RuleName:     "aws_elasticache_oversized",
							Severity:     providers.RecommendationSeverityMedium,
							Savings:      savings,
							Data: map[string]any{
								"cluster_id":               resource.Id,
								"current_node_type":        currentNodeType,
								"recommendedInstances":     alternativeInstances,
								"memory_utilization":       memoryUtilization,
								"avg_cache_hit_rate":       avgCacheHitRate,
								"max_evictions":            maxEvictions,
								"current_monthly_cost":     currentPrice * 24 * 30,
								"recommended_monthly_cost": minPrice * 24 * 30,
								"startDate":                startDate.Format(time.RFC3339),
								"endDate":                  endDate.Format(time.RFC3339),
							},
							Action:              providers.RecommendationActionModify,
							ResourceServiceName: resource.ServiceName,
							ResourceId:          resource.Id,
							ResourceType:        resource.Type,
							ResourceRegion:      resource.Region,
						})
					}
				}
			}
		}

		// Check for undersized cache
		// Criteria: Evictions >0 sustained AND CacheHitRate <80%
		if avgEvictions > 0 && avgCacheHitRate < 80 {
			currentNodeType, _ := ToString(meta["CacheNodeType"], "CacheNodeType", resource.Arn, resource.Region, ctx)
			engineStr, _ := ToString(meta["Engine"], "Engine", resource.Arn, resource.Region, ctx)

			// Get current pricing
			currentPrice := 0.0
			if instanceTypeDetailsAny, itdOk := meta["InstanceTypeDetails"]; itdOk {
				if instanceTypeDetails, itdMapOk := instanceTypeDetailsAny.(map[string]any); itdMapOk {
					price, errPrice := getPricingValue(instanceTypeDetails)
					if errPrice == nil {
						currentPrice = price
					}
				}
			}

			// Recommend instance with 2x memory
			recommendedMemory := int(totalMemoryBytes / (1024 * 1024 * 1024) * 2) // Double current memory in GiB

			alternativeInstances, errAlt := getAvailableCacheInstances(ctx.GetContext(), cfg, resource.Region, engineStr, recommendedMemory, 0, "")
			if errAlt == nil && len(alternativeInstances) > 0 {
				// Sort by price to get cheapest alternative with more memory
				sort.Slice(alternativeInstances, func(i, j int) bool {
					priceI, errI := getPricingValue(alternativeInstances[i])
					priceJ, errJ := getPricingValue(alternativeInstances[j])
					if errI != nil || errJ != nil {
						return false
					}
					return priceI < priceJ
				})

				// Get cheapest alternative price
				minPrice, errMinPrice := getPricingValue(alternativeInstances[0])
				if errMinPrice == nil {
					additionalCost := (minPrice - currentPrice) * 24 * 30
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "aws_elasticache_undersized",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      -additionalCost, // Negative savings (cost increase)
						Data: map[string]any{
							"cluster_id":               resource.Id,
							"current_node_type":        currentNodeType,
							"recommendedInstances":     alternativeInstances,
							"memory_utilization":       memoryUtilization,
							"avg_cache_hit_rate":       avgCacheHitRate,
							"avg_evictions":            avgEvictions,
							"max_evictions":            maxEvictions,
							"current_monthly_cost":     currentPrice * 24 * 30,
							"recommended_monthly_cost": minPrice * 24 * 30,
							"reason":                   "Cache is experiencing evictions and low hit rate, indicating memory pressure",
							"startDate":                startDate.Format(time.RFC3339),
							"endDate":                  endDate.Format(time.RFC3339),
						},
						Action:              providers.RecommendationActionModify,
						ResourceServiceName: resource.ServiceName,
						ResourceId:          resource.Id,
						ResourceType:        resource.Type,
						ResourceRegion:      resource.Region,
					})
				}
			}
		}

		// Check for ineffective cache (low hit rate without memory pressure)
		// Criteria: CacheHitRate <50%
		if avgCacheHitRate < 50 && avgEvictions == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "aws_elasticache_low_hit_rate",
				Severity:     providers.RecommendationSeverityMedium,
				Savings:      0,
				Data: map[string]any{
					"cluster_id":         resource.Id,
					"avg_cache_hit_rate": avgCacheHitRate,
					"memory_utilization": memoryUtilization,
					"avg_evictions":      avgEvictions,
					"reason":             "Cache hit rate is very low (<50%), indicating the cache strategy may need review. This is not a sizing issue.",
					"startDate":          startDate.Format(time.RFC3339),
					"endDate":            endDate.Format(time.RFC3339),
				},
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

// Helper function to calculate average of float64 slice

// Helper function for safe map string access (similar to what might be in a common utility)
func ToString(valAny any, keyName string, resourceArn string, region string, ctx providers.CloudProviderContext) (string, bool) {
	if valAny == nil {
		return "", false
	}
	strVal, ok := valAny.(string)
	if !ok {
		ctx.GetLogger().Warn(fmt.Sprintf("'%s' is not a string", keyName), "resourceArn", resourceArn, "region", region, "actualType", fmt.Sprintf("%T", valAny))
		return "", false
	}
	return strVal, true
}

// Helper function for safe map bool access
func ToBool(valAny any, keyName string, resourceArn string, region string, ctx providers.CloudProviderContext) (bool, bool) {
	if valAny == nil {
		return false, false
	}
	boolVal, ok := valAny.(bool)
	if !ok {
		ctx.GetLogger().Warn(fmt.Sprintf("'%s' is not a boolean", keyName), "resourceArn", resourceArn, "region", region, "actualType", fmt.Sprintf("%T", valAny))
		return false, false
	}
	return boolVal, true
}

func (a *amazonElasticCache) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonElasticCache) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "elasticcache",
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
