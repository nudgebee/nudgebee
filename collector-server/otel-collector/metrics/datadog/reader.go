package datadog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/collector/otel/metrics"
	"nudgebee/collector/otel/security"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV1"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV2"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// Reader implements the metrics.MetricsReader interface for Datadog.
type Reader struct {
	metricsAPIV2       *datadogV2.MetricsApi
	metricsAPIV1       *datadogV1.MetricsApi
	logger             *slog.Logger
	apiKey             string
	appKey             string
	site               string
	metricNameMappings map[string]string // Prom name -> Datadog name
	labelNameMappings  map[string]string // Prom label -> Datadog tag
}

// NewReader creates a new Datadog metrics reader.
// It initializes the Datadog API client
func NewReader(logger *slog.Logger, apiKey, appKey, site string) (*Reader, error) {
	// The Datadog client automatically uses DD_API_KEY, DD_APP_KEY, DD_SITE environment variables.
	configuration := datadog.NewConfiguration()
	apiClient := datadog.NewAPIClient(configuration)
	return &Reader{
		metricsAPIV2:       datadogV2.NewMetricsApi(apiClient),
		metricsAPIV1:       datadogV1.NewMetricsApi(apiClient),
		logger:             logger.With("metrics_reader", "datadog"),
		apiKey:             apiKey,
		appKey:             appKey,
		site:               site,
		metricNameMappings: make(map[string]string), // Initialize empty, load from config or pass in
		labelNameMappings:  make(map[string]string), // Initialize empty, load from config or pass in
	}, nil
}

// createAuthContext creates a new context with authentication (API key, App key)
// and server variables (site) for Datadog API calls.
func (r *Reader) createAuthContext(baseCtx context.Context) context.Context {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx := baseCtx

	// Set API and App keys in context
	keys := make(map[string]datadog.APIKey)
	if r.apiKey != "" {
		keys["apiKeyAuth"] = datadog.APIKey{Key: r.apiKey}
	}
	if r.appKey != "" {
		keys["appKeyAuth"] = datadog.APIKey{Key: r.appKey}
	}
	ctx = context.WithValue(ctx, datadog.ContextAPIKeys, keys)

	// Set Site in context
	if r.site != "" {
		ctx = context.WithValue(ctx, datadog.ContextServerVariables, map[string]string{"site": r.site})
	}
	return ctx
}

// --- PromQL to Datadog Translation (Highly Simplified Placeholders) ---

// promQLToDatadogQuery translates a PromQL query to a Datadog query.
// It also returns the extracted original Prometheus metric name (before any mapping or underscore/dot conversion).
func (r *Reader) promQLToDatadogQuery(promqlQuery string) (string, string, error) {
	r.logger.Debug("Attempting to translate PromQL to Datadog query", "promql_query", promqlQuery)

	p := parser.NewParser(parser.Options{})
	expr, err := p.ParseExpr(promqlQuery)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse PromQL query '%s': %w", promqlQuery, err)
	}

	var metricName string
	var ddTags []string
	var aggregation string
	var groupingKeys []string
	var originalPrometheusMetricName string

	// This flag helps to process only the most relevant VectorSelector.
	// For simple queries, it's the top-level one.
	// For aggregations, it's the one directly inside the aggregation.
	var mainSelectorProcessed bool

	var firstVisitorError error // Variable to capture the first error from the visitor

	// Inspect the AST - this is a simplified walker
	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		if firstVisitorError != nil {
			return firstVisitorError // Stop inspection if an error has already occurred
		}

		switch n := node.(type) {
		case *parser.VectorSelector:
			// Process this selector if it's the first one encountered (not inside an aggregate yet processed)
			// OR if we are inside an aggregate and haven't found its specific metric yet.
			isInsideAggregate := aggregation != "" && metricName == "" // True if we're in an aggregate and need its metric
			if !mainSelectorProcessed || isInsideAggregate {
				if n.Name != "" { // Prefer name from selector itself
					metricName = n.Name
					// Capture the original Prometheus name before any mapping or conversion
					if originalPrometheusMetricName == "" {
						originalPrometheusMetricName = n.Name
					}
				}
				for _, matcher := range n.LabelMatchers {
					if matcher.Name == model.MetricNameLabel { // Handle cases like {__name__="metric"}
						if metricName != "" && metricName != matcher.Value {
							r.logger.Warn("Metric name mismatch in VectorSelector, preferring explicit name", "explicit_name", metricName, "label_name", matcher.Value)
						}
						if metricName == "" { // If metric name wasn't set by n.Name
							metricName = matcher.Value
						}
						if originalPrometheusMetricName == "" {
							originalPrometheusMetricName = matcher.Value
						}
						continue // Don't translate __name__ as a regular tag
					}
					tag, err := r.translateMatcher(matcher)
					if err != nil {
						r.logger.Warn("Failed to translate PromQL matcher to Datadog tag, skipping matcher", "matcher", matcher.String(), "error", err)
						continue
					}
					if tag != "" {
						ddTags = append(ddTags, tag)
					}
				}
				if metricName != "" { // If we successfully got a metric name from this selector
					mainSelectorProcessed = true
				}
			}

		case *parser.AggregateExpr:
			// Basic support for sum, avg, min, max
			// Datadog syntax: aggregator:metric{scope} by {group}
			switch n.Op {
			case parser.SUM:
				aggregation = "sum"
			case parser.AVG:
				aggregation = "avg"
			case parser.MIN:
				aggregation = "min"
			case parser.MAX:
				aggregation = "max"
			default:
				r.logger.Warn("Unsupported PromQL aggregation operator for Datadog translation", "operator", n.Op.String())
				firstVisitorError = fmt.Errorf("unsupported aggregation operator: %s", n.Op.String())
				return firstVisitorError
			}
			if n.Grouping != nil {
				groupingKeys = n.Grouping
			}
			// Reset metricName and ddTags to be populated by the VectorSelector within this aggregate
			metricName = ""
			ddTags = []string{}
			mainSelectorProcessed = false // Allow processing of the VectorSelector inside this aggregate
			// Continue inspection to find the VectorSelector within the aggregate

		case *parser.Call:
			// Function calls like rate(), increase() are very hard to map directly.
			// Datadog often applies rate/derivative as a function on the query or as a display option.
			r.logger.Warn("PromQL function calls are not yet fully supported for Datadog translation", "function", n.Func.Name)
			firstVisitorError = fmt.Errorf("unsupported PromQL function: %s", n.Func.Name)
			return firstVisitorError
		case *parser.ParenExpr:
			// Do nothing, just continue to inspect the wrapped expression.
			// This ensures that queries like `sum((metric_name))` are handled.
			return nil
		default:
			// For other node types, we don't do anything special but allow inspection to continue.
			// This is important for nodes like ParenExpr that wrap other expressions.
		}
		return nil
	})

	if firstVisitorError != nil {
		return "", "", firstVisitorError // Return the captured error from the visitor
	}

	if metricName == "" {
		return "", "", errors.New("could not extract metric name from PromQL query")
	}

	// Construct Datadog query
	// Example: sum:system.cpu.idle{host:myhost,env:prod} by {host}
	var sb strings.Builder
	if aggregation != "" {
		sb.WriteString(aggregation)
		sb.WriteString(":")
	}

	// Use mapped metric name if available, otherwise convert underscores to dots
	datadogMetricName, found := r.metricNameMappings[metricName]
	if !found {
		datadogMetricName = strings.ReplaceAll(metricName, "_", ".")
	} else {
		r.logger.Debug("Used mapped metric name", "prometheus_metric", metricName, "datadog_metric", datadogMetricName)
	}
	sb.WriteString(datadogMetricName)

	sb.WriteString("{")
	if len(ddTags) > 0 {
		sb.WriteString(strings.Join(ddTags, ","))
	} else {
		sb.WriteString("*") // Use '*' to match all tags if no specific tags are derived
	}
	sb.WriteString("}")

	if len(groupingKeys) > 0 {
		sb.WriteString(" by {")
		sb.WriteString(strings.Join(groupingKeys, ","))
		sb.WriteString("}")
	}

	ddQuery := sb.String()
	r.logger.Info("Translated PromQL to Datadog query", "promql_query", promqlQuery, "datadog_query", ddQuery)
	return ddQuery, originalPrometheusMetricName, nil
}

// promMatchersToDatadogTags translates Prometheus matchers to a Datadog tag filter string.
// THIS IS A VERY BASIC PLACEHOLDER.
// func (r *Reader) promMatchersToDatadogTags(matchers []string) (string, error) {
// 	var ddTags []string
// 	for _, matcherStr := range matchers {
// 		// This is overly simplistic. Prom matchers can be complex (e.g. `foo=~"bar.*"`).
// 		// We're only handling `label="value"` or assuming it's a metric name part of a selector.
// 		// A proper implementation would parse `matcherStr` using Prometheus libraries.
// 		// Example: `job="prometheus"` -> `job:prometheus`
// 		// Example: `metric_name{job="foo"}` -> this is complex, our simple split won't work.
// 		// For now, assume simple `key=value` or metric names.
// 		if strings.Contains(matcherStr, "{") { // Likely a full selector like 'metric{tag="val"}'
// 			r.logger.Warn("Complex matcher string found, naive translation for matchers might be incorrect", "matcher", matcherStr)
// 			// Attempt to apply the same naive logic as promQLToDatadogQuery
// 			// This is a HACK and will not work for many valid Prometheus matchers.
// 			translated, _, _ := r.promQLToDatadogQuery(matcherStr) // Ignoring error for simplicity here
// 			ddTags = append(ddTags, translated)                    // This will result in `metric{tag:val,nb_...}`
// 		} else if strings.Contains(matcherStr, "=") {
// 			parts := strings.SplitN(matcherStr, "=", 2)
// 			if len(parts) == 2 {
// 				key := strings.TrimSpace(parts[0])
// 				value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
// 				ddTags = append(ddTags, fmt.Sprintf("%s:%s", key, value))
// 			}
// 		} else {
// 			// If no '=', assume it's a metric name for filtering.
// 			ddTags = append(ddTags, matcherStr)
// 		}
// 	}

// 	filterStr := strings.Join(ddTags, ",")
// 	r.logger.Debug("Translated Prom matchers to Datadog tags (basic)", "datadog_tags_filter", filterStr)
// 	return filterStr, nil
// }

// translateMatcher converts a Prometheus LabelMatcher to a Datadog tag string.
func (r *Reader) translateMatcher(m *labels.Matcher) (string, error) {
	switch m.Type {
	case labels.MatchEqual:
		return fmt.Sprintf("%s:%s", m.Name, m.Value), nil
	case labels.MatchNotEqual:
		return fmt.Sprintf("!%s:%s", m.Name, m.Value), nil
	case labels.MatchRegexp:
		// Datadog metric queries don't have direct regex support like PromQL.
		// A very simple case: if regex is just an exact string, treat as equal.
		// Otherwise, this is a significant limitation.
		// Example: if m.Value is `foo` (no regex chars), it's like `foo`.
		// If m.Value is `foo|bar`, this is hard.
		// For now, we'll log a warning and try to treat it as an exact match if it looks simple.
		// A more robust solution would involve checking if m.Value is a simple string without regex metacharacters.
		r.logger.Warn("PromQL regex matcher has limited support in Datadog translation, attempting direct mapping", "matcher_name", m.Name, "regex_value", m.Value)
		return fmt.Sprintf("%s:%s", m.Name, m.Value), nil // Simplified
	case labels.MatchNotRegexp:
		r.logger.Warn("PromQL not-regex matcher has limited support in Datadog translation, attempting direct mapping", "matcher_name", m.Name, "regex_value", m.Value)
		return fmt.Sprintf("!%s:%s", m.Name, m.Value), nil // Simplified
	}
	return "", fmt.Errorf("unsupported matcher type: %s", m.Type.String())
}

// --- MetricsReader Interface Implementation ---

func (r *Reader) Query(_ security.Account, logger *slog.Logger, params metrics.QueryParams) metrics.MetricsResponse {
	logger.Info("DatadogReader: Query called", "params", params)

	ddQuery, originalPromMetricName, err := r.promQLToDatadogQuery(params.Query)
	if err != nil {
		return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("failed to translate PromQL query: %w", err)}
	}

	var from, to int64
	if params.Time != "" {
		parsedTime, err := parsePromTime(params.Time)
		if err != nil {
			return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("invalid time parameter: %w", err)}
		}
		from = parsedTime.Unix() - 30 // 30 seconds before
		to = parsedTime.Unix() + 30   // 30 seconds after
	} else {
		now := time.Now().Unix()
		from = now - 60*60 // Default to a 1-hour window if no time is given
		to = now
	}

	ctx := r.createAuthContext(context.Background())
	resp, httpResp, err := r.metricsAPIV1.QueryMetrics(ctx, from, to, ddQuery)
	if err != nil {
		return r.handleDatadogError("QueryMetrics", err, httpResp, logger)
	}

	var queryEvalTimeSec int64
	if params.Time != "" {
		parsedTime, _ := parsePromTime(params.Time) // Error already checked
		queryEvalTimeSec = parsedTime.Unix()
	} else {
		queryEvalTimeSec = to // Use 'to' as evaluation time if params.Time was empty
	}
	return r.formatDataDogResponse(&resp, httpResp, logger, "vector", originalPromMetricName, queryEvalTimeSec)
}

func (r *Reader) QueryRange(_ security.Account, logger *slog.Logger, params metrics.QueryRangeParams) metrics.MetricsResponse {
	logger.Info("DatadogReader: QueryRange called", "params", params)

	ddQuery, originalPromMetricName, err := r.promQLToDatadogQuery(params.Query)
	if err != nil {
		return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("failed to translate PromQL query: %w", err)}
	}

	start, err := parsePromTime(params.Start)
	if err != nil {
		return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("invalid start time: %w", err)}
	}
	end, err := parsePromTime(params.End)
	if err != nil {
		return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("invalid end time: %w", err)}
	}

	ctx := r.createAuthContext(context.Background())
	resp, httpResp, err := r.metricsAPIV1.QueryMetrics(ctx, start.Unix(), end.Unix(), ddQuery)
	if err != nil {
		return r.handleDatadogError("QueryMetrics (range)", err, httpResp, logger)
	}

	return r.formatDataDogResponse(&resp, httpResp, logger, "matrix", originalPromMetricName, 0) // queryTimeSec is 0 for matrix
}

func (r *Reader) Series(_ security.Account, logger *slog.Logger, params metrics.SeriesParams) metrics.MetricsResponse {
	logger.Warn("DatadogReader: Series endpoint is complex to map directly and has limited implementation.")
	// Prometheus /series returns series (metric names + labels) matching selectors.
	// Datadog's `ListActiveMetrics` or `GetMetricMetadata` could be used, but filtering is different.
	// `promMatchersToDatadogTags` would produce a tag string.
	// For `ListActiveMetrics`, the `tag_filter` parameter is for a *single* tag, not complex selectors.
	// This implementation will be very limited.

	// This is a placeholder. A full implementation is very complex.
	return metrics.MetricsResponse{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"status":"success","data":[]}`), // Empty data
		Error:       errors.New("datadog /series equivalent is limited and not fully implemented"),
	}
}

func (r *Reader) Labels(_ security.Account, logger *slog.Logger, params metrics.LabelsParams) metrics.MetricsResponse {
	return metrics.MetricsResponse{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"status":"success","data":[]}`), // Empty data, similar to Prometheus if no labels found
		Error:       errors.New("datadog /labels equivalent is limited and not fully implemented at this time"),
	}
}

func (r *Reader) LabelValues(_ security.Account, logger *slog.Logger, labelName string, params metrics.LabelValuesParams) metrics.MetricsResponse {
	return metrics.MetricsResponse{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        []byte(`{"status":"success","data":[]}`), // Empty data, similar to Prometheus if no values found
		Error:       errors.New("datadog /label/<name>/values equivalent is limited and not fully implemented at this time"),
	}
}

func (r *Reader) FormatQuery(_ security.Account, logger *slog.Logger, params metrics.FormatQueryParams) metrics.MetricsResponse {
	logger.Info("DatadogReader: FormatQuery called", "params", params)
	ddQuery, _, err := r.promQLToDatadogQuery(params.Query) // Original prom metric name not needed for this response
	if err != nil {
		return metrics.MetricsResponse{StatusCode: http.StatusBadRequest, Error: fmt.Errorf("failed to translate PromQL query for formatting: %w", err)}
	}

	// Return the translated Datadog query string.
	// For consistency with MetricsResponse, wrap it in a simple JSON structure.
	responseBody, err := json.Marshal(map[string]any{
		"status": "success",
		"data":   map[string]string{"datadog_query": ddQuery},
	})
	if err != nil { // Good practice to handle potential marshalling errors
		logger.Error("Failed to marshal FormatQuery response", "error", err)
		return metrics.MetricsResponse{StatusCode: http.StatusInternalServerError, Error: fmt.Errorf("failed to marshal format_query response: %w", err)}
	}
	return metrics.MetricsResponse{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        responseBody,
	}
}

func (r *Reader) handleDatadogError(apiCallName string, err error, httpResp *http.Response, logger *slog.Logger) metrics.MetricsResponse {
	errMsg := fmt.Sprintf("Datadog API error for %s: %v", apiCallName, err)
	if apiErr, ok := err.(datadog.GenericOpenAPIError); ok {
		errMsg = fmt.Sprintf("%s, Body: %s", errMsg, string(apiErr.Body()))
	}
	logger.Error(errMsg)
	statusCode := http.StatusInternalServerError
	if httpResp != nil {
		statusCode = httpResp.StatusCode
	}
	// Ensure a valid status code is returned if it's 0 from a nil httpResp
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}
	return metrics.MetricsResponse{StatusCode: statusCode, Error: errors.New(strings.Split(errMsg, ", Body:")[0])} // Return cleaner error to client
}

// formatDataDogResponse transforms a Datadog MetricsQueryResponse into a Prometheus-compatible format.
func (r *Reader) formatDataDogResponse(
	ddResponse *datadogV1.MetricsQueryResponse,
	httpResp *http.Response,
	logger *slog.Logger,
	promResultType string, // "vector" or "matrix"
	originalPromMetricName string, // The original Prometheus metric name from the query for __name__, can be empty
	queryTimeSec int64, // For "vector" type, the evaluation time in seconds (0 if not applicable for matrix)
) metrics.MetricsResponse {
	if ddResponse == nil {
		logger.Error("Received nil Datadog response to format")
		body, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": promResultType, "result": []any{}}})
		return metrics.MetricsResponse{StatusCode: http.StatusOK, ContentType: "application/json", Body: body}
	}

	promResults := make([]any, 0)

	if ddResponse.HasSeries() {
		for _, ddSeries := range ddResponse.GetSeries() {
			metricMap := make(map[string]string)

			// Use the original PromQL metric name for __name__ if it was successfully extracted.
			if originalPromMetricName != "" {
				metricMap["__name__"] = originalPromMetricName
			} else if ddSeries.HasMetric() {
				// Fallback: Convert Datadog metric name (dots) to Prometheus style (underscores)
				// This is a best-effort if originalPrometheusMetricName wasn't found (e.g., query was just {label="value"})
				// This might not be ideal if there was a specific mapping.
				metricMap["__name__"] = strings.ReplaceAll(ddSeries.GetMetric(), ".", "_")
			} else {
				metricMap["__name__"] = "unknown_metric"
			}

			// Labels from scope
			if ddSeries.HasScope() {
				scopeTags := strings.SplitSeq(ddSeries.GetScope(), ",")
				for scopeTag := range scopeTags {
					parts := strings.SplitN(scopeTag, ":", 2)
					if len(parts) == 2 {
						labelKey := parts[0]
						// Apply reverse label mapping if available/needed (from r.labelNameMappings in reverse)
						// For now, use Datadog tag key directly or check if it's a mapped one.
						// This part is tricky without reverse mappings. Let's assume direct use for now.
						metricMap[labelKey] = parts[1]
					}
				}
			}

			switch promResultType {
			case "vector":
				if ddSeries.HasPointlist() && len(ddSeries.GetPointlist()) > 0 {
					var chosenPoint []*float64
					points := ddSeries.GetPointlist()

					if queryTimeSec > 0 && len(points) > 0 {
						// Find point closest to queryTimeSec for instant queries
						// If queryTimeSec is 0 (e.g. not specified in PromQL, or for some other reason),
						// default to the last point.
						chosenPoint = points[len(points)-1] // Default to last
						if queryTimeSec != 0 {              // Only search if queryTimeSec is meaningful
							minDiff := int64(1<<63 - 1) // Max int64
							for _, point := range points {
								if len(point) == 2 {
									pointTimeSec := int64(*point[0] / 1000.0) // Dereference pointer
									diff := queryTimeSec - pointTimeSec
									if diff < 0 {
										diff = -diff
									} // abs
									if diff < minDiff {
										minDiff = diff
										chosenPoint = point
									} else if diff == minDiff && *point[0] > *chosenPoint[0] { // Dereference pointers
										// Prefer later point if time diff is the same
										chosenPoint = point
									}
								}
							}
						}
					} else if len(points) > 0 {
						chosenPoint = points[len(points)-1] // Default to last point if no queryTimeSec
					}

					if len(chosenPoint) == 2 && chosenPoint[0] != nil && chosenPoint[1] != nil { // Check for nil pointers
						promValue := []any{
							int64(*chosenPoint[0] / 1000.0),                   // Dereference pointer
							strconv.FormatFloat(*chosenPoint[1], 'f', -1, 64), // Dereference pointer
						}
						promResults = append(promResults, map[string]any{
							"metric": metricMap,
							"value":  promValue,
						})
					}
				}
			case "matrix":
				promValues := make([][]any, 0)
				if ddSeries.HasPointlist() {
					for _, point := range ddSeries.GetPointlist() {
						if len(point) == 2 && point[0] != nil && point[1] != nil { // Check for nil pointers
							promValues = append(promValues, []any{
								int64(*point[0] / 1000.0),                   // Dereference pointer
								strconv.FormatFloat(*point[1], 'f', -1, 64), // Dereference pointer
							})
						}
					}
				}
				if len(promValues) > 0 { // Only add series if it has values
					promResults = append(promResults, map[string]any{
						"metric": metricMap,
						"values": promValues,
					})
				}
			}
		}
	}

	promResponseData := map[string]any{
		"resultType": promResultType,
		"result":     promResults,
	}

	finalBody, err := json.Marshal(map[string]any{
		"status": "success",
		"data":   promResponseData,
	})
	if err != nil {
		logger.Error("Failed to marshal Prometheus-compatible response", "error", err)
		return metrics.MetricsResponse{StatusCode: http.StatusInternalServerError, Error: fmt.Errorf("failed to marshal response: %w", err)}
	}

	statusCode := http.StatusOK
	if httpResp != nil {
		statusCode = httpResp.StatusCode
	}
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	return metrics.MetricsResponse{
		StatusCode:  statusCode,
		ContentType: "application/json",
		Body:        finalBody,
	}
}

// parsePromTime parses a Prometheus time string (Unix timestamp or RFC3339) into time.Time.
func parsePromTime(timeStr string) (time.Time, error) {
	if timeStr == "" {
		return time.Time{}, errors.New("time string is empty")
	}
	// Try parsing as float (Unix timestamp)
	if ts, err := strconv.ParseFloat(timeStr, 64); err == nil {
		return time.Unix(int64(ts), int64((ts-float64(int64(ts)))*1e9)).UTC(), nil
	}
	// Try parsing as RFC3339
	formats := []string{time.RFC3339Nano, time.RFC3339}
	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time format: '%s', expected Unix timestamp or RFC3339", timeStr)
}

// Ensure Reader implements MetricsReader interface
var _ metrics.MetricsReader = &Reader{}
