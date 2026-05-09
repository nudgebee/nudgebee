package metrics

import (
	"log/slog"
	"nudgebee/collector/otel/security"
)

// MetricsResponse encapsulates the data returned by a metrics reader method.
// This structure allows implementations to return all necessary details
// for constructing an HTTP response.
type MetricsResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
	Error       error
}

// QueryParams defines parameters for the /api/v1/query endpoint.
type QueryParams struct {
	Query   string // PromQL query string
	Time    string // Optional: Evaluation timestamp (RFC3339 or Unix timestamp)
	Timeout string // Optional: Evaluation timeout (Prometheus duration string)
}

// QueryRangeParams defines parameters for the /api/v1/query_range endpoint.
type QueryRangeParams struct {
	Query   string // PromQL query string
	Start   string // Start timestamp (RFC3339 or Unix timestamp)
	End     string // End timestamp (RFC3339 or Unix timestamp)
	Step    string // Query resolution step width (Prometheus duration string or float)
	Timeout string // Optional: Evaluation timeout (Prometheus duration string)
}

// SeriesParams defines parameters for the /api/v1/series endpoint.
type SeriesParams struct {
	Matchers []string // Repeated series selector argument 'match[]'
	Start    string   // Optional: Start timestamp (RFC3339 or Unix timestamp)
	End      string   // Optional: End timestamp (RFC3339 or Unix timestamp)
	Limit    string   // Optional: Limit parameter, if supported by the backend
}

// LabelsParams defines parameters for the /api/v1/labels endpoint (to fetch label names).
type LabelsParams struct {
	Matchers []string // Optional: Repeated series selector 'match[]' to filter results
	Start    string   // Optional: Start timestamp (RFC3339 or Unix timestamp)
	End      string   // Optional: End timestamp (RFC3339 or Unix timestamp)
	Limit    string   // Optional: Limit parameter, if supported by the backend
}

// LabelValuesParams defines parameters for the /api/v1/label/<label_name>/values endpoint.
type LabelValuesParams struct {
	// LabelName is passed as a direct argument to the interface method.
	Matchers []string // Optional: Repeated series selector 'match[]' to filter results
	Start    string   // Optional: Start timestamp (RFC3339 or Unix timestamp)
	End      string   // Optional: End timestamp (RFC3339 or Unix timestamp)
	Limit    string   // Optional: Limit parameter, if supported by the backend
}

// FormatQueryParams defines parameters for the /api/v1/format_query endpoint.
type FormatQueryParams struct {
	Query string // PromQL query string
}

// MetricsReader defines an interface for reading metrics from a backend.
// Queries are expected to be Prometheus-compatible.
// Each method corresponds to a specific Prometheus API endpoint type.
//
// 'agentDetail' provides necessary security and tenancy context.
// 'logger' is for structured logging.
type MetricsReader interface {
	Query(agentDetail security.Account, logger *slog.Logger, params QueryParams) MetricsResponse
	QueryRange(agentDetail security.Account, logger *slog.Logger, params QueryRangeParams) MetricsResponse
	Series(agentDetail security.Account, logger *slog.Logger, params SeriesParams) MetricsResponse
	Labels(agentDetail security.Account, logger *slog.Logger, params LabelsParams) MetricsResponse
	LabelValues(agentDetail security.Account, logger *slog.Logger, labelName string, params LabelValuesParams) MetricsResponse
	FormatQuery(agentDetail security.Account, logger *slog.Logger, params FormatQueryParams) MetricsResponse
}
