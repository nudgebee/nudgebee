package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenObserveMetricSource implements MetricSource interface for OpenObserve
type OpenObserveMetricSource struct{}

func openObserveMetricQueryRangeParams(req FetchMetricsRequest) (start, end string, step int) {
	durationSecs := (req.EndTime - req.StartTime) / 1000
	step = req.StepInterval
	if step <= 0 {
		step = int(durationSecs / 100)
		if step < 1 {
			step = 1
		}
	}

	return strconv.FormatInt(req.StartTime/1000, 10), strconv.FormatInt(req.EndTime/1000, 10), step
}

// escapePromQLLabelValue escapes a value being embedded in a PromQL matcher literal, so a
// metric name containing a quote cannot break out of `{__name__="..."}`.
func escapePromQLLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

// openObserveMetricMetadataQuery builds the query string shared by the Prometheus metadata
// endpoints (/labels, /label/<name>/values, /label/__name__/values).
//
// All three accept start/end alongside match[], and every FetchMetric*Request already
// carries the window — it was simply never sent. Omitting it leaves OpenObserve to pick its
// own default range, which is why a match[]-filtered lookup returns nothing while the same
// endpoint without match[] returns the full set.
func openObserveMetricMetadataQuery(startMs, endMs int64, matchers []string, now time.Time) string {
	if endMs <= 0 {
		endMs = now.UnixMilli()
	}
	if startMs <= 0 {
		startMs = now.Add(-time.Hour).UnixMilli()
	}

	params := neturl.Values{}
	params.Set("start", strconv.FormatInt(startMs/1000, 10))
	params.Set("end", strconv.FormatInt(endMs/1000, 10))
	for _, m := range matchers {
		if m != "" {
			params.Add("match[]", m)
		}
	}
	return "?" + params.Encode()
}

func (s *OpenObserveMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_regex"}
}

func (s *OpenObserveMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, rawQuery := range req.Queries {
		return injectPromQLMatchers(rawQuery, req.LabelMatchers, req.Labels)
	}
	return "", nil
}

func (s *OpenObserveMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	results := OutputMetricQuery{Results: []QueryResult{}}

	start, end, step := openObserveMetricQueryRangeParams(req)

	for queryKey, rawQuery := range req.Queries {
		// The utilisation query builders emit a __CLUSTER__ placeholder that relay-server
		// substitutes on the agent path and PrometheusSaasMetricSource expands itself. This
		// path does neither, so it must expand the token here -- otherwise it reaches
		// OpenObserve verbatim and every query fails to parse. OpenObserveConfig carries no
		// additional-labels field (the org is the tenant boundary), so it expands to empty.
		// Runs before matcher injection so that operates on valid PromQL.
		rawQuery = integrations.ExpandClusterPlaceholder(rawQuery, "")
		query, err := injectPromQLMatchers(rawQuery, req.LabelMatchers, req.Labels)
		if err != nil {
			errorMsg := err.Error()
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		if item, ok := req.QueryItems[queryKey]; ok && item.AggregateOperator != "" {
			query, err = wrapPromQLAggregator(query, item.AggregateOperator)
			if err != nil {
				errorMsg := err.Error()
				results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
				continue
			}
		}

		// Drop OTel "no recorded value" announcements, which OpenObserve stores as real
		// samples carrying a meaningless number. Applied here because this is the one
		// place every OpenObserve PromQL query passes through -- the cluster, per-pod,
		// workload and fallback builders all execute via FetchMetricsQuery -- and it must
		// run AFTER the aggregator wrap so the exclusion reaches the selectors inside it.
		// See openobserve_stale_markers.go.
		query = excludeOpenObserveNoRecordedValue(query)

		// OpenObserve does not evaluate an `*_over_time` wrapper whose inner expression
		// collapses to a single series -- it answers with the value at the evaluation
		// instant and discards the window, so max, min and avg all return the same
		// number. Where that wrapper is recognised, the inner expression is fetched as a
		// RANGE and the statistic computed below instead. See
		// openobserve_local_aggregate.go.
		// Gated on Instant, not on the query's shape. `Instant` is set exactly when the
		// caller asked for an Aggregate, and a wrapper can be present without one having
		// been asked for: the self-windowed keys (promSelfWindowedKeys) carry their own
		// `*_over_time` by definition, and this source also runs raw user PromQL. Keying
		// off the shape alone collapsed a plain range fetch to a single point and handed
		// a chart one sample where it asked for the series.
		localAgg, hasWrapper := parseOpenObserveOverTime(query)
		reduceLocally := req.Instant && hasWrapper
		queryToSend := query
		if reduceLocally {
			queryToSend = localAgg.Inner
		}

		// An instant query is a different endpoint, not a parameter. `Instant` is set
		// whenever the caller asked for an Aggregate (service.go: "a collapse answers
		// with one point per series"), and the builders have already wrapped the
		// expression in max_over_time(...[range:step]). Running that wrapper through
		// query_range evaluates it once per step and answers with a whole series -- 101
		// points where the contract promises one, which ~30 UI call sites read as
		// payload[0] and plot as the workload.
		//
		// Prometheus's own source has always branched here; this one did not, so every
		// aggregated key on an OpenObserve account came back uncollapsed. When the
		// wrapper is being reduced locally the range fetch is required, so `Instant`
		// does not apply -- the collapse happens in Go once the samples are in.
		endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/query_range", cfg.URL, cfg.OrgID)

		form := neturl.Values{}
		form.Add("query", queryToSend)
		if req.Instant && !reduceLocally {
			endpoint = fmt.Sprintf("%s/api/%s/prometheus/api/v1/query", cfg.URL, cfg.OrgID)
			form.Add("time", end)
		} else {
			form.Add("start", start)
			form.Add("end", end)
			// Sample on the grid the wrapper itself named, so the statistic is taken
			// over the same points Prometheus would have used for it.
			rangeStep := step
			if reduceLocally && localAgg.StepSecs > 0 {
				rangeStep = localAgg.StepSecs
			}
			form.Add("step", strconv.Itoa(rangeStep))
		}

		authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

		resp, err := common.HttpPost(endpoint,
			common.HttpWithHeaders(map[string]string{
				"Authorization": authHeader,
				"Content-Type":  "application/x-www-form-urlencoded",
			}),
			common.HttpWithBody(io.NopCloser(bytes.NewReader([]byte(form.Encode())))),
			common.HttpWithTimeout(30*time.Second),
		)

		if err != nil {
			errorMsg := err.Error()
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errorMsg := fmt.Sprintf("OpenObserve query failed with status %d: %s", resp.StatusCode, string(bodyBytes))
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		var payload struct {
			Data struct {
				Result []struct {
					Metric map[string]string `json:"metric"`
					// A range query answers with `values` (a matrix); an instant query
					// answers with `value` -- one [ts, "n"] pair. Both are read here so
					// the two endpoints share one decode path.
					Values [][]any `json:"values"`
					Value  []any   `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			errorMsg := fmt.Sprintf("failed to parse response: %v", err)
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &errorMsg})
			continue
		}

		var payloadResults []Result
		for _, r := range payload.Data.Result {
			// Normalise the instant shape onto the range one so everything below reads
			// the same. A pair with no timestamp is not a point, and is dropped by the
			// length check in the loop rather than silently becoming a zero.
			pairs := r.Values
			if len(pairs) == 0 && len(r.Value) > 0 {
				pairs = [][]any{r.Value}
			}
			timestamps := make([]int64, 0, len(pairs))
			values := make([]float64, 0, len(pairs))
			for _, v := range pairs {
				if len(v) != 2 {
					continue
				}
				ts, ok := v[0].(float64)
				if !ok {
					continue
				}
				valStr, ok := v[1].(string)
				if !ok {
					continue
				}
				valFloat, err := strconv.ParseFloat(valStr, 64)
				if err != nil {
					continue
				}
				timestamps = append(timestamps, int64(ts)*1000)
				values = append(values, valFloat)
			}
			payloadResults = append(payloadResults, Result{
				Metric:     r.Metric,
				Timestamps: timestamps,
				Values:     values,
			})
		}

		if reduceLocally {
			payloadResults = reduceOpenObserveSeries(payloadResults, localAgg)
		}

		results.Results = append(results.Results, QueryResult{
			QueryKey: queryKey,
			// The expression the CALLER asked for, not the unwrapped one that was sent:
			// a reader comparing this against another provider's query needs the
			// statistic to still be visible in it.
			Query:   query,
			Payload: payloadResults,
		})
	}

	return results, nil
}

func (s *OpenObserveMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	// Filtered client-side, not with a match[] selector. OpenObserve's Prometheus shim
	// resolves match[] to a metric NAME only: a `{__name__=~".*foo.*"}` selector comes
	// back as an empty list rather than the matching subset, and a selector that does not
	// start with a literal name is rejected outright with HTTP 400 ("match[] argument must
	// start with a metric name"). Verified against OpenObserve v1.0.3. Asking for the
	// whole catalog — the one form the endpoint answers — and matching here is what makes
	// keyword discovery return anything at all; the catalog is a single call and the
	// caller caps the list well below its size.
	endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/label/__name__/values", cfg.URL, cfg.OrgID) +
		openObserveMetricMetadataQuery(req.StartTime, req.EndTime, nil, time.Now())

	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve API returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	// Case-insensitive substring, matching what the keyword search advertises: callers
	// pass a technology keyword ("redis", "jvm") rather than an exact metric name.
	filter := strings.ToLower(req.Metric)

	var output []OutputMetrics
	for _, name := range payload.Data {
		if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
			continue
		}
		output = append(output, OutputMetrics{Metric: name, Attributes: map[string]any{}})
	}

	return output, nil
}

func (s *OpenObserveMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	// OpenObserve's Prometheus shim only implements /label/<name>/values for __name__:
	// asking it for any other label returns an empty list, with or without match[].
	// Verified live — /label/pod/values and /label/job/values both came back empty while
	// /label/__name__/values returned 1789 metrics. So read the values natively instead:
	// each metric is its own stream, and GROUP BY over it is the same query shape the log
	// source already uses for label values.
	metric := common.GetString(req.Request, "metric_name")
	if metric == "" {
		// Without a metric there is no stream to read; the Prometheus path cannot answer
		// this either, so return nothing rather than a misleading org-wide list.
		return nil, nil
	}
	if !integrations.IsSafeOpenObserveStreamName(metric) {
		return nil, fmt.Errorf("invalid or unsafe metric name: %q", metric)
	}

	col := req.Label
	if !isSafeIdentifier(col) {
		return nil, fmt.Errorf("invalid or unsafe label name: %q", col)
	}

	sql := fmt.Sprintf(`SELECT %s FROM "%s" WHERE %s IS NOT NULL GROUP BY %s ORDER BY %s LIMIT 100`,
		col, metric, col, col, col)

	values, err := openObserveMetricSearch(cfg, sql, req.StartTime, req.EndTime, col, openObserveSearchTimeout)
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetricsLabelValues, 0, len(values))
	for _, v := range values {
		output = append(output, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return output, nil
}

// openObserveMetricNonLabelColumns are columns on a metric stream that are not labels, so
// offering them in the label picker would be noise. Confirmed against a live stream schema:
// __hash__ is an internal series key, __name__ repeats the metric already selected, and
// _timestamp/value/start_time are the sample itself rather than a dimension of it.
var openObserveMetricNonLabelColumns = map[string]struct{}{
	"__hash__":   {},
	"__name__":   {},
	"_timestamp": {},
	"value":      {},
	"start_time": {},
}

// openObserveEmptyResultCodes are OpenObserve error codes that mean "this combination has
// no data", not "the request failed". 20002 is stream-not-found, 20004 is field-not-found.
var openObserveEmptyResultCodes = []string{`"code":20002`, `"code":20004`}

// isOpenObserveEmptyResultError reports whether an error body describes an absent stream or
// column rather than a genuine failure. Matching on the numeric code first keeps this from
// depending on message wording; the text checks are a fallback for older builds.
func isOpenObserveEmptyResultError(body string) bool {
	for _, code := range openObserveEmptyResultCodes {
		if strings.Contains(body, code) {
			return true
		}
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "stream not found") || strings.Contains(lower, "field not found")
}

// openObserveSearchTimeout is the default budget for a single /_search call. A sweep
// that issues several of them shortens it to whatever is left of its own deadline.
const openObserveSearchTimeout = 15 * time.Second

// openObserveMetricSearch runs a SQL query against a metric stream and returns the distinct
// values of one column. Metrics live in their own streams, so the /_search API answers
// metadata questions the Prometheus shim cannot. The caller supplies the timeout because a
// sweep of several searches has to divide one action deadline between them.
func openObserveMetricSearch(cfg integrations.OpenObserveConfig, sql string, startMs, endMs int64, col string, timeout time.Duration) ([]string, error) {
	startMicros, endMicros := openObserveTimeRangeMicros(startMs, endMs, time.Now())

	searchReq := openObserveSearchRequest{}
	searchReq.Query.SQL = sql
	searchReq.Query.StartTime = startMicros
	searchReq.Query.EndTime = endMicros

	payloadBytes, err := json.Marshal(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metric search request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/%s/_search?type=metrics", cfg.URL, cfg.OrgID)

	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": openObserveAuthHeader(cfg.Username, cfg.Password),
			"Content-Type":  "application/json",
		}),
		common.HttpWithBody(io.NopCloser(bytes.NewReader(payloadBytes))),
		common.HttpWithTimeout(timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("OpenObserve metric search failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		text := strings.TrimSpace(string(body))

		// Two of these are ordinary "nothing to suggest", not failures, and must not reach
		// the user as an error toast:
		//   - stream not found: the __name__ list includes families (e.g. a summary's base
		//     name) that have no stream of their own.
		//   - field not found: the label exists on other metrics but not on this one.
		if isOpenObserveEmptyResultError(text) {
			return nil, nil
		}

		return nil, fmt.Errorf("OpenObserve metric search returned HTTP %d: %s", resp.StatusCode, text)
	}

	searchResp, err := decodeOpenObserveSearchResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode OpenObserve metric search response: %w", err)
	}

	out := make([]string, 0, len(searchResp.Hits))
	for _, hit := range searchResp.Hits {
		if v, ok := hit[col]; ok && v != nil {
			if s := formatOpenObserveLabelValue(v); s != "" {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func (s *OpenObserveMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, err := integrations.GetOpenObserveConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenObserve configs: %w", err)
	}

	// Scoping to a metric goes through the stream schema, not the Prometheus shim:
	// /labels?match[]={__name__="x"} returns an empty list on OpenObserve (verified live),
	// while each metric's own stream lists exactly its label columns.
	if req.MetricName != "" {
		if !integrations.IsSafeOpenObserveStreamName(req.MetricName) {
			return nil, fmt.Errorf("invalid or unsafe metric name: %q", req.MetricName)
		}

		fields, err := fetchOpenObserveStreamFields(cfg.URL, cfg.OrgID, cfg.Username, cfg.Password, req.MetricName, "metrics")
		if err != nil {
			// A metric with no stream has no labels — that is an empty picker, not a
			// failure worth an error toast. Confirmed against a live instance: the
			// __name__ list includes families such as a summary's base name whose stream
			// never received samples (acquire_shards_latency: 0 fields, 0 docs), and the
			// matching search returns code 20002.
			if errors.Is(err, errOpenObserveStreamNotFound) || isOpenObserveEmptyResultError(err.Error()) {
				return nil, nil
			}
			return nil, err
		}

		names := make([]string, 0, len(fields))
		for name := range fields {
			if _, skip := openObserveMetricNonLabelColumns[name]; skip {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)

		output := make([]OutputMetricLabels, 0, len(names))
		for _, name := range names {
			output = append(output, OutputMetricLabels{Label: name, Attributes: map[string]any{}})
		}
		return output, nil
	}

	// No metric given: the org-wide label list is the one Prometheus call that does work.
	endpoint := fmt.Sprintf("%s/api/%s/prometheus/api/v1/labels", cfg.URL, cfg.OrgID) +
		openObserveMetricMetadataQuery(req.StartTime, req.EndTime, nil, time.Now())

	authHeader := openObserveAuthHeader(cfg.Username, cfg.Password)

	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenObserve API returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var output []OutputMetricLabels
	for _, name := range payload.Data {
		output = append(output, OutputMetricLabels{Label: name, Attributes: map[string]any{}})
	}

	return output, nil
}
