package traces

import (
	"nudgebee/services/query"
	"time"
)

// Configuration constants
const (
	DefaultQueryLimit         = 10000
	WorkloadQueryLimit        = 5000
	MaxTraceIDsForExpansion   = 500
	MaxTraceIDsInDrillDown    = 10
	DefaultFallbackDuration   = 1.0 // minutes
	NanosecondsToMilliseconds = 1e6
)

// Default application types
var DefaultApplicationTypes = []string{"Service"}

// ServiceMapConfig holds configuration for service map generation
type ServiceMapConfig struct {
	MaxTraceIDsForExpansion   int
	MaxTraceIDsInDrillDown    int
	WorkloadQueryLimit        int
	DefaultQueryLimit         int
	DefaultFallbackDuration   float64
	NanosecondsToMilliseconds float64
}

// DefaultServiceMapConfig returns default configuration
func DefaultServiceMapConfig() *ServiceMapConfig {
	return &ServiceMapConfig{
		MaxTraceIDsForExpansion:   MaxTraceIDsForExpansion,
		MaxTraceIDsInDrillDown:    MaxTraceIDsInDrillDown,
		WorkloadQueryLimit:        WorkloadQueryLimit,
		DefaultQueryLimit:         DefaultQueryLimit,
		DefaultFallbackDuration:   DefaultFallbackDuration,
		NanosecondsToMilliseconds: NanosecondsToMilliseconds,
	}
}

type TraceSpan struct {
	AccountID                    string            `json:"account_id"`
	DestinationName              string            `json:"destination_name"`
	DestinationWorkloadName      string            `json:"destination_workload_name"`
	DestinationWorkloadNamespace string            `json:"destination_workload_namespace"`
	DurationNs                   float64           `json:"duration_ns"`
	Headers                      string            `json:"headers"`
	HTTPResponse                 string            `json:"http_response"`
	HTTPStatusCode               string            `json:"http_status_code"`
	ParentSpanID                 string            `json:"parent_span_id"`
	RequestPayload               string            `json:"request_payload"`
	Resource                     string            `json:"resource"`
	SpanID                       string            `json:"span_id"`
	SpanName                     string            `json:"span_name"`
	SpanAttributes               map[string]string `json:"SpanAttributes"`
	StatusCode                   string            `json:"status_code"`
	TenantID                     string            `json:"tenant_id"`
	Timestamp                    string            `json:"timestamp"`
	TraceID                      string            `json:"trace_id"`
	TraceSource                  string            `json:"trace_source"`
	WorkloadName                 string            `json:"workload_name"`
	WorkloadNamespace            string            `json:"workload_namespace"`
}

type SpanAttributes struct {
	ServiceName          string `json:"service.name"`
	SpanKind             string `json:"span.kind"`
	DBSystem             string `json:"db.system"`
	DBHost               string `json:"db.host"`
	DBName               string `json:"db.name"`
	MessagingSystem      string `json:"messaging.system"`
	MessagingDestination string `json:"messaging.destination"`
	NetPeerName          string `json:"net.peer.name"`
	NetPeerPort          int    `json:"net.peer.port"`
	HTTPMethod           string `json:"http.method"`
	HTTPHost             string `json:"http.host"`
	HTTPRoute            string `json:"http.route"`
	HTTPStatusCode       int    `json:"http.status_code"`
	DeploymentEnv        string `json:"deployment.environment"`
	K8sCluster           string `json:"k8s_cluster"`

	// RawAttributes holds all attributes as strings for flexible access
	RawAttributes map[string]string `json:"-"`
}

type ServiceDependency struct {
	Source        string  `json:"source"`
	Target        string  `json:"target"`
	CallCount     int64   `json:"call_count"`
	TotalDuration float64 `json:"total_duration_ns"`
	AvgDuration   float64 `json:"avg_duration_ms"`
	ErrorCount    int64   `json:"error_count"`
	ErrorRate     float64 `json:"error_rate"`
	Protocol      string  `json:"protocol"`
	Environment   string  `json:"environment"`
	// Enhanced metadata for drill-down
	TraceIds       []string         `json:"trace_ids,omitempty"`
	FailedTraceIds []string         `json:"failed_trace_ids,omitempty"`
	Operations     map[string]int64 `json:"operations,omitempty"`
	StatusCodes    map[int]int64    `json:"status_codes,omitempty"`
	ErrorTypes     map[string]int64 `json:"error_types,omitempty"`
	// Track how this dependency was detected for better filter hints
	DependencyType string `json:"dependency_type,omitempty"` // "direct_service", "net_peer", "db_connection"
	OriginalTarget string `json:"original_target,omitempty"` // Original target before transformation
}

type ServiceCategory struct {
	Category string `json:"category"`
}

type NodeStats struct {
	Latency           float64 `json:"Latency"`
	RequestsPerSecond float64 `json:"RequestCount"`
	FailureCount      float64 `json:"FailureCount"`
}

type ServiceApplication struct {
	Id                ServiceApplicationId `json:"Id"`
	Category          ServiceCategory      `json:"Category"`
	Labels            map[string]string    `json:"Labels"`
	Status            *int                 `json:"Status"`
	Indicators        []string             `json:"Indicators"`
	Upstreams         []UpstreamLink       `json:"Upstreams"`
	Downstreams       []DownstreamLink     `json:"Downstreams"`
	Instances         []Instance           `json:"Instances"`
	Type              []string             `json:"Type"`
	DesiredInstances  int                  `json:"DesiredInstances"`
	FailedInstances   int                  `json:"FailedInstances"`
	OOMKills          int                  `json:"OOMKills"`
	Restarts          int                  `json:"Restarts"`
	CPUThrottlingTime float64              `json:"CPUThrottlingTime"`
	VolumeSize        float64              `json:"VolumeSize"`
	VolumeUsed        float64              `json:"VolumeUsed"`
	IsHealthy         bool                 `json:"IsHealthy"`
	HealthReason      string               `json:"HealthReason"`
	NodeStats         *NodeStats           `json:"NodeStats,omitempty"`
}

type ServiceApplicationId struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
}

type Instance struct {
	Id       ServiceApplicationId `json:"id"`
	IsFailed bool                 `json:"is_failed"`
}

type UpstreamLink struct {
	Id            string         `json:"Id"`
	Status        int            `json:"Status"`
	Stats         []string       `json:"Stats"`
	Weight        float64        `json:"Weight"`
	Latency       float64        `json:"Latency"`
	RequestCount  float64        `json:"RequestCount"`
	FailureCount  float64        `json:"FailureCount"`
	Protocol      string         `json:"Protocol"`
	BytesSent     float64        `json:"BytesSent"`
	BytesReceived float64        `json:"BytesReceived"`
	DrillDown     *LinkDrillDown `json:"DrillDown,omitempty"`
}

type DownstreamLink struct {
	Id            ServiceApplicationId `json:"Id"`
	Status        int                  `json:"Status"`
	Stats         []string             `json:"Stats"`
	Weight        float64              `json:"Weight"`
	Latency       float64              `json:"Latency"`
	RequestCount  float64              `json:"RequestCount"`
	FailureCount  float64              `json:"FailureCount"`
	Protocol      string               `json:"Protocol"`
	BytesSent     float64              `json:"BytesSent"`
	BytesReceived float64              `json:"BytesReceived"`
	DrillDown     *LinkDrillDown       `json:"DrillDown,omitempty"`
}

type LinkDrillDown struct {
	TimeRange       TimeRange        `json:"time_range"`
	ErrorTypes      []ErrorSummary   `json:"error_types,omitempty"`
	HTTPStatusCodes []StatusCodeStat `json:"http_status_codes,omitempty"`
	SampleTraceIds  []string         `json:"sample_trace_ids,omitempty"`
	FailedTraceIds  []string         `json:"failed_trace_ids,omitempty"`
	Operations      []OperationStat  `json:"operations,omitempty"`
	FilterHints     FilterHints      `json:"filter_hints"`
}

type TimeRange struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

type ErrorSummary struct {
	Type        string  `json:"type"` // "HTTP_ERROR", "TIMEOUT", "REDIS_ERROR", etc.
	Count       int64   `json:"count"`
	Percentage  float64 `json:"percentage"`
	StatusCodes []int   `json:"status_codes,omitempty"`
}

type StatusCodeStat struct {
	StatusCode int     `json:"status_code"`
	Count      int64   `json:"count"`
	Percentage float64 `json:"percentage"`
}

type OperationStat struct {
	Operation  string  `json:"operation"` // e.g., "GET /api/users", "Redis GET"
	Count      int64   `json:"count"`
	AvgLatency float64 `json:"avg_latency_ms"`
	ErrorCount int64   `json:"error_count"`
}

type FilterHints struct {
	SourceService        string            `json:"source_service"`
	TargetService        string            `json:"target_service,omitempty"`
	Protocol             string            `json:"protocol"`
	Operations           []string          `json:"operations,omitempty"`
	ErrorStatusCodes     []int             `json:"error_status_codes,omitempty"`
	SpanAttributeFilters map[string]string `json:"span_attribute_filters,omitempty"`
}

type ServiceMap struct {
	Applications []ServiceApplication       `json:"applications"`
	GeneratedAt  time.Time                  `json:"generated_at"`
	K8sMetadata  *K8sInfrastructureMetadata `json:"k8s_metadata,omitempty"` // K8s infrastructure extracted from traces
	Labels       []string                   `json:"labels"`
}

// K8sInfrastructureMetadata holds K8s infrastructure information extracted from traces
type K8sInfrastructureMetadata struct {
	Clusters   map[string]*K8sClusterInfo   `json:"clusters,omitempty"`
	Namespaces map[string]*K8sNamespaceInfo `json:"namespaces,omitempty"`
	Pods       map[string]*K8sPodInfo       `json:"pods,omitempty"`
	Nodes      map[string]*K8sNodeInfo      `json:"nodes,omitempty"`
}

// K8sClusterInfo contains information about a K8s cluster
type K8sClusterInfo struct {
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
}

// K8sNamespaceInfo contains information about a K8s namespace
type K8sNamespaceInfo struct {
	Name        string `json:"name"`
	Cluster     string `json:"cluster,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// K8sPodInfo contains information about a K8s pod
type K8sPodInfo struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace,omitempty"`
	Node        string `json:"node,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// K8sNodeInfo contains information about a K8s worker node
type K8sNodeInfo struct {
	Name        string `json:"name"`
	Cluster     string `json:"cluster,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type LabelFilter struct {
	Key      string                      `json:"key"`
	Value    string                      `json:"value"`
	Operator query.BinaryWhereClauseType `json:"operator"`
}

// TraceQueryParams holds parameters for trace fetching
type TraceQueryParams struct {
	WorkloadName      string
	WorkloadNamespace string
	StartTime         time.Time
	EndTime           time.Time
	AccountID         string
	LabelFilters      []LabelFilter // Key-value pairs for filtering span attributes
	ExcludeFilters    []LabelFilter // External services matching these filters will be excluded from service map
	UpstreamOnly      bool          // If true, only show upstream services (callers) of the target service
}
