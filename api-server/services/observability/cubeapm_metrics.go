package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// CubeAPMMetricSource implements MetricSource for CubeAPM.
//
// CubeAPM serves metrics through a VictoriaMetrics engine mounted at
// /api/metrics, so the full Prometheus HTTP API is available: query,
// query_range, labels, label/<name>/values and series all behave as they do on
// Prometheus. That is why this source is thinner than the OpenObserve one — it
// needs none of OpenObserve's fallbacks onto a native search API, because the
// metadata endpoints here actually answer.
type CubeAPMMetricSource struct{}

// cubeAPMMetricsAPIPath is the prefix every metrics call hangs off. CubeAPM
// namespaces each signal under its own /api/<signal> root and then re-exposes the
// upstream API verbatim underneath, hence the doubled "api".
const cubeAPMMetricsAPIPath = "/api/metrics/api/v1"

// cubeAPMMetricQueryTimeout bounds a data query. Metadata lookups use the shorter
// cubeAPMMetadataTimeout — they back interactive pickers, where a slow answer is
// worse than no answer.
const (
	cubeAPMMetricQueryTimeout = 30 * time.Second
	cubeAPMMetadataTimeout    = 15 * time.Second
)

// cubeAPMSeriesLimit caps how many series the label/series endpoints consider.
// VictoriaMetrics accepts `limit` on these and will otherwise scan the full index,
// which on a busy install is slow enough to time out an autocomplete dropdown.
const cubeAPMSeriesLimit = 5000

func (s *CubeAPMMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_regex"}
}

func (s *CubeAPMMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return injectPromQLMatchers(q, req.LabelMatchers, req.Labels)
	}
	return "", nil
}

// cubeAPMMetricRangeParams derives the start/end/step trio for a range query.
// Mirrors the OpenObserve source: an unset step is chosen so the window yields
// roughly 100 points, which is what the charts render.
func cubeAPMMetricRangeParams(req FetchMetricsRequest) (start, end string, step int) {
	step = req.StepInterval
	if step <= 0 {
		step = int((req.EndTime - req.StartTime) / 1000 / 100)
		if step < 1 {
			step = 1
		}
	}
	return strconv.FormatInt(req.StartTime/1000, 10),
		strconv.FormatInt(req.EndTime/1000, 10),
		step
}

// cubeAPMPromResponse is the Prometheus query/query_range envelope. Values decode
// as [][]any because Prometheus encodes a sample as [<unix seconds>, "<value>"] —
// a number and a *string*, which no homogeneous type can hold.
type cubeAPMPromResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// cubeAPMMetadataResponse is the envelope shared by /labels, /label/<n>/values
// and /series. The first two return a list of strings; /series returns a list of
// label sets, so both shapes are decoded lazily by the callers.
type cubeAPMMetadataResponse struct {
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Data   json.RawMessage `json:"data"`
}

func (s *CubeAPMMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	results := OutputMetricQuery{Results: []QueryResult{}}
	start, end, step := cubeAPMMetricRangeParams(req)

	for queryKey, rawQuery := range req.Queries {
		promQL, err := injectPromQLMatchers(rawQuery, req.LabelMatchers, req.Labels)
		if err != nil {
			results.Results = append(results.Results, cubeAPMQueryError(queryKey, err.Error()))
			continue
		}

		if item, ok := req.QueryItems[queryKey]; ok && item.AggregateOperator != "" {
			promQL, err = wrapPromQLAggregator(promQL, item.AggregateOperator)
			if err != nil {
				results.Results = append(results.Results, cubeAPMQueryError(queryKey, err.Error()))
				continue
			}
		}

		form := neturl.Values{}
		form.Set("query", promQL)
		endpoint := cfg.URL + cubeAPMMetricsAPIPath
		if req.Instant {
			endpoint += "/query"
			form.Set("time", end)
			form.Set("step", strconv.Itoa(step))
		} else {
			endpoint += "/query_range"
			form.Set("start", start)
			form.Set("end", end)
			form.Set("step", strconv.Itoa(step))
		}

		payload, err := cubeAPMPostForm(cfg, endpoint, form, cubeAPMMetricQueryTimeout)
		if err != nil {
			results.Results = append(results.Results, cubeAPMQueryError(queryKey, err.Error()))
			continue
		}

		var decoded cubeAPMPromResponse
		if err := json.Unmarshal(payload, &decoded); err != nil {
			results.Results = append(results.Results, cubeAPMQueryError(queryKey, fmt.Sprintf("failed to parse CubeAPM response: %v", err)))
			continue
		}
		// A PromQL error comes back as HTTP 200 with status:"error" on
		// VictoriaMetrics, so the status field — not the status code — is what
		// separates "bad query" from "no data".
		if decoded.Status == "error" {
			msg := decoded.Error
			if msg == "" {
				msg = "CubeAPM returned an error with no message"
			}
			results.Results = append(results.Results, cubeAPMQueryError(queryKey, msg))
			continue
		}

		results.Results = append(results.Results, QueryResult{
			QueryKey: queryKey,
			Query:    promQL,
			Payload:  cubeAPMPromResults(decoded),
		})
	}

	return results, nil
}

func cubeAPMQueryError(queryKey, msg string) QueryResult {
	return QueryResult{QueryKey: queryKey, Error: &msg}
}

// cubeAPMPromResults flattens a Prometheus envelope into the internal Result shape.
// Instant queries carry a single `value` pair per series rather than a `values`
// array; both are normalized to the array form so charts need not care which ran.
func cubeAPMPromResults(decoded cubeAPMPromResponse) []Result {
	var out []Result
	for _, r := range decoded.Data.Result {
		samples := r.Values
		if len(samples) == 0 && len(r.Value) == 2 {
			samples = [][]any{r.Value}
		}

		timestamps := make([]int64, 0, len(samples))
		values := make([]float64, 0, len(samples))
		for _, sample := range samples {
			ts, value, ok := cubeAPMSample(sample)
			if !ok {
				continue
			}
			timestamps = append(timestamps, ts)
			values = append(values, value)
		}

		out = append(out, Result{Metric: r.Metric, Timestamps: timestamps, Values: values})
	}
	return out
}

// cubeAPMSample reads one [<unix seconds>, "<value>"] pair. The value is a string
// in the Prometheus wire format so that NaN/Inf survive the round trip; those
// parse to non-finite float64s and are dropped rather than charted as spikes.
func cubeAPMSample(sample []any) (tsMillis int64, value float64, ok bool) {
	if len(sample) != 2 {
		return 0, 0, false
	}
	seconds, isNum := sample[0].(float64)
	if !isNum {
		return 0, 0, false
	}
	raw, isStr := sample[1].(string)
	if !isStr {
		return 0, 0, false
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, 0, false
	}
	if parsed != parsed || parsed > 1e308 || parsed < -1e308 {
		return 0, 0, false
	}
	return int64(seconds * 1000), parsed, true
}

func (s *CubeAPMMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	// The regex literal must be quoted — `{__name__=~.*foo.*}` is not valid PromQL
	// and gets rejected outright rather than narrowing anything.
	var matchers []string
	if req.Metric != "" {
		matchers = append(matchers, fmt.Sprintf(`{__name__=~".*%s.*"}`, escapePromQLLabelValue(req.Metric)))
	}

	names, err := cubeAPMStringList(cfg,
		cfg.URL+cubeAPMMetricsAPIPath+"/label/__name__/values"+
			cubeAPMMetadataQuery(req.StartTime, req.EndTime, matchers, time.Now()))
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetrics, 0, len(names))
	for _, name := range names {
		output = append(output, OutputMetrics{Metric: name, Attributes: map[string]any{}})
	}
	return output, nil
}

func (s *CubeAPMMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	var matchers []string
	if req.MetricName != "" {
		matchers = append(matchers, fmt.Sprintf(`{__name__="%s"}`, escapePromQLLabelValue(req.MetricName)))
	}

	labels, err := cubeAPMStringList(cfg,
		cfg.URL+cubeAPMMetricsAPIPath+"/labels"+
			cubeAPMMetadataQuery(req.StartTime, req.EndTime, matchers, time.Now()))
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetricLabels, 0, len(labels))
	for _, label := range labels {
		if label == "__name__" {
			// The metric is already chosen by this point; offering it as a
			// filterable dimension of itself is noise.
			continue
		}
		output = append(output, OutputMetricLabels{Label: label, Attributes: map[string]any{}})
	}
	return output, nil
}

func (s *CubeAPMMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	label := strings.TrimSpace(req.Label)
	if !promqlLabelNameRe.MatchString(label) {
		return nil, fmt.Errorf("invalid label name: %q", req.Label)
	}

	// Scoping to the selected metric matters: without it the picker lists every
	// value the label takes across the whole install, most of which return no data
	// for the metric the user actually chose.
	var matchers []string
	if metric := common.GetString(req.Request, "metric_name"); metric != "" {
		matchers = append(matchers, fmt.Sprintf(`{__name__="%s"}`, escapePromQLLabelValue(metric)))
	}

	values, err := cubeAPMStringList(cfg,
		cfg.URL+cubeAPMMetricsAPIPath+"/label/"+neturl.PathEscape(label)+"/values"+
			cubeAPMMetadataQuery(req.StartTime, req.EndTime, matchers, time.Now()))
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetricsLabelValues, 0, len(values))
	for _, v := range values {
		output = append(output, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return output, nil
}

// cubeAPMMetadataQuery builds the shared query string for the Prometheus metadata
// endpoints. All of them accept start/end alongside match[], and every request type
// already carries the window — omitting it lets the server pick its own default
// range, which is how a match[]-filtered lookup ends up returning nothing.
func cubeAPMMetadataQuery(startMs, endMs int64, matchers []string, now time.Time) string {
	if endMs <= 0 {
		endMs = now.UnixMilli()
	}
	if startMs <= 0 {
		startMs = now.Add(-time.Hour).UnixMilli()
	}

	params := neturl.Values{}
	params.Set("start", strconv.FormatInt(startMs/1000, 10))
	params.Set("end", strconv.FormatInt(endMs/1000, 10))
	params.Set("limit", strconv.Itoa(cubeAPMSeriesLimit))
	for _, m := range matchers {
		if m != "" {
			params.Add("match[]", m)
		}
	}
	return "?" + params.Encode()
}

// cubeAPMStringList GETs a metadata endpoint whose `data` is a list of strings.
func cubeAPMStringList(cfg integrations.CubeAPMConfig, endpoint string) ([]string, error) {
	payload, err := cubeAPMGet(cfg, endpoint, cubeAPMMetadataTimeout)
	if err != nil {
		return nil, err
	}

	var decoded cubeAPMMetadataResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse CubeAPM metadata response: %w", err)
	}
	if decoded.Status == "error" {
		return nil, fmt.Errorf("CubeAPM metadata query failed: %s", decoded.Error)
	}

	var values []string
	if len(decoded.Data) > 0 {
		if err := json.Unmarshal(decoded.Data, &values); err != nil {
			return nil, fmt.Errorf("failed to parse CubeAPM metadata values: %w", err)
		}
	}
	return values, nil
}

// cubeAPMGet issues an authenticated GET and returns the body, mapping the status
// codes that mean something specific onto actionable errors.
func cubeAPMGet(cfg integrations.CubeAPMConfig, endpoint string, timeout time.Duration) ([]byte, error) {
	resp, err := common.HttpGet(endpoint,
		common.HttpWithHeaders(integrations.CubeAPMRequestHeaders(cfg.Token, "")),
		common.HttpWithTimeout(timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("CubeAPM request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, cubeAPMStatusError(resp.StatusCode, body)
	}
	return body, nil
}

// cubeAPMPostForm issues an authenticated form POST and returns the body.
func cubeAPMPostForm(cfg integrations.CubeAPMConfig, endpoint string, form neturl.Values, timeout time.Duration) ([]byte, error) {
	resp, err := common.HttpPost(endpoint,
		common.HttpWithHeaders(integrations.CubeAPMRequestHeaders(cfg.Token, "application/x-www-form-urlencoded")),
		common.HttpWithBody(io.NopCloser(bytes.NewReader([]byte(form.Encode())))),
		common.HttpWithTimeout(timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("CubeAPM request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, cubeAPMStatusError(resp.StatusCode, body)
	}
	return body, nil
}

// cubeAPMStatusError turns a non-200 into an error whose text says what to fix.
// The body is truncated because CubeAPM echoes the offending query back on a parse
// error, and an unbounded query string in a UI toast is unreadable.
func cubeAPMStatusError(status int, body []byte) error {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		text = text[:500] + "…"
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("CubeAPM rejected the credentials (HTTP 401) — check the CubeAPM integration token")
	case http.StatusForbidden:
		return fmt.Errorf("insufficient permissions for CubeAPM (HTTP 403)")
	default:
		return fmt.Errorf("CubeAPM returned HTTP %d: %s", status, text)
	}
}

// FetchMetricSeries implements the optional MetricSeriesSource capability:
// "which metric families have series for workload W in namespace N".
//
// CubeAPM's engine answers /label/__name__/values with a match[] selector, which
// is the same primitive the Prometheus source uses — so the candidate label
// conventions, the selector rendering and the result assembly are all shared with
// it rather than reimplemented here. Only the transport differs: a direct HTTP
// call instead of a relay proxy hop.
func (s *CubeAPMMetricSource) FetchMetricSeries(ctx *security.RequestContext, req FetchMetricSeriesRequest) (MetricSeriesResult, error) {
	cfg, err := integrations.GetCubeAPMConfigs(ctx, req.AccountId)
	if err != nil {
		return MetricSeriesResult{}, fmt.Errorf("failed to get CubeAPM configs: %w", err)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = seriesMatchDefaultLimit
	}
	if limit > seriesMatchMaxLimit {
		limit = seriesMatchMaxLimit
	}

	start, end := req.StartTime, req.EndTime
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 {
		start = end - int64(seriesMatchDefaultLookback.Seconds())
	}

	selectors, err := buildSeriesMatchSelectors(req.Namespace, req.Workload, SeriesMatchCandidates)
	if err != nil {
		return MetricSeriesResult{}, err
	}

	// Probed sequentially rather than concurrently: unlike the relay-backed
	// Prometheus source, every lookup here is a direct HTTP call to one CubeAPM
	// instance, and fanning a dozen at it to save a fraction of a second on an
	// investigation-time path is not a trade worth making.
	families := make([][]string, len(selectors))
	truncated := make([]bool, len(selectors))
	failures := 0
	var firstErr error

	for i, selector := range selectors {
		endpoint := cfg.URL + cubeAPMMetricsAPIPath + "/label/__name__/values" +
			cubeAPMMetadataQuery(start*1000, end*1000, []string{selector}, time.Now())

		body, err := cubeAPMGet(cfg, endpoint, cubeAPMMetadataTimeout)
		if err != nil {
			failures++
			if firstErr == nil {
				firstErr = err
			}
			ctx.GetLogger().Warn("cubeapm series-match: candidate lookup failed",
				"selector", selector, "account_id", req.AccountId, "error", err)
			continue
		}

		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			failures++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if status, ok := decoded["status"].(string); ok && status == "error" {
			failures++
			if firstErr == nil {
				errMsg, _ := decoded["error"].(string)
				firstErr = fmt.Errorf("cubeapm: label-values query failed: %s", errMsg)
			}
			continue
		}

		families[i], truncated[i] = parseFamilyValues(decoded["data"], decoded["warnings"], limit)
	}

	// Every candidate failing means the backend is unreachable, not that the
	// workload has no metrics — surfacing it beats returning a misleading empty.
	if len(selectors) > 0 && failures == len(selectors) {
		return MetricSeriesResult{}, firstErr
	}

	return assembleSeriesResult(SeriesMatchCandidates, families, truncated), nil
}
