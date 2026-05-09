package prometheus

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/collector/otel/common"
	"nudgebee/collector/otel/metrics"
	"nudgebee/collector/otel/security"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// OTEL_NB_ACCOUNT_ID is the label name for the Nudgebee account ID.
const OTEL_NB_ACCOUNT_ID = "nb_account_id" // Standardized to snake_case for Prometheus labels

// OTEL_NB_TENANT_ID is the label name for the Nudgebee tenant ID.
const OTEL_NB_TENANT_ID = "nb_tenant_id" // Standardized to snake_case for Prometheus labels

const (
	apiPathQuery       = "/api/v1/query"
	apiPathQueryRange  = "/api/v1/query_range"
	apiPathSeries      = "/api/v1/series"
	apiPathLabels      = "/api/v1/labels"
	apiPathLabelValues = "/api/v1/label/%s/values" // Needs Sprintf for labelName
	apiPathFormatQuery = "/api/v1/format_query"
)

// Reader implements the metrics.MetricsReader interface for Prometheus.
type Reader struct {
	endpoint string
}

// NewReader creates a new Prometheus metrics reader.
func NewReader(endpoint string) *Reader {
	return &Reader{endpoint: endpoint}
}

// buildAndExecuteProxiedRequest constructs the Prometheus query, applies tenancy, and executes it.
func (r *Reader) buildAndExecuteProxiedRequest(
	agentDetail security.Account,
	logger *slog.Logger,
	apiSubPath string, // e.g., "/api/v1/query"
	// queryParams contains all necessary parameters for the specific request type, already processed.
	// It's up to the caller to ensure 'query' or 'match[]' are correctly set if needed.
	queryParams url.Values,
	requestType string, // "query", "query_range", "series", "labels", "label_values", "format_query"
) metrics.MetricsResponse {

	accountIdMatcher := labels.Matcher{
		Type:  labels.MatchEqual,
		Name:  OTEL_NB_ACCOUNT_ID,
		Value: agentDetail.AccountId,
	}
	tenantIdMatcher := labels.Matcher{
		Type:  labels.MatchEqual,
		Name:  OTEL_NB_TENANT_ID,
		Value: agentDetail.TenantId,
	}

	finalQueryValues := url.Values{}

	// Create a new request
	// For POST requests, body should be handled, but Prometheus API primarily uses GET or POST with form data.
	// Assuming GET for simplicity here, as original code used GET.
	// If POST is needed for some endpoints with body, this needs adjustment.
	req, err := http.NewRequest("GET", r.endpoint+apiSubPath, nil)
	if err != nil {
		logger.Error("metrics: unable to create request for forwarding", "error", err)
		return metrics.MetricsResponse{StatusCode: http.StatusInternalServerError, Error: fmt.Errorf("metrics: internal error creating request: %w", err)}
	}

	// Apply tenancy enforcement and copy relevant parameters
	switch requestType {
	case "query", "query_range":
		enforcer := NewPromQLEnforcer(true, &accountIdMatcher, &tenantIdMatcher)
		promql := queryParams.Get("query")
		if promql == "" {
			logger.Error("metrics: query parameter not found for "+requestType, "params", queryParams)
			return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: errors.New("metrics: query parameter is missing")}
		}

		promql, err = enforcer.Enforce(promql)
		if err != nil {
			logger.Error("metrics: unable to enforce query", "error", err, "original_query", queryParams.Get("query"))
			return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("metrics: invalid query: %w", err)}
		}
		finalQueryValues.Set("query", promql)
		// Copy other relevant params for query/query_range
		copyUrlValues([]string{"time", "timeout", "start", "end", "step"}, queryParams, finalQueryValues)

	case "format_query":
		finalQueryValues.Set("query", queryParams.Get("query")) // No tenancy enforcement on format_query by default

	case "series", "labels", "label_values":
		// Copy other relevant params for series/labels/label_values
		copyUrlValues([]string{"start", "end", "limit"}, queryParams, finalQueryValues)

		userMatchers := queryParams["match[]"] // Get all 'match[]' values
		p := parser.NewParser(parser.Options{})
		if len(userMatchers) > 0 {
			for _, s := range userMatchers {
				ms, err := p.ParseMetricSelector(s)
				if err != nil {
					logger.Error("metrics: unable to parse metric selector", "error", err, "matcher_string", s)
					return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("metrics: invalid 'match[]' parameter '%s': %w", s, err)}
				}
				ms = append(ms, &accountIdMatcher, &tenantIdMatcher)
				finalQueryValues.Add("match[]", matchersToString(ms...))
			}
		} else {
			// If no match[] is provided, enforce tenancy by default
			finalQueryValues.Set("match[]", matchersToString(&accountIdMatcher, &tenantIdMatcher))
		}
	default:
		logger.Error("metrics: unknown request type for parameter processing", "type", requestType)
		return metrics.MetricsResponse{StatusCode: http.StatusInternalServerError, Error: errors.New("metrics: internal configuration error")}
	}

	req.URL.RawQuery = finalQueryValues.Encode()
	logger.Debug("metrics: forwarding request to prometheus", "url", req.URL.String(), "type", requestType)

	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("metrics: unable to execute query against backend", "error", err, "target_url", req.URL.String())
		return metrics.MetricsResponse{StatusCode: http.StatusBadGateway, Error: errors.New("metrics: failed to connect to metrics backend")}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("metrics: unable to read response body from backend", "error", err)
		return metrics.MetricsResponse{StatusCode: http.StatusInternalServerError, Error: errors.New("metrics: failed to read response from backend")}
	}

	return metrics.MetricsResponse{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        respBody,
	}
}

// copyUrlValues copies specified keys from src url.Values to dst url.Values.
func copyUrlValues(keys []string, src url.Values, dst url.Values) {
	for _, k := range keys {
		if vals, ok := src[k]; ok {
			// For simplicity, taking the first value if multiple are present for these params.
			// Prometheus typically expects single values for params like 'time', 'start', 'step'.
			if len(vals) > 0 {
				dst.Set(k, vals[0])
			} else {
				dst.Set(k, "") // Explicitly set empty if present but valueless
			}
		}
	}
}

// Query implements the MetricsReader interface.
func (r *Reader) Query(agentDetail security.Account, logger *slog.Logger, params metrics.QueryParams) metrics.MetricsResponse {
	uv := url.Values{}
	uv.Set("query", params.Query)
	if params.Time != "" {
		uv.Set("time", params.Time)
	}
	if params.Timeout != "" {
		uv.Set("timeout", params.Timeout)
	}
	return r.buildAndExecuteProxiedRequest(agentDetail, logger, apiPathQuery, uv, "query")
}

// QueryRange implements the MetricsReader interface.
func (r *Reader) QueryRange(agentDetail security.Account, logger *slog.Logger, params metrics.QueryRangeParams) metrics.MetricsResponse {
	uv := url.Values{}
	uv.Set("query", params.Query)
	uv.Set("start", params.Start)
	uv.Set("end", params.End)
	uv.Set("step", params.Step)
	if params.Timeout != "" {
		uv.Set("timeout", params.Timeout)
	}
	return r.buildAndExecuteProxiedRequest(agentDetail, logger, apiPathQueryRange, uv, "query_range")
}

// Series implements the MetricsReader interface.
func (r *Reader) Series(agentDetail security.Account, logger *slog.Logger, params metrics.SeriesParams) metrics.MetricsResponse {
	uv := url.Values{}
	for _, m := range params.Matchers {
		uv.Add("match[]", m)
	}
	if params.Start != "" {
		uv.Set("start", params.Start)
	}
	if params.End != "" {
		uv.Set("end", params.End)
	}
	if params.Limit != "" {
		uv.Set("limit", params.Limit)
	}
	return r.buildAndExecuteProxiedRequest(agentDetail, logger, apiPathSeries, uv, "series")
}

// Labels implements the MetricsReader interface (for /api/v1/labels).
func (r *Reader) Labels(agentDetail security.Account, logger *slog.Logger, params metrics.LabelsParams) metrics.MetricsResponse {
	uv := url.Values{}
	for _, m := range params.Matchers {
		uv.Add("match[]", m)
	}
	if params.Start != "" {
		uv.Set("start", params.Start)
	}
	if params.End != "" {
		uv.Set("end", params.End)
	}
	if params.Limit != "" {
		uv.Set("limit", params.Limit)
	}
	return r.buildAndExecuteProxiedRequest(agentDetail, logger, apiPathLabels, uv, "labels")
}

// LabelValues implements the MetricsReader interface.
func (r *Reader) LabelValues(agentDetail security.Account, logger *slog.Logger, labelName string, params metrics.LabelValuesParams) metrics.MetricsResponse {
	uv := url.Values{}
	for _, m := range params.Matchers {
		uv.Add("match[]", m)
	}
	if params.Start != "" {
		uv.Set("start", params.Start)
	}
	if params.End != "" {
		uv.Set("end", params.End)
	}
	if params.Limit != "" {
		uv.Set("limit", params.Limit)
	}
	actualPath := fmt.Sprintf(apiPathLabelValues, url.PathEscape(labelName))
	return r.buildAndExecuteProxiedRequest(agentDetail, logger, actualPath, uv, "label_values")
}

// FormatQuery implements the MetricsReader interface.
func (r *Reader) FormatQuery(agentDetail security.Account, logger *slog.Logger, params metrics.FormatQueryParams) metrics.MetricsResponse {
	uv := url.Values{}
	uv.Set("query", params.Query)
	return r.buildAndExecuteProxiedRequest(agentDetail, logger, apiPathFormatQuery, uv, "format_query")
}

func matchersToString(ms ...*labels.Matcher) string {
	var el []string
	for _, m := range ms {
		el = append(el, fmt.Sprintf(`%s="%s"`, m.Name, m.Value))
	}
	return fmt.Sprintf("{%s}", strings.Join(el, ","))
}

// Ensure Reader implements MetricsReader interface
var _ metrics.MetricsReader = &Reader{}
