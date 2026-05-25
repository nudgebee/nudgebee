package observability

import (
	"encoding/json"
	"nudgebee/services/query"
	"strings"
	"testing"
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
