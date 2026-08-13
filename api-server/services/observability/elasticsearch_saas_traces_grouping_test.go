package observability

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
)

// error_count comes from the error_count filter sub-agg (4xx/5xx spans), NOT from
// the key — http_status_code is no longer a group dimension, so a group mixes
// statuses and error_count is a genuine subset of Count.
func TestParseTraceGroupBucketErrorFromSubAgg(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"key": map[string]any{
			"workload_name":             "api",
			"workload_namespace":        "prod",
			"destination_workload_name": "db",
			"resource":                  "/x",
			"span_name":                 "GET /x",
		},
		"doc_count":   100,
		"error_count": map[string]any{"doc_count": 13}, // 13 of 100 were 4xx/5xx
		"avg_latency": map[string]any{"value": 600.0},
		"p99_latency": map[string]any{"values": map[string]any{"99.0": 1500.0}},
		"p95_latency": map[string]any{"values": map[string]any{"95.0": 900.0}},
		"max_latency": map[string]any{"value": 3000.0},
	})

	v, ok := parseTraceGroupBucket(b)
	assert.True(t, ok)
	assert.Equal(t, 100, v.Count)
	assert.Equal(t, 13, v.ErrorCount)         // subset, not 0-or-Count
	assert.Equal(t, int64(600), v.DurationNS) // Duration column = avg latency
	assert.Equal(t, int64(1500), v.P99Latency)
	assert.Equal(t, int64(900), v.P95Latency)
	assert.Equal(t, int64(3000), v.MaxLatency)
	assert.Equal(t, "api", v.WorkloadName)
	assert.Equal(t, "prod", v.WorkloadNamespace)
	assert.Equal(t, "db", v.DestinationWorkloadName)
	assert.Equal(t, "/x", v.Resource)
	assert.Equal(t, "GET /x", v.SpanName)
	assert.Equal(t, "", v.HTTPStatusCode) // not a key dim anymore
}

// A composite source that bucketed as missing yields a nil key value → "".
func TestParseTraceGroupBucketMissingKey(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"key": map[string]any{
			"workload_name":      "api",
			"workload_namespace": nil,
		},
		"doc_count": 3,
	})
	v, ok := parseTraceGroupBucket(b)
	assert.True(t, ok)
	assert.Equal(t, 0, v.ErrorCount)
	assert.Equal(t, "", v.WorkloadNamespace)
}

// esHTTPErrorFilter builds a should-of-ranges over every candidate status field.
func TestEsHTTPErrorFilter(t *testing.T) {
	f := esHTTPErrorFilter([]string{"a", "b"})
	boolClause := f["bool"].(map[string]any)
	assert.Equal(t, 1, boolClause["minimum_should_match"])
	shoulds := boolClause["should"].([]map[string]any)
	assert.Len(t, shoulds, 2)
	rng := shoulds[0]["range"].(map[string]any)["a"].(map[string]any)
	assert.Equal(t, "400", rng["gte"])
	assert.Equal(t, "600", rng["lt"])
}

func groups(counts ...int) []TraceGroupingValues {
	out := make([]TraceGroupingValues, 0, len(counts))
	for _, c := range counts {
		out = append(out, TraceGroupingValues{Count: c})
	}
	return out
}

func countsOf(g []TraceGroupingValues) []int {
	out := make([]int, 0, len(g))
	for _, v := range g {
		out = append(out, v.Count)
	}
	return out
}

func TestSortAndPaginateTraceGroupsDefaultCountDesc(t *testing.T) {
	req := TracesV3Request{}
	req.QueryRequest.Limit = 0
	// No OrderBy → default count desc across all groups.
	res := sortAndPaginateTraceGroups(groups(3, 10, 1, 7), req)
	assert.Equal(t, []int{10, 7, 3, 1}, countsOf(res))
}

func TestSortAndPaginateTraceGroupsOffsetLimit(t *testing.T) {
	req := TracesV3Request{}
	req.QueryRequest.Limit = 2
	req.QueryRequest.Offset = 1
	// Sorted desc = [10,7,3,1]; offset 1 limit 2 → [7,3].
	res := sortAndPaginateTraceGroups(groups(3, 10, 1, 7), req)
	assert.Equal(t, []int{7, 3}, countsOf(res))
}

func TestSortAndPaginateTraceGroupsOffsetBeyondEnd(t *testing.T) {
	req := TracesV3Request{}
	req.QueryRequest.Offset = 99
	res := sortAndPaginateTraceGroups(groups(3, 10, 1), req)
	assert.Empty(t, res)
}

func TestSortAndPaginateTraceGroupsAscOrderBy(t *testing.T) {
	req := TracesV3Request{}
	req.QueryRequest.OrderBy = []query.QueryOrderBy{{Column: "count", Order: query.Asc}}
	res := sortAndPaginateTraceGroups(groups(3, 10, 1, 7), req)
	assert.Equal(t, []int{1, 3, 7, 10}, countsOf(res))
}

// Single-field dims compile to a `field` term source (with missing_bucket);
// multi-field dims compile to a painless `script` coalesce source.
func TestEsCompositeSources(t *testing.T) {
	dims := []esGroupDim{
		{Key: "span_name", Fields: []string{"name"}},
		{Key: "resource", Fields: []string{"a", "b"}},
	}
	sources := esCompositeSources(dims)
	assert.Len(t, sources, 2)

	spanTerms := sources[0]["span_name"].(map[string]any)["terms"].(map[string]any)
	assert.Equal(t, "name", spanTerms["field"])
	assert.Equal(t, true, spanTerms["missing_bucket"])
	assert.NotContains(t, spanTerms, "script")

	resTerms := sources[1]["resource"].(map[string]any)["terms"].(map[string]any)
	assert.Contains(t, resTerms, "script")
	assert.NotContains(t, resTerms, "field")
}

// The count cardinality script must reference the same fields as the composite key
// so the group count matches the number of grouped rows (no drift).
func TestEsGroupKeyCardinalityScriptCoversAllFields(t *testing.T) {
	src := esGroupKeyCardinalityScript(elasticOtelTraceGroupDims)
	for _, d := range elasticOtelTraceGroupDims {
		for _, f := range d.Fields {
			assert.Contains(t, src, f, "cardinality script missing field %s", f)
		}
	}
	// One '||' emitted per dimension (6 dims after dropping http_status_code).
	assert.Equal(t, len(elasticOtelTraceGroupDims), strings.Count(src, "'||'"))
}
