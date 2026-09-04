package observability

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/query"
)

// cubeAPMSampleSearchResponse is the traces search response from CubeAPM's HTTP
// API reference: Jaeger protobuf-JSON, with base64 ids, typed tag values and a
// bare nanosecond duration.
const cubeAPMSampleSearchResponse = `[
  {
    "keySpanId": "2d4bjpT7FaA=",
    "trace": {
      "spans": [
        {
          "trace_id": "V5OPQPPw/3hScwih8m9RBg==",
          "span_id": "2d4bjpT7FaA=",
          "operation_name": "POST /v1/payment",
          "references": [
            {"trace_id": "V5OPQPPw/3hScwih8m9RBg==", "span_id": "901CCWrDT6M="}
          ],
          "start_time": "2025-10-29T03:55:04.90625352Z",
          "duration": 40965161,
          "tags": [
            {"key": "http.status_code", "v_type": 2, "v_int64": 200},
            {"key": "http.route", "v_str": "/v1/payment"},
            {"key": "span.kind", "v_str": "server"},
            {"key": "http.method", "v_str": "POST"}
          ],
          "logs": null,
          "process": {
            "service_name": "notify-service",
            "tags": [
              {"key": "telemetry.sdk.language", "v_str": "java"},
              {"key": "k8s.namespace.name", "v_str": "payments"}
            ]
          }
        }
      ]
    }
  }
]`

func decodeSampleSpans(t *testing.T) []common.OpenTelemetryTrace {
	t.Helper()
	var matches []cubeAPMSearchMatch
	dec := json.NewDecoder(strings.NewReader(cubeAPMSampleSearchResponse))
	dec.UseNumber()
	if err := dec.Decode(&matches); err != nil {
		t.Fatalf("failed to decode sample response: %v", err)
	}

	var spans []common.OpenTelemetryTrace
	for _, m := range matches {
		for _, s := range m.Trace.Spans {
			spans = append(spans, cubeAPMSpanToTrace(s))
		}
	}
	return spans
}

func TestCubeAPMSpanToTrace(t *testing.T) {
	spans := decodeSampleSpans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	span := spans[0]

	// Ids arrive base64-encoded; every other provider here reports lowercase hex.
	// Expected values are the base64 payloads decoded independently:
	//   V5OPQPPw/3hScwih8m9RBg== -> 57938f40f3f0ff78527308a1f26f5106 (16 bytes)
	//   2d4bjpT7FaA=             -> d9de1b8e94fb15a0                 (8 bytes)
	//   901CCWrDT6M=             -> f74d42096ac34fa3                 (8 bytes)
	if span.TraceID != "57938f40f3f0ff78527308a1f26f5106" {
		t.Errorf("TraceID = %q, want the base64 id decoded to hex", span.TraceID)
	}
	if span.SpanID != "d9de1b8e94fb15a0" {
		t.Errorf("SpanID = %q", span.SpanID)
	}
	// The parent comes from references[0], which is what makes the waterfall nest.
	if span.ParentSpanID != "f74d42096ac34fa3" {
		t.Errorf("ParentSpanID = %q", span.ParentSpanID)
	}

	if span.SpanName != "POST /v1/payment" {
		t.Errorf("SpanName = %q", span.SpanName)
	}
	if span.ServiceName != "notify-service" {
		t.Errorf("ServiceName = %q", span.ServiceName)
	}
	if span.DurationNs != 40965161 {
		t.Errorf("DurationNs = %d, want 40965161 (the field is nanoseconds)", span.DurationNs)
	}
	if span.SpanKind != "server" {
		t.Errorf("SpanKind = %q", span.SpanKind)
	}
	// A typed int tag must render as its number, not as an empty string.
	if span.HTTPStatusCode != "200" {
		t.Errorf("HTTPStatusCode = %q, want 200", span.HTTPStatusCode)
	}
	if span.Resource != "/v1/payment" {
		t.Errorf("Resource = %q, want the http.route", span.Resource)
	}
	// Process tags are the span's resource attributes.
	if span.ResourceAttributes["k8s.namespace.name"] != "payments" {
		t.Errorf("ResourceAttributes = %v", span.ResourceAttributes)
	}
	if span.WorkloadNamespace != "payments" {
		t.Errorf("WorkloadNamespace = %q", span.WorkloadNamespace)
	}
	if span.ResourceAttributes["service.name"] != "notify-service" {
		t.Error("service.name should be present in ResourceAttributes")
	}
	if span.EndTime == "" {
		t.Error("EndTime should be derived from start_time + duration")
	}
}

func TestCubeAPMDecodeID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"base64 span id", "2d4bjpT7FaA=", "d9de1b8e94fb15a0"},
		{"already hex passes through", "d9de1b8e94fb15a0", "d9de1b8e94fb15a0"},
		{"uppercase hex is lowercased", "D9DE1B8E94FB15A0", "d9de1b8e94fb15a0"},
		{"empty", "", ""},
		// An undecodable value is returned as-is rather than becoming an empty id,
		// so a trace stays addressable even if the encoding changes.
		{"garbage passes through", "!!!not-base64!!!", "!!!not-base64!!!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMDecodeID(tt.in); got != tt.want {
				t.Errorf("cubeAPMDecodeID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsHexID(t *testing.T) {
	if !isHexID("d9de1b8e94fb15a0") {
		t.Error("16-char hex should be recognized")
	}
	if !isHexID(strings.Repeat("a", 32)) {
		t.Error("32-char hex should be recognized")
	}
	// "2d4bjpT7FaA=" is 12 chars — not an id length — and contains non-hex bytes.
	if isHexID("2d4bjpT7FaA=") {
		t.Error("base64 must not be mistaken for hex")
	}
	if isHexID("zzzzzzzzzzzzzzzz") {
		t.Error("non-hex characters should be rejected")
	}
}

func TestCubeAPMDurationNanos(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int64
	}{
		{"bare nanoseconds", `40965161`, 40965161},
		{"float", `40965161.7`, 40965161},
		// protobuf's canonical JSON encoding for a Duration is a suffixed string.
		{"protobuf seconds string", `"0.040965161s"`, 40965161},
		{"milliseconds string", `"40ms"`, 40000000},
		{"microseconds string", `"40us"`, 40000},
		{"nanoseconds string", `"40ns"`, 40},
		{"numeric string", `"40965161"`, 40965161},
		{"empty string", `""`, 0},
		{"null", `null`, 0},
		{"garbage", `"abc"`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMDurationNanos(json.RawMessage(tt.in)); got != tt.want {
				t.Errorf("cubeAPMDurationNanos(%s) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}

	t.Run("absent field", func(t *testing.T) {
		if got := cubeAPMDurationNanos(nil); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

func TestCubeAPMTagString(t *testing.T) {
	tests := []struct {
		name string
		tag  cubeAPMTag
		want string
	}{
		{"string", cubeAPMTag{VStr: "server"}, "server"},
		{"int64", cubeAPMTag{VType: 2, VInt64: json.Number("200")}, "200"},
		{"float", cubeAPMTag{VType: 3, VFloat: json.Number("1.5")}, "1.5"},
		{"bool true", cubeAPMTag{VType: 1, VBool: true}, "true"},
		// protobuf-JSON omits zero values, so a false bool is identified only by
		// its type tag — without this it would render as an absent tag.
		{"bool false", cubeAPMTag{VType: 1}, "false"},
		{"empty string tag", cubeAPMTag{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tag.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCubeAPMStatusCode(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]string
		want  string
	}{
		{"otel status", map[string]string{"otel.status_code": "error"}, "ERROR"},
		// error=true is the Jaeger convention and outlives the OTel status on
		// spans exported through a Jaeger-compatible path.
		{"jaeger error flag", map[string]string{"error": "true"}, "ERROR"},
		{"status.code fallback", map[string]string{"status.code": "ok"}, "OK"},
		{"none", map[string]string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cubeAPMStatusCode(tt.attrs); got != tt.want {
				t.Errorf("cubeAPMStatusCode(%v) = %q, want %q", tt.attrs, got, tt.want)
			}
		})
	}
}

func TestCubeAPMEndTime(t *testing.T) {
	got := cubeAPMEndTime("2025-10-29T03:55:04.000000000Z", int64(time.Second))
	if !strings.HasPrefix(got, "2025-10-29T03:55:05") {
		t.Errorf("EndTime = %q, want start + 1s", got)
	}

	// An unparseable start must leave the field empty rather than invent an
	// epoch-anchored end.
	if got := cubeAPMEndTime("not-a-time", 100); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := cubeAPMEndTime("2025-10-29T03:55:04Z", 0); got != "" {
		t.Errorf("got %q, want empty for a zero duration", got)
	}
}

func TestCubeAPMWhereHasFilters(t *testing.T) {
	if cubeAPMWhereHasFilters(query.QueryWhereClause{}) {
		t.Error("an empty clause has no filters")
	}
	for name, where := range map[string]query.QueryWhereClause{
		"binary": {Binary: query.BinaryWhereClause{"span_name": {query.Eq: "a"}}},
		"and":    {And: []query.QueryWhereClause{{}}},
		"or":     {Or: []query.QueryWhereClause{{}}},
		"not":    {Not: &query.QueryWhereClause{}},
	} {
		if !cubeAPMWhereHasFilters(where) {
			t.Errorf("%s clause should report filters", name)
		}
	}
}

func testCubeAPMSpans() []common.OpenTelemetryTrace {
	return []common.OpenTelemetryTrace{
		{SpanName: "POST /pay", ServiceName: "checkout", HTTPStatusCode: "500",
			WorkloadNamespace: "payments",
			SpanAttributes:    map[string]string{"http.method": "POST"}, ResourceAttributes: map[string]string{}},
		{SpanName: "GET /health", ServiceName: "checkout", HTTPStatusCode: "200",
			WorkloadNamespace: "payments",
			SpanAttributes:    map[string]string{"http.method": "GET"}, ResourceAttributes: map[string]string{}},
		{SpanName: "GET /balance", ServiceName: "ledger", HTTPStatusCode: "200",
			WorkloadNamespace: "payments",
			SpanAttributes:    map[string]string{"http.method": "GET"}, ResourceAttributes: map[string]string{}},
	}
}

func filterNames(t *testing.T, where query.QueryWhereClause) []string {
	t.Helper()
	got, err := filterCubeAPMSpans(testCubeAPMSpans(), where)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, span := range got {
		names = append(names, span.SpanName)
	}
	return names
}

func TestFilterCubeAPMSpans(t *testing.T) {
	t.Run("no filters returns everything", func(t *testing.T) {
		if got := filterNames(t, query.QueryWhereClause{}); len(got) != 3 {
			t.Errorf("got %v, want all three spans", got)
		}
	})

	t.Run("eq", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"http_status_code": {query.Eq: "500"},
		}})
		if len(got) != 1 || got[0] != "POST /pay" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("neq", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"http_status_code": {query.Nq: "500"},
		}})
		if len(got) != 2 {
			t.Errorf("got %v", got)
		}
	})

	t.Run("contains is case-insensitive", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"span_name": {query.Contains: "PAY"},
		}})
		if len(got) != 1 || got[0] != "POST /pay" {
			t.Errorf("got %v", got)
		}
	})

	t.Run("span attribute lookup", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"http.method": {query.Eq: "GET"},
		}})
		if len(got) != 2 {
			t.Errorf("got %v", got)
		}
	})

	t.Run("conditions in one clause are ANDed", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"http.method":      {query.Eq: "POST"},
			"http_status_code": {query.Eq: "200"},
		}})
		if len(got) != 0 {
			t.Errorf("got %v, want no matches", got)
		}
	})

	t.Run("in and not_in", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"span_name": {query.In: []any{"POST /pay", "GET /balance"}},
		}})
		if len(got) != 2 {
			t.Errorf("got %v", got)
		}
		got = filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"span_name": {query.NotIn: []any{"POST /pay"}},
		}})
		if len(got) != 2 {
			t.Errorf("got %v", got)
		}
	})
}

// An OR-shaped filter used to be dropped entirely by the flat filter collector,
// so the query returned every span as though the filter had matched — the exact
// failure this source refuses to accept from the server.
func TestFilterCubeAPMSpansEvaluatesOrAndNot(t *testing.T) {
	t.Run("or matches either branch", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"span_name": {query.Eq: "POST /pay"}}},
			{Binary: query.BinaryWhereClause{"span_name": {query.Eq: "GET /balance"}}},
		}})
		if len(got) != 2 {
			t.Fatalf("got %v, want exactly the two OR branches", got)
		}
		for _, name := range got {
			if name == "GET /health" {
				t.Error("a span matching neither OR branch was returned")
			}
		}
	})

	t.Run("not excludes", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Not: &query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "ledger"}},
		}})
		if len(got) != 2 {
			t.Errorf("got %v, want the two non-ledger spans", got)
		}
	})

	t.Run("nested and inside or", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{Or: []query.QueryWhereClause{
			{And: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "checkout"}}},
				{Binary: query.BinaryWhereClause{"http_status_code": {query.Eq: "500"}}},
			}},
			{Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "ledger"}}},
		}})
		if len(got) != 2 {
			t.Errorf("got %v, want POST /pay and GET /balance", got)
		}
	})

	// A top-level binary is ANDed with the OR group, so the service equality that
	// gets pushed down as the API's `service` parameter stays correct.
	t.Run("top-level binary ands with an or group", func(t *testing.T) {
		got := filterNames(t, query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "checkout"}},
			Or: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"span_name": {query.Eq: "POST /pay"}}},
				{Binary: query.BinaryWhereClause{"span_name": {query.Eq: "GET /balance"}}},
			},
		})
		if len(got) != 1 || got[0] != "POST /pay" {
			t.Errorf("got %v, want only POST /pay (GET /balance is on the other service)", got)
		}
	})
}

// Silently treating an unevaluatable operator as a match would return unfiltered
// spans as though the filter had been applied.
func TestFilterCubeAPMSpansRejectsUnsupportedOperator(t *testing.T) {
	_, err := filterCubeAPMSpans(testCubeAPMSpans(), query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"span_name": {query.Between: "a"}},
	})
	if err == nil {
		t.Fatal("expected an error rather than a silently unfiltered result")
	}
	if !strings.Contains(err.Error(), "unsupported operator") {
		t.Errorf("error = %v", err)
	}
}

func TestSortCubeAPMSpans(t *testing.T) {
	newSpans := func() []common.OpenTelemetryTrace {
		return []common.OpenTelemetryTrace{
			{SpanName: "a", Timestamp: "2026-09-04T01:00:01Z", DurationNs: 100},
			{SpanName: "b", Timestamp: "2026-09-04T01:00:03Z", DurationNs: 300},
			{SpanName: "c", Timestamp: "2026-09-04T01:00:02Z", DurationNs: 200},
		}
	}

	t.Run("defaults to newest first", func(t *testing.T) {
		spans := newSpans()
		sortCubeAPMSpans(spans, nil)
		if spans[0].SpanName != "b" || spans[2].SpanName != "a" {
			t.Errorf("order = %s %s %s, want b c a", spans[0].SpanName, spans[1].SpanName, spans[2].SpanName)
		}
	})

	t.Run("timestamp ascending", func(t *testing.T) {
		spans := newSpans()
		sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Asc}})
		if spans[0].SpanName != "a" || spans[2].SpanName != "b" {
			t.Errorf("order = %s %s %s, want a c b", spans[0].SpanName, spans[1].SpanName, spans[2].SpanName)
		}
	})

	t.Run("duration descending", func(t *testing.T) {
		spans := newSpans()
		sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "duration_ns", Order: query.Desc}})
		if spans[0].DurationNs != 300 || spans[2].DurationNs != 100 {
			t.Errorf("durations = %d %d %d", spans[0].DurationNs, spans[1].DurationNs, spans[2].DurationNs)
		}
	})
}

func TestAggregateCubeAPMTraceGroups(t *testing.T) {
	spans := []common.OpenTelemetryTrace{
		{ServiceName: "checkout", SpanName: "POST /pay", WorkloadNamespace: "payments", DurationNs: 100, StatusCode: "OK"},
		{ServiceName: "checkout", SpanName: "POST /pay", WorkloadNamespace: "payments", DurationNs: 300, StatusCode: "ERROR"},
		{ServiceName: "checkout", SpanName: "POST /pay", WorkloadNamespace: "payments", DurationNs: 200, HTTPStatusCode: "503"},
		{ServiceName: "ledger", SpanName: "GET /balance", WorkloadNamespace: "payments", DurationNs: 50},
	}

	groups := aggregateCubeAPMTraceGroups(spans)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}

	var pay *TraceGroupingValues
	for i := range groups {
		if groups[i].SpanName == "POST /pay" {
			pay = &groups[i]
		}
	}
	if pay == nil {
		t.Fatal("missing the POST /pay group")
	}

	if pay.Count != 3 {
		t.Errorf("Count = %d, want 3", pay.Count)
	}
	// An OTel ERROR status and a 5xx HTTP status both mark a failed span.
	if pay.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", pay.ErrorCount)
	}
	if pay.MaxLatency != 300 {
		t.Errorf("MaxLatency = %d, want 300", pay.MaxLatency)
	}
	if pay.DurationNS != 600 {
		t.Errorf("DurationNS = %d, want the group total 600", pay.DurationNS)
	}
	if pay.WorkloadNamespace != "payments" {
		t.Errorf("WorkloadNamespace = %q", pay.WorkloadNamespace)
	}
	if pay.P95Latency == 0 || pay.P99Latency == 0 {
		t.Errorf("percentiles not computed: p95=%d p99=%d", pay.P95Latency, pay.P99Latency)
	}
}

func TestIsCubeAPMErrorSpan(t *testing.T) {
	tests := []struct {
		name string
		span common.OpenTelemetryTrace
		want bool
	}{
		{"otel error", common.OpenTelemetryTrace{StatusCode: "ERROR"}, true},
		{"otel numeric error", common.OpenTelemetryTrace{StatusCode: "2"}, true},
		{"http 500", common.OpenTelemetryTrace{HTTPStatusCode: "500"}, true},
		{"http 503", common.OpenTelemetryTrace{HTTPStatusCode: "503"}, true},
		// A 4xx is a client error, not a failure of the span's own service.
		{"http 404 is not an error span", common.OpenTelemetryTrace{HTTPStatusCode: "404"}, false},
		{"http 200", common.OpenTelemetryTrace{HTTPStatusCode: "200"}, false},
		{"empty", common.OpenTelemetryTrace{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCubeAPMErrorSpan(tt.span); got != tt.want {
				t.Errorf("isCubeAPMErrorSpan(%+v) = %v, want %v", tt.span, got, tt.want)
			}
		})
	}
}

func TestCubeAPMPercentile(t *testing.T) {
	sorted := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := cubeAPMPercentile(sorted, 0.95); got != 90 {
		t.Errorf("p95 = %d, want 90", got)
	}
	if got := cubeAPMPercentile(sorted, 0.99); got != 90 {
		t.Errorf("p99 = %d, want 90", got)
	}
	if got := cubeAPMPercentile(nil, 0.95); got != 0 {
		t.Errorf("empty slice = %d, want 0", got)
	}
	if got := cubeAPMPercentile([]int64{5}, 0.99); got != 5 {
		t.Errorf("single element = %d, want 5", got)
	}
}

func TestCubeAPMSearchParams(t *testing.T) {
	req := TracesV3Request{
		StartTime: 1_700_000_000_000,
		EndTime:   1_700_003_600_000,
		QueryRequest: TracesQueryBuilderRequest{Where: query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "checkout"}},
		}},
	}

	got := cubeAPMSearchParams(req, "prod", 100)

	for _, want := range []string{"query=%2A", "limit=100", "env=prod", "service=checkout",
		"start=1700000000", "end=1700003600"} {
		if !strings.Contains(got, want) {
			t.Errorf("params missing %q\ngot: %s", want, got)
		}
	}

	t.Run("omits env and service when absent", func(t *testing.T) {
		got := cubeAPMSearchParams(TracesV3Request{StartTime: 1, EndTime: 2}, "", 10)
		if strings.Contains(got, "env=") {
			t.Errorf("env should be omitted when unconfigured: %s", got)
		}
		if strings.Contains(got, "service=") {
			t.Errorf("service should be omitted when unfiltered: %s", got)
		}
	})
}

func TestCubeAPMTraceLimit(t *testing.T) {
	if got := cubeAPMTraceLimit(TracesV3Request{}); got != cubeAPMDefaultTraceLimit {
		t.Errorf("got %d, want the default %d", got, cubeAPMDefaultTraceLimit)
	}
	if got := cubeAPMTraceLimit(TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 25}}); got != 25 {
		t.Errorf("got %d, want 25", got)
	}
	if got := cubeAPMTraceLimit(TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 99999}}); got != cubeAPMMaxTraceLimit {
		t.Errorf("got %d, want the cap %d", got, cubeAPMMaxTraceLimit)
	}
}

func TestCubeAPMHeatmapWindowSeconds(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)

	t.Run("pads a supplied window", func(t *testing.T) {
		start, end := cubeAPMHeatmapWindowSeconds(1_700_000_000_000, 1_700_000_060_000, now)
		// A trace's earliest span can begin before the row's timestamp and its
		// latest can end after; clipping either truncates the waterfall.
		if start != 1_700_000_000-300 {
			t.Errorf("start = %d, want the window start minus 5m", start)
		}
		if end != 1_700_000_060+300 {
			t.Errorf("end = %d, want the window end plus 5m", end)
		}
	})

	// The trace-detail view asks by trace_id alone; an hour-long default silently
	// returns nothing for any older trace.
	t.Run("falls back to a 30-day lookback", func(t *testing.T) {
		start, end := cubeAPMHeatmapWindowSeconds(0, 0, now)
		if end != now.Unix() {
			t.Errorf("end = %d, want now", end)
		}
		if end-start != int64((30 * 24 * time.Hour).Seconds()) {
			t.Errorf("lookback = %ds, want 30 days", end-start)
		}
	})
}

func TestCubeAPMTracesToHeatmap(t *testing.T) {
	spans := decodeSampleSpans(t)
	heatmap := cubeAPMTracesToHeatmap(spans)

	if len(heatmap) != 1 {
		t.Fatalf("got %d entries, want 1", len(heatmap))
	}
	h := heatmap[0]
	if h.DurationNs != 40965161 {
		t.Errorf("DurationNs = %d", h.DurationNs)
	}
	if h.ServiceName != "notify-service" {
		t.Errorf("ServiceName = %q", h.ServiceName)
	}
	if h.SpanName != "POST /v1/payment" {
		t.Errorf("SpanName = %q", h.SpanName)
	}
	if h.TraceID == "" || h.SpanID == "" {
		t.Error("heatmap entries must carry ids so the waterfall can link spans")
	}
}

func TestCubeAPMTraceSourceContract(t *testing.T) {
	var s any = &CubeAPMTraceSource{}
	if _, ok := s.(TraceSource); !ok {
		t.Error("CubeAPMTraceSource must implement TraceSource")
	}
}

func TestCubeAPMTraceSourceRoutedFromDispatcher(t *testing.T) {
	src, err := getTraceSource("cubeapm", "user")
	if err != nil {
		t.Fatalf("getTraceSource(cubeapm, user) failed: %v", err)
	}
	if _, ok := src.(*CubeAPMTraceSource); !ok {
		t.Errorf("got %T, want *CubeAPMTraceSource", src)
	}
}

func TestCubeAPMTraceCountsAreEstimates(t *testing.T) {
	s := &CubeAPMTraceSource{}

	count, err := s.CountTraces(nil, TracesV3Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CubeAPM exposes no trace-count endpoint; -1 is the contract's "estimate"
	// signal, which the frontend already handles for pagination.
	if count.Count != -1 {
		t.Errorf("CountTraces = %d, want -1", count.Count)
	}
}

// Every operator advertised for traces must be one the evaluator actually
// implements, or the builder offers a filter that errors at query time.
func TestCubeAPMTraceSupportedOperatorsAreEvaluated(t *testing.T) {
	s := &CubeAPMTraceSource{}
	for _, op := range s.GetSupportedOperators() {
		t.Run(op, func(t *testing.T) {
			_, err := filterCubeAPMSpans(testCubeAPMSpans(), query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"span_name": {query.BinaryWhereClauseType(op): "POST /pay"}},
			})
			if err != nil {
				t.Errorf("advertised operator %q is not evaluated: %v", op, err)
			}
		})
	}
}

// The service equality is pushed down as the API's `service` parameter AND
// re-evaluated locally. Re-checking is free — it is already true of every fetched
// span — and keeps the evaluator free of push-down special cases, which would be
// wrong inside an OR branch.
func TestCubeAPMServiceFilterIsEvaluatedLocallyToo(t *testing.T) {
	got := filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
		"workload_name": {query.Eq: "ledger"},
	}})
	if len(got) != 1 || got[0] != "GET /balance" {
		t.Errorf("got %v, want only the ledger span", got)
	}

	// A non-equality on the service has no server-side equivalent at all, so the
	// local evaluator is the only thing enforcing it.
	got = filterNames(t, query.QueryWhereClause{Binary: query.BinaryWhereClause{
		"workload_name": {query.Nq: "ledger"},
	}})
	if len(got) != 2 {
		t.Errorf("got %v, want the two checkout spans", got)
	}
}

// Regression: sorting the RFC3339 strings directly inverts order when two spans
// differ in fractional-second digit width. 100ms renders as ".1Z" and 120ms as
// ".12Z", which compare on 'Z' (0x5A) against '2' (0x32) — putting the later span
// first. Sub-millisecond spacing is what a trace waterfall is made of.
func TestSortCubeAPMSpansHandlesFractionalSecondWidths(t *testing.T) {
	spans := []common.OpenTelemetryTrace{
		{SpanName: "later", Timestamp: "2025-10-29T03:55:04.12Z"},
		{SpanName: "earlier", Timestamp: "2025-10-29T03:55:04.1Z"},
	}

	// Guard the premise: a raw string compare really does get this wrong, i.e.
	// the EARLIER span's timestamp sorts as greater than the later one's.
	if spans[1].Timestamp <= spans[0].Timestamp {
		t.Fatal("premise no longer holds; the fixture no longer exercises the bug")
	}

	sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Asc}})
	if spans[0].SpanName != "earlier" {
		t.Errorf("ascending order = %s then %s, want earlier first",
			spans[0].SpanName, spans[1].SpanName)
	}

	sortCubeAPMSpans(spans, []query.QueryOrderBy{{Column: "timestamp", Order: query.Desc}})
	if spans[0].SpanName != "later" {
		t.Errorf("descending order = %s then %s, want later first",
			spans[0].SpanName, spans[1].SpanName)
	}
}

func TestCubeAPMSpanStartNanos(t *testing.T) {
	got := cubeAPMSpanStartNanos(common.OpenTelemetryTrace{Timestamp: "2025-10-29T03:55:04.1Z"})
	want := cubeAPMSpanStartNanos(common.OpenTelemetryTrace{Timestamp: "2025-10-29T03:55:04.100Z"})
	if got != want {
		t.Errorf(".1Z and .100Z are the same instant but parsed to %d and %d", got, want)
	}

	// Falls back to StartTime when Timestamp is absent.
	if cubeAPMSpanStartNanos(common.OpenTelemetryTrace{StartTime: "2025-10-29T03:55:04Z"}) == 0 {
		t.Error("StartTime fallback did not parse")
	}

	// A malformed start sorts as 0 rather than failing the whole query.
	if cubeAPMSpanStartNanos(common.OpenTelemetryTrace{Timestamp: "not-a-time"}) != 0 {
		t.Error("an unparseable timestamp should sort as 0")
	}
}
