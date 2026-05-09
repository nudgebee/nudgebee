package providers

import (
	"fmt"
	"time"
)

// DatabasePerformanceRequest is the generic request for database performance insights
// across all cloud providers (AWS RDS, GCP Cloud SQL, Azure SQL Database)
type DatabasePerformanceRequest struct {
	DatabaseIdentifier string     `json:"database_identifier" validate:"required"` // DB instance/server identifier
	Region             string     `json:"region" validate:"required"`              // Cloud region
	StartTime          *time.Time `json:"start_time"`                              // Start time for metrics (RFC3339)
	EndTime            *time.Time `json:"end_time"`                                // End time for metrics (RFC3339)
	GranularitySeconds int32      `json:"granularity_seconds"`                     // Metric interval: 60, 300, 3600, 86400
	IncludeTopQueries  bool       `json:"include_top_queries"`                     // Whether to fetch top SQL queries
	IncludeWaitEvents  bool       `json:"include_wait_events"`                     // Whether to fetch wait events
	IncludeTopUsers    bool       `json:"include_top_users"`                       // Whether to fetch top database users (AWS only)
	IncludeTopHosts    bool       `json:"include_top_hosts"`                       // Whether to fetch top client hosts (AWS only)
	TopN               int        `json:"top_n"`                                   // Number of top queries/events/users/hosts to return
}

// DatabasePerformanceResponse is the unified response across all cloud providers
type DatabasePerformanceResponse struct {
	DatabaseIdentifier string                 `json:"database_identifier"` // Database instance identifier
	Provider           string                 `json:"provider"`            // Cloud provider: "aws", "gcp", "azure"
	PerformanceEnabled bool                   `json:"performance_enabled"` // Whether performance monitoring is enabled
	LoadMetrics        []TimeSeriesMetric     `json:"load_metrics"`        // Time-series metrics for database load
	ResourceMetrics    []TimeSeriesMetric     `json:"resource_metrics"`    // Time-series metrics for CPU, memory, IOPS, etc.
	TopQueries         []DatabaseQuery        `json:"top_queries"`         // Top SQL queries by database load/impact
	WaitEvents         []DatabaseWaitEvent    `json:"wait_events"`         // Wait events breakdown
	TopUsers           []DatabaseUser         `json:"top_users"`           // Top database users by load (AWS only)
	TopHosts           []DatabaseHost         `json:"top_hosts"`           // Top client hosts by load (AWS only)
	Metadata           map[string]interface{} `json:"metadata"`            // Provider-specific metadata and capabilities
}

// TimeSeriesMetric represents a time-series metric for charting
type TimeSeriesMetric struct {
	Name       string    `json:"name"`       // Metric name (e.g., "db.load.avg", "cpu_utilization")
	Unit       string    `json:"unit"`       // Unit of measurement (e.g., "AAS", "percent", "bytes")
	Timestamps []int64   `json:"timestamps"` // Unix timestamps in milliseconds
	Values     []float64 `json:"values"`     // Metric values corresponding to timestamps
}

// DatabaseQuery represents query performance statistics
type DatabaseQuery struct {
	QueryID          string   `json:"query_id"`           // Query hash or identifier
	QueryText        string   `json:"query_text"`         // SQL query text (may be truncated)
	DatabaseLoad     float64  `json:"database_load"`      // Primary impact metric (AWS: AAS contribution, GCP/Azure: calculated)
	ExecutionCount   int64    `json:"execution_count"`    // Number of times the query was executed
	TotalDuration    float64  `json:"total_duration"`     // Total execution time in milliseconds
	AvgDuration      float64  `json:"avg_duration"`       // Average execution time in milliseconds
	MinDuration      *float64 `json:"min_duration"`       // Minimum execution time in milliseconds (optional)
	MaxDuration      *float64 `json:"max_duration"`       // Maximum execution time in milliseconds (optional)
	AvgCPUTime       *float64 `json:"avg_cpu_time"`       // Average CPU time in milliseconds (optional)
	AvgRowsProcessed *int64   `json:"avg_rows_processed"` // Average number of rows processed (optional)
	CacheHitRatio    *float64 `json:"cache_hit_ratio"`    // Cache hit ratio 0.0-1.0 (optional)
}

// DatabaseWaitEvent represents wait event statistics
type DatabaseWaitEvent struct {
	EventType     string   `json:"event_type"`      // Wait event category: "CPU", "IO", "Lock", etc.
	EventName     string   `json:"event_name"`      // Specific wait event name
	DatabaseLoad  float64  `json:"database_load"`   // Contribution to overall database load
	Percentage    float64  `json:"percentage"`      // Percentage of total wait time
	WaitCount     *int64   `json:"wait_count"`      // Number of wait occurrences (optional)
	TotalWaitTime *float64 `json:"total_wait_time"` // Total wait time in milliseconds (optional)
	AvgWaitTime   *float64 `json:"avg_wait_time"`   // Average wait time in milliseconds (optional)
}

// DatabaseUser represents database user activity statistics (AWS only)
type DatabaseUser struct {
	UserName     string  `json:"user_name"`     // Database user name
	DatabaseLoad float64 `json:"database_load"` // Contribution to overall database load (AAS)
	Percentage   float64 `json:"percentage"`    // Percentage of total database load
}

// DatabaseHost represents client host/application activity statistics (AWS only)
type DatabaseHost struct {
	HostName     string  `json:"host_name"`     // Client host name or application name
	DatabaseLoad float64 `json:"database_load"` // Contribution to overall database load (AAS)
	Percentage   float64 `json:"percentage"`    // Percentage of total database load
}

// SetDefaults sets default values for optional fields in DatabasePerformanceRequest
func (req *DatabasePerformanceRequest) SetDefaults() {
	// Default time range: last 1 hour
	if req.StartTime == nil {
		t := time.Now().Add(-1 * time.Hour)
		req.StartTime = &t
	}
	if req.EndTime == nil {
		t := time.Now()
		req.EndTime = &t
	}

	// Default granularity: 5 minutes (300 seconds)
	if req.GranularitySeconds == 0 {
		req.GranularitySeconds = 300
	}

	// Default: include top queries, wait events, users, and hosts
	// Note: We set these to true by default. Users can explicitly set to false if not needed.
	req.IncludeTopQueries = true
	req.IncludeWaitEvents = true
	req.IncludeTopUsers = true
	req.IncludeTopHosts = true

	if req.TopN == 0 {
		req.TopN = 10
	}
}

// Validate validates the DatabasePerformanceRequest
func (req *DatabasePerformanceRequest) Validate() error {
	if req.DatabaseIdentifier == "" {
		return fmt.Errorf("database_identifier is required")
	}
	if req.Region == "" {
		return fmt.Errorf("region is required")
	}

	// Validate granularity
	validGranularities := map[int32]bool{60: true, 300: true, 3600: true, 86400: true}
	if !validGranularities[req.GranularitySeconds] {
		return fmt.Errorf("granularity_seconds must be one of: 60, 300, 3600, 86400")
	}

	// Validate time range
	if req.StartTime != nil && req.EndTime != nil {
		if req.EndTime.Before(*req.StartTime) {
			return fmt.Errorf("end_time must be after start_time")
		}

		// Max time range: 7 days
		maxDuration := 7 * 24 * time.Hour
		if req.EndTime.Sub(*req.StartTime) > maxDuration {
			return fmt.Errorf("time range cannot exceed 7 days")
		}
	}

	// Validate TopN
	if req.TopN < 1 || req.TopN > 50 {
		return fmt.Errorf("top_n must be between 1 and 50")
	}

	return nil
}

// NewDatabasePerformanceResponse creates a new DatabasePerformanceResponse with initialized slices
func NewDatabasePerformanceResponse(provider, dbIdentifier string, perfEnabled bool) DatabasePerformanceResponse {
	return DatabasePerformanceResponse{
		DatabaseIdentifier: dbIdentifier,
		Provider:           provider,
		PerformanceEnabled: perfEnabled,
		LoadMetrics:        []TimeSeriesMetric{},
		ResourceMetrics:    []TimeSeriesMetric{},
		TopQueries:         []DatabaseQuery{},
		WaitEvents:         []DatabaseWaitEvent{},
		TopUsers:           []DatabaseUser{},
		TopHosts:           []DatabaseHost{},
		Metadata:           make(map[string]interface{}),
	}
}
