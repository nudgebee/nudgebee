package observability

import (
	"nudgebee/services/query"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTraceQuery_DurationNsMappedToMicroseconds(t *testing.T) {
	s := &SplunkTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"duration_ns": {query.Gte: int64(5_000_000_000)}, // 5s in ns
				},
			},
		},
	}
	result := s.buildTraceQuery(req)
	// Splunk stores duration in microseconds: 5s = 5_000_000 µs
	assert.Contains(t, result, "duration:[5000000 TO *]", "5s ns should map to 5000000µs in Lucene range filter")
	assert.NotContains(t, result, "duration_ns", "duration_ns must not appear in generated query")
}

func TestBuildTraceQuery_DurationNsLte(t *testing.T) {
	s := &SplunkTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"duration_ns": {query.Lte: int64(1_000_000_000)}, // 1s in ns
				},
			},
		},
	}
	result := s.buildTraceQuery(req)
	// 1s = 1_000_000 µs
	assert.Contains(t, result, "duration:[* TO 1000000]")
	assert.NotContains(t, result, "duration_ns")
}

func TestBuildTraceQuery_DurationNsWithOtherFilter(t *testing.T) {
	s := &SplunkTraceSource{}
	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"duration_ns": {query.Gt: int64(500_000_000)}, // 500ms in ns
					"sf_service":  {query.Eq: "my-service"},
				},
			},
		},
	}
	result := s.buildTraceQuery(req)
	// 500ms = 500_000 µs
	assert.Contains(t, result, "duration:{500000 TO *}")
	assert.Contains(t, result, "sf_service") // value may have escaped chars
	assert.NotContains(t, result, "duration_ns")
}

func TestBuildTraceQuery_EmptyRequest(t *testing.T) {
	s := &SplunkTraceSource{}
	req := TracesV3Request{}
	result := s.buildTraceQuery(req)
	assert.Equal(t, "", result)
}

func TestBuildTraceQuery_RawQuery(t *testing.T) {
	s := &SplunkTraceSource{}
	req := TracesV3Request{Query: "sf_service:api AND sf_error:true"}
	result := s.buildTraceQuery(req)
	require.Equal(t, "sf_service:api AND sf_error:true", result)
}
