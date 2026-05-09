package account

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
)

type CloudAccountMetricsJob struct {
	JobId           string   `json:"job_id"`
	AccountId       string   `json:"account_id"`
	TenantId        string   `json:"tenant_id"`
	ServiceName     string   `json:"service_name"`
	Regions         []string `json:"regions"`
	StartDate       string   `json:"start_date"`
	EndDate         string   `json:"end_date"`
	TargetAccountId string   `json:"target_account_id,omitempty"`
}

func StoreMetricesForAllAccounts(ctx *security.RequestContext, targetAccountId string) {
	t0 := time.Now()
	ctx.GetLogger().Info("metrics: starting metrics job enqueuing for accounts", "targetAccountId", lo.Ternary(targetAccountId != "", targetAccountId, "all"))

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		ctx.GetLogger().Error("metrics: unable to get database manager", "error", err)
		return
	}

	accountTenantIds := map[string]string{}
	if targetAccountId != "" {
		// Fetch tenant for the target account
		var tenantId string
		err := dbms.QueryRowAndScan(&tenantId, "select tenant from cloud_accounts where id = $1 and status = 'active' and lower(cloud_provider) IN ('aws', 'azure', 'gcp', 'cloudfoundry')", targetAccountId)
		if err != nil {
			ctx.GetLogger().Error("metrics: unable to fetch tenant for target account", "accountId", targetAccountId, "error", err)
			return
		}
		accountTenantIds[targetAccountId] = tenantId
	} else {
		// Fetch all active cloud accounts (AWS, Azure, GCP)
		queryResponse := []map[string]any{}
		err := dbms.QueryAndScan(&queryResponse, "select id::text, tenant::text from cloud_accounts where status = 'active' and lower(cloud_provider) IN ('aws', 'azure', 'gcp', 'cloudfoundry')")
		if err != nil {
			ctx.GetLogger().Error("metrics: unable to fetch active accounts", "error", err)
			return
		}
		for _, qr := range queryResponse {
			accountTenantIds[qr["id"].(string)] = qr["tenant"].(string)
		}
	}

	if len(accountTenantIds) == 0 {
		ctx.GetLogger().Info("metrics: no active accounts found")
		return
	}
	ctx.GetLogger().Info("metrics: fetched active accounts", "count", len(accountTenantIds), "time", time.Since(t0).String())

	currentDate := time.Now().UTC()
	startDate := time.Date(currentDate.Year(), currentDate.Month(), currentDate.Day()-1, 0, 0, 0, 0, currentDate.Location())
	endDate := time.Date(currentDate.Year(), currentDate.Month(), currentDate.Day(), 0, 0, 0, 0, currentDate.Location())

	// Publish jobs to RabbitMQ
	publishedCount := 0
	failedCount := 0

	for accountId, tenantId := range accountTenantIds {
		// Fetch service-region combinations for this account
		queryResponse := []map[string]any{}
		err := dbms.QueryAndScan(&queryResponse, `select distinct cr.region, cr.service_name
		from cloud_resourses cr
		where cr.account = $1 and cr.is_active = true and cr.resourse_id is not null and cr.resourse_id != ''
		`, accountId)
		if err != nil {
			ctx.GetLogger().Error("metrics: unable to fetch resources", "error", err, "accountId", accountId)
			failedCount++
			continue
		}

		serviceRegions := map[string][]string{}
		for _, qr := range queryResponse {
			serviceName := qr["service_name"].(string)
			region := qr["region"].(string)
			if _, ok := serviceRegions[serviceName]; !ok {
				serviceRegions[serviceName] = []string{}
			}
			serviceRegions[serviceName] = append(serviceRegions[serviceName], region)
		}

		// Publish one job per service
		for serviceName, regions := range serviceRegions {
			job := CloudAccountMetricsJob{
				JobId:           uuid.New().String(),
				AccountId:       accountId,
				TenantId:        tenantId,
				ServiceName:     serviceName,
				Regions:         regions,
				StartDate:       startDate.Format(time.RFC3339),
				EndDate:         endDate.Format(time.RFC3339),
				TargetAccountId: targetAccountId,
			}
			err = common.MqPublish(config.Config.RabbitMqCloudAccountMetricsExchange, config.Config.RabbitMqCloudAccountMetricsQueue, job)
			if err != nil {
				ctx.GetLogger().Error("metrics: failed to publish job", "error", err, "accountId", accountId, "service", serviceName, "job_id", job.JobId)
				failedCount++
			} else {
				ctx.GetLogger().Debug("metrics: published job", "accountId", accountId, "service", serviceName, "job_id", job.JobId)
				publishedCount++
			}
		}
	}

	ctx.GetLogger().Info("metrics: finished enqueuing metrics jobs", "total_time", time.Since(t0).String(), "published", publishedCount, "failed", failedCount)
}

// ConsumeCloudAccountMetricsJobs starts a RabbitMQ consumer that processes cloud account metrics jobs
func ConsumeCloudAccountMetricsJobs(ctx *security.RequestContext, concurrency int) error {
	if concurrency <= 0 {
		concurrency = config.Config.CloudCollectorServerCostProcessingWorkersMax
		if concurrency <= 0 {
			concurrency = 1 // fallback default
		}
	}

	ctx.GetLogger().Info("metrics: starting cloud account metrics consumer", "concurrency", concurrency, "queue", config.Config.RabbitMqCloudAccountMetricsQueue, "exchange", config.Config.RabbitMqCloudAccountMetricsExchange)

	processor := func(data []byte) error {
		var job CloudAccountMetricsJob
		err := common.UnmarshalJson(data, &job)
		if err != nil {
			// Permanent error - malformed message. ACK to prevent poison message loop.
			// TODO: Send to DLQ for inspection
			ctx.GetLogger().Error("metrics: failed to unmarshal job - dropping message", "error", err, "data", string(data))
			return nil // Return nil to ACK and drop the message
		}

		logger := ctx.GetLogger().With("accountId", job.AccountId, "service", job.ServiceName, "job_id", job.JobId)
		logger.Info("metrics: processing metrics job")

		// Create a new request context for this specific account
		jobCtx := security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdminWithTenant(job.TenantId), logger, ctx.GetTracer(), ctx.GetMeter())

		// Execute StoreMetrices logic
		_, err = StoreMetrices(jobCtx, job.AccountId, StoreMetricesRequest{
			ServiceName: job.ServiceName,
			Regions:     job.Regions,
			StartDate:   job.StartDate,
			EndDate:     job.EndDate,
		})
		if err != nil {
			// Send to DLQ instead of retrying indefinitely to prevent infinite retry loops
			// that accumulate memory from repeated CloudWatch API responses
			logger.Error("metrics: failed to store metrics - sending to DLQ", "error", err)
			sendToDLQWithConfig(jobCtx, data, "metrics_processing_error", err,
				config.Config.RabbitMqCloudAccountMetricsDLQExchange,
				config.Config.RabbitMqCloudAccountMetricsDLQQueue,
			)
			return nil // Return nil to ACK and remove from main queue
		}

		logger.Info("metrics: successfully processed metrics job")
		return nil
	}

	return common.MqConsume(
		config.Config.RabbitMqCloudAccountMetricsExchange,
		config.Config.RabbitMqCloudAccountMetricsQueue,
		config.Config.RabbitMqCloudAccountMetricsQueue,
		concurrency,
		processor,
	)
}

// normalizeMetricName converts cloud provider metric names to AWS-compatible format
// This ensures Azure and GCP metrics can be queried using the same GraphQL filters as AWS
func normalizeMetricName(provider, statistics, metricName string) string {
	if provider == "Azure" {
		// Azure-specific metric name mappings to match AWS naming conventions
		azureToAwsMetricMap := map[string]string{
			"percentage cpu":              "cpuutilization",
			"available memory bytes":      "memoryutilization",
			"available memory percentage": "memoryutilization",
			"network in total":            "networkin",
			"network out total":           "networkout",
			"disk read bytes":             "diskreadbytes",
			"disk write bytes":            "diskwritebytes",
			"disk read operations/sec":    "diskreadops",
			"disk write operations/sec":   "diskwriteops",
		}

		metricKey := strings.ToLower(metricName)
		if mappedName, ok := azureToAwsMetricMap[metricKey]; ok {
			return strings.ToLower(statistics) + "_" + mappedName
		}
	}

	if strings.EqualFold(provider, "gcp") {
		// GCP-specific metric name mappings to match AWS naming conventions
		gcpToAwsMetricMap := map[string]string{
			"cpu/utilization":              "cpuutilization",
			"uptime":                       "uptime",
			"disk/read_bytes_count":        "diskreadbytes",
			"disk/write_bytes_count":       "diskwritebytes",
			"network/received_bytes_count": "networkin",
			"network/sent_bytes_count":     "networkout",
			"memory/utilization":           "memoryutilization",
			"disk/bytes_used":              "diskbytesused",
			"disk/utilization":             "diskutilization",
			"network/connections":          "networkconnections",
		}

		metricKey := strings.ToLower(metricName)
		if mappedName, ok := gcpToAwsMetricMap[metricKey]; ok {
			return strings.ToLower(statistics) + "_" + mappedName
		}
	}

	// Fallback for unmapped metrics: normalize the name
	normalized := strings.ToLower(statistics + "_" + metricName)
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "/", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	return normalized
}

func StoreMetrices(ctx *security.RequestContext, accountId string, query StoreMetricesRequest) (StoreMetricesResponse, error) {
	t0 := time.Now()
	account, provider, err := getAccount(ctx, accountId)
	if err != nil {
		ctx.GetLogger().Error("unable to fetch account", "error", err)
		return StoreMetricesResponse{
			Duration: time.Since(t0),
		}, err
	}
	cloudProvider, ok := providers.GetProvider(provider)
	if !ok {
		return StoreMetricesResponse{
			Duration: time.Since(t0),
		}, fmt.Errorf("provider not found")
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return StoreMetricesResponse{}, err
	}

	queryResponse := []map[string]any{}

	err = dbms.QueryAndScan(&queryResponse, `select cr.region, cr.service_name, cr.resourse_id::text, cr.type, cr.id::text, cr.meta::text
	from cloud_resourses cr
	join cloud_accounts ca on cr.account = ca.id and ca.status = 'active' and cr.is_active = true
	where cr.account = $1 and lower(cr.service_name) = $2 and cr.resourse_id is not null and cr.resourse_id != ''
	`, accountId, strings.ToLower(query.ServiceName),
	)
	if err != nil {
		return StoreMetricesResponse{}, err
	}

	serviceResourceMap := map[string][]string{}
	resourceIdMap := map[string]string{}
	resourceMetaMap := map[string]map[string]any{} // Store metadata for each resource

	for _, qr := range queryResponse {
		region := qr["region"].(string)
		serviceName := qr["service_name"].(string)
		resourceId := qr["resourse_id"].(string)
		resourceType := qr["type"].(string)
		id := qr["id"].(string)

		// Parse metadata JSON
		var meta map[string]any
		if metaStr, ok := qr["meta"].(string); ok && metaStr != "" {
			if err := common.UnmarshalJson([]byte(metaStr), &meta); err != nil {
				ctx.GetLogger().Warn("failed to unmarshal resource meta", "resourceId", resourceId, "error", err)
			} else {
				resourceMetaMap[resourceId] = meta
			}
		}

		key := serviceName + "::" + region + "::" + resourceType
		if _, ok := serviceResourceMap[key]; !ok {
			serviceResourceMap[key] = []string{}
		}
		serviceResourceMap[key] = append(serviceResourceMap[key], resourceId)
		resourceIdMap[resourceId] = id
	}

	currentDate := time.Now().UTC()
	// Default: Use last 24 hours of data
	startDate := time.Date(currentDate.Year(), currentDate.Month(), currentDate.Day()-1, 0, 0, 0, 0, currentDate.Location())
	endDate := time.Date(currentDate.Year(), currentDate.Month(), currentDate.Day(), 0, 0, 0, 0, currentDate.Location())
	if query.StartDate != "" {
		startDate, err = time.Parse(time.RFC3339, query.StartDate)
		if err != nil {
			return StoreMetricesResponse{}, err
		}
	}

	if query.EndDate != "" {
		endDate, err = time.Parse(time.RFC3339, query.EndDate)
		if err != nil {
			return StoreMetricesResponse{}, err
		}
	}

	// Use a map to deduplicate metrics by (cloud_resource_id, metric, timestamp)
	// This prevents PostgreSQL error: "ON CONFLICT DO UPDATE command cannot affect row a second time"
	//
	// Why duplicates occur:
	// 1. Cloud provider APIs may return the same metric point multiple times
	// 2. Multiple metric queries (e.g., different statistics) may overlap
	// 3. Batching logic may process the same resource multiple times
	//
	// PostgreSQL's ON CONFLICT DO UPDATE requires unique keys within a single INSERT batch.
	// By deduplicating first, we ensure each (resource_id, metric, timestamp) appears only once.
	dbDataMap := make(map[string]map[string]any)

	for service, resources := range serviceResourceMap {
		//split resources in batches of 10
		batches := lo.Chunk(resources, 10)
		serviceAndRegionAndType := strings.Split(service, "::")
		for _, batch := range batches {
			// Use 1-hour step for GCP to avoid query timeout (7-day range with 1-min step = too much data)
			// AWS/Azure can use 24-hour step for daily aggregates
			step := time.Hour * 24
			if strings.EqualFold(provider, "gcp") {
				step = time.Hour * 1 // 1-hour aggregation for GCP
			}

			metrices, err := cloudProvider.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds:  batch,
				ResourceType: serviceAndRegionAndType[2],
				ServiceName:  serviceAndRegionAndType[0],
				Region:       serviceAndRegionAndType[1],
				Step:         step,
				StartDate:    &startDate,
				EndDate:      &endDate,
			})
			if err != nil {
				ctx.GetLogger().Error("unable to fetch metrices", "error", err)
				continue
			}

			ctx.GetLogger().Info("metrics: received from provider",
				"service", serviceAndRegionAndType[0],
				"region", serviceAndRegionAndType[1],
				"resourceCount", len(batch),
				"metricsCount", len(metrices.Items))

			for _, m := range metrices.Items {
				if len(m.Timestamps) == 0 {
					continue
				}

				// Look up the cloud_resource_id from our database
				var resourceId *string
				if id, ok := resourceIdMap[m.ResourceId]; ok {
					resourceId = &id
				}

				// Skip metrics without a matching resource in our database
				if resourceId == nil {
					ctx.GetLogger().Debug("skipping metric for unknown resource",
						"resourceId", m.ResourceId,
						"metric", m.Name,
						"service", serviceAndRegionAndType[0])
					continue
				}

				tags := map[string]string{
					"service_name": serviceAndRegionAndType[0],
					"region":       serviceAndRegionAndType[1],
					"resource_id":  m.ResourceId,
				}
				tagsStr, err := common.MarshalJson(tags)
				if err != nil {
					return StoreMetricesResponse{}, err
				}

				// Check if this is Azure Available Memory Bytes metric - convert to utilization percentage
				isAzureMemoryBytes := provider == "Azure" && strings.Contains(strings.ToLower(m.Name), "available memory bytes")

				for i, t := range m.Timestamps {
					// Normalize metric name to match AWS format for GraphQL compatibility
					metricName := normalizeMetricName(provider, m.Statistics, m.Name)
					value := m.Values[i]

					// For Azure: Convert "Available Memory Bytes" to "Memory Utilization %"
					if isAzureMemoryBytes {
						var utilizationCalculated bool
						// Get total memory from VM metadata (InstanceTypeDetails matches AWS format)
						if meta, ok := resourceMetaMap[m.ResourceId]; ok {
							if instanceTypeDetails, ok := meta["InstanceTypeDetails"].(map[string]any); ok {
								if memInfo, ok := instanceTypeDetails["MemoryInfo"].(map[string]any); ok {
									if sizeInMiB, ok := memInfo["SizeInMiB"].(float64); ok && sizeInMiB > 0 {
										// Convert total memory from MiB to bytes
										totalMemoryBytes := sizeInMiB * 1024 * 1024

										// Calculate memory utilization percentage
										// Utilization % = (Total - Available) / Total * 100
										usedMemoryBytes := totalMemoryBytes - value
										memoryUtilizationPercent := (usedMemoryBytes / totalMemoryBytes) * 100

										// Store as percentage (0-100)
										value = memoryUtilizationPercent
										utilizationCalculated = true

										ctx.GetLogger().Debug("azure: converted memory bytes to percentage",
											"resourceId", m.ResourceId,
											"totalMemoryMiB", sizeInMiB,
											"availableBytes", m.Values[i],
											"utilizationPercent", value)
									}
								}
							}
						}

						if !utilizationCalculated {
							ctx.GetLogger().Warn("azure: could not calculate memory utilization due to missing or invalid metadata", "resourceId", m.ResourceId)
							continue // Skip this metric point as it cannot be correctly calculated
						}
					}

					// Create unique key for deduplication: resource_id + metric + timestamp
					timestampStr := t.Format(time.RFC3339)
					dedupeKey := fmt.Sprintf("%s|%s|%s", *resourceId, metricName, timestampStr)

					// Only keep the latest value if there are duplicates
					dbDataMap[dedupeKey] = map[string]any{
						"timestamp":         timestampStr,
						"metric":            metricName,
						"value":             value,
						"metric_type":       "g",
						"tags":              string(tagsStr),
						"id":                uuid.New().String(),
						"cloud_resource_id": resourceId,
						"cloud_account_id":  accountId,
						"tenant_id":         ctx.GetSecurityContext().GetTenantId(),
					}
				}
			}
		}
	}

	// Collect Performance Insights metrics for RDS instances
	for service, resources := range serviceResourceMap {
		serviceAndRegionAndType := strings.Split(service, "::")
		serviceName := serviceAndRegionAndType[0]
		region := serviceAndRegionAndType[1]
		resourceType := serviceAndRegionAndType[2]

		// Only collect PI metrics for RDS database instances
		if serviceName != "AmazonRDS" || resourceType != "db" {
			continue
		}

		// Split resources in batches of 10 (same as CloudWatch)
		batches := lo.Chunk(resources, 10)
		for _, batch := range batches {
			// Query Performance Insights metrics using the same QueryMetrices API
			// The AWS/PI namespace routes to the PI implementation via aws_rds.go
			piMetrics, err := cloudProvider.QueryMetrices(ctx, account, providers.QueryMetricsRequest{
				ResourceIds:     batch,
				ResourceType:    resourceType,
				ServiceName:     serviceName,
				MetricNamespace: "AWS/PI", // Routes to getAwsPerformanceInsightsMetrics()
				MetricNames:     []string{"db.load.avg"},
				Region:          region,
				Step:            time.Hour,
				StartDate:       &startDate,
				EndDate:         &endDate,
			})

			if err != nil {
				// PI might not be enabled on all instances - log but don't fail
				if strings.Contains(err.Error(), "Performance Insights not enabled") {
					ctx.GetLogger().Debug("Performance Insights not enabled", "service", service, "resources", batch)
				} else {
					ctx.GetLogger().Error("unable to fetch Performance Insights metrics", "error", err, "service", service)
				}
				continue
			}

			// Transform PI metrics to database format (same as CloudWatch)
			for _, m := range piMetrics.Items {
				if len(m.Timestamps) == 0 {
					continue
				}

				tags := map[string]string{
					"service_name": serviceName,
					"region":       region,
					"resource_id":  m.ResourceId,
				}
				tagsStr, err := common.MarshalJson(tags)
				if err != nil {
					ctx.GetLogger().Error("unable to marshal tags", "error", err)
					continue
				}

				var resourceId *string
				if id, ok := resourceIdMap[m.ResourceId]; ok {
					resourceId = &id
				}

				// Add PI metric data points
				for i, t := range m.Timestamps {
					// Normalize metric name for consistency
					metricName := normalizeMetricName(provider, m.Statistics, m.Name)

					// Create unique key for deduplication: resource_id + metric + timestamp
					timestampStr := t.Format(time.RFC3339)
					dedupeKey := fmt.Sprintf("%s|%s|%s", *resourceId, metricName, timestampStr)

					// Only keep the latest value if there are duplicates
					dbDataMap[dedupeKey] = map[string]any{
						"timestamp":         timestampStr,
						"metric":            metricName,
						"value":             m.Values[i],
						"metric_type":       "g",
						"tags":              string(tagsStr),
						"id":                uuid.New().String(),
						"cloud_resource_id": resourceId,
						"cloud_account_id":  accountId,
						"tenant_id":         ctx.GetSecurityContext().GetTenantId(),
					}
				}
			}
		}
	}

	// Convert deduplicated map to slice for batch insert
	dbData := make([]map[string]any, 0, len(dbDataMap))
	for _, data := range dbDataMap {
		dbData = append(dbData, data)
	}

	if len(dbData) > 0 {
		ctx.GetLogger().Info("metrics: inserting data into database", "count", len(dbData), "deduplicated_from", len(dbDataMap))

		// Log sample data for debugging
		if len(dbData) > 0 {
			ctx.GetLogger().Debug("metrics: sample data", "first_row", dbData[0])
		}

		_, err = dbms.NamedExec(`insert into cloud_resource_metrics (timestamp, metric, value, metric_type, tags, id, cloud_resource_id, cloud_account_id, tenant_id)
			values (:timestamp, :metric, :value, :metric_type, :tags, :id, :cloud_resource_id, :cloud_account_id, :tenant_id)
			on conflict (cloud_resource_id, metric, timestamp)
				do update set value = excluded.value
			`, dbData)
		if err != nil {
			ctx.GetLogger().Error("unable to insert metrices", "error", err)
			return StoreMetricesResponse{}, err
		}

		ctx.GetLogger().Info("metrics: successfully inserted data into database", "count", len(dbData))
	} else {
		ctx.GetLogger().Warn("metrics: no data to insert", "reason", "dbData is empty")
	}

	return StoreMetricesResponse{
		Duration: time.Since(t0),
		Count:    len(dbData),
	}, nil
}
