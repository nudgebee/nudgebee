package observability

import (
	"nudgebee/services/query"
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
		"http_status_code":          "http_response_status_code",
		"span_name":                 "operation_name",
		"resource":                  "http_url",
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

	sql, err := s.buildSQL(req, "default")
	require.NoError(t, err)

	expected := `SELECT * FROM "default" WHERE status_code = '2' ORDER BY _timestamp DESC LIMIT 25`
	assert.Equal(t, expected, sql)
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

func TestOpenObserveTraceSourceBuildSQLUsesConfiguredStream(t *testing.T) {
	s := &OpenObserveTraceSource{}
	req := TracesV3Request{}
	req.QueryRequest.Limit = 25

	sql, err := s.buildSQL(req, "otel_traces")
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "otel_traces" ORDER BY _timestamp DESC LIMIT 25`, sql)

	_, err = s.buildSQL(req, `x" OR "1"="1`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe stream name")
}
