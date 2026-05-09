package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/pi"
	"github.com/aws/aws-sdk-go-v2/service/pi/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// PerformanceInsightsRequest represents the input for fetching PI metrics
type PerformanceInsightsRequest struct {
	DBInstanceIdentifier string     `json:"db_instance_identifier"`
	Region               string     `json:"region"`
	StartTime            *time.Time `json:"start_time"`
	EndTime              *time.Time `json:"end_time"`
	PeriodInSeconds      *int32     `json:"period_in_seconds,omitempty"` // Optional: 60, 300, 3600, 86400; defaults to 300
}

// PerformanceInsightsResponse represents the structured PI metrics output
type PerformanceInsightsResponse struct {
	DBInstanceIdentifier       string                      `json:"db_instance_identifier"`
	PerformanceInsightsEnabled bool                        `json:"performance_insights_enabled"`
	Metrics                    []PerformanceInsightsMetric `json:"metrics"`
	TopSQL                     []TopSQLQuery               `json:"top_sql,omitempty"`
	WaitEvents                 []WaitEvent                 `json:"wait_events,omitempty"`
}

// PerformanceInsightsMetric represents a time-series metric
type PerformanceInsightsMetric struct {
	Name       string    `json:"name"`
	Timestamps []int64   `json:"timestamps"`
	Values     []float64 `json:"values"`
	Unit       string    `json:"unit"`
}

// TopSQLQuery represents a top SQL query by DB load
type TopSQLQuery struct {
	SQLText     string  `json:"sql_text"`
	DBLoad      float64 `json:"db_load"`
	CallsPerSec float64 `json:"calls_per_sec,omitempty"`
	AvgLatency  float64 `json:"avg_latency_ms,omitempty"`
	RowsPerCall float64 `json:"rows_per_call,omitempty"`
}

// WaitEvent represents a wait event breakdown
type WaitEvent struct {
	EventType  string  `json:"event_type"`
	DBLoad     float64 `json:"db_load"`
	Percentage float64 `json:"percentage"`
}

// checkPerformanceInsightsEnabled checks if PI is enabled for the RDS instance
func checkPerformanceInsightsEnabled(ctx context.Context, cfg aws.Config, dbInstanceIdentifier string) (bool, string, error) {
	svc := rds.NewFromConfig(cfg)

	result, err := svc.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbInstanceIdentifier),
	})

	if err != nil {
		return false, "", fmt.Errorf("failed to describe DB instance: %w", err)
	}

	if len(result.DBInstances) == 0 {
		return false, "", fmt.Errorf("DB instance %s not found", dbInstanceIdentifier)
	}

	instance := result.DBInstances[0]
	enabled := instance.PerformanceInsightsEnabled != nil && *instance.PerformanceInsightsEnabled

	resourceID := ""
	if instance.DbiResourceId != nil {
		resourceID = *instance.DbiResourceId
	}

	return enabled, resourceID, nil
}

// GetPerformanceInsightsMetrics fetches Performance Insights metrics for an RDS instance
func GetPerformanceInsightsMetrics(ctx providers.CloudProviderContext, account providers.Account, request PerformanceInsightsRequest) (PerformanceInsightsResponse, error) {
	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return PerformanceInsightsResponse{}, err
	}

	// Check if Performance Insights is enabled
	enabled, resourceID, err := checkPerformanceInsightsEnabled(ctx.GetContext(), cfg, request.DBInstanceIdentifier)
	if err != nil {
		ctx.GetLogger().Error("failed to check PI status", "error", err, "dbInstance", request.DBInstanceIdentifier)
		return PerformanceInsightsResponse{}, err
	}

	response := PerformanceInsightsResponse{
		DBInstanceIdentifier:       request.DBInstanceIdentifier,
		PerformanceInsightsEnabled: enabled,
	}

	if !enabled {
		ctx.GetLogger().Info("Performance Insights not enabled for instance", "dbInstance", request.DBInstanceIdentifier)
		return response, nil
	}

	if resourceID == "" {
		return response, fmt.Errorf("DbiResourceId not found for instance %s", request.DBInstanceIdentifier)
	}

	piSvc := pi.NewFromConfig(cfg)

	// Set default time range if not provided
	endTime := time.Now()
	startTime := endTime.Add(-1 * time.Hour)

	if request.EndTime != nil {
		endTime = *request.EndTime
	}
	if request.StartTime != nil {
		startTime = *request.StartTime
	}

	// Set period in seconds (default to 5 minutes if not provided)
	periodInSeconds := int32(300)
	if request.PeriodInSeconds != nil {
		periodInSeconds = *request.PeriodInSeconds
	}

	// Fetch db.load.avg (Average Active Sessions)
	dbLoadMetric, err := fetchDBLoadMetric(ctx, piSvc, resourceID, startTime, endTime, periodInSeconds)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch db.load metric", "error", err, "dbInstance", request.DBInstanceIdentifier)
	} else {
		response.Metrics = append(response.Metrics, dbLoadMetric)
	}

	// Fetch top SQL queries
	topSQL, err := fetchTopSQL(ctx, piSvc, resourceID, startTime, endTime)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch top SQL", "error", err, "dbInstance", request.DBInstanceIdentifier)
	} else {
		response.TopSQL = topSQL
	}

	// Fetch wait events breakdown
	waitEvents, err := fetchWaitEvents(ctx, piSvc, resourceID, startTime, endTime)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch wait events", "error", err, "dbInstance", request.DBInstanceIdentifier)
	} else {
		response.WaitEvents = waitEvents
	}

	return response, nil
}

// fetchDBLoadMetric fetches the db.load.avg metric
func fetchDBLoadMetric(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, startTime, endTime time.Time, periodInSeconds int32) (PerformanceInsightsMetric, error) {
	input := &pi.GetResourceMetricsInput{
		ServiceType:     types.ServiceTypeRds,
		Identifier:      aws.String(resourceID),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(periodInSeconds),
		MetricQueries: []types.MetricQuery{
			{
				Metric: aws.String("db.load.avg"),
			},
		},
	}

	result, err := piSvc.GetResourceMetrics(ctx.GetContext(), input)
	if err != nil {
		return PerformanceInsightsMetric{}, err
	}

	metric := PerformanceInsightsMetric{
		Name:       "db.load.avg",
		Unit:       "AAS", // Average Active Sessions
		Timestamps: []int64{},
		Values:     []float64{},
	}

	if len(result.MetricList) > 0 && len(result.MetricList[0].DataPoints) > 0 {
		for _, dp := range result.MetricList[0].DataPoints {
			if dp.Timestamp != nil && dp.Value != nil {
				metric.Timestamps = append(metric.Timestamps, dp.Timestamp.Unix())
				metric.Values = append(metric.Values, *dp.Value)
			}
		}
	}

	return metric, nil
}

// fetchTopSQL fetches top SQL queries by DB load with execution count and latency
func fetchTopSQL(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, startTime, endTime time.Time) ([]TopSQLQuery, error) {
	// Calculate period based on time range for better aggregation
	timeRange := endTime.Sub(startTime)
	periodInSeconds := int32(300) // Default 5 minutes
	if timeRange > 24*time.Hour {
		periodInSeconds = 3600 // 1 hour for longer ranges
	} else if timeRange > 6*time.Hour {
		periodInSeconds = 900 // 15 minutes for medium ranges
	}

	// Fetch db.load.avg grouped by SQL with additional metrics for execution stats
	input := &pi.GetResourceMetricsInput{
		ServiceType:     types.ServiceTypeRds,
		Identifier:      aws.String(resourceID),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(periodInSeconds),
		MetricQueries: []types.MetricQuery{
			{
				Metric: aws.String("db.load.avg"),
				GroupBy: &types.DimensionGroup{
					Group:      aws.String("db.sql"),
					Dimensions: []string{"db.sql.id", "db.sql.db_id", "db.sql.statement"},
					Limit:      aws.Int32(10), // Fetch top 10
				},
			},
		},
	}

	result, err := piSvc.GetResourceMetrics(ctx.GetContext(), input)
	if err != nil {
		return nil, err
	}

	topSQL := []TopSQLQuery{}

	if len(result.MetricList) > 0 {
		// Collect SQL data including any available statement text from dimensions
		type sqlData struct {
			sqlID      string
			sqlText    string
			totalLoad  float64
			dataPoints int
		}
		sqlDataMap := make(map[string]*sqlData)

		for _, metric := range result.MetricList {
			if metric.Key == nil || metric.Key.Dimensions == nil {
				continue
			}

			sqlID := ""
			sqlText := ""
			if sqlDim, ok := metric.Key.Dimensions["db.sql.id"]; ok {
				sqlID = sqlDim
			}
			// Check if statement text is available in dimensions
			if stmtDim, ok := metric.Key.Dimensions["db.sql.statement"]; ok {
				sqlText = stmtDim
			}

			if sqlID == "" {
				continue
			}

			// Calculate total DB load for this SQL
			totalLoad := 0.0
			dataPointCount := 0
			for _, dp := range metric.DataPoints {
				if dp.Value != nil {
					totalLoad += *dp.Value
					dataPointCount++
				}
			}

			if totalLoad > 0 {
				sqlDataMap[sqlID] = &sqlData{
					sqlID:      sqlID,
					sqlText:    sqlText,
					totalLoad:  totalLoad,
					dataPoints: dataPointCount,
				}
			}
		}

		// Fetch SQL text and tokenized IDs for all SQL IDs
		// We need tokenized IDs for ALL queries to bridge to digest stats
		allSQLIDs := make([]string, 0, len(sqlDataMap))
		for sqlID := range sqlDataMap {
			allSQLIDs = append(allSQLIDs, sqlID)
		}

		sqlTextMap, tokenizedIDMap := fetchSQLText(ctx, piSvc, resourceID, allSQLIDs)
		for sqlID, text := range sqlTextMap {
			if data, ok := sqlDataMap[sqlID]; ok && data.sqlText == "" {
				data.sqlText = text
			}
		}

		// Fetch per-query execution statistics from digest counter metrics
		digestStatsMap := fetchSQLDigestStats(ctx, piSvc, resourceID, startTime, endTime, periodInSeconds)

		// Build the result
		for sqlID, data := range sqlDataMap {
			sqlText := data.sqlText
			if sqlText == "" {
				sqlText = sqlID // Use SQL ID as last resort fallback
			}

			// Calculate average load per data point
			avgLoad := data.totalLoad
			if data.dataPoints > 0 {
				avgLoad = data.totalLoad / float64(data.dataPoints)
			}

			query := TopSQLQuery{
				SQLText:     truncateSQLText(sqlText, 500),
				DBLoad:      avgLoad,
				CallsPerSec: 0,
				AvgLatency:  0,
			}

			// Match digest stats using tokenized ID (deterministic match)
			if tokID, ok := tokenizedIDMap[sqlID]; ok {
				if stats, ok := digestStatsMap[tokID]; ok {
					query.CallsPerSec = stats.CallsPerSec
					query.AvgLatency = stats.AvgLatencyMs
					query.RowsPerCall = stats.RowsPerCall
				}
			}

			// Fallback: try text-based matching if tokenized ID match failed
			if query.CallsPerSec == 0 && query.AvgLatency == 0 {
				if stats, ok := matchDigestStatsByText(sqlText, digestStatsMap); ok {
					query.CallsPerSec = stats.CallsPerSec
					query.AvgLatency = stats.AvgLatencyMs
					query.RowsPerCall = stats.RowsPerCall
				}
			}

			topSQL = append(topSQL, query)
		}
	}

	return topSQL, nil
}

// sqlDigestStats holds per-query execution statistics from PI counter metrics
type sqlDigestStats struct {
	SQLText      string
	CallsPerSec  float64
	AvgLatencyMs float64
	RowsPerCall  float64
}

// fetchSQLDigestStats fetches per-query execution statistics using db.sql_tokenized.stats counter metrics
func fetchSQLDigestStats(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, startTime, endTime time.Time, periodInSeconds int32) map[string]sqlDigestStats {
	input := &pi.GetResourceMetricsInput{
		ServiceType:     types.ServiceTypeRds,
		Identifier:      aws.String(resourceID),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(periodInSeconds),
		MetricQueries: []types.MetricQuery{
			{
				Metric: aws.String("db.sql_tokenized.stats.calls_per_sec.avg"),
				GroupBy: &types.DimensionGroup{
					Group:      aws.String("db.sql_tokenized"),
					Dimensions: []string{"db.sql_tokenized.id", "db.sql_tokenized.statement"},
					Limit:      aws.Int32(25),
				},
			},
			{
				Metric: aws.String("db.sql_tokenized.stats.avg_latency_per_call.avg"),
				GroupBy: &types.DimensionGroup{
					Group:      aws.String("db.sql_tokenized"),
					Dimensions: []string{"db.sql_tokenized.id", "db.sql_tokenized.statement"},
					Limit:      aws.Int32(25),
				},
			},
			{
				Metric: aws.String("db.sql_tokenized.stats.rows_per_call.avg"),
				GroupBy: &types.DimensionGroup{
					Group:      aws.String("db.sql_tokenized"),
					Dimensions: []string{"db.sql_tokenized.id", "db.sql_tokenized.statement"},
					Limit:      aws.Int32(25),
				},
			},
		},
	}

	result, err := piSvc.GetResourceMetrics(ctx.GetContext(), input)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch SQL digest stats", "error", err)
		return nil
	}

	// Build a map keyed by tokenized ID, aggregating all three metrics
	type statsAccumulator struct {
		sqlText      string
		callsPerSec  float64
		avgLatencyMs float64
		rowsPerCall  float64
	}
	statsMap := make(map[string]*statsAccumulator)

	for _, metric := range result.MetricList {
		if metric.Key == nil || metric.Key.Dimensions == nil || metric.Key.Metric == nil {
			continue
		}

		tokenizedID := metric.Key.Dimensions["db.sql_tokenized.id"]
		if tokenizedID == "" {
			continue
		}

		// Calculate average value across all data points
		var total float64
		var count int
		for _, dp := range metric.DataPoints {
			if dp.Value != nil {
				total += *dp.Value
				count++
			}
		}
		if count == 0 {
			continue
		}
		avgValue := total / float64(count)

		acc, ok := statsMap[tokenizedID]
		if !ok {
			acc = &statsAccumulator{
				sqlText: metric.Key.Dimensions["db.sql_tokenized.statement"],
			}
			statsMap[tokenizedID] = acc
		}

		switch *metric.Key.Metric {
		case "db.sql_tokenized.stats.calls_per_sec.avg":
			acc.callsPerSec = avgValue
		case "db.sql_tokenized.stats.avg_latency_per_call.avg":
			acc.avgLatencyMs = avgValue
		case "db.sql_tokenized.stats.rows_per_call.avg":
			acc.rowsPerCall = avgValue
		}
	}

	// Return map keyed by tokenized ID for deterministic matching
	digestMap := make(map[string]sqlDigestStats, len(statsMap))
	for tokenizedID, acc := range statsMap {
		digestMap[tokenizedID] = sqlDigestStats{
			SQLText:      acc.sqlText,
			CallsPerSec:  acc.callsPerSec,
			AvgLatencyMs: acc.avgLatencyMs,
			RowsPerCall:  acc.rowsPerCall,
		}
	}
	return digestMap
}

// matchDigestStatsByText is a fallback that finds matching digest stats using SQL text comparison.
// Used when tokenized ID bridging is not available for a query.
func matchDigestStatsByText(sqlText string, digestStatsMap map[string]sqlDigestStats) (sqlDigestStats, bool) {
	if len(digestStatsMap) == 0 {
		return sqlDigestStats{}, false
	}

	// Try exact match first
	for _, stats := range digestStatsMap {
		if stats.SQLText != "" && stats.SQLText == sqlText {
			return stats, true
		}
	}

	// Try prefix match (tokenized text is often a prefix of the full SQL)
	for _, stats := range digestStatsMap {
		if stats.SQLText == "" || len(sqlText) < 20 {
			continue
		}
		if extractSQLPrefix(stats.SQLText) == extractSQLPrefix(sqlText) {
			return stats, true
		}
	}

	return sqlDigestStats{}, false
}

// extractSQLPrefix extracts the SQL command + table name portion for matching
// e.g., "update cloud_resourses set" from a full UPDATE statement
func extractSQLPrefix(sql string) string {
	// Take first 80 chars max, lowercase for comparison
	s := sql
	if len(s) > 80 {
		s = s[:80]
	}
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32 // toLower
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// fetchSQLText fetches the actual SQL text and tokenized IDs for given SQL IDs using GetDimensionKeyDetails API.
// Returns two maps: sqlID -> sqlText, and sqlID -> tokenizedID (for bridging to digest stats).
func fetchSQLText(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, sqlIDs []string) (map[string]string, map[string]string) {
	sqlTextMap := make(map[string]string)
	tokenizedIDMap := make(map[string]string)

	if len(sqlIDs) == 0 {
		return sqlTextMap, tokenizedIDMap
	}

	// Use GetDimensionKeyDetails to fetch actual SQL text for each SQL ID
	for _, sqlID := range sqlIDs {
		input := &pi.GetDimensionKeyDetailsInput{
			ServiceType:     types.ServiceTypeRds,
			Identifier:      aws.String(resourceID),
			Group:           aws.String("db.sql"),
			GroupIdentifier: aws.String(sqlID),
			RequestedDimensions: []string{
				"db.sql.statement",
				"db.sql.tokenized_id",
			},
		}

		result, err := piSvc.GetDimensionKeyDetails(ctx.GetContext(), input)
		if err != nil {
			ctx.GetLogger().Debug("failed to fetch SQL text for ID", "sqlID", sqlID, "error", err)
			sqlTextMap[sqlID] = sqlID
			continue
		}

		// Extract SQL text and tokenized ID from the response
		sqlText := sqlID // Default to SQL ID
		tokenizedID := ""
		if result != nil && len(result.Dimensions) > 0 {
			for _, dim := range result.Dimensions {
				if dim.Value != nil && dim.Dimension != nil {
					switch *dim.Dimension {
					case "db.sql.statement":
						sqlText = *dim.Value
					case "db.sql.tokenized_id":
						tokenizedID = *dim.Value
					}
				}
			}
		}

		sqlTextMap[sqlID] = sqlText
		if tokenizedID != "" {
			tokenizedIDMap[sqlID] = tokenizedID
		}
	}

	return sqlTextMap, tokenizedIDMap
}

// fetchWaitEvents fetches wait event breakdown
func fetchWaitEvents(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, startTime, endTime time.Time) ([]WaitEvent, error) {
	input := &pi.GetResourceMetricsInput{
		ServiceType:     types.ServiceTypeRds,
		Identifier:      aws.String(resourceID),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(3600), // 1 hour period
		MetricQueries: []types.MetricQuery{
			{
				Metric: aws.String("db.load.avg"),
				GroupBy: &types.DimensionGroup{
					Group: aws.String("db.wait_event"),
					Limit: aws.Int32(10), // Top 10 wait events
				},
			},
		},
	}

	result, err := piSvc.GetResourceMetrics(ctx.GetContext(), input)
	if err != nil {
		return nil, err
	}

	waitEvents := []WaitEvent{}
	totalLoad := 0.0
	eventLoads := make(map[string]float64)

	if len(result.MetricList) > 0 {
		// Calculate load per wait event
		for _, metric := range result.MetricList {
			if metric.Key == nil || metric.Key.Dimensions == nil {
				continue
			}

			eventType := "Unknown"
			if eventDim, ok := metric.Key.Dimensions["db.wait_event.name"]; ok {
				eventType = eventDim
			}

			load := 0.0
			for _, dp := range metric.DataPoints {
				if dp.Value != nil {
					load += *dp.Value
				}
			}

			eventLoads[eventType] = load
			totalLoad += load
		}

		// Calculate percentages
		for eventType, load := range eventLoads {
			percentage := 0.0
			if totalLoad > 0 {
				percentage = (load / totalLoad) * 100
			}

			waitEvents = append(waitEvents, WaitEvent{
				EventType:  eventType,
				DBLoad:     load,
				Percentage: percentage,
			})
		}
	}

	return waitEvents, nil
}

// fetchTopUsers fetches top database users by DB load
func fetchTopUsers(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, startTime, endTime time.Time, topN int) ([]providers.DatabaseUser, error) {
	input := &pi.GetResourceMetricsInput{
		ServiceType:     types.ServiceTypeRds,
		Identifier:      aws.String(resourceID),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(3600), // 1 hour period
		MetricQueries: []types.MetricQuery{
			{
				Metric: aws.String("db.load.avg"),
				GroupBy: &types.DimensionGroup{
					Group: aws.String("db.user"),
					Limit: aws.Int32(int32(topN)),
				},
			},
		},
	}

	result, err := piSvc.GetResourceMetrics(ctx.GetContext(), input)
	if err != nil {
		return nil, err
	}

	topUsers := []providers.DatabaseUser{}
	totalLoad := 0.0
	userLoads := make(map[string]float64)

	if len(result.MetricList) > 0 {
		// Calculate load per user
		for _, metric := range result.MetricList {
			if metric.Key == nil || metric.Key.Dimensions == nil {
				continue
			}

			userName := "Unknown"
			if userDim, ok := metric.Key.Dimensions["db.user.name"]; ok {
				userName = userDim
			}

			load := 0.0
			for _, dp := range metric.DataPoints {
				if dp.Value != nil {
					load += *dp.Value
				}
			}

			userLoads[userName] = load
			totalLoad += load
		}

		// Calculate percentages and build result
		for userName, load := range userLoads {
			percentage := 0.0
			if totalLoad > 0 {
				percentage = (load / totalLoad) * 100
			}

			topUsers = append(topUsers, providers.DatabaseUser{
				UserName:     userName,
				DatabaseLoad: load,
				Percentage:   percentage,
			})
		}
	}

	return topUsers, nil
}

// fetchTopHosts fetches top client hosts by DB load
func fetchTopHosts(ctx providers.CloudProviderContext, piSvc *pi.Client, resourceID string, startTime, endTime time.Time, topN int) ([]providers.DatabaseHost, error) {
	input := &pi.GetResourceMetricsInput{
		ServiceType:     types.ServiceTypeRds,
		Identifier:      aws.String(resourceID),
		StartTime:       aws.Time(startTime),
		EndTime:         aws.Time(endTime),
		PeriodInSeconds: aws.Int32(3600), // 1 hour period
		MetricQueries: []types.MetricQuery{
			{
				Metric: aws.String("db.load.avg"),
				GroupBy: &types.DimensionGroup{
					Group: aws.String("db.host"),
					Limit: aws.Int32(int32(topN)),
				},
			},
		},
	}

	result, err := piSvc.GetResourceMetrics(ctx.GetContext(), input)
	if err != nil {
		return nil, err
	}

	topHosts := []providers.DatabaseHost{}
	totalLoad := 0.0
	hostLoads := make(map[string]float64)

	if len(result.MetricList) > 0 {
		// Calculate load per host
		for _, metric := range result.MetricList {
			if metric.Key == nil || metric.Key.Dimensions == nil {
				continue
			}

			hostName := "Unknown"
			if hostDim, ok := metric.Key.Dimensions["db.host.name"]; ok {
				hostName = hostDim
			}

			load := 0.0
			for _, dp := range metric.DataPoints {
				if dp.Value != nil {
					load += *dp.Value
				}
			}

			hostLoads[hostName] = load
			totalLoad += load
		}

		// Calculate percentages and build result
		for hostName, load := range hostLoads {
			percentage := 0.0
			if totalLoad > 0 {
				percentage = (load / totalLoad) * 100
			}

			topHosts = append(topHosts, providers.DatabaseHost{
				HostName:     hostName,
				DatabaseLoad: load,
				Percentage:   percentage,
			})
		}
	}

	return topHosts, nil
}

// truncateSQLText truncates SQL text to a specified length
func truncateSQLText(sql string, maxLen int) string {
	if len(sql) <= maxLen {
		return sql
	}
	return sql[:maxLen] + "..."
}

// QueryDatabasePerformance implements the generic database performance insights interface for AWS RDS
// This method bridges the AWS-specific PI API to the provider-agnostic interface
func (p amazonRds) QueryDatabasePerformance(ctx providers.CloudProviderContext, account providers.Account, request providers.DatabasePerformanceRequest) (providers.DatabasePerformanceResponse, error) {
	// Set defaults
	request.SetDefaults()

	// Validate request
	if err := request.Validate(); err != nil {
		return providers.DatabasePerformanceResponse{}, err
	}

	cfg, err := getAwsConfigFromAccount(ctx.GetContext(), account)
	if err != nil {
		ctx.GetLogger().Error("failed to create aws config", "error", err, "accountNumber", account.AccountNumber)
		return providers.DatabasePerformanceResponse{}, err
	}

	// Set region for the AWS config
	if request.Region != "" {
		cfg.Region = request.Region
	}

	// Check if Performance Insights is enabled
	enabled, resourceID, err := checkPerformanceInsightsEnabled(ctx.GetContext(), cfg, request.DatabaseIdentifier)
	if err != nil {
		ctx.GetLogger().Error("failed to check PI status", "error", err, "dbInstance", request.DatabaseIdentifier)
		return providers.DatabasePerformanceResponse{}, err
	}

	// Initialize response
	response := providers.NewDatabasePerformanceResponse("aws", request.DatabaseIdentifier, enabled)

	if !enabled {
		ctx.GetLogger().Info("Performance Insights not enabled for instance", "dbInstance", request.DatabaseIdentifier)
		response.Metadata["message"] = "Performance Insights is not enabled for this RDS instance"
		response.Metadata["how_to_enable"] = "Enable Performance Insights in RDS console or via AWS CLI"
		return response, nil
	}

	if resourceID == "" {
		return response, fmt.Errorf("DbiResourceId not found for instance %s", request.DatabaseIdentifier)
	}

	piSvc := pi.NewFromConfig(cfg)

	// Convert granularity to period in seconds
	periodInSeconds := request.GranularitySeconds

	// Fetch db.load.avg (Average Active Sessions) - the primary load metric for AWS
	if dbLoadMetric, err := fetchDBLoadMetric(ctx, piSvc, resourceID, *request.StartTime, *request.EndTime, periodInSeconds); err != nil {
		ctx.GetLogger().Warn("failed to fetch db.load metric", "error", err, "dbInstance", request.DatabaseIdentifier)
	} else {
		// Convert to generic TimeSeriesMetric
		response.LoadMetrics = append(response.LoadMetrics, providers.TimeSeriesMetric{
			Name:       dbLoadMetric.Name,
			Unit:       dbLoadMetric.Unit,
			Timestamps: convertTimestampsToMillis(dbLoadMetric.Timestamps),
			Values:     dbLoadMetric.Values,
		})
	}

	// Fetch resource metrics from CloudWatch (CPU, memory, IOPS, etc.)
	resourceMetrics, err := fetchRDSResourceMetrics(ctx, cfg, request.DatabaseIdentifier, *request.StartTime, *request.EndTime, periodInSeconds)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch resource metrics", "error", err, "dbInstance", request.DatabaseIdentifier)
	} else {
		response.ResourceMetrics = resourceMetrics
	}

	// Fetch top SQL queries if requested
	if request.IncludeTopQueries {
		if topSQL, err := fetchTopSQL(ctx, piSvc, resourceID, *request.StartTime, *request.EndTime); err != nil {
			ctx.GetLogger().Warn("failed to fetch top SQL", "error", err, "dbInstance", request.DatabaseIdentifier)
		} else {
			// Convert AWS TopSQLQuery to generic DatabaseQuery
			timePeriodSeconds := request.EndTime.Sub(*request.StartTime).Seconds()
			response.TopQueries = convertTopSQLToGeneric(topSQL, request.TopN, timePeriodSeconds)
		}
	}

	// Fetch wait events if requested
	if request.IncludeWaitEvents {
		if waitEvents, err := fetchWaitEvents(ctx, piSvc, resourceID, *request.StartTime, *request.EndTime); err != nil {
			ctx.GetLogger().Warn("failed to fetch wait events", "error", err, "dbInstance", request.DatabaseIdentifier)
		} else {
			// Convert AWS WaitEvent to generic DatabaseWaitEvent
			response.WaitEvents = convertWaitEventsToGeneric(waitEvents, request.TopN)
		}
	}

	// Fetch top database users if requested
	if request.IncludeTopUsers {
		if topUsers, err := fetchTopUsers(ctx, piSvc, resourceID, *request.StartTime, *request.EndTime, request.TopN); err != nil {
			ctx.GetLogger().Warn("failed to fetch top users", "error", err, "dbInstance", request.DatabaseIdentifier)
		} else {
			response.TopUsers = topUsers
		}
	}

	// Fetch top client hosts if requested
	if request.IncludeTopHosts {
		if topHosts, err := fetchTopHosts(ctx, piSvc, resourceID, *request.StartTime, *request.EndTime, request.TopN); err != nil {
			ctx.GetLogger().Warn("failed to fetch top hosts", "error", err, "dbInstance", request.DatabaseIdentifier)
		} else {
			response.TopHosts = topHosts
		}
	}

	// Add metadata
	response.Metadata["provider_name"] = "AWS RDS"
	response.Metadata["performance_insights_retention_days"] = 7 // Default retention
	response.Metadata["features_available"] = []string{"db_load", "top_queries", "wait_events", "top_users", "top_hosts", "resource_metrics"}
	response.Metadata["data_points_returned"] = len(response.LoadMetrics)

	return response, nil
}

// convertTimestampsToMillis converts Unix seconds to milliseconds
func convertTimestampsToMillis(timestamps []int64) []int64 {
	result := make([]int64, len(timestamps))
	for i, ts := range timestamps {
		result[i] = ts * 1000 // Convert seconds to milliseconds
	}
	return result
}

// convertTopSQLToGeneric converts AWS TopSQLQuery to generic DatabaseQuery
func convertTopSQLToGeneric(topSQL []TopSQLQuery, topN int, timePeriodSeconds float64) []providers.DatabaseQuery {
	result := []providers.DatabaseQuery{}

	// Limit to topN
	limit := topN
	if len(topSQL) < limit {
		limit = len(topSQL)
	}

	for _, query := range topSQL[:limit] {
		// Generate a query ID (hash of SQL text for now)
		queryID := fmt.Sprintf("0x%X", hashString(query.SQLText))

		dbQuery := providers.DatabaseQuery{
			QueryID:      queryID,
			QueryText:    query.SQLText,
			DatabaseLoad: query.DBLoad,
		}

		// Populate execution stats from digest counter metrics
		if query.CallsPerSec > 0 {
			dbQuery.ExecutionCount = int64(query.CallsPerSec * timePeriodSeconds)
		}
		if query.AvgLatency > 0 {
			dbQuery.AvgDuration = query.AvgLatency
		}
		if dbQuery.ExecutionCount > 0 && dbQuery.AvgDuration > 0 {
			dbQuery.TotalDuration = float64(dbQuery.ExecutionCount) * dbQuery.AvgDuration
		}
		if query.RowsPerCall > 0 {
			rowsPerCall := int64(query.RowsPerCall)
			dbQuery.AvgRowsProcessed = &rowsPerCall
		}

		result = append(result, dbQuery)
	}

	return result
}

// convertWaitEventsToGeneric converts AWS WaitEvent to generic DatabaseWaitEvent
func convertWaitEventsToGeneric(waitEvents []WaitEvent, topN int) []providers.DatabaseWaitEvent {
	result := []providers.DatabaseWaitEvent{}

	// Limit to topN
	limit := topN
	if len(waitEvents) < limit {
		limit = len(waitEvents)
	}

	for _, event := range waitEvents[:limit] {
		dbEvent := providers.DatabaseWaitEvent{
			EventType:    categorizeWaitEvent(event.EventType),
			EventName:    event.EventType,
			DatabaseLoad: event.DBLoad,
			Percentage:   event.Percentage,
		}

		result = append(result, dbEvent)
	}

	return result
}

// categorizeWaitEvent categorizes AWS wait events into broader categories
func categorizeWaitEvent(eventName string) string {
	// Common AWS wait event categories
	if eventName == "CPU" {
		return "CPU"
	}
	if len(eventName) >= 3 && eventName[:3] == "IO:" {
		return "IO"
	}
	if len(eventName) >= 5 && eventName[:5] == "Lock:" {
		return "Lock"
	}
	if len(eventName) >= 8 && eventName[:8] == "Network:" {
		return "Network"
	}
	return "Other"
}

// hashString generates a simple hash for a string (for query ID generation)
func hashString(s string) uint32 {
	hash := uint32(0)
	for i := 0; i < len(s); i++ {
		hash = hash*31 + uint32(s[i])
	}
	return hash
}

// fetchRDSResourceMetrics fetches resource metrics from CloudWatch
func fetchRDSResourceMetrics(ctx providers.CloudProviderContext, cfg aws.Config, dbInstanceID string, startTime, endTime time.Time, periodSeconds int32) ([]providers.TimeSeriesMetric, error) {
	// Use the existing CloudWatch metrics query infrastructure
	// This will fetch CPU, memory, IOPS, network, etc.
	return fetchCloudWatchMetricsForRDS(ctx, cfg, dbInstanceID, startTime, endTime, periodSeconds)
}

// fetchCloudWatchMetricsForRDS fetches CloudWatch metrics directly for RDS
func fetchCloudWatchMetricsForRDS(ctx providers.CloudProviderContext, cfg aws.Config, dbInstanceID string, startTime, endTime time.Time, periodSeconds int32) ([]providers.TimeSeriesMetric, error) {
	svc := cloudwatch.NewFromConfig(cfg)

	// Define the key RDS metrics to fetch
	// These metrics provide comprehensive resource utilization data
	metricsToFetch := []struct {
		name string
		unit string
		stat string
	}{
		{"CPUUtilization", "Percent", "Average"},
		{"DatabaseConnections", "Count", "Average"},
		{"FreeableMemory", "Bytes", "Average"},
		{"FreeStorageSpace", "Bytes", "Average"},
		{"ReadIOPS", "Count/Second", "Average"},
		{"WriteIOPS", "Count/Second", "Average"},
		{"ReadLatency", "Seconds", "Average"},
		{"WriteLatency", "Seconds", "Average"},
		{"ReadThroughput", "Bytes/Second", "Average"},
		{"WriteThroughput", "Bytes/Second", "Average"},
		{"NetworkReceiveThroughput", "Bytes/Second", "Average"},
		{"NetworkTransmitThroughput", "Bytes/Second", "Average"},
		{"SwapUsage", "Bytes", "Average"},
		{"DiskQueueDepth", "Count", "Average"},
	}

	// Build metric data queries
	queries := make([]cloudwatchtypes.MetricDataQuery, 0, len(metricsToFetch))
	for i, metric := range metricsToFetch {
		queries = append(queries, cloudwatchtypes.MetricDataQuery{
			Id:         aws.String(fmt.Sprintf("m%d", i)),
			ReturnData: aws.Bool(true),
			MetricStat: &cloudwatchtypes.MetricStat{
				Metric: &cloudwatchtypes.Metric{
					Namespace:  aws.String("AWS/RDS"),
					MetricName: aws.String(metric.name),
					Dimensions: []cloudwatchtypes.Dimension{
						{
							Name:  aws.String("DBInstanceIdentifier"),
							Value: aws.String(dbInstanceID),
						},
					},
				},
				Period: aws.Int32(periodSeconds),
				Stat:   aws.String(metric.stat),
			},
		})
	}

	// Fetch metrics from CloudWatch
	result, err := svc.GetMetricData(ctx.GetContext(), &cloudwatch.GetMetricDataInput{
		StartTime:         aws.Time(startTime),
		EndTime:           aws.Time(endTime),
		MetricDataQueries: queries,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch CloudWatch metrics: %w", err)
	}

	// Convert CloudWatch results to TimeSeriesMetric format
	metrics := []providers.TimeSeriesMetric{}
	for _, metricDataResult := range result.MetricDataResults {
		if metricDataResult.Id == nil || len(metricDataResult.Values) == 0 {
			continue
		}

		// Find the corresponding metric info
		var metricIndex int
		_, err := fmt.Sscanf(*metricDataResult.Id, "m%d", &metricIndex)
		if err != nil || metricIndex >= len(metricsToFetch) {
			continue
		}
		metricInfo := metricsToFetch[metricIndex]

		// Convert timestamps to milliseconds
		timestamps := make([]int64, len(metricDataResult.Timestamps))
		for j, ts := range metricDataResult.Timestamps {
			timestamps[j] = ts.Unix() * 1000
		}

		// Convert values from float64 pointers to float64
		values := make([]float64, len(metricDataResult.Values))
		copy(values, metricDataResult.Values)

		metrics = append(metrics, providers.TimeSeriesMetric{
			Name:       metricInfo.name,
			Unit:       metricInfo.unit,
			Timestamps: timestamps,
			Values:     values,
		})

		// Add additional calculated metrics for better insights
		if metricInfo.name == "ReadLatency" || metricInfo.name == "WriteLatency" {
			// Convert latency from seconds to milliseconds for better readability
			msValues := make([]float64, len(values))
			for j, val := range values {
				msValues[j] = val * 1000
			}
			metrics = append(metrics, providers.TimeSeriesMetric{
				Name:       metricInfo.name + "Ms",
				Unit:       "Milliseconds",
				Timestamps: timestamps,
				Values:     msValues,
			})
		}
	}

	// Log summary of fetched metrics
	ctx.GetLogger().Debug("Successfully fetched CloudWatch metrics for RDS",
		"dbInstance", dbInstanceID,
		"metricsCount", len(metrics),
		"startTime", startTime,
		"endTime", endTime,
		"periodSeconds", periodSeconds)

	return metrics, nil
}

// getAwsPerformanceInsightsMetrics fetches PI metrics in the standard QueryMetricsResponse format
// This function bridges the PI API with the standard metrics flow
// It processes all resource IDs in the batch and aggregates results
func getAwsPerformanceInsightsMetrics(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	if len(filter.ResourceIds) == 0 {
		return providers.QueryMetricsResponse{}, fmt.Errorf("no resource IDs provided")
	}

	// Set default time range if not provided
	startDate := time.Now().Add(-1 * time.Hour)
	endDate := time.Now()
	if filter.StartDate != nil {
		startDate = *filter.StartDate
	}
	if filter.EndDate != nil {
		endDate = *filter.EndDate
	}

	// Calculate PeriodInSeconds from filter.Step
	// Performance Insights supports: 60, 300 (5min), 3600 (1hr), 86400 (1day)
	step := filter.Step
	if step == 0 {
		step = 5 * time.Minute // Default to 5 minutes
	}
	periodInSeconds := int32(step.Seconds())

	// Validate and adjust to nearest supported PI period
	switch {
	case periodInSeconds < 300:
		periodInSeconds = 60 // 1 minute
	case periodInSeconds < 3600:
		periodInSeconds = 300 // 5 minutes
	case periodInSeconds < 86400:
		periodInSeconds = 3600 // 1 hour
	default:
		periodInSeconds = 86400 // 1 day
	}

	// Aggregate metrics from all resource IDs in the batch
	allItems := []providers.MetricItem{}

	// Process each resource ID in the batch
	for _, resourceId := range filter.ResourceIds {
		// Convert QueryMetricsRequest to PerformanceInsightsRequest
		request := PerformanceInsightsRequest{
			DBInstanceIdentifier: resourceId,
			Region:               filter.Region,
			StartTime:            &startDate,
			EndTime:              &endDate,
			PeriodInSeconds:      &periodInSeconds,
		}

		// Fetch PI data using existing function
		piResponse, err := GetPerformanceInsightsMetrics(ctx, account, request)
		if err != nil {
			// Log error but continue processing other resources
			ctx.GetLogger().Error("failed to fetch PI metrics for resource", "error", err, "resourceId", resourceId)
			continue
		}

		// Check if PI is enabled
		if !piResponse.PerformanceInsightsEnabled {
			// PI not enabled for this instance - skip silently
			ctx.GetLogger().Debug("Performance Insights not enabled", "resourceId", resourceId)
			continue
		}

		// Convert PI metrics to standard metric items
		for _, metric := range piResponse.Metrics {
			// Convert Unix timestamps to time.Time
			timestamps := make([]time.Time, len(metric.Timestamps))
			for i, ts := range metric.Timestamps {
				timestamps[i] = time.Unix(ts, 0)
			}

			allItems = append(allItems, providers.MetricItem{
				Name:        metric.Name,
				Statistics:  "Average", // PI metrics are already averaged
				ResourceId:  piResponse.DBInstanceIdentifier,
				Values:      metric.Values,
				Timestamps:  timestamps,
				Region:      filter.Region,
				ServiceName: ServiceNameRDS,
			})
		}
	}

	return providers.QueryMetricsResponse{
		Items:     allItems,
		StartDate: startDate,
		EndDate:   endDate,
		Step:      time.Duration(periodInSeconds) * time.Second,
	}, nil
}
