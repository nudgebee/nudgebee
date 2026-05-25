package cloud

import (
	"strings"
	"time"
)

type CloudExecuteCliCommandRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	Command   string `json:"command" validate:"required"`
}

type StoreUsageRequest struct {
	AccountId string     `json:"account_id" validate:"required"`
	Month     time.Month `json:"month" validate:"required"`
	Year      int        `json:"year" validate:"required"`
}

type QueryMetricsRequest struct {
	AccountId string       `json:"account_id" validate:"required"`
	Query     MetricsQuery `json:"query" validate:"required"`
}
type MetricsQuery struct {
	StartDate       *time.Time          `json:"start_date"`
	EndDate         *time.Time          `json:"end_date"`
	ResourceIds     []string            `json:"resource_ids"`
	ResourceType    string              `json:"resource_type"`
	ServiceName     string              `json:"service_name" validate:"required"`
	Region          string              `json:"region" validate:"required"`
	MetricNames     []string            `json:"metric_names"`
	Step            time.Duration       `json:"step"`
	Dimensions      []map[string]string `json:"dimensions,omitempty"`
	Statistics      []string            `json:"statistics"`
	MetricNamespace string              `json:"metric_namespace"`
	Query           string              `json:"query"`
}

type QueryMetricsResponse struct {
	Items     []MetricItem  `json:"items"`
	StartDate time.Time     `json:"start_date"`
	EndDate   time.Time     `json:"end_date"`
	Step      time.Duration `json:"step"`
}

type MetricItem struct {
	Name        string      `json:"name"`
	Statistics  string      `json:"statistics"`
	ResourceId  string      `json:"resource_id"`
	Values      []float64   `json:"values"`
	Timestamps  []time.Time `json:"timestamps"`
	Region      string      `json:"region"`
	ServiceName string      `json:"service_name"`
}

type ListMetricsRequest struct {
	ServiceName  string `json:"service_name"`
	ResourceType string `json:"resource_type"`
	Region       string `json:"region"`
}

type ListMetricsResponse struct {
	Metrics []MetricListItem `json:"metrics"`
}

type MetricListItem struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Statistics []string          `json:"statistics,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type QueryResourceRequest struct {
	AccountId   string   `json:"account_id,omitempty"`
	ServiceName string   `json:"service_name" validate:"required"`
	ResourceIds []string `json:"resource_ids"`
	Regions     []string `json:"regions"`
}

type QueryResourceResponse struct {
	Items []Resource `json:"items"`
}

type ResourceStatus string

const (
	ResourceStatusActive   ResourceStatus = "Active"
	ResourceStatusInactive ResourceStatus = "Inactive"
	ResourceStatusDeleted  ResourceStatus = "Deleted"
	ResourceStatusUnknown  ResourceStatus = "Unknown"
)

type Resource struct {
	Id          string              `json:"id" mapstructure:"id" validate:"required"`
	Name        string              `json:"name" mapstructure:"name" validate:"required"`
	Type        string              `json:"type" mapstructure:"type" validate:"required"`
	Arn         string              `json:"arn" mapstructure:"arn"`
	ServiceName string              `json:"service_name" mapstructure:"service_name" validate:"required"`
	Status      ResourceStatus      `json:"status" mapstructure:"status" validate:"required"`
	Region      string              `json:"region" mapstructure:"region"`
	Tags        map[string][]string `json:"tags" mapstructure:"tags"`
	Meta        map[string]any      `json:"meta" mapstructure:"meta"`
	CreatedAt   time.Time           `json:"created_at" mapstructure:"created_at" validate:"required"`
}

type QueryLogsRequest struct {
	AccountId string   `json:"account_id" validate:"required"`
	Query     LogQuery `json:"query" validate:"required"`
}

type QueryDatabasePerformanceRequest struct {
	AccountId          string     `json:"account_id" validate:"required"`
	DatabaseIdentifier string     `json:"database_identifier" validate:"required"`
	Region             string     `json:"region" validate:"required"`
	StartTime          *time.Time `json:"start_time"`
	EndTime            *time.Time `json:"end_time"`
	GranularitySeconds int32      `json:"granularity_seconds"`
	IncludeTopQueries  bool       `json:"include_top_queries"`
	IncludeWaitEvents  bool       `json:"include_wait_events"`
	IncludeTopUsers    bool       `json:"include_top_users"`
	IncludeTopHosts    bool       `json:"include_top_hosts"`
	TopN               int        `json:"top_n"`
}

type QueryDatabasePerformanceResponse struct {
	DatabaseIdentifier string                 `json:"database_identifier"`
	Provider           string                 `json:"provider"`
	PerformanceEnabled bool                   `json:"performance_enabled"`
	LoadMetrics        []PerformanceMetric    `json:"load_metrics"`
	ResourceMetrics    []PerformanceMetric    `json:"resource_metrics"`
	TopQueries         []PerformanceQuery     `json:"top_queries"`
	WaitEvents         []PerformanceWaitEvent `json:"wait_events"`
	TopUsers           []PerformanceUser      `json:"top_users,omitempty"`
	TopHosts           []PerformanceHost      `json:"top_hosts,omitempty"`
	Metadata           map[string]interface{} `json:"metadata"`
}

type PerformanceMetric struct {
	Name       string    `json:"name"`
	Unit       string    `json:"unit"`
	Timestamps []int64   `json:"timestamps"`
	Values     []float64 `json:"values"`
}

type PerformanceQuery struct {
	QueryID          string   `json:"query_id"`
	QueryText        string   `json:"query_text"`
	DatabaseLoad     float64  `json:"database_load"`
	ExecutionCount   int64    `json:"execution_count"`
	TotalDuration    float64  `json:"total_duration"`
	AvgDuration      float64  `json:"avg_duration"`
	MinDuration      *float64 `json:"min_duration,omitempty"`
	MaxDuration      *float64 `json:"max_duration,omitempty"`
	AvgCPUTime       *float64 `json:"avg_cpu_time,omitempty"`
	AvgRowsProcessed *int64   `json:"avg_rows_processed,omitempty"`
	CacheHitRatio    *float64 `json:"cache_hit_ratio,omitempty"`
}

type PerformanceWaitEvent struct {
	EventType     string   `json:"event_type"`
	EventName     string   `json:"event_name"`
	DatabaseLoad  float64  `json:"database_load"`
	Percentage    float64  `json:"percentage"`
	WaitCount     *int64   `json:"wait_count,omitempty"`
	TotalWaitTime *float64 `json:"total_wait_time,omitempty"`
	AvgWaitTime   *float64 `json:"avg_wait_time,omitempty"`
}

type PerformanceUser struct {
	UserName     string  `json:"user_name"`
	DatabaseLoad float64 `json:"database_load"`
	Percentage   float64 `json:"percentage"`
}

type PerformanceHost struct {
	HostName     string  `json:"host_name"`
	DatabaseLoad float64 `json:"database_load"`
	Percentage   float64 `json:"percentage"`
}

type LogQuery struct {
	Region        string     `json:"region"`
	LogGroupName  string     `json:"log_group_name"`
	ServiceName   string     `json:"service_name"`
	ResourceId    string     `json:"resource_id"`
	QueryString   string     `json:"query_string"`
	StartTime     *time.Time `json:"start_time"`
	EndTime       *time.Time `json:"end_time"`
	Limit         *int64     `json:"limit"`
	LogMetricName string     `json:"log_metric_name"`
	FilterPattern string     `json:"filter_pattern"`
}

type QueryLogResponse struct {
	QueryId    string             `json:"query_id"` // ID of the query that was run
	Results    []LogMessage       `json:"results"`
	Status     string             `json:"status"` // e.g., Complete, Failed, Cancelled, Running, Scheduled
	Statistics LogQueryStatistics `json:"statistics"`
}

type LogMessage struct {
	Message   string     `json:"message"`
	Timestamp int64      `json:"timestamp"`
	Labels    []LogLabel `json:"labels"`
}

type LogLabel struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type LogQueryStatistics struct {
	RecordsMatched float64 `json:"records_matched"`
	RecordsScanned float64 `json:"records_scanned"`
	BytesScanned   float64 `json:"bytes_scanned"`
}

type QueryServiceMapResourceRequest struct {
	ServiceName string `json:"service_name"`
	Resource    string `json:"resource"`
}

type QueryServiceMapRequest struct {
	AccountId string `json:"account_id"`
	Query     QueryServiceMapQuery
}
type QueryServiceMapQuery struct {
	Region    string `json:"region"`
	Resources []QueryServiceMapResourceRequest
}

type ServiceApplicationLink struct {
	Id ServiceApplicationId `json:"Id"`
}

type ServiceApplicationId struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
}

// Key returns the string representation of the ServiceApplicationId in K8s format "namespace:kind:name"
// For ARNs, extracts the resource identifier to avoid colon conflicts
func (a ServiceApplicationId) Key() string {
	name := a.Name

	// Extract resource identifier from ARN (strip arn:aws:service:region:account:resource/ prefix)
	if strings.HasPrefix(name, "arn:") {
		parts := strings.SplitN(name, ":", 6)
		if len(parts) == 6 {
			// parts[5] contains "loadbalancer/app/Demo-Frontend-ALB/9ef0c75b824fa80c"
			// or "targetgroup/Demo-Frontend-TG/6d4076c4e9596e84"
			name = parts[5]
		}
	}

	return strings.Join([]string{a.Namespace, a.Kind, name}, ":")
}

// UpstreamLink represents an upstream dependency with string ID (K8s format)
type UpstreamLink struct {
	Id            string  `json:"Id"`
	Status        int     `json:"Status,omitempty"`
	Latency       float64 `json:"Latency,omitempty"`
	RequestCount  float64 `json:"RequestCount,omitempty"`
	FailureCount  float64 `json:"FailureCount,omitempty"`
	Protocol      string  `json:"Protocol,omitempty"`
	BytesSent     float64 `json:"BytesSent,omitempty"`
	BytesReceived float64 `json:"BytesReceived,omitempty"`
}

// DownstreamLink represents a downstream dependency with object ID (K8s format)
type DownstreamLink struct {
	Id            ServiceApplicationId `json:"Id"`
	Status        int                  `json:"Status,omitempty"`
	Latency       float64              `json:"Latency,omitempty"`
	RequestCount  float64              `json:"RequestCount,omitempty"`
	FailureCount  float64              `json:"FailureCount,omitempty"`
	Protocol      string               `json:"Protocol,omitempty"`
	BytesSent     float64              `json:"BytesSent,omitempty"`
	BytesReceived float64              `json:"BytesReceived,omitempty"`
}

type ServiceMapApplication struct {
	Id          ServiceApplicationId `json:"Id"`
	Upstreams   []UpstreamLink       `json:"Upstreams"`
	Downstreams []DownstreamLink     `json:"Downstreams"`
	Status      string               `json:"Status"`
}

type QueryServiceMapResponse struct {
	Applications []ServiceMapApplication `json:"applications"`
}

type TriggerCloudSyncRequest struct {
	AccountId string `json:"account_id" validate:"required"`
}

type TriggerCloudSyncResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type ApplyCommandRequest struct {
	AccountId   string                 `json:"account_id" validate:"required"`
	ServiceName string                 `json:"service_name" validate:"required"`
	Region      string                 `json:"region" validate:"required"`
	ResourceId  string                 `json:"resource_id" validate:"required"`
	Command     string                 `json:"command" validate:"required"`
	Args        map[string]interface{} `json:"args"`
}

type ApplyCommandResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
