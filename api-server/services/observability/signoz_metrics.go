package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"nudgebee/services/common"
	"nudgebee/services/security"

	"golang.org/x/sync/errgroup"
)

// SignozMetricSource implements MetricSource for a user-configured SigNoz
// (self-hosted or Cloud).
//
// SigNoz has no Prometheus HTTP API. PromQL runs through its own
// /api/v4/query_range (present from v0.51 to current releases), and metric
// names, labels and values come from its autocomplete endpoints. Metrics sent
// by SigNoz's own collectors keep their OTel names (k8s.pod.cpu.usage,
// k8s.namespace.name), which PromQL can only address in quoted form:
// {"k8s.pod.cpu.usage","k8s.namespace.name"="shop"}. Current releases no
// longer answer for the underscore spelling, so queries must use the dotted
// names.
type SignozMetricSource struct {
	conn SignozSaasSource
}

const (
	signozQueryRangePath = "/api/v4/query_range"
	signozAutocomplete   = "/api/v3/autocomplete/"
	// signozMetricListLimit raises the autocomplete's default page of 50.
	signozMetricListLimit = 1000
)

func (s *SignozMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_regex"}
}

func (s *SignozMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return signozRenderPromQL(q, req.LabelMatchers, req.Labels)
	}
	return "", nil
}

// signozBareMetricNameRe is a bare metric name in OTel spelling, which has at
// least one dot: what the builder sends when a user picks a metric and nothing
// else. It stops short of `-` and `/`, so `a/b` stays a division.
var signozBareMetricNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_:]*(\.[a-zA-Z0-9_:]+)+$`)

// signozRenderPromQL turns a caller-supplied query into the PromQL sent to
// SigNoz. The preview (GetQuery) and execution share it, so the query shown is
// the query that ran. It drops the relay-only __CLUSTER__ token (a direct API
// call has no cluster label to substitute), quotes a bare OTel metric name, and
// writes matcher labels quoted when they are not classic Prometheus names.
func signozRenderPromQL(raw string, matchers []LabelMatcher, labels map[string]string) (string, error) {
	expr := signozQuoteDottedNames(strings.TrimSpace(stripClusterPlaceholder(raw)))
	return injectPromQLMatchersWith(expr, matchers, labels, signozPromQLLabelName)
}

// signozQuoteDottedNames rewrites dotted OTel names written bare — by a person in
// the Code tab or by an agent used to classic PromQL — into the quoted form SigNoz
// parses: a metric becomes {"k8s.pod.cpu.usage"} (taking any selector that follows
// into the same braces), a label inside {…} or by/without(…) becomes
// "k8s.namespace.name". Classic names, string literals, numbers and [range]
// durations are left as they are, so an expression without dotted names comes
// back unchanged.
func signozQuoteDottedNames(expr string) string {
	var b strings.Builder
	braces := 0
	inGrouping := false
	lastWord := ""
	for i := 0; i < len(expr); {
		c := expr[i]
		switch c {
		case '"', '\'', '`':
			j := i + 1
			for j < len(expr) && expr[j] != c {
				if expr[j] == '\\' && c != '`' {
					j++
				}
				j++
			}
			if j < len(expr) {
				j++
			}
			b.WriteString(expr[i:j])
			i = j
			continue
		case '[':
			if j := strings.IndexByte(expr[i:], ']'); j >= 0 {
				b.WriteString(expr[i : i+j+1])
				i += j + 1
				continue
			}
		case '{':
			braces++
		case '}':
			if braces > 0 {
				braces--
			}
		case '(':
			switch strings.ToLower(lastWord) {
			case "by", "without", "on", "ignoring", "group_left", "group_right":
				inGrouping = true
			}
		case ')':
			inGrouping = false
		}

		if isPromIdentStart(c) && (i == 0 || !isPromIdentChar(expr[i-1])) {
			j := i
			for j < len(expr) && (isPromIdentChar(expr[j]) || expr[j] == '.') {
				j++
			}
			word := expr[i:j]
			lastWord = word
			if !signozBareMetricNameRe.MatchString(word) {
				b.WriteString(word)
				i = j
				continue
			}
			if braces > 0 || inGrouping {
				b.WriteString(`"` + word + `"`)
				i = j
				continue
			}
			// Metric position. A selector that follows is folded into one {…}.
			k := j
			for k < len(expr) && expr[k] == ' ' {
				k++
			}
			if k < len(expr) && expr[k] == '{' {
				m := k + 1
				for m < len(expr) && expr[m] == ' ' {
					m++
				}
				if m < len(expr) && expr[m] == '}' {
					b.WriteString(`{"` + word + `"}`)
					i = m + 1
					continue
				}
				b.WriteString(`{"` + word + `", `)
				braces++
				i = m
				continue
			}
			b.WriteString(`{"` + word + `"}`)
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func isPromIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isPromIdentChar(c byte) bool {
	return isPromIdentStart(c) || c == ':' || (c >= '0' && c <= '9')
}

// signozPromQLLabelName writes a classic label name bare and any other valid
// UTF-8 name quoted. A quote, backslash or control character is refused rather
// than escaped: no OTel attribute uses one, and accepting it would let a
// caller close the quote and add selectors of their own.
func signozPromQLLabelName(name string) (string, error) {
	if promqlLabelNameRe.MatchString(name) {
		return name, nil
	}
	if name == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "\"\\{}") {
		return "", fmt.Errorf("invalid label name %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid label name %q", name)
		}
	}
	return `"` + name + `"`, nil
}

// signozRangeParams returns start/end in milliseconds and the step in seconds.
// An unset step gives about 100 points over the window, as the other PromQL
// sources do. An instant query with no window evaluates the last minute.
func signozRangeParams(req FetchMetricsRequest) (start, end int64, step int) {
	start, end = req.StartTime, req.EndTime
	step = req.StepInterval
	if start <= 0 || start >= end {
		if step <= 0 {
			step = 60
		}
		return end - int64(step)*1000, end, step
	}
	if step <= 0 {
		step = int((end - start) / 1000 / 100)
	}
	if step < 1 {
		step = 1
	}
	return start, end, step
}

// signozQueryRangeResponse is the v4 query_range envelope. A failed PromQL
// query comes back as HTTP 500 with data {"Query-A": "<parse error>"}.
type signozQueryRangeResponse struct {
	Status string          `json:"status"`
	Error  string          `json:"error"`
	Data   json.RawMessage `json:"data"`
}

type signozQueryRangeData struct {
	Result []struct {
		QueryName string `json:"queryName"`
		Series    []struct {
			Labels map[string]string `json:"labels"`
			Values []struct {
				Timestamp int64  `json:"timestamp"`
				Value     string `json:"value"`
			} `json:"values"`
		} `json:"series"`
	} `json:"result"`
}

func (s *SignozMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	results := OutputMetricQuery{Results: []QueryResult{}}
	start, end, step := signozRangeParams(req)
	// SigNoz's "value" panel refuses more than one series, so an instant query runs
	// as a range over its last step and keeps each series' latest sample.
	if req.Instant {
		start = end - int64(step)*1000
	}

	// One SigNoz call per key, a few at a time: a chart page asks for ten or more keys,
	// and running them one after another made it wait for every round trip in turn.
	var mu sync.Mutex
	add := func(r QueryResult) {
		mu.Lock()
		results.Results = append(results.Results, r)
		mu.Unlock()
	}
	var g errgroup.Group
	g.SetLimit(signozQueryConcurrency)
	for queryKey, rawQuery := range req.Queries {
		g.Go(func() error {
			add(signozRunKeySafely(ctx, queryKey, func() QueryResult {
				return s.runQuery(ctx, req, queryKey, rawQuery, start, end, step)
			}))
			return nil
		})
	}
	_ = g.Wait()
	sort.Slice(results.Results, func(i, j int) bool { return results.Results[i].QueryKey < results.Results[j].QueryKey })
	return results, nil
}

// signozRunKeySafely runs one key's query on its own goroutine. A panic there would
// otherwise take the whole process down; instead it is logged with its stack and
// fails that key alone, so the other keys of the request still answer.
func signozRunKeySafely(ctx *security.RequestContext, queryKey string, run func() QueryResult) (result QueryResult) {
	defer func() {
		if r := recover(); r != nil {
			ctx.GetLogger().Error("signoz: metrics query panicked", "query_key", queryKey, "panic", r, "stack", string(debug.Stack()))
			result = signozQueryError(queryKey, fmt.Sprintf("internal error while running this query: %v", r))
		}
	}()
	return run()
}

// signozQueryConcurrency bounds how many queries one request runs at once.
const signozQueryConcurrency = 4

// runQuery renders and runs one key; a failure is reported on that key alone.
func (s *SignozMetricSource) runQuery(ctx *security.RequestContext, req FetchMetricsRequest, queryKey, rawQuery string, start, end int64, step int) QueryResult {
	promQL, err := signozRenderPromQL(rawQuery, req.LabelMatchers, req.Labels)
	if err != nil {
		return signozQueryError(queryKey, err.Error())
	}
	if item, ok := req.QueryItems[queryKey]; ok && item.AggregateOperator != "" {
		if promQL, err = wrapPromQLAggregator(promQL, item.AggregateOperator); err != nil {
			return signozQueryError(queryKey, err.Error())
		}
	}
	body := map[string]any{
		"start": start,
		"end":   end,
		"step":  step,
		"compositeQuery": map[string]any{
			"queryType": "promql",
			"panelType": "graph",
			"promQueries": map[string]any{
				"A": map[string]any{"name": "A", "query": promQL, "disabled": false},
			},
		},
	}
	payload, err := s.call(ctx, req.AccountId, http.MethodPost, signozQueryRangePath, body)
	if err != nil {
		return signozQueryError(queryKey, err.Error())
	}
	series, err := signozPromResults(payload)
	if err != nil {
		return signozQueryError(queryKey, err.Error())
	}
	if req.Instant {
		series = signozLatestSamples(series)
	}
	return QueryResult{QueryKey: queryKey, Query: promQL, Payload: series}
}

// signozLatestSamples keeps each series' last sample: the instant value.
func signozLatestSamples(series []Result) []Result {
	for i := range series {
		if n := len(series[i].Values); n > 1 {
			series[i].Timestamps = series[i].Timestamps[n-1:]
			series[i].Values = series[i].Values[n-1:]
		}
	}
	return series
}

func signozQueryError(queryKey, msg string) QueryResult {
	return QueryResult{QueryKey: queryKey, Error: &msg}
}

// signozPromResults reads a v4 query_range answer into the internal shape.
// Timestamps arrive in milliseconds and values as strings; NaN/Inf samples are
// dropped rather than charted as spikes.
func signozPromResults(payload []byte) ([]Result, error) {
	var env signozQueryRangeResponse
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz response: %w", err)
	}
	if env.Status == "error" {
		return nil, signozQueryRangeError(env)
	}
	var data signozQueryRangeData
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return nil, fmt.Errorf("failed to parse SigNoz response: %w", err)
		}
	}
	out := []Result{}
	for _, r := range data.Result {
		for _, s := range r.Series {
			res := Result{Metric: s.Labels, Timestamps: []int64{}, Values: []float64{}}
			if res.Metric == nil {
				res.Metric = map[string]string{}
			}
			for _, v := range s.Values {
				f, err := strconv.ParseFloat(v.Value, 64)
				if err != nil || f != f || f > 1e308 || f < -1e308 {
					continue
				}
				res.Timestamps = append(res.Timestamps, v.Timestamp/1000)
				res.Values = append(res.Values, f)
			}
			out = append(out, res)
		}
	}
	return out, nil
}

// signozQueryRangeError surfaces SigNoz's per-query message ("1:5: parse
// error: …") rather than its generic "error in prom queries".
func signozQueryRangeError(env signozQueryRangeResponse) error {
	var perQuery map[string]string
	if json.Unmarshal(env.Data, &perQuery) == nil {
		for _, msg := range perQuery {
			if msg != "" {
				return fmt.Errorf("SigNoz rejected the query: %s", msg)
			}
		}
	}
	if env.Error != "" {
		return fmt.Errorf("SigNoz rejected the query: %s", env.Error)
	}
	return fmt.Errorf("SigNoz returned an error with no message")
}

// call sends one request and returns the body. A non-2xx answer that carries
// SigNoz's error envelope is returned as the body, so the caller reports
// SigNoz's own message; anything else is an error.
func (s *SignozMetricSource) call(ctx *security.RequestContext, accountId, method, path string, body any) ([]byte, error) {
	resp, err := s.conn.signozDo(ctx, accountId, method, path, body)
	if err != nil {
		return nil, fmt.Errorf("failed to call SigNoz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read SigNoz response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return payload, nil
	}
	var env signozQueryRangeResponse
	if json.Unmarshal(payload, &env) == nil && env.Status == "error" {
		return payload, nil
	}
	msg := strings.TrimSpace(string(payload))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return nil, fmt.Errorf("SigNoz %s returned HTTP %d: %s", path, resp.StatusCode, msg)
}

// autocomplete calls one of SigNoz's metric metadata endpoints.
func (s *SignozMetricSource) autocomplete(ctx *security.RequestContext, accountId, endpoint string, params neturl.Values) (json.RawMessage, error) {
	params.Set("dataSource", "metrics")
	params.Set("aggregateOperator", "noop")
	payload, err := s.call(ctx, accountId, http.MethodGet, signozAutocomplete+endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var env signozQueryRangeResponse
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("failed to parse SigNoz response: %w", err)
	}
	if env.Status == "error" {
		return nil, signozQueryRangeError(env)
	}
	return env.Data, nil
}

type signozAttributeKeys struct {
	AttributeKeys []struct {
		Key string `json:"key"`
	} `json:"attributeKeys"`
}

func (s *SignozMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	params := neturl.Values{}
	params.Set("searchText", req.Metric)
	params.Set("limit", strconv.Itoa(signozMetricListLimit))
	data, err := s.autocomplete(ctx, req.AccountId, "aggregate_attributes", params)
	if err != nil {
		return nil, err
	}
	var keys signozAttributeKeys
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &keys); err != nil {
			return nil, fmt.Errorf("failed to parse SigNoz metric list: %w", err)
		}
	}
	out := make([]OutputMetrics, 0, len(keys.AttributeKeys))
	for _, k := range keys.AttributeKeys {
		if k.Key != "" {
			out = append(out, OutputMetrics{Metric: k.Key, Attributes: map[string]any{}})
		}
	}
	return out, nil
}

func (s *SignozMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	// SigNoz lists labels per metric; with none chosen there is nothing to ask.
	if strings.TrimSpace(req.MetricName) == "" {
		return []OutputMetricLabels{}, nil
	}
	params := neturl.Values{}
	params.Set("aggregateAttribute", req.MetricName)
	params.Set("searchText", "")
	data, err := s.autocomplete(ctx, req.AccountId, "attribute_keys", params)
	if err != nil {
		return nil, err
	}
	var keys signozAttributeKeys
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &keys); err != nil {
			return nil, fmt.Errorf("failed to parse SigNoz labels: %w", err)
		}
	}
	out := make([]OutputMetricLabels, 0, len(keys.AttributeKeys))
	for _, k := range keys.AttributeKeys {
		if k.Key != "" && k.Key != "__name__" {
			out = append(out, OutputMetricLabels{Label: k.Key, Attributes: map[string]any{}})
		}
	}
	return out, nil
}

func (s *SignozMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	label := strings.TrimSpace(req.Label)
	if _, err := signozPromQLLabelName(label); err != nil {
		return nil, err
	}
	params := neturl.Values{}
	params.Set("aggregateAttribute", common.GetString(req.Request, "metric_name"))
	params.Set("attributeKey", label)
	params.Set("searchText", "")
	params.Set("filterAttributeKeyDataType", "string")
	params.Set("tagType", "tag")
	data, err := s.autocomplete(ctx, req.AccountId, "attribute_values", params)
	if err != nil {
		return nil, err
	}
	var values struct {
		StringAttributeValues []string `json:"stringAttributeValues"`
	}
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("failed to parse SigNoz label values: %w", err)
		}
	}
	out := make([]OutputMetricsLabelValues, 0, len(values.StringAttributeValues))
	for _, v := range values.StringAttributeValues {
		out = append(out, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return out, nil
}
