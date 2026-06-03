package ml

import "time"

type MetricsRequest struct {
	Deployment string `json:"deployment" mapstructure:"deployment" validate:"required"`
	Namespace  string `json:"namespace" mapstructure:"namespace" validate:"required"`
	Account    string `json:"account" mapstructure:"account" validate:"required"`
}

type MetricsResponse struct {
	Metrics []MetricsResponseMetrics `json:"metrics" mapstructure:"metrics"`
}

type MetricsResponseMetrics struct {
	Cpu       float32 `json:"cpu" mapstructure:"cpu"`
	Latency   float32 `json:"latency" mapstructure:"latency"`
	Memory    float32 `json:"memory" mapstructure:"memory"`
	Replicas  float32 `json:"replicas" mapstructure:"replicas"`
	Rps       float32 `json:"rps" mapstructure:"rps"`
	Timestamp int     `json:"timestamp" mapstructure:"timestamp"`
}

type RecommendationRequest struct {
	Namespace             string `json:"namespace" mapstructure:"namespace"`
	Deployment            string `json:"deployment" mapstructure:"deployment"`
	Container             string `json:"container" mapstructure:"container"`
	ResourceId            string `json:"resource_id" mapstructure:"resource_id"`
	Account               string `json:"account" mapstructure:"account"`
	Tenant                string `json:"tenant" mapstructure:"tenant"`
	PersistRecommendation bool   `json:"persist_recommendation" mapstructure:"persist_recommendation"`
}

type NodeRecommendationRequest struct {
	Account                 string   `json:"account" mapstructure:"account"`
	Tenant                  string   `json:"tenant" mapstructure:"tenant"`
	BufferPercentage        int      `json:"buffer_percentage" mapstructure:"buffer_percentage"`
	NumberOfRecommendations int      `json:"number_of_recommendations" mapstructure:"number_of_recommendations"`
	MinNodes                int      `json:"min_nodes" mapstructure:"min_nodes"`
	MinCPUPerNode           int      `json:"min_cpu_per_node" mapstructure:"min_cpu_per_node"`
	MinMemoryPerNode        int      `json:"min_memory_per_node" mapstructure:"min_memory_per_node"`
	PreferredInstanceGroups []string `json:"preferred_instance_groups" mapstructure:"preferred_instance_groups"`
	Graviton                bool     `json:"graviton" mapstructure:"graviton"`
}

type RecommendationResponse struct {
	Kind           string                               `json:"kind" mapstructure:"kind"`
	Metadata       RecommendationResponseMetadata       `json:"metadata" mapstructure:"metadata"`
	Recommendation RecommendationResponseRecommendation `json:"recommendation" mapstructure:"recommendation"`
}

type RecommendationResponseRecommendation struct {
	Allocated       []RecommendationResponseReplica `json:"allocated" mapstructure:"allocated"`
	Recommended     []RecommendationResponseReplica `json:"recommended" mapstructure:"recommended"`
	RecommendedType string                          `json:"recommended_type" mapstructure:"recommended_type"`
	Resource        string                          `json:"resource" mapstructure:"resource"`
	Info            string                          `json:"info" mapstructure:"info"`
}

type RecommendationResponseReplica struct {
	Replicas  int    `json:"replicas" mapstructure:"replicas"`
	Timestamp string `json:"timestamp" mapstructure:"timestamp"`
}

type RecommendationResponseMetadata struct {
	Name      string `json:"name" mapstructure:"name"`
	Namespace string `json:"namespace" mapstructure:"namespace"`
}

type InstanceType struct {
	Cost           float64  `json:"cost" mapstructure:"cost"`
	InstanceTypes  []string `json:"instance_types" mapstructure:"instance_types"`
	NetworkProfile []string `json:"network_profile" mapstructure:"network_profile"`
	NumberOfNodes  int      `json:"number_of_nodes" mapstructure:"number_of_nodes"`
	Region         string   `json:"region" mapstructure:"region"`
	ReservedCPU    float64  `json:"reserved_cpu" mapstructure:"reserved_cpu"`
	ReservedMemory float64  `json:"reserved_memory" mapstructure:"reserved_memory"`
	TotalCPU       float64  `json:"total_cpu" mapstructure:"total_cpu"`
	TotalMemory    float64  `json:"total_memory" mapstructure:"total_memory"`
	Graviton       bool     `json:"graviton" mapstructure:"graviton"`
}

type NodeRecommendationResponse struct {
	CurrentInstanceType     InstanceType   `json:"current_instance_type" mapstructure:"current_instance_type"`
	RecommendedInstanceType []InstanceType `json:"recommended_instance_type" mapstructure:"recommended_instance_type"`
}

type AnomalyRequest struct {
	Namespace               string     `json:"namespace" mapstructure:"namespace" validate:"required"`
	Deployment              string     `json:"deployment" mapstructure:"deployment" validate:"required"`
	Tenant                  string     `json:"tenant" mapstructure:"tenant" validate:"required"`
	Account                 string     `json:"account" mapstructure:"account" validate:"required"`
	Type                    string     `json:"type" mapstructure:"type" validate:"required"`
	StartTime               *time.Time `json:"start_time" mapstructure:"start_time"`
	EndTime                 *time.Time `json:"end_time" mapstructure:"end_time"`
	EvaluationPeriodMinutes *int       `json:"evaluation_period" mapstructure:"evaluation_period"`
}

type AnomalyResponseMetrics struct {
	Anomaly      bool    `json:"anomaly" mapstructure:"anomaly"`
	AnomalyScore float64 `json:"anomaly_score" mapstructure:"anomaly_score"`
	Data         float64 `json:"data" mapstructure:"data"`
	Timestamp    string  `json:"timestamp" mapstructure:"timestamp"`
}

type AnomalyInsight struct {
	Timestamp         string  `json:"timestamp" mapstructure:"timestamp"`
	Value             float64 `json:"value" mapstructure:"value"`
	BaselineValue     float64 `json:"baseline_value" mapstructure:"baseline_value"`
	DeviationAbsolute float64 `json:"deviation_absolute" mapstructure:"deviation_absolute"`
	DeviationPercent  float64 `json:"deviation_percent" mapstructure:"deviation_percent"`
	Severity          string  `json:"severity" mapstructure:"severity"`
	AnomalyScore      float64 `json:"anomaly_score" mapstructure:"anomaly_score"`
	ComparisonWindow  string  `json:"comparison_window" mapstructure:"comparison_window"`
}

type AnomalyResponse struct {
	Namespace           string                   `json:"namespace" mapstructure:"namespace"`
	Deployment          string                   `json:"deployment" mapstructure:"deployment"`
	Tenant              string                   `json:"tenant" mapstructure:"tenant"`
	Account             string                   `json:"account" mapstructure:"account"`
	Type                string                   `json:"anomaly_type" mapstructure:"anomaly_type"`
	EndTime             string                   `json:"end_time" mapstructure:"end_time"`
	StartTime           string                   `json:"start_time" mapstructure:"start_time"`
	HasAnomaly          bool                     `json:"has_anomaly" mapstructure:"has_anomaly"`
	Stats               map[string]any           `json:"stats" mapstructure:"stats"`
	TriggerThresholdMax *float64                 `json:"trigger_threshold_max" mapstructure:"trigger_threshold_max"`
	ScoresThreshold     *float64                 `json:"scores_threshold" mapstructure:"scores_threshold"`
	EvaluationPeriod    *int                     `json:"evaluation_period" mapstructure:"evaluation_period"`
	Data                []AnomalyResponseMetrics `json:"data" mapstructure:"data"`
	Pod                 string                   `json:"pod" mapstructure:"pod"`
	TrainingEndTime     *string                  `json:"training_end_time" mapstructure:"training_end_time"`
	Insights            []AnomalyInsight         `json:"insights" mapstructure:"insights"`
}

type MetricAnomalyDetectRequest struct {
	Namespace             string `json:"namespace" mapstructure:"namespace"`
	Deployment            string `json:"deployment" mapstructure:"deployment"`
	Account               string `json:"account" mapstructure:"account" validate:"required"`
	Tenant                string `json:"tenant" mapstructure:"tenant" validate:"required"`
	Query                 string `json:"query" mapstructure:"query" validate:"required"`
	AnalysisStartTime     string `json:"analysis_start_time" mapstructure:"analysis_start_time" validate:"required"`
	AnalysisEndTime       string `json:"analysis_end_time" mapstructure:"analysis_end_time" validate:"required"`
	HistoricalWindowHours int    `json:"historical_window_hours" mapstructure:"historical_window_hours"`
	IncludeHistoricalData bool   `json:"include_historical_data" mapstructure:"include_historical_data"`
}

type MetricAnomalyDetectResponse struct {
	Account             string                   `json:"account" mapstructure:"account"`
	AnomalyType         string                   `json:"anomaly_type" mapstructure:"anomaly_type"`
	Deployment          string                   `json:"deployment" mapstructure:"deployment"`
	Namespace           string                   `json:"namespace" mapstructure:"namespace"`
	Query               string                   `json:"query" mapstructure:"query"`
	StartTime           string                   `json:"start_time" mapstructure:"start_time"`
	EndTime             string                   `json:"end_time" mapstructure:"end_time"`
	EvaluationPeriod    *int                     `json:"evaluation_period" mapstructure:"evaluation_period"`
	HasAnomaly          bool                     `json:"has_anomaly" mapstructure:"has_anomaly"`
	ScoresThreshold     *float64                 `json:"scores_threshold" mapstructure:"scores_threshold"`
	TriggerThresholdMax *float64                 `json:"trigger_threshold_max" mapstructure:"trigger_threshold_max"`
	Stats               map[string]any           `json:"stats" mapstructure:"stats"`
	Data                []AnomalyResponseMetrics `json:"data" mapstructure:"data"`
	HistoricalData      []map[string]any         `json:"historical_data" mapstructure:"historical_data"`
}

// VerticalRightsizingRequest represents the request to ml-k8s-server /rightsizing/vertical
type VerticalRightsizingRequest struct {
	AccountId             string   `json:"account_id" validate:"required"`
	TenantId              string   `json:"tenant_id" validate:"required"`
	Namespace             string   `json:"namespace,omitempty"`
	ResourceNames         []string `json:"resource_names,omitempty"`
	PersistRecommendation bool     `json:"persist_recommendation"`
	BatchByNamespace      bool     `json:"batch_by_namespace"`
	MaxRecommendations    *int     `json:"max_recommendations,omitempty"`
	MetricsProvider       string   `json:"metrics_provider,omitempty"`
	DatadogApiKey         string   `json:"datadog_api_key,omitempty"`
	DatadogAppKey         string   `json:"datadog_app_key,omitempty"`
	DatadogSite           string   `json:"datadog_site,omitempty"`
}

// VerticalRightsizingResponse represents the async acknowledgment from ml-k8s-server
type VerticalRightsizingResponse struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
	Namespace string `json:"namespace,omitempty"`
}

// VolumeRightsizingRequest represents the request to ml-k8s-server /rightsizing/volume
type VolumeRightsizingRequest struct {
	AccountId             string `json:"account" validate:"required"`
	TenantId              string `json:"tenant" validate:"required"`
	Namespace             string `json:"namespace,omitempty"`
	PersistRecommendation bool   `json:"persist_recommendation"`
	MaxRecommendations    *int   `json:"max_recommendations,omitempty"`
	MetricsProvider       string `json:"metrics_provider,omitempty"`
	DatadogApiKey         string `json:"datadog_api_key,omitempty"`
	DatadogAppKey         string `json:"datadog_app_key,omitempty"`
	DatadogSite           string `json:"datadog_site,omitempty"`
}

// VolumeRightsizingResponse represents the async acknowledgment from ml-k8s-server
type VolumeRightsizingResponse struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	AccountId string `json:"account"`
	TenantId  string `json:"tenant"`
	Namespace string `json:"namespace,omitempty"`
}
