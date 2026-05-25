package aws

import (
	"context"
	"errors"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/elasticsearchservice"
)

type amazonES struct {
	DefaultAwsServiceImpl
}

func (a *amazonES) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Applying recommendations is not supported for OpenSearch Service yet.
	return errors.ErrUnsupported
}

func (a *amazonES) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	// Applying commands is not supported for OpenSearch Service yet.
	return providers.ApplyCommandResponse{}, errors.ErrUnsupported
}

func (a *amazonES) QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	return getAwsCloudwatchMetrics(ctx, account, filter)
}

func (a *amazonES) GetResources(ctx providers.CloudProviderContext, account providers.Account, regionName string) ([]providers.Resource, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws session", "error", err, "accountNumber", account.AccountNumber, "region", regionName, "service", ServiceNameES)
		return []providers.Resource{}, err
	}
	cfg.Region = regionName
	svc := elasticsearchservice.NewFromConfig(cfg)
	resources := []providers.Resource{}

	// 1. List Domain Names
	domainNamesOutput, err := svc.ListDomainNames(context.TODO(), &elasticsearchservice.ListDomainNamesInput{})
	if err != nil {
		ctx.GetLogger().Error("failed to list opensearch domain names", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return resources, err
	}

	if len(domainNamesOutput.DomainNames) == 0 {
		return resources, nil // No domains in this region
	}

	// 2. Describe Domains (batch describe)
	var domainNames []string
	for _, dn := range domainNamesOutput.DomainNames {
		domainNames = append(domainNames, *dn.DomainName)
	}

	// DescribeElasticsearchDomains might be deprecated, use DescribeDomains if available/needed
	// For simplicity with older SDKs, let's stick to DescribeElasticsearchDomains
	describeOutput, err := svc.DescribeElasticsearchDomains(context.TODO(), &elasticsearchservice.DescribeElasticsearchDomainsInput{
		DomainNames: domainNames,
	})
	if err != nil {
		ctx.GetLogger().Error("failed to describe opensearch domains", "error", err, "accountNumber", account.AccountNumber, "region", regionName)
		return resources, err
	}

	for _, domainStatus := range describeOutput.DomainStatusList {
		tags := make(map[string][]string)

		// Get Tags for the domain
		tagsOutput, err := svc.ListTags(context.TODO(), &elasticsearchservice.ListTagsInput{
			ARN: domainStatus.ARN,
		})
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch tags for opensearch domain", "error", err, "domainArn", *domainStatus.ARN, "accountNumber", account.AccountNumber, "region", regionName)
		} else {
			for _, tag := range tagsOutput.TagList {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
		}

		meta := structToMap(domainStatus) // Use detailed status info for Meta

		// Determine status
		status := providers.ResourceStatusUnknown
		if domainStatus.Processing != nil && *domainStatus.Processing {
			status = providers.ResourceStatusActive
		} else if domainStatus.Deleted != nil && *domainStatus.Deleted {
			status = providers.ResourceStatusDeleted
		} else if domainStatus.Created != nil && *domainStatus.Created {
			// If not processing and not deleted, assume active if created flag is true
			status = providers.ResourceStatusActive
		}

		// Determine creation time (Describe doesn't provide it directly, fallback)
		createdAt := time.Now() // Fallback, maybe ListDomainNames has it? No.

		resource := providers.Resource{
			Id:          *domainStatus.DomainName,
			ServiceName: ServiceNameES,
			Name:        *domainStatus.DomainName,
			Status:      status,
			Region:      regionName,
			Tags:        tags,
			Meta:        meta,
			Arn:         *domainStatus.ARN,
			CreatedAt:   createdAt, // Placeholder time
			Type:        getAwsServiceResourceType(ServiceNameES, "domain"),
		}
		resources = append(resources, resource)
	}

	return resources, nil
}

func (a *amazonES) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}
	// Session might be needed if additional API calls are required beyond what's in Meta
	// session, err := getAwsSessionFromAccount(account)
	// if err != nil {
	// 	ctx.GetLogger().Error("failed to create aws session for recommendations", "error", err, "accountNumber", account.AccountNumber, "service", ServiceNameES)
	// 	return recommendations, err
	// }

	for _, resource := range existingResources {
		// Ensure we are looking at OpenSearch Domains
		if resource.Type != getAwsServiceResourceType(ServiceNameES, "domain") {
			continue
		}

		meta := resource.Meta
		if len(meta) == 0 {
			continue // Skip if no metadata available
		}

		// Check 1: Encryption at Rest
		encryptionAtRestEnabled := false
		if encOptions, ok := meta["EncryptionAtRestOptions"].(map[string]any); ok {
			if enabled, ok := encOptions["Enabled"].(bool); ok {
				encryptionAtRestEnabled = enabled
			}
		}
		if !encryptionAtRestEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_es_encryption_at_rest",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"domain_name": resource.Name, "domain_arn": resource.Arn, "reason": "Encryption at rest is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 2: Node-to-Node Encryption
		nodeToNodeEncryptionEnabled := false
		if n2nOptions, ok := meta["NodeToNodeEncryptionOptions"].(map[string]any); ok {
			if enabled, ok := n2nOptions["Enabled"].(bool); ok {
				nodeToNodeEncryptionEnabled = enabled
			}
		}
		if !nodeToNodeEncryptionEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity,
				RuleName:            "aws_es_node_to_node_encryption",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"domain_name": resource.Name, "domain_arn": resource.Arn, "reason": "Node-to-node encryption is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 3: Dedicated Master Nodes
		// Recommend if cluster config exists but dedicated master is not enabled (or count is < 3 for HA)
		dedicatedMasterEnabled := false
		dedicatedMasterCount := 0.0                                                       // Use float64 as structToMap likely converts numbers
		if clusterConfig, ok := meta["ElasticsearchClusterConfig"].(map[string]any); ok { // Check key name carefully
			if enabled, ok := clusterConfig["DedicatedMasterEnabled"].(bool); ok {
				dedicatedMasterEnabled = enabled
			}
			if count, ok := clusterConfig["DedicatedMasterCount"].(float64); ok {
				dedicatedMasterCount = count
			}
		}
		// Recommend if not enabled OR enabled but with fewer than 3 nodes (for production HA)
		if !dedicatedMasterEnabled || (dedicatedMasterEnabled && dedicatedMasterCount < 3) {
			reason := "Dedicated master nodes are not enabled."
			if dedicatedMasterEnabled && dedicatedMasterCount < 3 {
				reason = "Dedicated master nodes are enabled but count is less than 3, reducing high availability."
			}
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration, // Or Availability
				RuleName:            "aws_es_dedicated_master",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"domain_name": resource.Name, "domain_arn": resource.Arn, "reason": reason},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 4: Audit Logs / Slow Logs Publishing
		auditLogsEnabled := false
		slowLogsEnabled := false // Can check for SEARCH_SLOW_LOGS and INDEX_SLOW_LOGS
		if logOptions, ok := meta["LogPublishingOptions"].(map[string]any); ok {
			if audit, ok := logOptions["AUDIT_LOGS"].(map[string]any); ok {
				if enabled, ok := audit["Enabled"].(bool); ok {
					auditLogsEnabled = enabled
				}
			}
			// Example check for search slow logs
			if searchSlow, ok := logOptions["SEARCH_SLOW_LOGS"].(map[string]any); ok {
				if enabled, ok := searchSlow["Enabled"].(bool); ok && enabled {
					slowLogsEnabled = true
				}
			}
			// Could add similar check for INDEX_SLOW_LOGS
		}
		if !auditLogsEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategorySecurity, // Or Auditing
				RuleName:            "aws_es_audit_logs_enabled",
				Severity:            providers.RecommendationSeverityMedium,
				Savings:             0,
				Data:                map[string]any{"domain_name": resource.Name, "domain_arn": resource.Arn, "reason": "Audit logging is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}
		if !slowLogsEnabled {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration, // Or Performance
				RuleName:            "aws_es_slow_logs_enabled",
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"domain_name": resource.Name, "domain_arn": resource.Arn, "reason": "Slow query logging (e.g., SEARCH_SLOW_LOGS) is not enabled."},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Check 5: Latest Engine Version
		// This requires fetching compatible versions (GetCompatibleElasticsearchVersions) and comparing.
		// Placeholder for now as it adds complexity.
		/*
			engineVersion := ""
			if version, ok := meta["ElasticsearchVersion"].(string); ok { // Check key name
				engineVersion = version
			}
			// Call GetCompatibleElasticsearchVersions API
			// Compare engineVersion with the latest compatible version
			// If not latest, create recommendation
		*/

		// Check 6: Missing Tags
		if len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName:        providers.RecommendationCategoryConfiguration,
				RuleName:            "aws_tags", // Use generic tag rule name
				Severity:            providers.RecommendationSeverityLow,
				Savings:             0,
				Data:                map[string]any{"domain_name": resource.Name, "domain_arn": resource.Arn},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Potential Future Checks:
		// - Instance Type Generation (requires pricing/instance type details)
		// - Zone Awareness enabled
		// - Access Policy review (requires GetDomainConfig API)
		// - Idle/Underutilized check based on metrics (CPU, JVM Pressure, Request counts)
	}

	return recommendations, nil
}

func (a *amazonES) GetLogGroupName(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (string, error) {
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

func (a *amazonES) GetServiceMap(ctx providers.CloudProviderContext, account providers.Account, region, resourceId string) (providers.ServiceMapApplication, error) {
	app := providers.ServiceMapApplication{
		Id: providers.ServiceApplicationId{
			Name:      resourceId,
			Kind:      "es",
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
