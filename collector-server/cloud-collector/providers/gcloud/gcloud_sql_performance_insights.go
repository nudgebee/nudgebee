package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"sync"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// QueryDatabasePerformance implements the generic database performance insights interface for GCP Cloud SQL
// Uses Cloud Monitoring API for metrics and Query Insights (pg_stat_statements/Performance Schema)
func (p *gcloudProvider) QueryDatabasePerformance(ctx providers.CloudProviderContext, account providers.Account, request providers.DatabasePerformanceRequest) (providers.DatabasePerformanceResponse, error) {
	// Set defaults
	request.SetDefaults()

	// Validate request
	if err := request.Validate(); err != nil {
		return providers.DatabasePerformanceResponse{}, err
	}

	// Initialize response
	response := providers.NewDatabasePerformanceResponse("gcp", request.DatabaseIdentifier, true)

	// Get GCloud session
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		ctx.GetLogger().Error("failed to get gcloud session", "error", err, "accountNumber", account.AccountNumber)
		return providers.DatabasePerformanceResponse{}, err
	}

	// Fetch all data in parallel to avoid timeout
	var (
		wg                   sync.WaitGroup
		queryInsightsEnabled bool
		databaseVersion      string
		loadMetrics          []providers.TimeSeriesMetric
		resourceMetrics      []providers.TimeSeriesMetric
		topQueries           []providers.DatabaseQuery
		waitEvents           []providers.DatabaseWaitEvent
	)

	// 1. Check Query Insights status
	wg.Add(1)
	go func() {
		defer wg.Done()
		enabled, version, err := checkQueryInsightsEnabled(ctx, session, request.DatabaseIdentifier)
		if err != nil {
			ctx.GetLogger().Warn("failed to check Query Insights status", "error", err, "instance", request.DatabaseIdentifier)
			return
		}
		queryInsightsEnabled = enabled
		databaseVersion = version
	}()

	// 2. Fetch load metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		metrics, err := fetchGCPLoadMetrics(ctx, session, account, request.DatabaseIdentifier, request.Region, *request.StartTime, *request.EndTime, request.GranularitySeconds)
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch load metrics", "error", err, "instance", request.DatabaseIdentifier)
			return
		}
		loadMetrics = metrics
	}()

	// 3. Fetch resource metrics
	wg.Add(1)
	go func() {
		defer wg.Done()
		metrics, err := fetchGCPResourceMetrics(ctx, session, account, request.DatabaseIdentifier, request.Region, *request.StartTime, *request.EndTime, request.GranularitySeconds)
		if err != nil {
			ctx.GetLogger().Warn("failed to fetch resource metrics", "error", err, "instance", request.DatabaseIdentifier)
			return
		}
		resourceMetrics = metrics
	}()

	// 4. Fetch Query Insights (top queries + wait events)
	if request.IncludeTopQueries || request.IncludeWaitEvents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Note: this runs without waiting for queryInsightsEnabled check.
			// If insights aren't enabled, the API will return empty results which is fine.
			tq, we, err := fetchQueryInsightsMetrics(ctx, session, account, request.DatabaseIdentifier, request.Region, *request.StartTime, *request.EndTime, request.GranularitySeconds, "", request.TopN)
			if err != nil {
				ctx.GetLogger().Warn("failed to fetch Query Insights metrics", "error", err, "instance", request.DatabaseIdentifier)
				return
			}
			topQueries = tq
			waitEvents = we
		}()
	}

	wg.Wait()

	// Assign results
	response.Metadata["database_version"] = databaseVersion
	response.Metadata["query_insights_enabled"] = queryInsightsEnabled

	if loadMetrics != nil {
		response.LoadMetrics = loadMetrics
	}
	if resourceMetrics != nil {
		response.ResourceMetrics = resourceMetrics
	}
	if request.IncludeTopQueries && topQueries != nil {
		response.TopQueries = topQueries
	}
	if request.IncludeWaitEvents && waitEvents != nil {
		response.WaitEvents = waitEvents
	}
	if !queryInsightsEnabled && (request.IncludeTopQueries || request.IncludeWaitEvents) {
		ctx.GetLogger().Info("Query Insights not enabled on instance", "instance", request.DatabaseIdentifier)
		response.Metadata["query_insights_note"] = "Query Insights is not enabled. Enable it in Cloud SQL settings to access query-level performance data."
	}

	// Add metadata
	response.Metadata["provider_name"] = "GCP Cloud SQL"
	response.Metadata["limitations"] = []string{}

	// Determine available features based on what we successfully fetched
	features := []string{}
	if len(response.LoadMetrics) > 0 {
		features = append(features, "cpu_load_proxy")
	}
	if len(response.ResourceMetrics) > 0 {
		features = append(features, "resource_metrics")
	}
	if len(response.TopQueries) > 0 {
		features = append(features, "top_queries")
	}
	if len(response.WaitEvents) > 0 {
		features = append(features, "limited_wait_events")
	}
	response.Metadata["features_available"] = features

	return response, nil
}

// checkQueryInsightsEnabled checks if Query Insights is enabled for the Cloud SQL instance
func checkQueryInsightsEnabled(ctx providers.CloudProviderContext, session gcloudAuthSession, instanceName string) (bool, string, error) {
	service, err := sqladmin.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return false, "", fmt.Errorf("failed to create Cloud SQL admin service: %w", err)
	}

	instance, err := service.Instances.Get(session.ProjectId, instanceName).Context(ctx.GetContext()).Do()
	if err != nil {
		return false, "", fmt.Errorf("failed to get Cloud SQL instance: %w", err)
	}

	// Determine database engine type
	databaseVersion := ""
	if instance.DatabaseVersion != "" {
		databaseVersion = instance.DatabaseVersion
	}

	// Check if Query Insights is enabled
	queryInsightsEnabled := false
	if instance.Settings != nil && instance.Settings.InsightsConfig != nil {
		queryInsightsEnabled = instance.Settings.InsightsConfig.QueryInsightsEnabled
	}

	return queryInsightsEnabled, databaseVersion, nil
}

// fetchGCPLoadMetrics fetches CPU utilization as a proxy for database load
func fetchGCPLoadMetrics(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName, region string, startTime, endTime time.Time, granularitySeconds int32) ([]providers.TimeSeriesMetric, error) {
	// Use Cloud Monitoring to fetch CPU utilization metric
	// GCP Cloud SQL requires database_id in format: {project}:{instance}
	databaseID := fmt.Sprintf("%s:%s", session.ProjectId, instanceName)

	metricsRequest := providers.QueryMetricsRequest{
		StartDate:   &startTime,
		EndDate:     &endTime,
		ResourceIds: []string{databaseID},
		ServiceName: ServiceNameSQL,
		Region:      region,
		MetricNames: []string{"cpu/utilization"},
		Statistics:  []string{"mean"},
		Step:        time.Duration(granularitySeconds) * time.Second,
	}

	response, err := getGcloudMonitoringMetrics(ctx, account, metricsRequest)
	if err != nil {
		return nil, err
	}

	// Convert to TimeSeriesMetric format
	loadMetrics := []providers.TimeSeriesMetric{}
	for _, item := range response.Items {
		timestamps := make([]int64, len(item.Timestamps))
		for i, ts := range item.Timestamps {
			timestamps[i] = ts.UnixMilli()
		}

		loadMetrics = append(loadMetrics, providers.TimeSeriesMetric{
			Name:       "cpu_utilization", // GCP uses CPU as load proxy
			Unit:       "percent",
			Timestamps: timestamps,
			Values:     item.Values,
		})
	}

	return loadMetrics, nil
}

// fetchGCPResourceMetrics fetches resource utilization metrics from Cloud Monitoring
func fetchGCPResourceMetrics(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName, region string, startTime, endTime time.Time, granularitySeconds int32) ([]providers.TimeSeriesMetric, error) {
	// Fetch multiple resource metrics
	// Note: getMetricTypePrefix already returns "cloudsql.googleapis.com/database"
	// so we just need the metric suffix without "database/" prefix
	// GCP Cloud SQL requires database_id in format: {project}:{instance}
	databaseID := fmt.Sprintf("%s:%s", session.ProjectId, instanceName)

	metricNames := []string{
		"cpu/utilization",
		"memory/utilization",
		"disk/bytes_used",
		"network/connections",
	}

	metricsRequest := providers.QueryMetricsRequest{
		StartDate:   &startTime,
		EndDate:     &endTime,
		ResourceIds: []string{databaseID},
		ServiceName: ServiceNameSQL,
		Region:      region,
		MetricNames: metricNames,
		Statistics:  []string{"mean"},
		Step:        time.Duration(granularitySeconds) * time.Second,
	}

	response, err := getGcloudMonitoringMetrics(ctx, account, metricsRequest)
	if err != nil {
		return nil, err
	}

	// Convert to TimeSeriesMetric format
	resourceMetrics := []providers.TimeSeriesMetric{}
	for _, item := range response.Items {
		timestamps := make([]int64, len(item.Timestamps))
		for i, ts := range item.Timestamps {
			timestamps[i] = ts.UnixMilli()
		}

		// Determine unit based on metric name
		var unit string
		switch item.Name {
		case "cpu/utilization", "memory/utilization":
			unit = "percent"
		case "disk/bytes_used":
			unit = "bytes"
		default:
			unit = "count"
		}

		resourceMetrics = append(resourceMetrics, providers.TimeSeriesMetric{
			Name:       item.Name,
			Unit:       unit,
			Timestamps: timestamps,
			Values:     item.Values,
		})
	}

	return resourceMetrics, nil
}

// fetchGCPTopQueries fetches top SQL queries from Cloud Monitoring Query Insights
func fetchGCPTopQueries(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName, region string, startTime, endTime time.Time, topN int) ([]providers.DatabaseQuery, error) {
	// GCP Query Insights uses per-query metrics with labels:
	// - query_hash: unique identifier for the query
	// - querystring: the actual SQL text
	// We need to query perquery metrics, not aggregate metrics

	// Try PostgreSQL first
	queries, err := fetchGCPPostgreSQLTopQueries(ctx, session, account, instanceName, startTime, endTime, topN)
	if err != nil {
		ctx.GetLogger().Debug("PostgreSQL query insights not available, trying MySQL", "error", err)
		// Try MySQL metrics
		return fetchGCPMySQLTopQueries(ctx, session, account, instanceName, region, startTime, endTime, topN)
	}

	return queries, nil
}

// fetchGCPPostgreSQLTopQueries fetches top queries for PostgreSQL instances using perquery metrics
func fetchGCPPostgreSQLTopQueries(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName string, startTime, endTime time.Time, topN int) ([]providers.DatabaseQuery, error) {
	// Use Cloud Monitoring client directly to get per-query metrics with labels
	monitoring := getGcloudMonitoringClient(ctx, session)
	if monitoring == nil {
		return nil, fmt.Errorf("failed to create monitoring client")
	}
	defer func() {
		if cerr := monitoring.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close monitoring client", "error", cerr)
		}
	}()

	databaseID := fmt.Sprintf("%s:%s", session.ProjectId, instanceName)

	// Fetch execution time metrics (CUMULATIVE INT64 in microseconds)
	// Using perquery metrics which include query_hash and querystring labels
	executionTimeMetrics, err := fetchPerQueryMetrics(ctx, monitoring, session.ProjectId, databaseID,
		"cloudsql.googleapis.com/database/postgresql/insights/perquery/execution_time",
		startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch execution time metrics: %w", err)
	}

	// Fetch latency distribution metrics to get execution count and avg latency
	latencyMetrics, err := fetchPerQueryMetrics(ctx, monitoring, session.ProjectId, databaseID,
		"cloudsql.googleapis.com/database/postgresql/insights/perquery/latencies",
		startTime, endTime)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch latency metrics", "error", err)
		// Continue without latency data
	}

	// Build query map from execution time metrics
	queryMap := make(map[string]*providers.DatabaseQuery)
	for _, metric := range executionTimeMetrics {
		queryHash, ok := metric.Labels["query_hash"]
		if !ok {
			continue
		}

		// Calculate total execution time from CUMULATIVE metric
		// Use ALIGN_DELTA: last value - first value
		totalExecTime := calculateDeltaFromPoints(metric.Points)

		query := &providers.DatabaseQuery{
			QueryID:       queryHash,
			QueryText:     metric.Labels["querystring"],
			DatabaseLoad:  totalExecTime / 1000000.0, // Convert microseconds to seconds (matches GCP Console "Load by total time")
			TotalDuration: totalExecTime / 1000.0,    // Convert to milliseconds
		}

		queryMap[queryHash] = query
	}

	// Enrich with latency data (execution count and avg latency)
	for _, metric := range latencyMetrics {
		queryHash, ok := metric.Labels["query_hash"]
		if !ok {
			continue
		}

		query, exists := queryMap[queryHash]
		if !exists {
			continue
		}

		// Extract count and mean from distribution
		if len(metric.Points) > 0 {
			lastPoint := metric.Points[len(metric.Points)-1]
			if dist := lastPoint.Distribution; dist != nil {
				query.ExecutionCount = dist.Count
				if query.ExecutionCount > 0 {
					avgDuration := dist.Mean / 1000.0 // Convert to milliseconds
					query.AvgDuration = avgDuration
				}
			}
		}
	}

	// Convert map to slice and sort by database load
	queries := make([]providers.DatabaseQuery, 0, len(queryMap))
	for _, q := range queryMap {
		queries = append(queries, *q)
	}

	// Sort by database load (total execution time) descending
	sortQueriesByLoad(queries)

	// Return top N
	if len(queries) > topN {
		queries = queries[:topN]
	}

	return queries, nil
}

// fetchGCPMySQLTopQueries fetches top queries for MySQL instances
func fetchGCPMySQLTopQueries(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName, region string, startTime, endTime time.Time, topN int) ([]providers.DatabaseQuery, error) {
	// Use Cloud Monitoring client directly to get per-query metrics with labels
	monitoring := getGcloudMonitoringClient(ctx, session)
	if monitoring == nil {
		return nil, fmt.Errorf("failed to create monitoring client")
	}
	defer func() {
		if cerr := monitoring.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close monitoring client", "error", cerr)
		}
	}()

	databaseID := fmt.Sprintf("%s:%s", session.ProjectId, instanceName)

	// Fetch MySQL Query Insights metrics with labels
	// MySQL uses "mysql/queries" metric with labels
	executionTimeMetrics, err := fetchPerQueryMetrics(ctx, monitoring, session.ProjectId, databaseID,
		"cloudsql.googleapis.com/database/mysql/queries",
		startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("MySQL query insights not available: %w", err)
	}

	// Build query map from MySQL metrics
	queryMap := make(map[string]*providers.DatabaseQuery)

	for _, metric := range executionTimeMetrics {
		// MySQL uses different label names than PostgreSQL
		queryText := ""
		queryHash := ""

		// Try to get query text from various possible labels
		if text, ok := metric.Labels["query"]; ok {
			queryText = text
			queryHash = fmt.Sprintf("0x%X", hashString(text))
		} else if digest, ok := metric.Labels["query_digest"]; ok {
			queryHash = digest
			if text, ok := metric.Labels["query_sample"]; ok {
				queryText = text
			} else {
				queryText = digest // Use digest as fallback
			}
		} else if text, ok := metric.Labels["normalized_query"]; ok {
			queryText = text
			queryHash = fmt.Sprintf("0x%X", hashString(text))
		} else {
			// Skip metrics without query information
			continue
		}

		// Calculate execution metrics
		totalExecTime := 0.0
		execCount := int64(0)

		// For CUMULATIVE metrics, calculate delta
		if len(metric.Points) > 1 {
			// For cumulative, take the difference between last and first points
			totalExecTime = calculateDeltaFromPoints(metric.Points)
			// Estimate execution count from number of data points
			execCount = int64(len(metric.Points))
		} else if len(metric.Points) == 1 {
			// For single point, use the value directly
			totalExecTime = getValueFromPoint(metric.Points[0])
			execCount = 1
		}

		// Create or update query entry
		if existing, ok := queryMap[queryHash]; ok {
			// Aggregate if we've seen this query before
			existing.TotalDuration += totalExecTime / 1000.0   // Convert to milliseconds
			existing.DatabaseLoad += totalExecTime / 1000000.0 // Convert to seconds
			existing.ExecutionCount += execCount
		} else {
			queryMap[queryHash] = &providers.DatabaseQuery{
				QueryID:        queryHash,
				QueryText:      truncateSQLText(queryText, 500),
				DatabaseLoad:   totalExecTime / 1000000.0, // Convert to seconds
				TotalDuration:  totalExecTime / 1000.0,    // Convert to milliseconds
				ExecutionCount: execCount,
			}
		}
	}

	// Calculate average duration
	for _, query := range queryMap {
		if query.ExecutionCount > 0 {
			query.AvgDuration = query.TotalDuration / float64(query.ExecutionCount)
		}
	}

	// Convert map to slice and sort by database load
	queries := make([]providers.DatabaseQuery, 0, len(queryMap))
	for _, q := range queryMap {
		queries = append(queries, *q)
	}

	// Sort by database load descending
	sortQueriesByLoad(queries)

	// Return top N
	if len(queries) > topN {
		queries = queries[:topN]
	}

	// Log success
	ctx.GetLogger().Info("Successfully fetched MySQL top queries",
		"instance", instanceName,
		"queriesFound", len(queries))

	return queries, nil
}

// fetchGCPWaitEvents fetches wait event data (limited for GCP)
func fetchGCPWaitEvents(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName, region string, startTime, endTime time.Time, topN int) ([]providers.DatabaseWaitEvent, error) {
	// GCP has limited wait event data compared to AWS
	// We can derive some wait categories from:
	// - IO time (from query insights)
	// - Lock time (from query insights)

	waitEvents := []providers.DatabaseWaitEvent{}

	// GCP Cloud SQL requires database_id in format: {project}:{instance}
	databaseID := fmt.Sprintf("%s:%s", session.ProjectId, instanceName)

	// For PostgreSQL, we can get io_time and lock_time
	metricsRequest := providers.QueryMetricsRequest{
		StartDate:    &startTime,
		EndDate:      &endTime,
		ResourceIds:  []string{databaseID},
		ServiceName:  ServiceNameSQL,
		ResourceType: "cloudsql_instance_database", // Signals to use resource_id instead of database_id
		Region:       "",                           // Empty region - Query Insights metrics don't support region filtering
		MetricNames: []string{
			"postgresql/insights/aggregate/io_time",
			"postgresql/insights/aggregate/lock_time",
		},
		Statistics: []string{"delta"}, // CUMULATIVE metrics need ALIGN_DELTA, not ALIGN_SUM
		Step:       time.Duration(endTime.Sub(startTime)),
	}

	response, err := getGcloudMonitoringMetrics(ctx, account, metricsRequest)
	if err != nil {
		ctx.GetLogger().Debug("PostgreSQL wait event metrics not available", "error", err)
		return waitEvents, nil // Return empty, not an error
	}

	// Calculate total for percentage
	totalWaitTime := 0.0
	waitTimeMap := make(map[string]float64)

	for _, item := range response.Items {
		if len(item.Values) > 0 {
			// Sum all values
			for _, val := range item.Values {
				totalWaitTime += val
				waitTimeMap[item.Name] += val
			}
		}
	}

	// Convert to wait events
	for metricName, waitTime := range waitTimeMap {
		eventType := "IO"
		eventName := "IO Time"
		if metricName == "postgresql/insights/aggregate/lock_time" {
			eventType = "Lock"
			eventName = "Lock Time"
		}

		percentage := 0.0
		if totalWaitTime > 0 {
			percentage = (waitTime / totalWaitTime) * 100
		}

		waitEvents = append(waitEvents, providers.DatabaseWaitEvent{
			EventType:    eventType,
			EventName:    eventName,
			DatabaseLoad: waitTime / 1000000.0, // Convert microseconds to seconds
			Percentage:   percentage,
		})
	}

	return waitEvents, nil
}

// fetchQueryInsightsMetrics fetches both top queries and wait events from Query Insights
func fetchQueryInsightsMetrics(ctx providers.CloudProviderContext, session gcloudAuthSession, account providers.Account, instanceName, region string, startTime, endTime time.Time, granularitySeconds int32, databaseVersion string, topN int) ([]providers.DatabaseQuery, []providers.DatabaseWaitEvent, error) {
	// Fetch top queries
	topQueries, err := fetchGCPTopQueries(ctx, session, account, instanceName, region, startTime, endTime, topN)
	if err != nil {
		ctx.GetLogger().Debug("failed to fetch top queries", "error", err, "instance", instanceName)
		// Continue to try wait events
	}

	// Fetch wait events
	waitEvents, err := fetchGCPWaitEvents(ctx, session, account, instanceName, region, startTime, endTime, topN)
	if err != nil {
		ctx.GetLogger().Debug("failed to fetch wait events", "error", err, "instance", instanceName)
		// Continue with partial results
	}

	return topQueries, waitEvents, nil
}

// perQueryMetric represents a single metric with labels and points
type perQueryMetric struct {
	Labels map[string]string
	Points []metricPoint
}

// metricPoint represents a single data point
type metricPoint struct {
	Timestamp    time.Time
	Int64Value   int64
	DoubleValue  float64
	Distribution *distributionValue
}

// distributionValue represents a distribution metric value
type distributionValue struct {
	Count int64
	Mean  float64
}

// getGcloudMonitoringClient creates a Cloud Monitoring client
func getGcloudMonitoringClient(ctx providers.CloudProviderContext, session gcloudAuthSession) *monitoring.MetricClient {
	client, err := monitoring.NewMetricClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		ctx.GetLogger().Error("failed to create monitoring client", "error", err)
		return nil
	}
	return client
}

// fetchPerQueryMetrics fetches per-query metrics from Cloud Monitoring with labels
func fetchPerQueryMetrics(ctx providers.CloudProviderContext, client *monitoring.MetricClient, projectID, databaseID, metricType string, startTime, endTime time.Time) ([]perQueryMetric, error) {
	// Cap time range to 24 hours to avoid slow queries on large time ranges
	maxDuration := 24 * time.Hour
	if endTime.Sub(startTime) > maxDuration {
		startTime = endTime.Add(-maxDuration)
	}

	// Build filter for the metric type and resource
	filter := fmt.Sprintf("metric.type = \"%s\" AND resource.labels.resource_id = \"%s\"", metricType, databaseID)

	// Create the request
	req := &monitoringpb.ListTimeSeriesRequest{
		Name:   fmt.Sprintf("projects/%s", projectID),
		Filter: filter,
		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		},
	}

	// Execute the query
	var metrics []perQueryMetric
	it := client.ListTimeSeries(ctx.GetContext(), req)
	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate time series: %w", err)
		}

		// Extract metric labels (including query_hash and querystring)
		labels := make(map[string]string)
		for key, value := range resp.Metric.Labels {
			labels[key] = value
		}

		// Extract data points
		var points []metricPoint
		for _, point := range resp.Points {
			mp := metricPoint{
				Timestamp: point.Interval.EndTime.AsTime(),
			}

			// Extract value based on type
			switch v := point.Value.Value.(type) {
			case *monitoringpb.TypedValue_Int64Value:
				mp.Int64Value = v.Int64Value
			case *monitoringpb.TypedValue_DoubleValue:
				mp.DoubleValue = v.DoubleValue
			case *monitoringpb.TypedValue_DistributionValue:
				mp.Distribution = &distributionValue{
					Count: v.DistributionValue.Count,
					Mean:  v.DistributionValue.Mean,
				}
			}

			points = append(points, mp)
		}

		metrics = append(metrics, perQueryMetric{
			Labels: labels,
			Points: points,
		})
	}

	return metrics, nil
}

// calculateDeltaFromPoints calculates the delta (last - first) from CUMULATIVE metric points
func calculateDeltaFromPoints(points []metricPoint) float64 {
	if len(points) == 0 {
		return 0
	}

	// For CUMULATIVE metrics, calculate delta as last value - first value
	// Points are already sorted by timestamp
	firstValue := float64(points[0].Int64Value)
	lastValue := float64(points[len(points)-1].Int64Value)

	delta := lastValue - firstValue
	if delta < 0 {
		// If counter reset, just use last value
		return lastValue
	}
	return delta
}

// sortQueriesByLoad sorts queries by database load (total execution time) in descending order
func sortQueriesByLoad(queries []providers.DatabaseQuery) {
	// Simple bubble sort - good enough for small lists (topN is typically 10-50)
	n := len(queries)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if queries[j].DatabaseLoad < queries[j+1].DatabaseLoad {
				queries[j], queries[j+1] = queries[j+1], queries[j]
			}
		}
	}
}

// hashString generates a simple hash for a string (for query ID generation)
func hashString(s string) uint32 {
	hash := uint32(0)
	for i := 0; i < len(s); i++ {
		hash = hash*31 + uint32(s[i])
	}
	return hash
}

// truncateSQLText truncates SQL text to a specified length
func truncateSQLText(sql string, maxLen int) string {
	if len(sql) <= maxLen {
		return sql
	}
	return sql[:maxLen] + "..."
}

// getValueFromPoint extracts value from a metric point
func getValueFromPoint(point metricPoint) float64 {
	// Try double value first
	if point.DoubleValue != 0 {
		return point.DoubleValue
	}
	// Fall back to int64 value
	return float64(point.Int64Value)
}
