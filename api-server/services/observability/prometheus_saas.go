package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
)

// PrometheusSaasMetricSource queries a user-configured Prometheus-compatible
// endpoint directly over HTTP, instead of relaying the query through the k8s
// agent the way PrometheusMetricSource does. Everything else about the provider
// is identical — same PromQL, same operators, same result shape — so the UI
// cannot tell the two apart beyond which credentials were used.
type PrometheusSaasMetricSource struct{}

func (s *PrometheusSaasMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_regex"}
}

func (s *PrometheusSaasMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return injectPromQLMatchers(q, req.LabelMatchers, req.Labels)
	}
	return "", nil
}

// loadPrometheusUserConfig resolves the account's direct Prometheus connection.
// Indirected through a variable so tests can supply a config pointing at an
// httptest server without standing up the integrations database.
var loadPrometheusUserConfig = integrations.GetPrometheusUserConfigs

// prometheusSaasGetJSON issues an authenticated GET and decodes the Prometheus
// API envelope, turning a non-200 or a `status: error` body into a Go error.
func prometheusSaasGetJSON(ctx *security.RequestContext, accountID, apiPath string, params url.Values) (map[string]any, error) {
	cfg, err := loadPrometheusUserConfig(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return prometheusSaasGetJSONWithConfig(ctx, cfg, apiPath, params)
}

func prometheusSaasGetJSONWithConfig(ctx *security.RequestContext, cfg integrations.PrometheusUserConfig, apiPath string, params url.Values) (map[string]any, error) {
	resp, err := cfg.DoGet(ctx.GetContext(), apiPath, params)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prometheus: failed to read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus: %s returned HTTP %d: %s", apiPath, resp.StatusCode, string(body))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("prometheus: failed to parse response from %s: %w", apiPath, err)
	}
	if status, ok := payload["status"].(string); ok && status == "error" {
		errType, _ := payload["errorType"].(string)
		errMsg, _ := payload["error"].(string)
		return nil, fmt.Errorf("prometheus: query failed (%s): %s", errType, errMsg)
	}
	return payload, nil
}

// prometheusSaasStringData extracts the `data` array of a label-names or
// label-values response.
func prometheusSaasStringData(payload map[string]any) []string {
	raw, _ := payload["data"].([]any)
	values := make([]string, 0, len(raw))
	for _, entry := range raw {
		if str, ok := entry.(string); ok && str != "" {
			values = append(values, str)
		}
	}
	return values
}

// prometheusSaasTimeParams renders the start/end pair the Prometheus API expects.
// Request timestamps are milliseconds internally; the API takes unix seconds.
func prometheusSaasTimeParams(params url.Values, startMs, endMs int64) {
	if startMs > 0 {
		params.Set("start", strconv.FormatInt(startMs/1000, 10))
	}
	if endMs > 0 {
		params.Set("end", strconv.FormatInt(endMs/1000, 10))
	}
}

func (s *PrometheusSaasMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	params := url.Values{}
	prometheusSaasTimeParams(params, req.StartTime, req.EndTime)
	if req.MetricName != "" {
		params.Add("match[]", fmt.Sprintf(`{__name__="%s"}`, escapePromQLString(req.MetricName)))
	}
	if limit, ok := req.Request["limit"].(int); ok && limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	payload, err := prometheusSaasGetJSON(ctx, req.AccountId, "/api/v1/labels", params)
	if err != nil {
		return nil, err
	}

	names := prometheusSaasStringData(payload)
	labels := make([]OutputMetricLabels, 0, len(names))
	for _, name := range names {
		labels = append(labels, OutputMetricLabels{Label: name, Attributes: map[string]any{}})
	}
	return labels, nil
}

func (s *PrometheusSaasMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	if req.Label == "" {
		return nil, fmt.Errorf("prometheus: label is required")
	}

	params := url.Values{}
	prometheusSaasTimeParams(params, req.StartTime, req.EndTime)
	if metric, ok := req.Request["metric"].(string); ok && metric != "" {
		params.Add("match[]", fmt.Sprintf(`{__name__="%s"}`, escapePromQLString(metric)))
	}
	if limit, ok := req.Request["limit"].(int); ok && limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	// The label name is a path segment, so it must be escaped — a label
	// containing a slash would otherwise change which endpoint is called.
	apiPath := "/api/v1/label/" + url.PathEscape(req.Label) + "/values"
	payload, err := prometheusSaasGetJSON(ctx, req.AccountId, apiPath, params)
	if err != nil {
		return nil, err
	}

	values := prometheusSaasStringData(payload)
	labelValues := make([]OutputMetricsLabelValues, 0, len(values))
	for _, value := range values {
		labelValues = append(labelValues, OutputMetricsLabelValues{Value: value, Attributes: map[string]any{}})
	}
	return labelValues, nil
}

func (s *PrometheusSaasMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	params := url.Values{}
	prometheusSaasTimeParams(params, req.StartTime, req.EndTime)
	if req.Metric != "" {
		// Substring search over the metric catalogue, same selector the
		// agent-relayed source builds.
		params.Add("match[]", fmt.Sprintf(`{__name__=~".*%s.*"}`, escapePromQLString(req.Metric)))
	}
	if limit, ok := req.Request["limit"].(int); ok && limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	payload, err := prometheusSaasGetJSON(ctx, req.AccountId, "/api/v1/label/__name__/values", params)
	if err != nil {
		return nil, err
	}

	names := prometheusSaasStringData(payload)
	metrics := make([]OutputMetrics, 0, len(names))
	for _, name := range names {
		metrics = append(metrics, OutputMetrics{Metric: name, Attributes: map[string]any{}})
	}
	return metrics, nil
}

func (s *PrometheusSaasMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := loadPrometheusUserConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get Prometheus configs: %w", err)
	}

	// The `instant` request override wins over the typed field, matching the
	// agent source — the UI's Validate-Query button sends it that way.
	instant := req.Instant
	if v, ok := req.Request["instant"].(bool); ok {
		instant = v
	}

	results := OutputMetricQuery{Results: make([]QueryResult, 0, len(req.Queries))}
	for queryKey, rawQuery := range req.Queries {
		// The query builders emit a __CLUSTER__ placeholder that the relay-server
		// substitutes on the agent path. This path never touches the relay, so it
		// must expand the token itself — otherwise it reaches the backend verbatim
		// and every query fails to parse. Runs before matcher injection so that
		// operates on valid PromQL.
		rawQuery = integrations.ExpandClusterPlaceholder(rawQuery, cfg.AdditionalLabels)
		promQL, err := injectPromQLMatchers(rawQuery, req.LabelMatchers, req.Labels)
		if err != nil {
			results.Results = append(results.Results, prometheusSaasQueryError(queryKey, rawQuery, err))
			continue
		}
		if item, ok := req.QueryItems[queryKey]; ok && item.AggregateOperator != "" {
			promQL, err = wrapPromQLAggregator(promQL, item.AggregateOperator)
			if err != nil {
				results.Results = append(results.Results, prometheusSaasQueryError(queryKey, rawQuery, err))
				continue
			}
		}

		apiPath := "/api/v1/query_range"
		params := url.Values{"query": []string{promQL}}
		if instant {
			apiPath = "/api/v1/query"
			// The instant value the UI wants is the one at the END of the
			// selected window, not its start.
			if req.EndTime > 0 {
				params.Set("time", strconv.FormatInt(req.EndTime/1000, 10))
			}
		} else {
			prometheusSaasTimeParams(params, req.StartTime, req.EndTime)
			step := req.StepInterval
			if step <= 0 {
				step = 60
			}
			params.Set("step", strconv.Itoa(step))
		}

		// One failing query must not discard the results of the others: the
		// dashboard sends a batch and renders each panel independently.
		payload, err := prometheusSaasGetJSONWithConfig(ctx, cfg, apiPath, params)
		if err != nil {
			results.Results = append(results.Results, prometheusSaasQueryError(queryKey, promQL, err))
			continue
		}

		results.Results = append(results.Results, QueryResult{
			QueryKey: queryKey,
			Query:    promQL,
			Payload:  parsePrometheusMatrixOrVector(payload),
		})
	}

	return results, nil
}

func prometheusSaasQueryError(queryKey, query string, err error) QueryResult {
	msg := err.Error()
	return QueryResult{QueryKey: queryKey, Query: query, Payload: []Result{}, Error: &msg}
}

// parsePrometheusMatrixOrVector flattens a Prometheus `data.result` array into
// Result rows. Both shapes are handled — instant queries return a vector whose
// entries carry a single `value` tuple, range queries a matrix whose entries
// carry a `values` array. Timestamps are passed through in unix SECONDS, which
// is what the agent-relayed source and ChronosphereMetricSaasSource already
// return and what the charts expect.
func parsePrometheusMatrixOrVector(payload map[string]any) []Result {
	data, _ := payload["data"].(map[string]any)
	entries, _ := data["result"].([]any)

	results := make([]Result, 0, len(entries))
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		metric := map[string]string{}
		if raw, ok := entryMap["metric"].(map[string]any); ok {
			for k, v := range raw {
				if str, ok := v.(string); ok {
					metric[k] = str
				}
			}
		}

		var timestamps []int64
		var values []float64
		appendSample := func(sample []any) {
			if len(sample) != 2 {
				return
			}
			ts, ok := sample[0].(float64)
			if !ok {
				return
			}
			value, ok := parsePrometheusSampleValue(sample[1])
			if !ok {
				return
			}
			timestamps = append(timestamps, int64(ts))
			values = append(values, value)
		}

		if sample, ok := entryMap["value"].([]any); ok {
			appendSample(sample)
		}
		if samples, ok := entryMap["values"].([]any); ok {
			for _, sample := range samples {
				if pair, ok := sample.([]any); ok {
					appendSample(pair)
				}
			}
		}

		results = append(results, Result{Metric: metric, Timestamps: timestamps, Values: values})
	}
	return results
}

// parsePrometheusSampleValue reads a sample value, which Prometheus encodes as a
// string. NaN is normalised to 0 the way the agent source does, because the JSON
// the API layer emits cannot represent NaN.
func parsePrometheusSampleValue(raw any) (float64, bool) {
	str, ok := raw.(string)
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, true
	}
	return value, true
}

// FetchMetricSeries answers the workload metric-family discovery question by
// running the shared candidate sweep against the direct endpoint. The
// orchestration (candidate selectors, bounded concurrency, partial-failure
// handling) is the same as the agent source's; only the lookup differs.
func (s *PrometheusSaasMetricSource) FetchMetricSeries(ctx *security.RequestContext, req FetchMetricSeriesRequest) (MetricSeriesResult, error) {
	cfg, err := loadPrometheusUserConfig(ctx, req.AccountId)
	if err != nil {
		return MetricSeriesResult{}, err
	}

	return fetchMetricSeriesCommon(ctx, req, func(selector string, start, end int64, limit int) ([]string, bool, error) {
		params := url.Values{
			"match[]": []string{selector},
			"start":   []string{strconv.FormatInt(start, 10)},
			"end":     []string{strconv.FormatInt(end, 10)},
			"limit":   []string{strconv.Itoa(limit)},
		}
		payload, err := prometheusSaasGetJSONWithConfig(ctx, cfg, "/api/v1/label/__name__/values", params)
		if err != nil {
			return nil, false, err
		}
		families, truncated := parseFamilyValues(payload["data"], payload["warnings"], limit)
		return families, truncated, nil
	})
}
