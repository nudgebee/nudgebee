package relay

type ActionExecuteBody struct {
	AccountID    string         `json:"account_id" validate:"required"`
	ActionName   string         `json:"action_name" validate:"required"`
	ActionParams map[string]any `json:"action_params"`
	Origin       string         `json:"origin,omitempty"`
	NoSinks      bool           `json:"no_sinks,omitempty"`
}

type RelayExecuteRequest struct {
	Body           ActionExecuteBody `json:"body" validate:"required"`
	NoSinks        bool              `json:"no_sinks,omitempty"`
	Cache          bool              `json:"cache,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	AgentType      string            `json:"-"` // passed via X-NB-Agent-Type header, not body
}

type ApplicationStatsRequest struct {
	Name      string `json:"name" validate:"required"`
	Namespace string `json:"namespace" validate:"required"`
}

type ApplicationStatsResponse struct {
	ApplicationId       string             `json:"application_id" validate:"required"`
	Name                string             `json:"name" validate:"required"`
	Namespace           string             `json:"namespace" validate:"required"`
	Container           string             `json:"container" validate:"required"`
	MaxCpuRequest       float64            `json:"max_cpu_request,omitempty"`
	MaxMemoryRequest    float64            `json:"max_memory_request,omitempty"`
	MaxCpuLimit         float64            `json:"max_cpu_limit,omitempty"`
	MaxMemoryLimit      float64            `json:"max_memory_limit,omitempty"`
	Latency             float64            `json:"latency,omitempty"`
	LatencyP99          float64            `json:"latency_p99,omitempty"`
	CpuP99              float64            `json:"cpu_p99,omitempty"`
	CpuP50              float64            `json:"cpu_p50,omitempty"`
	CpuMax              float64            `json:"cpu_max,omitempty"`
	MemoryMax           float64            `json:"memory_max,omitempty"`
	MemoryP99           float64            `json:"memory_p99,omitempty"`
	MemoryP50           float64            `json:"memory_p50,omitempty"`
	LogFailureCount     int                `json:"log_failure_count,omitempty"`
	TotalRequestCount   int                `json:"total_request_count,omitempty"`
	FailureRequestCount int                `json:"failure_request_count,omitempty"`
	BadDataCount        int                `json:"bad_data_count,omitempty"`
	GoodDataCount       int                `json:"good_data_count,omitempty"`
	ValidDataCount      int                `json:"valid_data_count,omitempty"`
	OomKillLimit        int                `json:"oom_kill_limit,omitempty"`
	OtherMetrics        map[string]float64 `json:"other_metrics,omitempty"`
}

type AlertEvent struct {
	ExternalUrl       string            `json:"externalURL" validate:"required"`
	GroupKey          string            `json:"groupKey" validate:"required"`
	Version           string            `json:"version" validate:"required"`
	CommonAnnotations map[string]string `json:"commonAnnotations,omitempty"`
	CommonLabels      map[string]string `json:"commonLabels,omitempty"`
	GroupLabels       map[string]string `json:"groupLabels,omitempty"`
	Receiver          string            `json:"receiver" validate:"required"`
	Status            string            `json:"status" validate:"required"`
	Alerts            []Alert           `json:"alerts" validate:"required"`
}

type Alert struct {
	EndsAt        string            `json:"endsAt" validate:"required"`
	GenerationUrl string            `json:"generatorURL" validate:"required"`
	StartsAt      string            `json:"startsAt" validate:"required"`
	Fingerprint   string            `json:"fingerprint,omitempty"`
	Labels        map[string]string `json:"labels" validate:"required"`
	Annotations   map[string]string `json:"annotations" validate:"required"`
	Status        string            `json:"status" validate:"required"`
}

const (
	PodLogsEnricherActionName        = "logs_enricher"
	PodEnricherActionName            = "pod_enricher"
	PodNodeMetricsEnricherActionName = "pod_node_metrics_enricher"
	PodMetricsEnricherActionName     = "pod_metric_enricher"
	PodNodeEventsEnricherActionName  = "pod_node_event_enricher"
	WorkloadTracesEnricherActionName = "api_traces_enricher_v2"
	ServiceMapActionName             = "service_map"
)
