package aws

import (
	"context"
	"errors"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	// Ensure other necessary imports like "github.com/samber/lo" are present if used
)

// getRedshiftNodePricing gets the hourly pricing for a Redshift node type
func getRedshiftNodePricing(cfg aws.Config, region string, nodeType string) (float64, error) {
	filtersMap := map[string]string{
		"regionCode":    region,
		"productFamily": "Compute Instance",
		"instanceType":  nodeType,
	}

	priceList, err := getAvailableInstancesFromPricing(cfg, "AmazonRedshift", filtersMap)
	if err != nil {
		return 0, err
	}

	if len(priceList) == 0 {
		return 0, fmt.Errorf("no pricing found for node type %s in region %s", nodeType, region)
	}
	price, err := getPricingValue(priceList[0])
	if err != nil {
		return 0, err
	}

	return price, nil
}

type amazonRedshift struct {
	DefaultAwsServiceImpl
}

func (a *amazonRedshift) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for Redshift yet.
	return errors.ErrUnsupported
}

func (a *amazonRedshift) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for Redshift yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonRedshift) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonRedshift) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameRedshift)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := redshift.NewFromConfig(cfg)
	resources := []providers.Resource{}

	paginator := redshift.NewDescribeClustersPaginator(svc, &redshift.DescribeClustersInput{})
	for paginator.HasMorePages() {
		clustersOutput, err := paginator.NextPage(context.TODO())
		if err != nil {
			ctx.GetLogger().Error("failed to fetch redshift clusters", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
			// Decide if we should return partial results or fail completely
			return resources, err
		}

		for _, cluster := range clustersOutput.Clusters {
			tags := make(map[string][]string)
			// Tags are included in DescribeClusters response
			for _, tag := range cluster.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}

			meta := structToMap(cluster) // Use detailed cluster info for Meta

			// Map Redshift ClusterStatus to standard status
			status := providers.ResourceStatusUnknown
			if cluster.ClusterStatus != nil {
				switch strings.ToLower(*cluster.ClusterStatus) {
				case "available", "modifying", "rebooting", "resizing", "maintenance":
					status = providers.ResourceStatusActive
				case "creating", "final-snapshot":
					status = providers.ResourceStatusActive
				case "deleting":
					status = providers.ResourceStatusDeleted // Or Inactive
				case "hardware-failure", "incompatible-parameters", "incompatible-network":
					status = providers.ResourceStatusInactive // Or Error
				default:
					status = providers.ResourceStatusUnknown
				}
			}

			// Determine creation time (use ClusterCreateTime if available, fallback)
			createdAt := time.Now() // Fallback
			if cluster.ClusterCreateTime != nil {
				createdAt = *cluster.ClusterCreateTime
			}

			resource := providers.Resource{
				Id:          *cluster.ClusterIdentifier,
				ServiceName: ServiceNameRedshift,
				Name:        *cluster.ClusterIdentifier,
				Status:      status,
				Region:      regionName,
				Tags:        tags,
				Meta:        meta,
				CreatedAt:   createdAt,
				Type:        getAwsServiceResourceType(ServiceNameRedshift, "cluster"),
			}
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func (a *amazonRedshift) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameRedshift)
		return recommendations, err
	}

	for _, resource := range existingResources {
		// Ensure we are looking at Redshift Clusters
		if resource.Type != getAwsServiceResourceType(ServiceNameRedshift, "cluster") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: Encryption at Rest
		encrypted := false
		if enc, ok := meta["Encrypted"].(bool); ok {
			encrypted = enc
		}
		if !encrypted {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_redshift_encryption_at_rest",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_identifier": resource.Id, "cluster_arn": resource.Arn, "reason": "Cluster is not encrypted at rest."},
				Action:              providers.RecommendationActionModify, // Note: Encryption must be enabled at creation time. Action might be 'Recreate'.
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Public Accessibility
		publiclyAccessible := false
		if pub, ok := meta["PubliclyAccessible"].(bool); ok {
			publiclyAccessible = pub
		}
		if publiclyAccessible {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_redshift_public_access",
				Severity:            providers.RecommendationSeverityHigh,
				Savings:             0,
				Data:                map[string]any{"cluster_identifier": resource.Id, "cluster_arn": resource.Arn, "reason": "Cluster is publicly accessible."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Enhanced VPC Routing
		// Recommend enabling if not already enabled
		enhancedVpcRoutingEnabled := false
		if evr, ok := meta["EnhancedVpcRouting"].(bool); ok {
			enhancedVpcRoutingEnabled = evr
		}
		if !enhancedVpcRoutingEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity, // Or Configuration
				RuleName:            "aws_redshift_enhanced_vpc_routing",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_identifier": resource.Id, "cluster_arn": resource.Arn, "reason": "Enhanced VPC routing is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 4: Audit Logging
		// Check LoggingStatus.LoggingEnabled field
		auditLoggingEnabled := false
		if logStatus, ok := meta["LoggingStatus"].(map[string]any); ok {
			if enabled, ok := logStatus["LoggingEnabled"].(bool); ok {
				auditLoggingEnabled = enabled
			}
		}
		if !auditLoggingEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity, // Or Auditing
				RuleName:            "aws_redshift_audit_logging",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_identifier": resource.Id, "cluster_arn": resource.Arn, "reason": "Audit logging is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 5: Automated Snapshot Retention Period
		// Recommend if < 7 days
		retentionPeriod := 0.0 // Use float64 as structToMap likely converts numbers
		if period, ok := meta["AutomatedSnapshotRetentionPeriod"].(float64); ok {
			retentionPeriod = period
		}
		if retentionPeriod < 7 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration, // Or Reliability
				RuleName:            "aws_redshift_snapshot_retention",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"cluster_identifier": resource.Id, "cluster_arn": resource.Arn, "current_retention_days": int(retentionPeriod), "reason": "Automated snapshot retention period is less than 7 days."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 6: User Activity Logging (Requires checking database parameters, complex, placeholder)
		/*
			// Need to call DescribeClusterParameters and check specific parameters like 'enable_user_activity_logging'
			// This requires knowing the parameter group associated with the cluster.
			paramGroupName := ""
			if pgGroups, ok := meta["ClusterParameterGroups"].([]any); ok && len(pgGroups) > 0 {
				// Assuming the first one is the active one
				if pgName, ok := pgGroups[0].(map[string]any)["ParameterGroupName"].(string); ok {
					paramGroupName = pgName
				}
			}
			if paramGroupName != "" {
				// Call DescribeClusterParameters with ParameterGroupName
				// Check if 'enable_user_activity_logging' parameter is 'true'
				// If not, create recommendation
			}
		*/

		// Check 7: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"cluster_identifier": resource.Id, "cluster_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Idle Redshift Cluster Detection
		// Check CPU < 5% average for 99% of 7 days AND DatabaseConnections ≤ 0
		startDate := time.Now().Add(-time.Hour * 24 * 7)
		endDate := time.Now()

		cpuMetrics, err := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id},
			ServiceName: resource.ServiceName,
			StartDate:   &startDate,
			EndDate:     &endDate,
			Region:      resource.Region,
			MetricNames: []string{"CPUUtilization"},
			Step:        3600 * time.Second,
			Statistics:  []string{"Average"},
		})

		connectionMetrics, connErr := a.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
			ResourceIds: []string{resource.Id},
			ServiceName: resource.ServiceName,
			StartDate:   &startDate,
			EndDate:     &endDate,
			Region:      resource.Region,
			MetricNames: []string{"DatabaseConnections"},
			Step:        3600 * time.Second,
			Statistics:  []string{"Average"},
		})

		// Check for idle cluster
		if err == nil && connErr == nil && len(cpuMetrics.Items) > 0 && len(connectionMetrics.Items) > 0 {
			cpuValues := cpuMetrics.Items[0].Values
			connectionValues := connectionMetrics.Items[0].Values

			// Count how many CPU readings are < 5%
			lowCPUCount := 0
			for _, cpu := range cpuValues {
				if cpu < 5.0 {
					lowCPUCount++
				}
			}

			// Check if 99% of readings have low CPU
			lowCPUPercent := float64(lowCPUCount) / float64(len(cpuValues)) * 100

			// Check if connections are zero
			maxConnections := 0.0
			for _, conn := range connectionValues {
				if conn > maxConnections {
					maxConnections = conn
				}
			}

			isIdle := lowCPUPercent >= 99.0 && maxConnections == 0

			if isIdle {
				// Calculate savings: node type × node count × hours × price
				nodeCount := 1.0
				if nc, ok := meta["NumberOfNodes"].(float64); ok {
					nodeCount = nc
				}

				nodeType := ""
				if nt, ok := meta["NodeType"].(string); ok {
					nodeType = nt
				}

				// Get actual pricing from AWS Pricing API
				hourlyPricePerNode := 0.25 // Fallback price
				if nodeType != "" {
					if price, err := getRedshiftNodePricing(cfg, resource.Region, nodeType); err == nil {
						hourlyPricePerNode = price
					} else {
						ctx.GetLogger().Warn("failed to get Redshift node pricing, using default", "error", err, "nodeType", nodeType, "region", resource.Region)
					}
				}

				// Calculate monthly savings
				monthlySavings := hourlyPricePerNode * nodeCount * 24 * 30

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryRightSizing,
					RuleName:     "aws_redshift_idle_cluster",
					Severity:     providers.RecommendationSeverityHigh,
					Savings:      monthlySavings,
					Data: map[string]any{
						"cluster_id":            resource.Id,
						"low_cpu_percent":       lowCPUPercent,
						"max_connections":       maxConnections,
						"node_count":            nodeCount,
						"node_type":             nodeType,
						"hourly_price_per_node": hourlyPricePerNode,
						"cpu_metrics":           cpuMetrics.Items[0],
						"connection_metrics":    connectionMetrics.Items[0],
						"startDate":             startDate.Format(time.RFC3339),
						"endDate":               endDate.Format(time.RFC3339),
						"reason":                fmt.Sprintf("Cluster is idle with CPU < 5%% for %.1f%% of time and zero database connections", lowCPUPercent),
					},
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			} else if err == nil && len(cpuMetrics.Items) > 0 {
				// Check for underutilized cluster (CPU < 10% average over 7 days)
				avgCPU := 0.0
				for _, cpu := range cpuValues {
					avgCPU += cpu
				}
				avgCPU = avgCPU / float64(len(cpuValues))

				if avgCPU < 10.0 {
					nodeCount := 1.0
					if nc, ok := meta["NumberOfNodes"].(float64); ok {
						nodeCount = nc
					}

					nodeType := ""
					if nt, ok := meta["NodeType"].(string); ok {
						nodeType = nt
					}

					// Get actual pricing from AWS Pricing API
					hourlyPricePerNode := 0.25 // Fallback price
					if nodeType != "" {
						if price, err := getRedshiftNodePricing(cfg, resource.Region, nodeType); err == nil {
							hourlyPricePerNode = price
						} else {
							ctx.GetLogger().Warn("failed to get Redshift node pricing, using default", "error", err, "nodeType", nodeType, "region", resource.Region)
						}
					}

					// Estimate potential savings by downsizing (50% reduction estimate)
					monthlySavings := hourlyPricePerNode * nodeCount * 0.5 * 24 * 30

					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "aws_redshift_underutilized",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      monthlySavings,
						Data: map[string]any{
							"cluster_id":            resource.Id,
							"avg_cpu":               avgCPU,
							"node_count":            nodeCount,
							"node_type":             nodeType,
							"hourly_price_per_node": hourlyPricePerNode,
							"cpu_metrics":           cpuMetrics.Items[0],
							"startDate":             startDate.Format(time.RFC3339),
							"endDate":               endDate.Format(time.RFC3339),
							"recommendation":        "Consider downsizing cluster or reducing node count by 50%",
							"reason":                fmt.Sprintf("Cluster is underutilized with average CPU %.2f%% over 7 days", avgCPU),
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

		// Check 7: Node Type Generation (Current vs Latest)
		nodeType := ""
		if nt, ok := meta["NodeType"].(string); ok {
			nodeType = nt
		}

		if nodeType != "" {
			// Try to get node type details from pricing to check generation
			nodeTypePricing, err := getRedshiftNodePricing(cfg, resource.Region, nodeType)
			if err == nil && nodeTypePricing > 0 {
				// Check if this is an older generation node type (dc1, dc2, ds2 are older, ra3 is latest)
				isOldGeneration := false
				recommendedNodeType := ""

				// Explicit node type mappings to avoid invalid instance types
				ra3Mapping := map[string]string{
					"dc2.large":   "ra3.xlplus",
					"dc2.8xlarge": "ra3.4xlarge",
					"ds2.xlarge":  "ra3.xlplus",
					"ds2.8xlarge": "ra3.4xlarge",
				}

				if strings.HasPrefix(nodeType, "dc1.") {
					isOldGeneration = true
					recommendedNodeType = strings.Replace(nodeType, "dc1.", "dc2.", 1)
				} else if strings.HasPrefix(nodeType, "dc2.") || strings.HasPrefix(nodeType, "ds2.") {
					isOldGeneration = true
					if mapped, ok := ra3Mapping[nodeType]; ok {
						recommendedNodeType = mapped
					}
				}

				if isOldGeneration && recommendedNodeType != "" {
					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryInfraUpgrade,
						RuleName:     "aws_redshift_node_generation",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      0,
						Data: map[string]any{
							"cluster_id":            resource.Id,
							"current_node_type":     nodeType,
							"recommended_node_type": recommendedNodeType,
							"reason":                fmt.Sprintf("Cluster is using older generation node type %s, consider upgrading to %s", nodeType, recommendedNodeType),
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

		// Check 9: SSL Connection Requirement
		// Check ClusterParameterGroups for require_ssl parameter
		// IMPORTANT: This is a heuristic check based on parameter group name patterns.
		// It does NOT call DescribeClusterParameters to verify actual SSL settings.
		// Limitations:
		// - Custom parameter groups without SSL enforcement won't be flagged (false negatives)
		// - Renamed default groups won't match (edge case)
		// - Only flags clusters using parameter groups containing "default" in the name
		requireSSL := true // Default assumption
		if paramGroups, ok := meta["ClusterParameterGroups"].([]interface{}); ok && len(paramGroups) > 0 {
			// For this implementation, we'll check if there's a custom parameter group
			// and recommend verifying SSL is required
			for _, pgInterface := range paramGroups {
				if pg, ok := pgInterface.(map[string]interface{}); ok {
					paramGroupName := ""
					if pgn, ok := pg["ParameterGroupName"].(string); ok {
						paramGroupName = pgn
					}

					// If using default parameter group, SSL is typically not enforced
					if strings.Contains(paramGroupName, "default") {
						requireSSL = false
						break
					}
				}
			}
		}

		if !requireSSL {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategorySecurity,
				RuleName:     "aws_redshift_require_ssl",
				Severity:     providers.RecommendationSeverityHigh,
				Savings:      0,
				Data: map[string]any{
					"cluster_id": resource.Id,
					"reason":     "Cluster may not require SSL connections (heuristic check based on parameter group name). Verify require_ssl parameter is enabled to enforce encrypted connections",
					"note":       "This check uses parameter group name heuristics. For definitive SSL status, inspect cluster parameter group settings in AWS Console or use DescribeClusterParameters API",
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

func (a *amazonRedshift) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonRedshift) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "redshift",
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
