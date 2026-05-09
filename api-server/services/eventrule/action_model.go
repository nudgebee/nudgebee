package eventrule

type AlertActionTemplate struct {
	Name        string         `json:"name"`
	Params      map[string]any `json:"params"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	ActionName  string         `json:"action_name"`
	Source      *string        `json:"source"`
	AlertType   *string        `json:"alert_type"`
}

type ListActionsRequest struct {
	CloudAccountId string `json:"cloud_account_id"`
	Query          string `json:"query"`
	Source         string `json:"source"`
	AlertType      string `json:"alert_type"`
}

// PrometheusMetric represents key-value labels for a Prometheus series
type PrometheusMetric map[string]string

// PrometheusSeries represents a time-series data structure
type PrometheusSeries struct {
	Metric     PrometheusMetric `json:"metric"`
	Timestamps []float64        `json:"timestamps"`
	Values     []string         `json:"values"`
}

// PrometheusVector represents a single vector result
type PrometheusVector struct {
	Metric PrometheusMetric      `json:"metric"`
	Value  PrometheusScalarValue `json:"value"` // Usually [timestamp, value]
}

// PrometheusScalarValue represents a scalar value in Prometheus
type PrometheusScalarValue struct {
	Timestamp float64 `json:"timestamp"`
	Value     string  `json:"value"`
}

// PrometheusQueryResult represents the result from a Prometheus query
type PrometheusQueryResult struct {
	ResultType       string                 `json:"result_type"`
	VectorResult     []PrometheusVector     `json:"vector_result,omitempty"`
	SeriesListResult []PrometheusSeries     `json:"series_list_result,omitempty"`
	ScalarResult     *PrometheusScalarValue `json:"scalar_result,omitempty"`
	StringResult     *string                `json:"string_result,omitempty"`
}

var PrometheusLabelCategoryMapping = map[string]string{
	"Deployment":  "deployment",
	"DaemonSet":   "daemonset",
	"StatefulSet": "statefulset",
	"ReplicaSet":  "replicaset",
	"Job":         "job_name",
	"Pod":         "pod",
	"HPA":         "horizontalpodautoscaler",
	"PVC":         "persistentvolumeclaim",
	"Node":        "node",
}
