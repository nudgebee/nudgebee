package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/security"
)

// SolarWindsMetricSource implements MetricSource for SolarWinds Observability.
//
// Time-series data is fetched from the SolarWinds REST API:
//
//	GET /v1/metrics/{metricName}/measurements
//
// All shared structs (swMeasurementsResponse, swPageInfo, swMeasurementGrouping, etc.)
// and the DoSolarWindsGET transport are defined in solarwinds_traces.go and
// solarwinds_logs.go — they are reused here without duplication.
//
// Query format:
//   - req.Queries is map[queryKey → SWO metric name]
//   - aggregateBy / groupBy / filter are read from req.Request (optional)
//
// Label discovery limitation:
//
//	SWO measurements return non-empty attributes[] only when groupBy is specified.
//	FetchMetricsLabels therefore requires a "groupBy" hint in req.Request to return
//	any keys — without it the response attributes are always empty and an empty list
//	is returned. FetchMetricLabelValues requires "metric" in req.Request.
type SolarWindsMetricSource struct{}

// GetSupportedOperators returns an empty slice: SolarWinds metrics do not
// consume the structured Where clause at all. FetchMetricsQuery reads only a
// raw "filter" string from req.Request (see line 100) and ignores req.Where,
// so advertising any operator in the UI would be misleading — the filter
// builder would never translate it into the SWO request. The UI falls back
// to its minimal built-in descriptor set when this list is empty. See issue
// #29227 and the Gemini review pointer on #29233.
func (s *SolarWindsMetricSource) GetSupportedOperators() []string {
	return []string{}
}

func (s *SolarWindsMetricSource) GetQuery(_ *security.RequestContext, _ FetchMetricsRequest) (string, error) {
	return "", nil
}

const swMetricConfigsErrFmt = "failed to get SolarWinds configs: %w"

// swMetricsListResponse is the response shape for GET /v1/metrics.
type swMetricsListResponse struct {
	MetricsInfo []swMetricInfo `json:"metricsInfo"`
	PageInfo    *swPageInfo    `json:"pageInfo"`
}

type swMetricInfo struct {
	Name string `json:"name"`
}

// swMeasurementQuery groups the optional query parameters for the measurements API.
// Separating these from the transport-level params (token, baseURL, path) keeps
// fetchAllMeasurements within the allowed parameter count.
type swMeasurementQuery struct {
	// aggregateBy must be uppercase: AVG, MAX, MIN, SUM, COUNT (SWO is case-sensitive).
	aggregateBy string
	// groupBy is a comma-separated list of SWO attribute keys (e.g. "k8s.namespace.name").
	// When empty, SWO returns a single ungrouped series with no attributes.
	groupBy string
	// filter is a SWO filter expression (e.g. "k8s.namespace.name: [nudgebee]").
	filter string
	// from / to are ISO-8601 timestamps. When empty the API uses its own defaults.
	from string
	to   string
}

// FetchMetricsQuery fetches time-series data for each query in req.Queries.
//
// req.Request optional keys:
//
//	"aggregateBy" – "AVG" (default), "MAX", "MIN", "SUM", "COUNT" (case-insensitive)
//	"groupBy"     – comma-separated SWO attribute keys, e.g. "k8s.namespace.name"
//	"filter"      – SWO filter expression, e.g. "k8s.namespace.name: [nudgebee]"
//
// Step interval (req.StepInterval) is not used: SWO measurements always return
// 1-minute buckets and the API does not expose a resolution parameter.
func (s *SolarWindsMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricsQuery: failed to get configs", "error", err)
		return OutputMetricQuery{}, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	from, to := swTraceTimeRange(req.StartTime, req.EndTime)

	q := swMeasurementQuery{
		// SWO rejects lowercase aggregateBy with HTTP 400.
		aggregateBy: strings.ToUpper(swStringFromRequest(req.Request, "aggregateBy", "AVG")),
		groupBy:     swStringFromRequest(req.Request, "groupBy", ""),
		filter:      swStringFromRequest(req.Request, "filter", ""),
		from:        from,
		to:          to,
	}

	results := OutputMetricQuery{Results: []QueryResult{}}
	for queryKey, metricName := range req.Queries {
		qr := s.fetchOneMetricQuery(ctx, apiToken, baseURL, queryKey, metricName, q, req.Instant)
		results.Results = append(results.Results, qr)
	}
	return results, nil
}

// fetchOneMetricQuery fetches measurements for a single metric name and wraps
// any error into the QueryResult rather than aborting the whole batch.
func (s *SolarWindsMetricSource) fetchOneMetricQuery(ctx *security.RequestContext, apiToken, baseURL, queryKey, metricName string, q swMeasurementQuery, instant bool) QueryResult {
	if strings.TrimSpace(metricName) == "" {
		errMsg := "metric name must not be empty"
		return QueryResult{QueryKey: queryKey, Error: &errMsg}
	}

	path := fmt.Sprintf("/v1/metrics/%s/measurements", url.PathEscape(metricName))
	ctx.GetLogger().Info("SolarWindsMetricSource.FetchMetricsQuery",
		"key", queryKey, "path", path, "aggregateBy", q.aggregateBy, "instant", instant)

	groupings, err := fetchAllMeasurements(apiToken, baseURL, path, q)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricsQuery: fetch failed",
			"key", queryKey, "error", err)
		errMsg := err.Error()
		return QueryResult{QueryKey: queryKey, Error: &errMsg}
	}
	return swGroupingsToQueryResult(groupings, queryKey, metricName, instant)
}

// FetchMetricList returns all available SWO metric names, optionally filtered
// by a case-insensitive substring match on req.Metric.
func (s *SolarWindsMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricList: failed to get configs", "error", err)
		return nil, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	infos, err := swFetchAllMetricInfos(apiToken, baseURL)
	if err != nil {
		return nil, err
	}

	nameFilter := strings.ToLower(req.Metric)
	metrics := make([]OutputMetrics, 0, len(infos))
	for _, m := range infos {
		if nameFilter == "" || strings.Contains(strings.ToLower(m.Name), nameFilter) {
			metrics = append(metrics, OutputMetrics{Metric: m.Name, Attributes: map[string]any{}})
		}
	}
	return metrics, nil
}

// FetchMetricLabelValues returns distinct values for a label/dimension on a metric.
//
// req.Request required keys:
//
//	"metric" – the SWO metric name (e.g. "k8s.cluster.spec.memory.requests")
//
// If "metric" is absent, an empty slice is returned — the SWO attributes endpoint
// is per-metric and cannot enumerate values without a metric context.
func (s *SolarWindsMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	if req.Label == "" {
		return nil, fmt.Errorf("label name is required")
	}
	metricName := swStringFromRequest(req.Request, "metric", swStringFromRequest(req.Request, "metric_name", ""))
	if metricName == "" {
		return []OutputMetricsLabelValues{}, nil
	}

	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	path := fmt.Sprintf("/v1/metrics/%s/attributes/%s",
		url.PathEscape(metricName), url.PathEscape(req.Label))
	body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, path, map[string]string{})
	if err != nil {
		return nil, fmt.Errorf("SolarWinds label values request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("SolarWinds label values API returned HTTP %d: %s", statusCode, string(body))
	}

	var resp struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse SolarWinds label values response: %w", err)
	}

	out := make([]OutputMetricsLabelValues, 0, len(resp.Values))
	for _, v := range resp.Values {
		if v != "" {
			out = append(out, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
		}
	}
	return out, nil
}

// FetchMetricsLabels returns the attribute (label) keys observable for req.MetricName.
//
// SWO has no "list all attributes" endpoint. This method samples one page of
// measurements over the last hour and extracts the attribute keys from the groupings.
//
// Requirement: req.Request["groupBy"] must be provided. SWO only populates
// attributes[] in the response when groupBy is specified — without it every
// grouping has an empty attributes[] and this method returns an empty list.
// When groupBy is provided, the keys specified there are echoed back in the
// response and returned as the available labels for the metric.
func (s *SolarWindsMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	if req.MetricName == "" {
		return []OutputMetricLabels{}, nil
	}

	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	// Default groupBy covers the most common k8s label dimensions. SWO only
	// populates attributes[] when groupBy is specified; without it every grouping
	// returns attributes:null and FetchMetricsLabels returns an empty list.
	const swDefaultGroupBy = "k8s.namespace.name,k8s.pod.name,k8s.container.name,k8s.node.name,k8s.deployment.name,sw.k8s.cluster.uid"

	now := time.Now().UTC()
	q := swMeasurementQuery{
		aggregateBy: "AVG",
		groupBy:     swStringFromRequest(req.Request, "groupBy", swDefaultGroupBy),
		from:        now.Add(-1 * time.Hour).Format(time.RFC3339),
		to:          now.Format(time.RFC3339),
	}

	path := fmt.Sprintf("/v1/metrics/%s/measurements", url.PathEscape(req.MetricName))
	params := swBuildMeasurementParams(q)
	params["pageSize"] = "5" // only a few groupings needed to discover attribute keys

	body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, path, params)
	if err != nil {
		return nil, fmt.Errorf("SolarWinds labels sample request failed: %w", err)
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("SolarWinds labels sample API returned HTTP %d: %s", statusCode, string(body))
	}

	var resp swMeasurementsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse SolarWinds measurements response: %w", err)
	}

	seen := make(map[string]bool)
	var labels []OutputMetricLabels
	for _, g := range resp.Groupings {
		for _, a := range g.Attributes {
			if a.Key != "" && !seen[a.Key] {
				seen[a.Key] = true
				labels = append(labels, OutputMetricLabels{Label: a.Key, Attributes: map[string]any{}})
			}
		}
	}
	return labels, nil
}

// --- private helpers ---

// swFetchAllMetricInfos paginates GET /v1/metrics and returns all metric info
// records, capped at swMaxPages for safety.
func swFetchAllMetricInfos(apiToken, baseURL string) ([]swMetricInfo, error) {
	params := map[string]string{"pageSize": "500"}
	var all []swMetricInfo

	for page := 0; page < swMaxPages; page++ {
		body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, "/v1/metrics", params)
		if err != nil {
			return nil, fmt.Errorf("SolarWinds metrics list request failed: %w", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("SolarWinds metrics list API returned HTTP %d: %s", statusCode, string(body))
		}

		var resp swMetricsListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse SolarWinds metrics list response: %w", err)
		}
		all = append(all, resp.MetricsInfo...)

		next := swNextSkipToken(resp.PageInfo)
		if next == "" {
			break
		}
		params["skipToken"] = next
	}
	return all, nil
}

// fetchAllMeasurements paginates GET /v1/metrics/{path} and returns all groupings,
// capped at swMaxPages for safety.
func fetchAllMeasurements(apiToken, baseURL, path string, q swMeasurementQuery) ([]swMeasurementGrouping, error) {
	params := swBuildMeasurementParams(q)
	params["pageSize"] = "100"

	var all []swMeasurementGrouping
	for page := 0; page < swMaxPages; page++ {
		body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, path, params)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", statusCode, string(body))
		}

		var resp swMeasurementsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse measurements response: %w", err)
		}
		all = append(all, resp.Groupings...)

		next := swNextSkipToken(resp.PageInfo)
		if next == "" {
			break
		}
		params["skipToken"] = next
	}
	return all, nil
}

// swBuildMeasurementParams converts a swMeasurementQuery into the HTTP query-param
// map expected by DoSolarWindsGET, omitting empty optional fields.
func swBuildMeasurementParams(q swMeasurementQuery) map[string]string {
	params := map[string]string{"aggregateBy": q.aggregateBy}
	if q.from != "" {
		params["startTime"] = q.from
	}
	if q.to != "" {
		params["endTime"] = q.to
	}
	if q.groupBy != "" {
		params["groupBy"] = q.groupBy
	}
	if q.filter != "" {
		params["filter"] = q.filter
	}
	return params
}

// swNextSkipToken extracts the skipToken cursor from a SWO PageInfo URL.
// Returns "" when there are no more pages or the URL cannot be parsed.
func swNextSkipToken(pageInfo *swPageInfo) string {
	if pageInfo == nil || pageInfo.NextPage == "" {
		return ""
	}
	u, err := url.Parse(pageInfo.NextPage)
	if err != nil {
		return ""
	}
	return u.Query().Get("skipToken")
}

// swAttrToLabel maps SolarWinds OTel attribute keys to the short canonical label names
// used in Result.Metric, matching the label convention of Prometheus/Datadog/NewRelic so
// the UI can render SWO responses uniformly without provider-specific handling.
var swAttrToLabel = map[string]string{
	"k8s.node.name":        "node",
	"k8s.namespace.name":   "namespace",
	"k8s.deployment.name":  "workload",
	"k8s.statefulset.name": "workload",
	"k8s.daemonset.name":   "workload",
	"k8s.pod.name":         "pod",
	"k8s.container.name":   "container",
	"service.name":         "workload_name",
	"sw.transaction":       "span_name",
	"otel.status_code":     "status_code",
}

// swGroupingsToQueryResult converts SWO measurement groupings into a QueryResult.
//
// Each grouping becomes one Result series. Sparse (nil) bucket values are dropped;
// their corresponding timestamps are also omitted so the two slices stay aligned.
// Timestamps are in Unix seconds.
//
// SWO OTel attribute keys (e.g. "k8s.node.name") are mapped to the same short canonical
// labels used by Prometheus/Datadog/NewRelic (e.g. "node") so the UI can render results
// uniformly. The internal "__name__" field is not emitted — it is not part of the
// canonical Result.Metric contract.
//
// When instant is true, each series is reduced to a single data point (the last non-nil
// bucket), matching the single-timestamp/value format returned by Prometheus instant queries.
func swGroupingsToQueryResult(groupings []swMeasurementGrouping, queryKey, metricName string, instant bool) QueryResult {
	qr := QueryResult{
		QueryKey: queryKey,
		Query:    metricName,
		Payload:  make([]Result, 0, len(groupings)),
	}

	for _, g := range groupings {
		metric := make(map[string]string, len(g.Attributes))
		for _, a := range g.Attributes {
			if a.Key == "" {
				continue
			}
			labelKey := a.Key
			if canonical, ok := swAttrToLabel[a.Key]; ok {
				labelKey = canonical
			}
			metric[labelKey] = a.Value
		}

		measurements := g.Measurements
		if instant {
			measurements = swLastMeasurement(g.Measurements)
		}

		qr.Payload = append(qr.Payload, Result{
			Metric:     metric,
			Timestamps: swExtractTimestamps(measurements),
			Values:     swExtractValues(measurements),
		})
	}
	return qr
}

// swLastMeasurement returns a slice containing only the last non-nil bucket measurement.
// Used for instant queries to mirror the single [timestamp, value] format of Prometheus
// instant query results. Returns an empty slice when all buckets are nil.
func swLastMeasurement(measurements []swBucketMeasurement) []swBucketMeasurement {
	for i := len(measurements) - 1; i >= 0; i-- {
		if measurements[i].Value != nil {
			return measurements[i : i+1]
		}
	}
	return nil
}

// swExtractTimestamps returns Unix-second timestamps for all non-nil bucket measurements.
func swExtractTimestamps(measurements []swBucketMeasurement) []int64 {
	out := make([]int64, 0, len(measurements))
	for _, m := range measurements {
		if m.Value == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, m.Time)
		if err != nil {
			continue
		}
		out = append(out, t.Unix())
	}
	return out
}

// swExtractValues returns the float64 values for all non-nil bucket measurements,
// in the same order as swExtractTimestamps so the two slices stay aligned.
func swExtractValues(measurements []swBucketMeasurement) []float64 {
	out := make([]float64, 0, len(measurements))
	for _, m := range measurements {
		if m.Value != nil {
			out = append(out, *m.Value)
		}
	}
	return out
}

// swStringFromRequest extracts a string from the freeform Request map with a default.
func swStringFromRequest(r map[string]any, key, defaultVal string) string {
	if r == nil {
		return defaultVal
	}
	if v, ok := r[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}
