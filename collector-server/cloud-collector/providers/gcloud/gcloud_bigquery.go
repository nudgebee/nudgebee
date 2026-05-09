package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"
)

const (
	ServiceNameBigQuery = "BigQuery"

	// bigQueryUnusedTableLookbackDays defines the lookback period for detecting unused tables.
	// Tables not queried within this period are considered unused.
	// This value is used consistently in both:
	// - GetRecommendations (unused table detection logic)
	// - getQueriedTablesFromJobs (INFORMATION_SCHEMA.JOBS query window)
	// IMPORTANT: If changed, must be updated in both locations to maintain consistency.
	bigQueryUnusedTableLookbackDays = 60
)

type bigQueryService struct{}

func (s *bigQueryService) GetMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	// Query Cloud Monitoring metrics for BigQuery
	// Common metrics: query/count, query/execution_times, slots/allocated,
	// storage/stored_bytes, table/uploaded_bytes, table/uploaded_row_count
	return getGcloudMonitoringMetrics(ctx, account, filter)
}

func (s *bigQueryService) GetResources(ctx providers.CloudProviderContext, account providers.Account, region string) ([]providers.Resource, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := bigquery.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create BigQuery client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close BigQuery client", "error", cerr)
		}
	}()

	var resources []providers.Resource

	// List all datasets in the project
	// BigQuery datasets can be regional or multi-regional, but we fetch them all once
	dit := client.Datasets(ctx.GetContext())
	for {
		dataset, err := dit.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping BigQuery datasets — API disabled or permission denied", "error", err)
				break
			}
			ctx.GetLogger().Error("failed to list datasets", "error", err)
			break
		}

		// Get dataset metadata
		metadata, err := dataset.Metadata(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to get dataset metadata", "error", err, "dataset", dataset.DatasetID)
			RecordGCPPermissionError(ctx, err)
			continue
		}

		// No region filtering - return all datasets with their actual location
		resource := s.datasetToResource(dataset.DatasetID, metadata, session.ProjectId)
		resources = append(resources, resource)

		// Also list tables within the dataset
		tableResources := s.getTablesInDataset(ctx, dataset, metadata.Location, session.ProjectId)
		resources = append(resources, tableResources...)
	}

	ctx.GetLogger().Info("fetched bigquery datasets and tables", "count", len(resources), "region", region)
	return resources, nil
}

func (s *bigQueryService) getTablesInDataset(ctx providers.CloudProviderContext, dataset *bigquery.Dataset, location, projectId string) []providers.Resource {
	var resources []providers.Resource

	tit := dataset.Tables(ctx.GetContext())
	for {
		table, err := tit.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			RecordGCPPermissionError(ctx, err)
			if isGCPPermissionOrNotFoundError(err) {
				ctx.GetLogger().Warn("skipping BigQuery tables — API disabled or permission denied", "error", err, "dataset", dataset.DatasetID)
			} else {
				ctx.GetLogger().Error("failed to list tables", "error", err, "dataset", dataset.DatasetID)
			}
			break
		}

		metadata, err := table.Metadata(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Error("failed to get table metadata", "error", err, "table", table.TableID)
			RecordGCPPermissionError(ctx, err)
			continue
		}

		resource := s.tableToResource(dataset.DatasetID, table.TableID, metadata, location, projectId)
		resources = append(resources, resource)
	}

	return resources
}

func (s *bigQueryService) datasetToResource(datasetId string, metadata *bigquery.DatasetMetadata, projectId string) providers.Resource {
	// Use dataset ID as resource ID (matches GCP Monitoring dataset_id label)
	resourceId := datasetId

	// Store full path for reference
	selfLink := fmt.Sprintf("projects/%s/datasets/%s", projectId, datasetId)

	// Extract tags/labels
	tags := make(map[string][]string)
	if metadata.Labels != nil {
		for key, value := range metadata.Labels {
			tags[key] = []string{value}
		}
	}

	// Datasets are always active
	status := providers.ResourceStatusActive

	// Extract creation timestamp
	createdAt := time.Now()
	if metadata.CreationTime.Unix() > 0 {
		createdAt = metadata.CreationTime
	}

	// Convert metadata to map for Meta field
	meta := structToMap(metadata)
	meta["selfLink"] = selfLink

	return providers.Resource{
		Id:          resourceId, // Dataset ID (matches GCP Monitoring dataset_id)
		Name:        datasetId,
		Type:        "bigquery.googleapis.com/Dataset",
		Arn:         selfLink, // Full path for ARN
		ServiceName: ServiceNameBigQuery,
		Status:      status,
		Region:      metadata.Location,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

func (s *bigQueryService) tableToResource(datasetId, tableId string, metadata *bigquery.TableMetadata, location, projectId string) providers.Resource {
	// Build resource ID
	resourceId := fmt.Sprintf("projects/%s/datasets/%s/tables/%s", projectId, datasetId, tableId)

	// Extract tags/labels
	tags := make(map[string][]string)
	if metadata.Labels != nil {
		for key, value := range metadata.Labels {
			tags[key] = []string{value}
		}
	}

	// Tables are always active
	status := providers.ResourceStatusActive

	// Extract creation timestamp
	createdAt := time.Now()
	if metadata.CreationTime.Unix() > 0 {
		createdAt = metadata.CreationTime
	}

	// Convert metadata to map for Meta field
	meta := structToMap(metadata)

	// Determine table type
	var resourceType string
	switch metadata.Type {
	case bigquery.ViewTable:
		resourceType = "bigquery.googleapis.com/View"
	case bigquery.MaterializedView:
		resourceType = "bigquery.googleapis.com/MaterializedView"
	case bigquery.ExternalTable:
		resourceType = "bigquery.googleapis.com/ExternalTable"
	default:
		resourceType = "bigquery.googleapis.com/Table"
	}

	return providers.Resource{
		Id:          resourceId,
		Name:        tableId,
		Type:        resourceType,
		Arn:         resourceId,
		ServiceName: ServiceNameBigQuery,
		Status:      status,
		Region:      location,
		Tags:        tags,
		Meta:        meta,
		CreatedAt:   createdAt,
	}
}

// BigQuery storage pricing (USD per GB per month)
const (
	bqActiveStoragePricePerGBMonth   = 0.02 // Active storage
	bqLongTermStoragePricePerGBMonth = 0.01 // Long-term storage (>90 days unmodified)
	bqOnDemandQueryPricePerTB        = 6.25 // On-demand query pricing per TB scanned
	bqPartitionSavingsEstimate       = 0.30 // Conservative 30% scan reduction from partitioning
	bqClusteringSavingsEstimate      = 0.20 // Conservative 20% scan reduction from clustering
)

// getTableSizeGB extracts table size from Meta. After structToMap JSON serialization, numBytes is float64.
func getTableSizeGB(meta map[string]any) (float64, bool) {
	numBytes, ok := meta["numBytes"].(float64)
	if !ok || numBytes <= 0 {
		return 0, false
	}
	return numBytes / (1024 * 1024 * 1024), true
}

// hasTimePartitioning checks if table has time partitioning. After structToMap, this is map[string]interface{}.
func hasTimePartitioning(meta map[string]any) bool {
	tp, ok := meta["timePartitioning"].(map[string]interface{})
	return ok && tp != nil
}

// hasRangePartitioning checks if table has range partitioning. After structToMap, this is map[string]interface{}.
func hasRangePartitioning(meta map[string]any) bool {
	rp, ok := meta["rangePartitioning"].(map[string]interface{})
	return ok && rp != nil
}

// hasClustering checks if table has clustering configured. After structToMap, this is map[string]interface{}.
func hasClustering(meta map[string]any) bool {
	cl, ok := meta["clustering"].(map[string]interface{})
	if !ok || cl == nil {
		return false
	}
	fields, ok := cl["fields"].([]interface{})
	return ok && len(fields) > 0
}

// hasExpiration checks if a table has an expiration time set. After structToMap, time.Time becomes RFC3339 string.
func hasExpiration(meta map[string]any) bool {
	exp, ok := meta["expirationTime"].(string)
	if !ok || exp == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil {
		return false
	}
	return !t.IsZero()
}

// hasDefaultTableExpiration checks if a dataset has a default table expiration.
// After structToMap, time.Duration (int64 nanoseconds) becomes float64.
func hasDefaultTableExpiration(meta map[string]any) bool {
	exp, ok := meta["defaultTableExpiration"].(float64)
	return ok && exp > 0
}

// getLastModifiedTime extracts lastModifiedTime from Meta. After structToMap, time.Time becomes RFC3339 string.
func getLastModifiedTime(meta map[string]any) (time.Time, bool) {
	str, ok := meta["lastModifiedTime"].(string)
	if !ok || str == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, str)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// hasCMEK checks if a dataset has customer-managed encryption. After structToMap, this is map[string]interface{}.
func hasCMEK(meta map[string]any) bool {
	enc, ok := meta["defaultEncryptionConfiguration"].(map[string]interface{})
	if !ok || enc == nil {
		return false
	}
	kmsKey, ok := enc["kmsKeyName"].(string)
	return ok && kmsKey != ""
}

func (s *bigQueryService) GetRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) ([]providers.Recommendation, error) {
	recommendations := []providers.Recommendation{}

	// Query INFORMATION_SCHEMA.JOBS to find recently queried tables
	// This provides accurate usage data rather than relying on lastModifiedTime
	// Pass existingResources to avoid re-querying dataset regions (optimization)
	queriedTables, err := s.getQueriedTablesFromJobs(ctx, account, filter, existingResources)
	skipUnusedTableDetection := false
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch queried tables from INFORMATION_SCHEMA.JOBS - skipping unused table detection to avoid false positives",
			"error", err,
			"note", "Without query activity data, we cannot reliably detect unused tables")
		skipUnusedTableDetection = true
		// Initialize empty map to prevent nil pointer dereference
		queriedTables = make(map[string]time.Time)
	}

	for _, resource := range existingResources {
		if resource.ServiceName != ServiceNameBigQuery {
			continue
		}

		// Recommendation 1: Check for datasets without labels
		if resource.Type == "bigquery.googleapis.com/Dataset" && len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_bigquery_dataset_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"dataset_id":   resource.Id,
					"dataset_name": resource.Name,
					"region":       resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 2: Check for tables without labels
		if (resource.Type == "bigquery.googleapis.com/Table" || resource.Type == "bigquery.googleapis.com/View") && len(resource.Tags) == 0 {
			recommendations = append(recommendations, providers.Recommendation{
				CategoryName: providers.RecommendationCategoryConfiguration,
				RuleName:     "gcp_bigquery_table_no_labels",
				Severity:     providers.RecommendationSeverityLow,
				Savings:      0,
				Data: map[string]any{
					"table_id":   resource.Id,
					"table_name": resource.Name,
					"region":     resource.Region,
				},
				Action:              providers.RecommendationActionModify,
				ResourceServiceName: resource.ServiceName,
				ResourceId:          resource.Id,
				ResourceType:        resource.Type,
				ResourceRegion:      resource.Region,
			})
		}

		// Recommendation 3: Check for tables without expiration
		if resource.Type == "bigquery.googleapis.com/Table" {
			if !hasExpiration(resource.Meta) {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_bigquery_table_no_expiration",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"table_id":   resource.Id,
						"table_name": resource.Name,
						"region":     resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 4: Check for dataset without default table expiration
		if resource.Type == "bigquery.googleapis.com/Dataset" {
			if !hasDefaultTableExpiration(resource.Meta) {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategoryConfiguration,
					RuleName:     "gcp_bigquery_dataset_no_default_expiration",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"dataset_id":   resource.Id,
						"dataset_name": resource.Name,
						"region":       resource.Region,
					},
					Action:              providers.RecommendationActionModify,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 5: Check for unused tables based on query activity
		// Tables that haven't been queried in the lookback period are considered unused
		// IMPORTANT: Skip this check if we failed to fetch query activity data to avoid false positives
		if resource.Type == "bigquery.googleapis.com/Table" && !skipUnusedTableDetection {
			// TODO: Make lookback period configurable via environment variable or account settings
			lookbackDays := bigQueryUnusedTableLookbackDays

			// Check if table was queried recently
			tableId := resource.Id
			lastQueried, wasQueried := queriedTables[tableId]

			// Table is unused if:
			// 1. It wasn't found in INFORMATION_SCHEMA.JOBS (never queried in lookback period)
			// 2. OR it was last queried more than lookbackDays ago
			isUnused := !wasQueried || time.Since(lastQueried) > time.Duration(lookbackDays)*24*time.Hour

			if isUnused {
				// Get table size for cost calculation
				sizeGB, hasSizeInfo := getTableSizeGB(resource.Meta)

				// Calculate savings based on actual storage tier
				// BigQuery charges long-term rate ($0.01/GB) for data not modified in 90+ days
				storageRate := bqActiveStoragePricePerGBMonth
				if lastModified, ok := getLastModifiedTime(resource.Meta); ok {
					if time.Since(lastModified) > 90*24*time.Hour {
						storageRate = bqLongTermStoragePricePerGBMonth
					}
				} else if time.Since(resource.CreatedAt) > 90*24*time.Hour {
					// Fallback: if no modification time, use creation date
					storageRate = bqLongTermStoragePricePerGBMonth
				}
				savings := sizeGB * storageRate

				daysSinceQueried := -1
				if wasQueried {
					daysSinceQueried = int(time.Since(lastQueried).Hours() / 24)
				}

				recData := map[string]any{
					"table_id":           resource.Id,
					"table_name":         resource.Name,
					"region":             resource.Region,
					"age_days":           int(time.Since(resource.CreatedAt).Hours() / 24),
					"days_since_queried": daysSinceQueried,
					"lookback_days":      lookbackDays,
					"detection_method":   "query_activity",
					"size_gb":            sizeGB,
					"has_size_info":      hasSizeInfo,
				}

				if wasQueried {
					recData["last_queried"] = lastQueried.Format(time.RFC3339)
				} else {
					recData["last_queried"] = nil
				}

				recommendations = append(recommendations, providers.Recommendation{
					CategoryName:        providers.RecommendationCategoryRightSizing,
					RuleName:            "gcp_bigquery_table_unused",
					Severity:            providers.RecommendationSeverityMedium,
					Savings:             savings,
					Data:                recData,
					Action:              providers.RecommendationActionDelete,
					ResourceServiceName: resource.ServiceName,
					ResourceId:          resource.Id,
					ResourceType:        resource.Type,
					ResourceRegion:      resource.Region,
				})
			}
		}

		// Recommendation 6: Check for tables with large size and no partitioning
		if resource.Type == "bigquery.googleapis.com/Table" {
			if sizeGB, ok := getTableSizeGB(resource.Meta); ok && sizeGB > 10 { // > 10GB
				if !hasTimePartitioning(resource.Meta) && !hasRangePartitioning(resource.Meta) {
					// Estimate savings: partitioning typically reduces full-table scans by ~30%
					// Based on on-demand query pricing ($6.25/TB scanned)
					sizeTB := sizeGB / 1024
					savings := sizeTB * bqOnDemandQueryPricePerTB * bqPartitionSavingsEstimate

					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "gcp_bigquery_table_no_partitioning",
						Severity:     providers.RecommendationSeverityHigh,
						Savings:      savings,
						Data: map[string]any{
							"table_id":   resource.Id,
							"table_name": resource.Name,
							"region":     resource.Region,
							"size_gb":    sizeGB,
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

		// Recommendation 7: Check for tables with no clustering
		if resource.Type == "bigquery.googleapis.com/Table" {
			if sizeGB, ok := getTableSizeGB(resource.Meta); ok && sizeGB > 10 { // > 10GB
				if !hasClustering(resource.Meta) {
					// Estimate savings: clustering typically reduces scanned data by ~20%
					sizeTB := sizeGB / 1024
					savings := sizeTB * bqOnDemandQueryPricePerTB * bqClusteringSavingsEstimate

					recommendations = append(recommendations, providers.Recommendation{
						CategoryName: providers.RecommendationCategoryRightSizing,
						RuleName:     "gcp_bigquery_table_no_clustering",
						Severity:     providers.RecommendationSeverityMedium,
						Savings:      savings,
						Data: map[string]any{
							"table_id":   resource.Id,
							"table_name": resource.Name,
							"region":     resource.Region,
							"size_gb":    sizeGB,
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

		// Recommendation 8: Check for dataset without customer-managed encryption
		if resource.Type == "bigquery.googleapis.com/Dataset" {
			if !hasCMEK(resource.Meta) {
				recommendations = append(recommendations, providers.Recommendation{
					CategoryName: providers.RecommendationCategorySecurity,
					RuleName:     "gcp_bigquery_dataset_no_cmek",
					Severity:     providers.RecommendationSeverityMedium,
					Savings:      0,
					Data: map[string]any{
						"dataset_id":   resource.Id,
						"dataset_name": resource.Name,
						"region":       resource.Region,
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

	return recommendations, nil
}

// getQueriedTablesFromJobs queries INFORMATION_SCHEMA.JOBS to find tables that have been queried
// Returns a map of table IDs to their last queried timestamp
// OPTIMIZATION: Extracts regions from existingResources instead of re-querying BigQuery API
func (s *bigQueryService) getQueriedTablesFromJobs(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, existingResources []providers.Resource) (map[string]time.Time, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := bigquery.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create BigQuery client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close BigQuery client", "error", cerr)
		}
	}()

	// TODO: Make lookback period configurable via environment variable or account settings
	lookbackDays := bigQueryUnusedTableLookbackDays

	queriedTables := make(map[string]time.Time)

	// OPTIMIZATION: Extract unique regions from existing resources instead of re-querying
	// This avoids duplicate API calls since GetResources already fetched all datasets
	regionSet := make(map[string]bool)
	for _, resource := range existingResources {
		if resource.ServiceName == ServiceNameBigQuery && resource.Region != "" {
			regionSet[resource.Region] = true
		}
	}
	regions := make([]string, 0, len(regionSet))
	for region := range regionSet {
		regions = append(regions, region)
	}

	// Query INFORMATION_SCHEMA.JOBS for each region
	for _, region := range regions {
		// Validate region format to prevent SQL injection
		// GCP regions follow pattern: [a-z]+-[a-z]+[0-9]+ (e.g., us-central1, europe-west1)
		// This validation is defensive even though regions come from GCP metadata
		if !isValidGCPRegion(region) {
			ctx.GetLogger().Warn("skipping invalid region format in JOBS query", "region", region)
			continue
		}

		// Build query to find tables referenced in jobs
		// Note: INFORMATION_SCHEMA.JOBS is region-scoped
		query := fmt.Sprintf(`
			SELECT
				referenced_tables.project_id,
				referenced_tables.dataset_id,
				referenced_tables.table_id,
				MAX(creation_time) AS last_queried
			FROM
				`+"`region-%s`.INFORMATION_SCHEMA.JOBS,"+`
				UNNEST(referenced_tables) AS referenced_tables
			WHERE
				creation_time > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL %d DAY)
				AND referenced_tables.table_id IS NOT NULL
			GROUP BY
				referenced_tables.project_id,
				referenced_tables.dataset_id,
				referenced_tables.table_id
		`, region, lookbackDays)

		q := client.Query(query)
		q.Location = region

		it, err := q.Read(ctx.GetContext())
		if err != nil {
			ctx.GetLogger().Warn("failed to query INFORMATION_SCHEMA.JOBS for region", "region", region, "error", err)
			RecordGCPPermissionError(ctx, err)
			continue
		}

		// Process results
		for {
			var row struct {
				ProjectID   string    `bigquery:"project_id"`
				DatasetID   string    `bigquery:"dataset_id"`
				TableID     string    `bigquery:"table_id"`
				LastQueried time.Time `bigquery:"last_queried"`
			}

			err := it.Next(&row)
			if err == iterator.Done {
				break
			}
			if err != nil {
				ctx.GetLogger().Warn("failed to read row from INFORMATION_SCHEMA.JOBS query", "region", region, "error", err)
				break
			}

			// Build full table ID in the same format as resource.Id
			fullTableId := fmt.Sprintf("projects/%s/datasets/%s/tables/%s", row.ProjectID, row.DatasetID, row.TableID)

			// Keep the most recent query time if we've seen this table before
			if existingTime, exists := queriedTables[fullTableId]; !exists || row.LastQueried.After(existingTime) {
				queriedTables[fullTableId] = row.LastQueried
			}
		}

		ctx.GetLogger().Info("queried INFORMATION_SCHEMA.JOBS for region", "region", region, "tables_found", len(queriedTables))
	}

	return queriedTables, nil
}

func (s *bigQueryService) ApplyRecommendation(ctx providers.CloudProviderContext, account providers.Account, recommendation providers.Recommendation) error {
	// Check if this is an alarm/alert policy recommendation
	if _, ok := recommendation.Data["alarm_config"]; ok {
		ctx.GetLogger().Info("gcp: applying alarm recommendation for BigQuery",
			"rule_name", recommendation.RuleName,
			"resource_id", recommendation.ResourceId)
		return CreateGCPAlertPolicyFromRecommendation(ctx, account, recommendation)
	}

	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := bigquery.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return fmt.Errorf("failed to create BigQuery client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close BigQuery client", "error", cerr)
		}
	}()

	switch recommendation.RuleName {
	case "gcp_bigquery_dataset_no_labels", "gcp_bigquery_table_no_labels":
		return fmt.Errorf("automatic label addition not yet implemented - please add labels manually via GCP console or bq CLI")

	case "gcp_bigquery_table_no_expiration":
		return fmt.Errorf("automatic table expiration configuration not yet implemented - please configure manually via GCP console")

	case "gcp_bigquery_dataset_no_default_expiration":
		return fmt.Errorf("automatic dataset default expiration configuration not yet implemented - please configure manually via GCP console")

	case "gcp_bigquery_table_unused":
		// Delete the unused table
		tableId, ok := recommendation.Data["table_id"].(string)
		if !ok || tableId == "" {
			return fmt.Errorf("table_id not found in recommendation data")
		}

		// Parse table ID (format: projects/{project}/datasets/{dataset}/tables/{table})
		// Extract dataset and table names
		return fmt.Errorf("automatic table deletion not yet implemented - please review and delete manually if appropriate")

	case "gcp_bigquery_table_no_partitioning":
		return fmt.Errorf("automatic partitioning configuration requires table recreation and cannot be automatically applied")

	case "gcp_bigquery_table_no_clustering":
		return fmt.Errorf("automatic clustering configuration requires table recreation and cannot be automatically applied")

	case "gcp_bigquery_dataset_no_cmek":
		return fmt.Errorf("automatic CMEK configuration not yet implemented - please configure manually via GCP console")

	default:
		return fmt.Errorf("unknown recommendation rule: %s", recommendation.RuleName)
	}
}

func (s *bigQueryService) ApplyCommand(ctx providers.CloudProviderContext, account providers.Account, command providers.ApplyCommandRequest) (providers.ApplyCommandResponse, error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to get gcloud session: %w", err)
	}

	client, err := bigquery.NewClient(ctx.GetContext(), session.ProjectId, session.Opts...)
	if err != nil {
		return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create BigQuery client: %w", err)
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close BigQuery client", "error", cerr)
		}
	}()

	switch command.Command {
	case "create_dataset":
		datasetId, ok := command.Args["dataset_id"].(string)
		if !ok || datasetId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("dataset_id arg required")
		}
		location, ok := command.Args["location"].(string)
		if !ok || location == "" {
			location = "US" // Default to US if not specified
		}

		dataset := client.Dataset(datasetId)
		meta := &bigquery.DatasetMetadata{
			Location: location,
		}

		if err := dataset.Create(ctx.GetContext(), meta); err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to create dataset: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully created dataset %s in location %s", datasetId, location),
		}, nil

	case "delete_dataset":
		datasetId, ok := command.Args["dataset_id"].(string)
		if !ok || datasetId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("dataset_id arg required")
		}

		dataset := client.Dataset(datasetId)
		if err := dataset.DeleteWithContents(ctx.GetContext()); err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to delete dataset: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted dataset %s", datasetId),
		}, nil

	case "delete_table":
		datasetId, ok := command.Args["dataset_id"].(string)
		if !ok || datasetId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("dataset_id arg required")
		}
		tableId, ok := command.Args["table_id"].(string)
		if !ok || tableId == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("table_id arg required")
		}

		table := client.Dataset(datasetId).Table(tableId)
		if err := table.Delete(ctx.GetContext()); err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to delete table: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully deleted table %s.%s", datasetId, tableId),
		}, nil

	case "query":
		sql, ok := command.Args["sql"].(string)
		if !ok || sql == "" {
			return providers.ApplyCommandResponse{}, fmt.Errorf("sql arg required")
		}

		q := client.Query(sql)
		job, err := q.Run(ctx.GetContext())
		if err != nil {
			return providers.ApplyCommandResponse{}, fmt.Errorf("failed to run query: %w", err)
		}

		return providers.ApplyCommandResponse{
			Success: true,
			Message: fmt.Sprintf("Successfully submitted query (job ID: %s)", job.ID()),
		}, nil

	default:
		return providers.ApplyCommandResponse{}, fmt.Errorf("unsupported command: %s", command.Command)
	}
}

func (s *bigQueryService) GetLogFilter(_ providers.CloudProviderContext, _ providers.Account, _ string) string {
	return ""
}

// isValidGCPRegion validates that a region string matches expected GCP region format.
// GCP regions follow pattern: [a-z]+-[a-z]+[0-9]+ (e.g., us-central1, europe-west1)
// This prevents SQL injection when region is interpolated into INFORMATION_SCHEMA queries.
func isValidGCPRegion(region string) bool {
	if region == "" || len(region) > 50 {
		return false
	}
	// Check each character is alphanumeric or hyphen
	for _, ch := range region {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return false
		}
	}
	// Ensure it doesn't start or end with hyphen
	if region[0] == '-' || region[len(region)-1] == '-' {
		return false
	}
	return true
}
