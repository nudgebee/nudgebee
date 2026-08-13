package observability

import (
	"encoding/json"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenObserveTraceSource_BuildSQLWhere(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{
				Binary: query.BinaryWhereClause{
					"workload_name": {
						query.Eq: "checkout-service",
					},
				},
			},
			{
				Binary: query.BinaryWhereClause{
					"span_name": {
						query.ILike: "POST /checkout",
					},
				},
			},
		},
	}

	sql, err := buildOpenObserveTraceWhereClause(where)
	require.NoError(t, err)

	expected := "(service_name = 'checkout-service' AND str_match_ignore_case(operation_name, 'POST /checkout'))"
	assert.Equal(t, expected, sql)
}

func TestOpenObserveTraceSource_BuildSQL(t *testing.T) {
	s := &OpenObserveTraceSource{}
	assert.Equal(t, map[string]string{
		"workload_namespace":        "service_k8s_namespace_name",
		"workload_name":             "service_name",
		"destination_workload_name": "service_peer_name",
		"http_status_code":          "http_status_code",
		"span_name":                 "operation_name",
		"resource":                  "http_target",
		"status_code":               "status_code",
		"trace_id":                  "trace_id",
		"span_id":                   "span_id",
		"parent_id":                 "reference_parent_span_id",
	}, s.GetLabelMapping())

	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Limit: 25,
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status_code": {
						query.Eq: "2",
					},
				},
			},
		},
	}

	sql, err := s.buildSQL(req, integrations.OpenObserveDefaultTraceStream)
	require.NoError(t, err)

	expected := `SELECT * FROM "default" WHERE status_code = '2' ORDER BY _timestamp DESC LIMIT 25`
	assert.Equal(t, expected, sql)
}

// The span stream is per-account config, not a constant. It is deliberately separate from
// the log stream: OpenObserve keeps each signal in its own stream, so renaming one says
// nothing about the other.
func TestOpenObserveTraceSource_BuildSQLUsesConfiguredStream(t *testing.T) {
	s := &OpenObserveTraceSource{}
	req := TracesV3Request{QueryRequest: TracesQueryBuilderRequest{Limit: 10}}

	sql, err := s.buildSQL(req, "otel_spans")
	require.NoError(t, err)

	assert.Equal(t, `SELECT * FROM "otel_spans" ORDER BY _timestamp DESC LIMIT 10`, sql)
}

// The stream reaches SQL as a bare identifier, so a name that can close the quote must be
// rejected rather than interpolated.
func TestOpenObserveTraceSource_BuildSQLRejectsUnsafeStream(t *testing.T) {
	s := &OpenObserveTraceSource{}

	_, err := s.buildSQL(TracesV3Request{}, `default" UNION SELECT * FROM secrets --`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe stream name")
}

func TestParseOpenObserveTraceHitsUsesNativeSchemaAndMicrosecondDuration(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{{
		"trace_id":                 "trace-1",
		"span_id":                  "span-1",
		"reference_parent_span_id": "parent-1",
		"operation_name":           "GET /health",
		"service_name":             "api",
		"duration":                 float64(123),
		"span_kind":                "server",
		"_timestamp":               float64(1700000000000000),
	}})

	require.Len(t, traces, 1)
	assert.Equal(t, "parent-1", traces[0].ParentSpanID)
	assert.Equal(t, "GET /health", traces[0].SpanName)
	assert.Equal(t, "api", traces[0].ServiceName)
	assert.Equal(t, int64(123000), traces[0].DurationNs)
}

// The trace list renders workload, status, resource and destination columns — not just the
// span identifiers. Each must be populated from the same source column that
// openObserveTraceLabelMapping filters on, or a row filters one way and displays another.
func TestParseOpenObserveTraceHitsPopulatesRenderedColumns(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{{
		"trace_id":                   "trace-1",
		"span_id":                    "span-1",
		"operation_name":             "GET /orders",
		"service_name":               "checkout",
		"service_k8s_namespace_name": "prod",
		"service_peer_name":          "payments",
		"http_url":                   "/orders/42",
		"http_response_status_code":  float64(503),
		"status_code":                "ERROR",
		"status_message":             "upstream unavailable",
		"duration":                   float64(2000),
		"_timestamp":                 float64(1700000000000000),
	}})

	require.Len(t, traces, 1)
	tr := traces[0]

	assert.Equal(t, "checkout", tr.WorkloadName)
	assert.Equal(t, "prod", tr.WorkloadNamespace)
	assert.Equal(t, "payments", tr.DestinationWorkload)
	assert.Equal(t, "payments", tr.DestinationName)
	assert.Equal(t, "/orders/42", tr.Resource)
	assert.Equal(t, "ERROR", tr.StatusCode)
	assert.Equal(t, "upstream unavailable", tr.StatusMessage)
	assert.Equal(t, "openobserve", tr.TraceSource)

	// Numeric columns must not arrive in scientific notation.
	assert.Equal(t, "503", tr.HTTPStatusCode)

	// EndTime is derived from start + duration; a waterfall needs the span's extent.
	assert.Equal(t, "1700000000000000000", tr.StartTimeUnixNano)
	assert.Equal(t, "1700000000002000000", tr.EndTimeUnixNano)
	assert.NotEmpty(t, tr.EndTime)
}

// Span start/end are epoch nanoseconds (~1.79e18) — about 200x past float64's exact-integer
// limit of 2^53. Decoded as float64 the low digits are gone before any formatting runs, and
// every span in a trace collapses to the same instant, which is what broke the waterfall.
// json.Number keeps the literal digits.
func TestParseOpenObserveTraceHitsPreservesNanosecondPrecision(t *testing.T) {
	const startNs = "1786645409481740300"
	const endNs = "1786645409482751200"

	body := `{"hits":[{"trace_id":"t1","start_time":` + startNs + `,"end_time":` + endNs + `,"duration":1011}]}`
	resp, err := decodeOpenObserveSearchResponse(strings.NewReader(body))
	require.NoError(t, err)

	traces := parseOpenObserveTraceHits(resp.Hits)
	require.Len(t, traces, 1)

	assert.Equal(t, startNs, traces[0].StartTimeUnixNano, "nanosecond start must survive decoding")
	assert.Equal(t, endNs, traces[0].EndTimeUnixNano, "reported end_time must be used verbatim, not derived")
	assert.NotEqual(t, traces[0].StartTimeUnixNano, traces[0].EndTimeUnixNano)

	// A plain float64 decode would have rounded both to the same value.
	var viaFloat map[string][]map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &viaFloat))
	lossy := int64(viaFloat["hits"][0]["start_time"].(float64))
	assert.NotEqual(t, startNs, strconv.FormatInt(lossy, 10),
		"guard: this test is meaningless if float64 were lossless at this magnitude")
}

// span_status is the readable OK/ERROR/UNSET; status_code is the numeric OTel code and
// renders as a bare "0" in the Status column.
func TestParseOpenObserveTraceHitsPrefersReadableSpanStatus(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{{
		"trace_id":    "t1",
		"span_status": "ERROR",
		"status_code": "2",
	}})

	require.Len(t, traces, 1)
	assert.Equal(t, "ERROR", traces[0].StatusCode)
	assert.Equal(t, "2", traces[0].SpanAttributes["status_code"])
}

// HTTP attributes were renamed between OpenTelemetry semantic-convention generations, and a
// real cluster emits both. Observed live: 61 of 100 spans used the older http_status_code /
// http_target, 5 used the newer names, 2 were gRPC — so reading one spelling left the
// Resource and HTTP Status columns blank on most rows and populated on a few.
func TestParseOpenObserveTraceHitsAcceptsBothHTTPConventions(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{
		{"trace_id": "old", "http_status_code": "200", "http_target": "/rpc/query"},
		{"trace_id": "new", "http_response_status_code": "503", "url_full": "/orders/42"},
		{"trace_id": "grpc", "rpc_grpc_status_code": "14"},
		{"trace_id": "route-only", "http_route": "/v1/{id}"},
	})

	require.Len(t, traces, 4)
	assert.Equal(t, "200", traces[0].HTTPStatusCode)
	assert.Equal(t, "/rpc/query", traces[0].Resource)
	assert.Equal(t, "503", traces[1].HTTPStatusCode)
	assert.Equal(t, "/orders/42", traces[1].Resource)
	assert.Equal(t, "14", traces[2].HTTPStatusCode)
	assert.Equal(t, "/v1/{id}", traces[3].Resource)
}

// Peer identity is spelled differently per convention too.
func TestParseOpenObserveTraceHitsResolvesDestinationAcrossConventions(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{
		{"trace_id": "a", "server_address": "payments.svc"},
		{"trace_id": "b", "net_sock_peer_addr": "10.0.0.7"},
		{"trace_id": "c", "service_peer_name": "payments"},
	})

	require.Len(t, traces, 3)
	assert.Equal(t, "payments.svc", traces[0].DestinationName)
	assert.Equal(t, "10.0.0.7", traces[1].DestinationName)
	// An explicit peer workload still wins and populates both fields.
	assert.Equal(t, "payments", traces[2].DestinationName)
	assert.Equal(t, "payments", traces[2].DestinationWorkload)
}

// A span carrying only identifiers must not fabricate an end time from a zero duration.
func TestParseOpenObserveTraceHitsSkipsEndTimeWithoutDuration(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{{
		"trace_id":   "trace-1",
		"_timestamp": float64(1700000000000000),
	}})

	require.Len(t, traces, 1)
	assert.Empty(t, traces[0].EndTimeUnixNano)
	assert.Empty(t, traces[0].EndTime)
}

// Duration arrives as a bare number from the search API, but OpenObserve also renders it
// with an explicit unit ("1001216us"). Failing to parse the annotated form yields a
// zero-length span, which collapses every bar in the waterfall.
func TestOpenObserveDurationMicros(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{float64(1011), 1011},
		{json.Number("1001216"), 1001216},
		{"1001216us", 1001216},
		{"1001216", 1001216},
		{"2ms", 2000},
		{"1500ns", 1},
		{"3s", 3000000},
	}
	for _, c := range cases {
		got, ok := openObserveDurationMicros(c.in)
		require.True(t, ok, "should parse %v", c.in)
		assert.Equal(t, c.want, got, "input %v", c.in)
	}

	_, ok := openObserveDurationMicros("not-a-duration")
	assert.False(t, ok)
	_, ok = openObserveDurationMicros(nil)
	assert.False(t, ok)
}

// The heatmap is the trace waterfall: it must carry each span's identity, timing and status,
// resolved the same way the trace list resolves them.
func TestOpenObserveTracesToHeatmap(t *testing.T) {
	traces := parseOpenObserveTraceHits([]map[string]any{{
		"trace_id":                   "6a13e99b454a75a62d86cdd5e5603be6",
		"span_id":                    "1133cd793925b6cd",
		"operation_name":             "sql.conn.exec",
		"service_name":               "product-catalog",
		"service_k8s_namespace_name": "demo",
		"span_status":                "UNSET",
		"duration":                   "1001216us",
		"server_address":             "astronomy-db",
		"db_system_name":             "postgresql",
	}})

	heat := openObserveTracesToHeatmap(traces)
	require.Len(t, heat, 1)
	h := heat[0]

	assert.Equal(t, "6a13e99b454a75a62d86cdd5e5603be6", h.TraceID)
	assert.Equal(t, "1133cd793925b6cd", h.SpanID)
	assert.Equal(t, "sql.conn.exec", h.SpanName)
	assert.Equal(t, "product-catalog", h.ServiceName)
	assert.Equal(t, "UNSET", h.StatusCode)
	assert.Equal(t, int64(1001216000), h.DurationNs, "unit-suffixed duration must survive")
	assert.Equal(t, "demo", h.ResourceAttributes["k8s.namespace.name"])
	// Attributes with no dedicated column stay available to the span detail panel.
	assert.Equal(t, "postgresql", h.SpanAttributes["db_system_name"])
}
