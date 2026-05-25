package azure

import (
	"nudgebee/collector/cloud/providers"
	"strings"
	"time"
)

// QueryDatabasePerformance implements the generic database performance insights interface for Azure SQL Database
// Phase 1: Uses Azure Monitor metrics only (resource-level metrics)
// Phase 2 (Future): Add Query Store support for query-level statistics
func (p *azureProvider) QueryDatabasePerformance(ctx providers.CloudProviderContext, account providers.Account, request providers.DatabasePerformanceRequest) (providers.DatabasePerformanceResponse, error) {
	// Set defaults
	request.SetDefaults()

	// Validate request
	if err := request.Validate(); err != nil {
		return providers.DatabasePerformanceResponse{}, err
	}

	// Azure Monitor requires a full resource ID path (/subscriptions/...)
	if !strings.HasPrefix(request.DatabaseIdentifier, "/subscriptions/") {
		ctx.GetLogger().Warn("Azure database identifier does not look like a full resource ID, metrics query will likely fail",
			"identifier", request.DatabaseIdentifier)
	}

	// Initialize response
	response := providers.NewDatabasePerformanceResponse("azure", request.DatabaseIdentifier, true)

	// Fetch load metrics (CPU and DTU as proxies for database load)
	loadMetrics, err := fetchAzureLoadMetrics(ctx, account, request.DatabaseIdentifier, request.Region, *request.StartTime, *request.EndTime, request.GranularitySeconds)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch load metrics", "error", err, "instance", request.DatabaseIdentifier)
	} else {
		response.LoadMetrics = loadMetrics
	}

	// Fetch resource metrics (CPU, memory, storage, connections)
	resourceMetrics, err := fetchAzureResourceMetrics(ctx, account, request.DatabaseIdentifier, request.Region, *request.StartTime, *request.EndTime, request.GranularitySeconds)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch resource metrics", "error", err, "instance", request.DatabaseIdentifier)
	} else {
		response.ResourceMetrics = resourceMetrics
	}

	// Top queries and wait events not available in Phase 1 (Azure Monitor only)
	// These would require Query Store SQL connection access
	if request.IncludeTopQueries {
		ctx.GetLogger().Debug("Query-level statistics require Query Store SQL connection (not implemented in Phase 1)", "instance", request.DatabaseIdentifier)
		response.TopQueries = []providers.DatabaseQuery{} // Empty for Phase 1
	}

	if request.IncludeWaitEvents {
		ctx.GetLogger().Debug("Wait events require Query Store SQL connection (not implemented in Phase 1)", "instance", request.DatabaseIdentifier)
		response.WaitEvents = []providers.DatabaseWaitEvent{} // Empty for Phase 1
	}

	// Add metadata
	response.Metadata["provider_name"] = "Azure SQL Database"
	response.Metadata["limitations"] = []string{
		"Query-level statistics require Query Store SQL connection (Phase 2)",
		"Wait events not available via Azure Monitor API (Phase 2)",
		"Resource metrics only for Phase 1",
	}
	response.Metadata["phase"] = "Phase 1 - Azure Monitor Only"
	response.Metadata["query_store_note"] = "Enable Query Store and use direct SQL connection for full query insights"

	// Determine available features
	features := []string{"resource_metrics"}
	if len(response.LoadMetrics) > 0 {
		features = append(features, "cpu_dtu_load_proxy")
	}
	response.Metadata["features_available"] = features

	return response, nil
}

// fetchAzureLoadMetrics fetches CPU and DTU consumption as proxies for database load
func fetchAzureLoadMetrics(ctx providers.CloudProviderContext, account providers.Account, instanceName, region string, startTime, endTime time.Time, granularitySeconds int32) ([]providers.TimeSeriesMetric, error) {
	// For Azure SQL Database, we use CPU percentage and DTU consumption as load metrics
	metricNames := []string{
		"cpu_percent",
		"dtu_consumption_percent", // For DTU-based databases
	}

	metricsRequest := providers.QueryMetricsRequest{
		StartDate:   &startTime,
		EndDate:     &endTime,
		ResourceIds: []string{instanceName},
		ServiceName: "Microsoft.Sql/servers/databases", // Azure SQL Database resource type
		Region:      region,
		MetricNames: metricNames,
		Statistics:  []string{"Average"},
		Step:        time.Duration(granularitySeconds) * time.Second,
	}

	response, err := getAzureMonitorMetrics(ctx, account, metricsRequest)
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

		// Determine the metric name and unit
		name := item.Name
		unit := "percent"

		loadMetrics = append(loadMetrics, providers.TimeSeriesMetric{
			Name:       name,
			Unit:       unit,
			Timestamps: timestamps,
			Values:     item.Values,
		})
	}

	return loadMetrics, nil
}

// fetchAzureResourceMetrics fetches resource utilization metrics from Azure Monitor
func fetchAzureResourceMetrics(ctx providers.CloudProviderContext, account providers.Account, instanceName, region string, startTime, endTime time.Time, granularitySeconds int32) ([]providers.TimeSeriesMetric, error) {
	// Comprehensive list of Azure SQL Database metrics
	metricNames := []string{
		"cpu_percent",
		"physical_data_read_percent", // IO percentage
		"log_write_percent",
		"dtu_consumption_percent",
		"storage_percent",
		"connection_successful",
		"connection_failed",
		"blocked_by_firewall",
		"deadlock",
		"sessions_percent",
		"workers_percent",
	}

	metricsRequest := providers.QueryMetricsRequest{
		StartDate:   &startTime,
		EndDate:     &endTime,
		ResourceIds: []string{instanceName},
		ServiceName: "Microsoft.Sql/servers/databases",
		Region:      region,
		MetricNames: metricNames,
		Statistics:  []string{"Average", "Maximum"},
		Step:        time.Duration(granularitySeconds) * time.Second,
	}

	response, err := getAzureMonitorMetrics(ctx, account, metricsRequest)
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
		unit := "percent"
		if item.Name == "connection_successful" || item.Name == "connection_failed" ||
			item.Name == "blocked_by_firewall" || item.Name == "deadlock" {
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
