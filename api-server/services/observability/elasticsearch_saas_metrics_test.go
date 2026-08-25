package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/query"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeESMetricsWhere_EqAppendsKeyword(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"attributes.metric.attributes.service@name": {query.Eq: "services-server"},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["attributes.metric.attributes.service@name.keyword"]; !ok {
		t.Fatalf("expected .keyword suffix, got fields: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_NumericEqDoesNotAppend(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"metric.attributes.http@response@status_code": {query.Eq: float64(200)},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["metric.attributes.http@response@status_code"]; !ok {
		t.Fatalf("expected bare field for numeric value, got: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_AlreadyKeyword(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"name.keyword": {query.Eq: "traces.span.metrics.calls"},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["name.keyword"]; !ok || len(got.Binary) != 1 {
		t.Fatalf("expected unchanged .keyword field, got: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_InWithStringSlice(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"serviceName": {query.In: []any{"services-server", "llm-server"}},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["serviceName.keyword"]; !ok {
		t.Fatalf("expected .keyword for _in string slice, got: %v", mapKeys(got.Binary))
	}
}

func TestNormalizeESMetricsWhere_NestedAndOrNot(t *testing.T) {
	nested := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"serviceName": {query.Eq: "services-server"},
		},
	}
	wc := query.QueryWhereClause{
		And: []query.QueryWhereClause{nested},
		Or:  []query.QueryWhereClause{nested},
		Not: &nested,
	}
	got := normalizeESMetricsWhere(wc)
	for _, branch := range [][]query.QueryWhereClause{got.And, got.Or} {
		if _, ok := branch[0].Binary["serviceName.keyword"]; !ok {
			t.Fatalf("nested And/Or not normalized")
		}
	}
	if _, ok := got.Not.Binary["serviceName.keyword"]; !ok {
		t.Fatalf("nested Not not normalized")
	}
}

func TestNormalizeESMetricsWhere_UserPayload(t *testing.T) {
	// Exact payload from user's failing request.
	raw := `[{"_binary":{"attributes.metric.attributes.service@name":{"_eq":"services-server"}}}]`
	var clauses []query.QueryWhereClause
	if err := json.Unmarshal([]byte(raw), &clauses); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := normalizeESMetricsWhere(clauses[0])
	clause, err := whereToBool(got)
	if err != nil {
		t.Fatalf("whereToBool: %v", err)
	}
	out, _ := json.Marshal(clause)
	if !strings.Contains(string(out), "service@name.keyword") {
		t.Fatalf("generated DSL missing .keyword suffix: %s", out)
	}
}

func mapKeys(m query.BinaryWhereClause) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ---------- buildESMetricsQueryBody tests ----------

func TestBuildESMetricsQueryBody_DSLMode_AddsDefaultSize(t *testing.T) {
	body, err := buildESMetricsQueryBody("dsl", `{"query":{"match_all":{}}}`, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := body["size"]; !ok || got != 10000 {
		t.Fatalf("expected size=10000 injected, got %v (ok=%v)", got, ok)
	}
}

func TestBuildESMetricsQueryBody_DSLMode_RespectsUserSize(t *testing.T) {
	body, err := buildESMetricsQueryBody("dsl", `{"size":5,"query":{"match_all":{}}}`, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// json.Unmarshal numbers into map[string]any as float64.
	if got, ok := body["size"].(float64); !ok || got != 5 {
		t.Fatalf("expected size=5 preserved, got %v (type %T)", body["size"], body["size"])
	}
}

func TestBuildESMetricsQueryBody_DSLMode_WrapsWithTimeRange(t *testing.T) {
	body, err := buildESMetricsQueryBody("dsl", `{"query":{"match":{"name":"x"}}}`, 1700000000000, 1700003600000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q, ok := body["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected query map, got %T", body["query"])
	}
	b, ok := q["bool"].(map[string]any)
	if !ok {
		t.Fatalf("expected bool wrapper, got %T", q["bool"])
	}
	filter, ok := b["filter"].([]any)
	if !ok || len(filter) != 2 {
		t.Fatalf("expected filter array of len 2, got %v", b["filter"])
	}
	out, _ := json.Marshal(body)
	if !strings.Contains(string(out), "epoch_millis") {
		t.Fatalf("expected epoch_millis in output: %s", out)
	}
	if !strings.Contains(string(out), `"name":"x"`) {
		t.Fatalf("expected original user query preserved: %s", out)
	}
}

func TestBuildESMetricsQueryBody_DSLMode_NoQueryFieldDefaultsToMatchAll(t *testing.T) {
	body, err := buildESMetricsQueryBody("dsl", `{}`, 1700000000000, 1700003600000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, _ := json.Marshal(body)
	if !strings.Contains(string(out), "match_all") {
		t.Fatalf("expected match_all substituted, got: %s", out)
	}
	if !strings.Contains(string(out), "epoch_millis") {
		t.Fatalf("expected epoch_millis appended, got: %s", out)
	}
}

func TestBuildESMetricsQueryBody_DSLMode_InvalidJSONReturnsError(t *testing.T) {
	_, err := buildESMetricsQueryBody("dsl", "not json", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "failed to parse DSL query body") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

func TestBuildESMetricsQueryBody_DSLMode_NullBodyReturnsError(t *testing.T) {
	_, err := buildESMetricsQueryBody("dsl", "null", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "must be a JSON object, got null") {
		t.Fatalf("expected null-body error, got: %v", err)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_BinaryEqRendersBoolFilter(t *testing.T) {
	body, err := buildESMetricsQueryBody("", `[{"_binary":{"serviceName":{"_eq":"api"}}}]`, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, _ := json.Marshal(body)
	if !strings.Contains(string(out), "serviceName.keyword") {
		t.Fatalf("expected .keyword suffix on string-eq field, got: %s", out)
	}
	if !strings.Contains(string(out), `"bool"`) || !strings.Contains(string(out), `"filter"`) {
		t.Fatalf("expected bool/filter structure, got: %s", out)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_AppendsTimeRange(t *testing.T) {
	body, err := buildESMetricsQueryBody("", `[{"_binary":{"serviceName":{"_eq":"api"}}}]`, 1700000000000, 1700003600000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := body["query"].(map[string]any)
	b := q["bool"].(map[string]any)
	filter, ok := b["filter"].([]any)
	if !ok || len(filter) != 2 {
		t.Fatalf("expected filter of len 2 (clause + time range), got: %v", b["filter"])
	}
	out, _ := json.Marshal(body)
	if !strings.Contains(string(out), "epoch_millis") {
		t.Fatalf("expected epoch_millis appended, got: %s", out)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_NoTimeRangeOmitsRangeClause(t *testing.T) {
	body, err := buildESMetricsQueryBody("", `[{"_binary":{"serviceName":{"_eq":"api"}}}]`, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, _ := json.Marshal(body)
	if strings.Contains(string(out), "epoch_millis") {
		t.Fatalf("expected no epoch_millis when start/end are 0, got: %s", out)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_InvalidJSONReturnsError(t *testing.T) {
	_, err := buildESMetricsQueryBody("", "not an array", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "failed to parse query filters") {
		t.Fatalf("expected parse error, got: %v", err)
	}
}

// ---------- Coverage tests: operator/shape variants through the helper ----------

func TestBuildESMetricsQueryBody_DSLMode_NonObjectInputReturnsError(t *testing.T) {
	// JSON arrays/scalars cannot be unmarshalled into map[string]any —
	// the parse branch fires before the nil-body branch.
	_, err := buildESMetricsQueryBody("dsl", "[]", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "failed to parse DSL query body") {
		t.Fatalf("expected parse error for array input, got: %v", err)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_EmptyArray(t *testing.T) {
	body, err := buildESMetricsQueryBody("", "[]", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := body["query"].(map[string]any)
	b := q["bool"].(map[string]any)
	// No filters appended (no clauses, no time range) — bool wrapper still present.
	if filter, ok := b["filter"].([]any); ok && len(filter) != 0 {
		t.Fatalf("expected empty filter slice, got: %v", filter)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_MultipleClauses(t *testing.T) {
	body, err := buildESMetricsQueryBody(
		"",
		`[{"_binary":{"serviceName":{"_eq":"api"}}},{"_binary":{"region":{"_eq":"us-east"}}}]`,
		0, 0,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := body["query"].(map[string]any)
	b := q["bool"].(map[string]any)
	filter, ok := b["filter"].([]any)
	if !ok || len(filter) != 2 {
		t.Fatalf("expected 2 filter clauses (one per input clause), got: %v", b["filter"])
	}
	out, _ := json.Marshal(body)
	if !strings.Contains(string(out), "serviceName.keyword") || !strings.Contains(string(out), "region.keyword") {
		t.Fatalf("expected both clauses normalized in output: %s", out)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_AndOrNotPropagate(t *testing.T) {
	// Nested And+Or+Not should pass through the helper into a coherent bool tree.
	raw := `[{"_and":[
		{"_binary":{"serviceName":{"_eq":"api"}}},
		{"_or":[
			{"_binary":{"level":{"_eq":"ERROR"}}},
			{"_binary":{"level":{"_eq":"WARN"}}}
		]},
		{"_not":{"_binary":{"region":{"_eq":"eu"}}}}
	]}]`
	body, err := buildESMetricsQueryBody("", raw, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, _ := json.Marshal(body)
	s := string(out)
	if !strings.Contains(s, "serviceName.keyword") {
		t.Fatalf("expected AND branch normalized: %s", s)
	}
	if !strings.Contains(s, "should") {
		t.Fatalf("expected OR rendered as should: %s", s)
	}
	if !strings.Contains(s, "must_not") {
		t.Fatalf("expected NOT rendered as must_not: %s", s)
	}
}

func TestBuildESMetricsQueryBody_BuilderMode_NonEqOperators(t *testing.T) {
	// Spot-check that non-_eq operators flow through the helper to whereToBool
	// and produce the operator-specific ES clause shape — not a structural
	// regression test of every operator (binaryToESClause covers those).
	cases := []struct {
		name           string
		input          string
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:        "_gt produces range/gt",
			input:       `[{"_binary":{"latency_ms":{"_gt":100}}}]`,
			mustContain: []string{`"range"`, `"gt":100`},
		},
		{
			name:        "_in produces terms",
			input:       `[{"_binary":{"serviceName":{"_in":["api","web"]}}}]`,
			mustContain: []string{`"terms"`, `serviceName.keyword`},
		},
		{
			name:        "_is_null=true produces must_not exists",
			input:       `[{"_binary":{"trace_id":{"_is_null":true}}}]`,
			mustContain: []string{`"must_not"`, `"exists"`, `"field":"trace_id"`},
		},
		{
			name:        "_neq produces must_not term",
			input:       `[{"_binary":{"serviceName":{"_neq":"api"}}}]`,
			mustContain: []string{`"must_not"`, `"term"`, `serviceName.keyword`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := buildESMetricsQueryBody("", tc.input, 0, 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out, _ := json.Marshal(body)
			s := string(out)
			for _, want := range tc.mustContain {
				if !strings.Contains(s, want) {
					t.Errorf("expected output to contain %q, got: %s", want, s)
				}
			}
			for _, unwant := range tc.mustNotContain {
				if strings.Contains(s, unwant) {
					t.Errorf("expected output NOT to contain %q, got: %s", unwant, s)
				}
			}
		})
	}
}

// ---------- GetQuery tests ----------

func TestGetQuery_EmptyQueriesReturnsEmptyString(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	got, err := src.GetQuery(nil, FetchMetricsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string for empty Queries, got: %q", got)
	}
}

func TestGetQuery_DSLMode_ReturnsCompactJSON(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	req := FetchMetricsRequest{
		Queries: map[string]string{"A": `{"query":{"match_all":{}}}`},
		Request: map[string]any{"query_type": "dsl"},
	}
	got, err := src.GetQuery(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("expected compact JSON (no newlines), got: %q", got)
	}
	var roundtrip map[string]any
	if err := json.Unmarshal([]byte(got), &roundtrip); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, got)
	}
	if _, ok := roundtrip["query"]; !ok {
		t.Fatalf("expected `query` key in output, got: %s", got)
	}
}

func TestGetQuery_BuilderMode_ProducesNormalizedDSL(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	req := FetchMetricsRequest{
		Queries: map[string]string{"A": `[{"_binary":{"serviceName":{"_eq":"api"}}}]`},
	}
	got, err := src.GetQuery(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, ".keyword") {
		t.Fatalf("expected normalization to add .keyword, got: %s", got)
	}
	if !strings.Contains(got, `"bool"`) || !strings.Contains(got, `"filter"`) {
		t.Fatalf("expected bool/filter structure, got: %s", got)
	}
}

func TestGetQuery_DSLMode_PropagatesParseError(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	req := FetchMetricsRequest{
		Queries: map[string]string{"A": "not json"},
		Request: map[string]any{"query_type": "dsl"},
	}
	_, err := src.GetQuery(nil, req)
	if err == nil {
		t.Fatalf("expected error for invalid DSL input, got nil")
	}
}

func TestGetQuery_DSLMode_TimeRangeAndMerged(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	req := FetchMetricsRequest{
		Queries:   map[string]string{"A": `{"query":{"match":{"name":"x"}}}`},
		Request:   map[string]any{"query_type": "dsl"},
		StartTime: 1700000000000,
		EndTime:   1700003600000,
	}
	got, err := src.GetQuery(nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "epoch_millis") {
		t.Fatalf("expected epoch_millis in time-range-merged output, got: %s", got)
	}
	if !strings.Contains(got, `"name":"x"`) {
		t.Fatalf("expected original user query preserved inside bool/filter, got: %s", got)
	}
}

// ---------- Parity tests: GetQuery output == helper output ----------

func TestGetQuery_MatchesHelperOutput_DSLMode(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	queryDSL := `{"query":{"match":{"name":"x"}}}`
	start, end := int64(1700000000000), int64(1700003600000)
	req := FetchMetricsRequest{
		Queries:   map[string]string{"A": queryDSL},
		Request:   map[string]any{"query_type": "dsl"},
		StartTime: start,
		EndTime:   end,
	}
	gotA, err := src.GetQuery(nil, req)
	if err != nil {
		t.Fatalf("GetQuery error: %v", err)
	}
	body, err := buildESMetricsQueryBody("dsl", queryDSL, start, end)
	if err != nil {
		t.Fatalf("helper error: %v", err)
	}
	gotBBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if gotA != string(gotBBytes) {
		t.Fatalf("parity mismatch:\n  GetQuery: %s\n  helper:   %s", gotA, string(gotBBytes))
	}
}

func TestGetQuery_MatchesHelperOutput_BuilderMode(t *testing.T) {
	src := &ElasticSaasMetricSource{}
	queryDSL := `[{"_binary":{"serviceName":{"_eq":"api"}}}]`
	start, end := int64(1700000000000), int64(1700003600000)
	req := FetchMetricsRequest{
		Queries:   map[string]string{"A": queryDSL},
		StartTime: start,
		EndTime:   end,
	}
	gotA, err := src.GetQuery(nil, req)
	if err != nil {
		t.Fatalf("GetQuery error: %v", err)
	}
	body, err := buildESMetricsQueryBody("", queryDSL, start, end)
	if err != nil {
		t.Fatalf("helper error: %v", err)
	}
	gotBBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if gotA != string(gotBBytes) {
		t.Fatalf("parity mismatch:\n  GetQuery: %s\n  helper:   %s", gotA, string(gotBBytes))
	}
}

func TestIsOTelKeywordField(t *testing.T) {
	for _, f := range []string{"resource.attributes.k8s.namespace.name", "scope.name", "metrics.k8s.pod.cpu.usage"} {
		if !isOTelKeywordField(f) {
			t.Fatalf("expected %q to be an OTel keyword field", f)
		}
	}
	for _, f := range []string{"serviceName", "name", "attributes.metric.attributes.service@name"} {
		if isOTelKeywordField(f) {
			t.Fatalf("did not expect %q to be an OTel keyword field", f)
		}
	}
}

// OTel-native resource attributes are already keyword — normalizeESMetricsWhere
// must not append .keyword (the subfield does not exist -> matches nothing).
func TestNormalizeESMetricsWhere_OTelFieldNoKeyword(t *testing.T) {
	wc := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"resource.attributes.k8s.namespace.name": {query.Eq: "nudgebee"},
		},
	}
	got := normalizeESMetricsWhere(wc)
	if _, ok := got.Binary["resource.attributes.k8s.namespace.name"]; !ok || len(got.Binary) != 1 {
		t.Fatalf("expected bare OTel field, got: %v", mapKeys(got.Binary))
	}
}

// findMetricSeries returns the Result whose __name__ matches, or fails.
func findMetricSeries(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Metric["__name__"] == name {
			return r
		}
	}
	t.Fatalf("no series with __name__=%q in %d results", name, len(results))
	return Result{}
}

// OTel-native mapping: metric name is a key under "metrics", dimensions under
// resource.attributes — value/labels must be extracted from there.
func TestParseESMetricsHits_OTelShape(t *testing.T) {
	body := `{"hits":{"hits":[
		{"_source":{"@timestamp":"2026-06-17T17:38:46.817Z","metrics":{"k8s.pod.cpu.usage":0.00121},
		 "resource":{"attributes":{"k8s.namespace.name":"nudgebee","k8s.pod.name":"notifications-x"}}}}
	]}}`
	results, err := parseESMetricsHits([]byte(body))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	s := findMetricSeries(t, results, "k8s.pod.cpu.usage")
	if s.Metric["k8s.namespace.name"] != "nudgebee" || s.Metric["k8s.pod.name"] != "notifications-x" {
		t.Fatalf("labels not flattened from resource.attributes: %v", s.Metric)
	}
	if len(s.Values) != 1 || s.Values[0] != 0.00121 {
		t.Fatalf("expected value 0.00121, got %v", s.Values)
	}
}

// One OTel doc can carry several metrics sharing labels+timestamp -> one series each.
func TestParseESMetricsHits_OTelMultipleMetricsPerDoc(t *testing.T) {
	body := `{"hits":{"hits":[
		{"_source":{"@timestamp":"2026-06-17T17:38:46.817Z",
		 "metrics":{"k8s.pod.cpu.usage":0.5,"k8s.pod.memory.usage":123456},
		 "resource":{"attributes":{"k8s.pod.name":"p"}}}}
	]}}`
	results, err := parseESMetricsHits([]byte(body))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 series, got %d", len(results))
	}
	if v := findMetricSeries(t, results, "k8s.pod.memory.usage").Values; len(v) != 1 || v[0] != 123456 {
		t.Fatalf("memory series value wrong: %v", v)
	}
}

// Legacy flat shape ({name,value,attributes}) must still parse unchanged.
func TestParseESMetricsHits_LegacyFlatShape(t *testing.T) {
	body := `{"hits":{"hits":[
		{"_source":{"@timestamp":"2026-06-17T17:38:46.817Z","name":"cpu","value":5,"attributes":{"pod":"p"}}}
	]}}`
	results, err := parseESMetricsHits([]byte(body))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	s := findMetricSeries(t, results, "cpu")
	if s.Metric["pod"] != "p" || len(s.Values) != 1 || s.Values[0] != 5 {
		t.Fatalf("legacy flat parse wrong: metric=%v values=%v", s.Metric, s.Values)
	}
}

// TestESFetchLabelValues_KeywordSuffixFallback pins the fallback that unblocks ECS
// indices. Aggregating a non-existent field is not an error in Elasticsearch — it returns
// zero buckets — so a `.keyword` suffix guessed onto a field that is already a plain
// keyword silently produced "no values". Elastic Agent / Metricbeat map
// `kubernetes.namespace`, `metricset.name`, `host.name` as plain keyword, so every
// label-values lookup on such an index came back empty while the unsuffixed field
// aggregated fine. Verified against the dev cluster:
//
//	kubernetes.namespace         -> 3 buckets
//	kubernetes.namespace.keyword -> 0 buckets, no error
func TestESFetchLabelValues_KeywordSuffixFallback(t *testing.T) {
	buckets := func(keys ...string) string {
		items := make([]string, 0, len(keys))
		for _, k := range keys {
			items = append(items, fmt.Sprintf(`{"key":%q,"doc_count":1}`, k))
		}
		return `{"aggregations":{"label_values":{"buckets":[` + strings.Join(items, ",") + `]}}}`
	}

	tests := []struct {
		name        string
		field       string
		respFor     map[string]string // aggregated field -> response body
		wantValues  []string
		wantQueried []string
	}{
		{
			// ECS: plain keyword field. The suffixed lookup returns zero buckets with a
			// 200, which must trigger the unsuffixed retry.
			name:  "plain keyword field falls back after an empty suffixed lookup",
			field: "kubernetes.namespace",
			respFor: map[string]string{
				"kubernetes.namespace.keyword": buckets(),
				"kubernetes.namespace":         buckets("payments", "payments-staging"),
			},
			wantValues:  []string{"payments", "payments-staging"},
			wantQueried: []string{"kubernetes.namespace.keyword", "kubernetes.namespace"},
		},
		{
			// OTel: `name` is text and errors unsuffixed, so the suffix must be tried
			// first and must not be retried away when it succeeds.
			name:  "text field with a keyword subfield is served by the suffixed lookup",
			field: "name",
			respFor: map[string]string{
				"name.keyword": buckets("container.cpu.usage"),
			},
			wantValues:  []string{"container.cpu.usage"},
			wantQueried: []string{"name.keyword"},
		},
		{
			name:  "genuinely absent label yields no values under either spelling",
			field: "granicus.application",
			respFor: map[string]string{
				"granicus.application.keyword": buckets(),
				"granicus.application":         buckets(),
			},
			wantValues:  nil,
			wantQueried: []string{"granicus.application.keyword", "granicus.application"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var queried []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var parsed struct {
					Aggs struct {
						LabelValues struct {
							Terms struct {
								Field string `json:"field"`
							} `json:"terms"`
						} `json:"label_values"`
					} `json:"aggs"`
				}
				_ = json.Unmarshal(body, &parsed)
				field := parsed.Aggs.LabelValues.Terms.Field
				queried = append(queried, field)
				resp, ok := tt.respFor[field]
				if !ok {
					// Mirror ES: aggregating a text field without .keyword is an error.
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"type":"search_phase_execution_exception"}}`))
					return
				}
				_, _ = w.Write([]byte(resp))
			}))
			defer srv.Close()

			out, err := esFetchLabelValues(&ElasticsearchConfig{Url: srv.URL}, "metricbeat-8.19.11", tt.field)
			require.NoError(t, err)

			got := make([]string, 0, len(out))
			for _, v := range out {
				got = append(got, v.Value)
			}
			assert.Equal(t, tt.wantValues, nilIfEmpty(got))
			assert.Equal(t, tt.wantQueried, queried, "field spellings attempted, in order")
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestESFetchMetricFields_MergesAllIndicesAndKeepsTypes pins the fix for the defect that
// cost one customer a 41-minute investigation. The mapping call used to `break` after the
// first index in the response, so a pattern spanning per-dataset data streams was
// characterised by whichever index Elasticsearch happened to list first — arbitrary, and
// sometimes an index with zero documents. It also discarded each field's type, leaving
// the caller to guess which fields are metric values and which are dimensions.
func TestESFetchMetricFields_MergesAllIndicesAndKeepsTypes(t *testing.T) {
	// Two datasets under one pattern, as a real estate looks: the non-Kubernetes one
	// listed first, exactly the case that hid pod CPU from the agent.
	mapping := `{
      ".ds-metrics-system.socket_summary-prod-000001": {"mappings":{"properties":{
         "@timestamp":{"type":"date"},
         "metricset":{"properties":{"name":{"type":"keyword"}}},
         "system":{"properties":{"socket":{"properties":{"summary":{"properties":{
            "all":{"properties":{"count":{"type":"long"}}}}}}}}}
      }}},
      ".ds-metrics-kubernetes.pod-prod-000001": {"mappings":{"properties":{
         "@timestamp":{"type":"date"},
         "kubernetes":{"properties":{
            "namespace":{"type":"keyword"},
            "pod":{"properties":{"cpu":{"properties":{"usage":{"properties":{
               "nanocores":{"type":"long"}}}}}}}}}
      }}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mapping))
	}))
	defer srv.Close()

	out, err := esFetchMetricFields(&ElasticsearchConfig{Url: srv.URL}, "metrics-*-prod")
	require.NoError(t, err)

	types := map[string]any{}
	for _, f := range out {
		types[f.Label] = f.Attributes["type"]
	}

	// The whole point: a field from the SECOND index must be present.
	assert.Contains(t, types, "kubernetes.pod.cpu.usage.nanocores",
		"fields from later indices must not be dropped — this is what hid pod CPU")
	assert.Contains(t, types, "system.socket.summary.all.count")

	// Types must survive, so callers can tell metric values from dimensions.
	assert.Equal(t, "long", types["kubernetes.pod.cpu.usage.nanocores"], "numeric = metric value path")
	assert.Equal(t, "keyword", types["kubernetes.namespace"], "keyword = dimension")

	// Output must be deterministic, not a function of index enumeration order.
	labels := make([]string, 0, len(out))
	for _, f := range out {
		labels = append(labels, f.Label)
	}
	assert.IsIncreasing(t, labels, "field list must be stably ordered")
}

// TestESFetchLabelValues_IsTimeBounded pins that label enumeration does not scan history.
// Unbounded, this terms agg runs over every index the pattern matches — on one estate 276
// indices, 211 of them searchable snapshots on object storage.
func TestESFetchLabelValues_IsTimeBounded(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"aggregations":{"label_values":{"buckets":[{"key":"default","doc_count":1}]}}}`))
	}))
	defer srv.Close()

	_, err := esFetchLabelValues(&ElasticsearchConfig{Url: srv.URL}, "metrics-*-prod", "kubernetes.namespace")
	require.NoError(t, err)

	assert.Contains(t, body, esLabelValuesTimeField, "aggregation must carry a time filter")
	assert.Contains(t, body, esLabelValuesWindow)
	assert.Contains(t, body, `"range"`)
}

// TestParseESMetricsHitsWithStats_SeparatesMatchedFromExtracted pins the distinction the
// caller could not previously make. An empty payload has two causes, and they need
// different responses from the agent: a wrong filter should be reformulated, a wrong
// projection should be re-run unchanged without `_source`.
func TestParseESMetricsHitsWithStats_SeparatesMatchedFromExtracted(t *testing.T) {
	t.Run("nothing matched", func(t *testing.T) {
		body := `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`
		payload, stats, err := parseESMetricsHitsWithStats([]byte(body))
		require.NoError(t, err)
		assert.Empty(t, payload)
		assert.Zero(t, stats.DocsMatched, "no documents matched the filter")
		assert.Zero(t, stats.SeriesParsed)
	})

	t.Run("documents matched but the projection dropped the numeric paths", func(t *testing.T) {
		// What a `_source: ["__name__","kubernetes.pod.name"]` projection returns: the
		// label fields survive, every numeric value path is gone, so nothing is
		// extractable. Reported identically to "nothing matched" before this change.
		body := `{"hits":{"total":{"value":1432,"relation":"eq"},"hits":[
		  {"_source":{"@timestamp":"2026-08-17T10:00:00.000Z","kubernetes":{"pod":{"name":"api-1"}}}}
		]}}`
		payload, stats, err := parseESMetricsHitsWithStats([]byte(body))
		require.NoError(t, err)
		assert.Empty(t, payload, "no series are extractable")
		assert.EqualValues(t, 1432, stats.DocsMatched, "but the query itself was correct")
		assert.Equal(t, 1, stats.HitsReturned)
		assert.Zero(t, stats.SeriesParsed)
	})

	t.Run("normal extraction reports both", func(t *testing.T) {
		body := `{"hits":{"total":{"value":120,"relation":"eq"},"hits":[
		  {"_source":{"@timestamp":"2026-08-17T10:00:00.000Z","kubernetes":{"pod":{"name":"api-1",
		     "cpu":{"usage":{"nanocores":5611970}}}}}}
		]}}`
		payload, stats, err := parseESMetricsHitsWithStats([]byte(body))
		require.NoError(t, err)
		assert.NotEmpty(t, payload)
		assert.EqualValues(t, 120, stats.DocsMatched)
		assert.Equal(t, len(payload), stats.SeriesParsed)
	})
}

// TestESQueryFieldNames_FindsFieldsAtAnyDepth covers the clause shapes a generated query
// actually uses, including inside must_not — the position where an absent field is most
// dangerous, because it silently excludes nothing.
func TestESQueryFieldNames_FindsFieldsAtAnyDepth(t *testing.T) {
	dsl := `{"query":{"bool":{"filter":[
	   {"exists":{"field":"kubernetes.pod.cpu.usage.nanocores"}},
	   {"term":{"kubernetes.namespace":"default"}},
	   {"range":{"@timestamp":{"gte":"now-2h"}}}],
	  "must_not":[
	   {"wildcard":{"__name__":{"value":"kubernetes.proxy*"}}},
	   {"prefix":{"kubernetes.node.name":"ip-"}}]}}}`
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(dsl), &body))

	got := map[string]bool{}
	esQueryFieldNames(body, got)

	for _, f := range []string{
		"kubernetes.pod.cpu.usage.nanocores", "kubernetes.namespace", "@timestamp",
		"__name__", "kubernetes.node.name",
	} {
		assert.True(t, got[f], "should have found %s", f)
	}
	assert.False(t, got["value"], "clause internals are not field names")
}

// TestESUnknownQueryFields_FlagsTheSilentNoOp pins the must_not case from the traced
// investigation: excluding on `__name__`, which does not exist, excluded nothing, and the
// query returned 224 series filtered by time alone while looking correctly filtered.
func TestESUnknownQueryFields_FlagsTheSilentNoOp(t *testing.T) {
	fields := []OutputMetricLabels{
		{Label: "@timestamp", Attributes: map[string]any{"type": "date"}},
		{Label: "kubernetes.namespace", Attributes: map[string]any{"type": "keyword"}},
		{Label: "kubernetes.pod.cpu.usage.nanocores", Attributes: map[string]any{"type": "long"}},
		{Label: "message", Attributes: map[string]any{"type": "text"}},
		{Label: "message.keyword", Attributes: map[string]any{"type": "keyword"}},
	}

	t.Run("absent field inside must_not is reported", func(t *testing.T) {
		dsl := `{"query":{"bool":{"filter":[{"range":{"@timestamp":{"gte":"now-2h"}}}],
		  "must_not":[{"wildcard":{"__name__":{"value":"kubernetes.proxy*"}}}]}}}`
		assert.Equal(t, []string{"__name__"}, esUnknownQueryFields(fields, dsl))
	})

	t.Run("fields that exist are not reported", func(t *testing.T) {
		dsl := `{"query":{"bool":{"filter":[
		   {"exists":{"field":"kubernetes.pod.cpu.usage.nanocores"}},
		   {"term":{"kubernetes.namespace":"default"}},
		   {"range":{"@timestamp":{"gte":"now-2h"}}}]}}}`
		assert.Empty(t, esUnknownQueryFields(fields, dsl))
	})

	t.Run("`.keyword` on an already-keyword field is reported", func(t *testing.T) {
		// ECS maps these as plain keyword, so the subfield does not exist and the clause
		// matches nothing — the same silent miss #36408 fixed on the read path.
		dsl := `{"query":{"bool":{"filter":[{"term":{"kubernetes.namespace.keyword":"default"}}]}}}`
		assert.Equal(t, []string{"kubernetes.namespace.keyword"}, esUnknownQueryFields(fields, dsl))
	})

	t.Run("`.keyword` on a text field is valid and not reported", func(t *testing.T) {
		dsl := `{"query":{"bool":{"filter":[{"term":{"message.keyword":"boom"}}]}}}`
		assert.Empty(t, esUnknownQueryFields(fields, dsl))
	})

	t.Run("unparseable query says nothing rather than warning wrongly", func(t *testing.T) {
		assert.Empty(t, esUnknownQueryFields(fields, "not json"))
	})

	t.Run("unresolvable mapping says nothing rather than warning wrongly", func(t *testing.T) {
		dsl := `{"query":{"bool":{"filter":[{"term":{"whatever":"x"}}]}}}`
		assert.Empty(t, esUnknownQueryFields(nil, dsl))
	})
}

func TestResolveESMetricsIndex(t *testing.T) {
	cases := []struct {
		name       string
		requested  string
		configured string
		want       string
	}{
		{"explicit selection wins", "metrics-explicit", "metrics-configured", "metrics-explicit"},
		{"falls back to configured index", "", "metrics-configured", "metrics-configured"},
		{"blank selection falls back", "   ", "metrics-configured", "metrics-configured"},
		{"trims the selection", "  metrics-explicit  ", "", "metrics-explicit"},
		{"trims the configured index", "", "  metrics-configured  ", "metrics-configured"},
		{"nothing configured stays empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ElasticsearchConfig{MetricsIndex: tc.configured}
			assert.Equal(t, tc.want, resolveESMetricsIndex(tc.requested, cfg))
		})
	}
}
