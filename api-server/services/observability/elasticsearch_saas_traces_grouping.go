package observability

// Shared trace-grouping core for the two Elasticsearch trace readers
// (ElasticSaasTraceSource / Data Prepper otel-v1-apm-span-* and
// ElasticOtelTraceSource / OTel-native traces-*.otel). Both group traces into one
// row per distinct (workload, destination, resource, span) tuple with
// count / error_count / p99 / p95 / max-latency aggregates, ordered by a metric
// and paginated. Unlike the ClickHouse source (otel_clickhouse.go QueryGroupedTraces),
// http_status_code is deliberately NOT a group-key dimension here, so a row mixes
// statuses and error_count is a genuine 4xx/5xx subset of Count rather than
// 0-or-Count. See esGroupSubAggs / esHTTPErrorFilter.
//
// The single-source-of-truth here is the []esGroupDim list each reader supplies.
// esCompositeSources and esGroupKeyCardinalityScript are both derived from it, so
// the group key used by QueryGroupedTraces can never drift from the distinct-key
// counted by QueryGroupedTracesCount. Elasticsearch's composite aggregation cannot
// order buckets by a metric sub-agg (only by the key), so ClickHouse-style
// "top-N by count/latency" requires paging every group and sorting in Go — that is
// what fetchAllCompositeTraceBuckets + sortAndPaginateTraceGroups do.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nudgebee/services/query"
	"nudgebee/services/security"
)

// esGroupDim is one group-by dimension. Fields is the ordered coalesce candidate
// list of ES field paths, mirroring the CASE/fallback the per-hit mapper uses; the
// first present, non-empty field wins. A single-field dim compiles to a composite
// `field` term source (fast path, doc_values); a multi-field dim compiles to a
// painless `script` coalesce source.
type esGroupDim struct {
	Key    string
	Fields []string
}

// Composite paging bounds. esGroupPageSize is one composite page; esGroupMaxGroups
// caps the total buckets pulled before sorting+paginating in Go, so a pathological
// high-cardinality key cannot page unbounded. Truncation is logged, never silent.
const (
	esGroupPageSize  = 1000
	esGroupMaxGroups = 10000
)

// esCoalesceReturnScript builds a painless snippet that RETURNS the first present,
// non-empty doc value among fields (for a composite `script` term source).
func esCoalesceReturnScript(fields []string) string {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "if (doc.containsKey('%s') && doc['%s'].size() > 0) { return doc['%s'].value; } ", f, f, f)
	}
	b.WriteString("return '';")
	return b.String()
}

// esCompositeSources compiles the dimension list into composite `sources`.
func esCompositeSources(dims []esGroupDim) []map[string]any {
	sources := make([]map[string]any, 0, len(dims))
	for _, d := range dims {
		terms := map[string]any{}
		if len(d.Fields) == 1 {
			// Single field: fast doc_values path; keep docs missing the field.
			terms["field"] = d.Fields[0]
			terms["missing_bucket"] = true
		} else {
			// Coalesce across candidates via script (returns "" when all absent).
			terms["script"] = map[string]any{
				"source": esCoalesceReturnScript(d.Fields),
				"lang":   "painless",
			}
		}
		sources = append(sources, map[string]any{d.Key: map[string]any{"terms": terms}})
	}
	return sources
}

// esGroupKeyCardinalityScript builds the painless script that concatenates the
// same per-dimension coalesced values into one distinct key, so a `cardinality`
// agg over it counts exactly the groups esCompositeSources would produce.
func esGroupKeyCardinalityScript(dims []esGroupDim) string {
	var b strings.Builder
	for i, d := range dims {
		fmt.Fprintf(&b, "def k%d = '';", i)
		for j, f := range d.Fields {
			kw := "if"
			if j > 0 {
				kw = "else if"
			}
			fmt.Fprintf(&b, " %s (doc.containsKey('%s') && doc['%s'].size() > 0) { k%d = doc['%s'].value; }", kw, f, f, i, f)
		}
		b.WriteString(" ")
	}
	b.WriteString("return ''")
	for i := range dims {
		fmt.Fprintf(&b, " + '||' + k%d", i)
	}
	b.WriteString(";")
	return b.String()
}

// esGroupSubAggs are the per-group sub-aggs: latency percentiles + max over
// durationField, plus error_count = the subset of the group's spans whose http
// status is 4xx/5xx. http_status_code is NOT a group-key dimension, so a group mixes
// statuses and error_count is a genuine subset of its Count (not 0-or-Count).
func esGroupSubAggs(durationField string, httpStatusFields []string) map[string]any {
	return map[string]any{
		"error_count": map[string]any{"filter": esHTTPErrorFilter(httpStatusFields)},
		// avg_latency backs the row's "Duration" column (average span duration for
		// the group); p99/p95/max are the tail-latency columns.
		"avg_latency": map[string]any{"avg": map[string]any{"field": durationField}},
		"p99_latency": map[string]any{"percentiles": map[string]any{"field": durationField, "percents": []float64{99}}},
		"p95_latency": map[string]any{"percentiles": map[string]any{"field": durationField, "percents": []float64{95}}},
		"max_latency": map[string]any{"max": map[string]any{"field": durationField}},
	}
}

// esHTTPErrorFilter matches spans whose http status is in [400,600) across any of the
// candidate status fields (mirrors ClickHouse's http_status_code LIKE '4%'/'5%'). A
// range — not prefix — so it holds whether the field is mapped as keyword (lexical
// 3-digit compare) or numeric (coerced).
func esHTTPErrorFilter(fields []string) map[string]any {
	shoulds := make([]map[string]any, 0, len(fields))
	for _, f := range fields {
		shoulds = append(shoulds, map[string]any{
			"range": map[string]any{f: map[string]any{"gte": "400", "lt": "600"}},
		})
	}
	return map[string]any{
		"bool": map[string]any{
			"should":               shoulds,
			"minimum_should_match": 1,
		},
	}
}

// fetchAllCompositeTraceBuckets pages the composite aggregation via after_key until
// exhausted or the esGroupMaxGroups cap is hit, returning every raw bucket. Sorting
// by a metric is impossible server-side (composite orders by key only), so callers
// sort the full set in Go afterwards.
func fetchAllCompositeTraceBuckets(
	ctx *security.RequestContext,
	cfg *ElasticsearchConfig,
	searchURL string,
	queryFilter []map[string]any,
	sources []map[string]any,
	subAggs map[string]any,
) ([]json.RawMessage, error) {
	var all []json.RawMessage
	var afterKey map[string]any
	truncated := false

	for {
		composite := map[string]any{
			"size":    esGroupPageSize,
			"sources": sources,
		}
		if afterKey != nil {
			composite["after"] = afterKey
		}
		grouped := map[string]any{"composite": composite}
		if len(subAggs) > 0 {
			grouped["aggs"] = subAggs
		}
		dsl := map[string]any{
			"size":  0,
			"query": map[string]any{"bool": map[string]any{"filter": queryFilter}},
			"aggs":  map[string]any{"grouped": grouped},
		}

		resp, err := esRequestJSON("POST", searchURL, dsl, cfg) //nolint:bodyclose
		if err != nil {
			return nil, fmt.Errorf("failed to query grouped traces: %w", err)
		}
		bodyBytes, err := readResponse(resp, "grouped traces")
		if err != nil {
			return nil, err
		}
		var searchResp esTraceSearchResponse
		if err := json.Unmarshal(bodyBytes, &searchResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal grouped traces response: %w", err)
		}
		raw, ok := searchResp.Aggregations["grouped"]
		if !ok {
			break
		}
		var page struct {
			AfterKey map[string]any    `json:"after_key"`
			Buckets  []json.RawMessage `json:"buckets"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("failed to unmarshal composite aggregation: %w", err)
		}
		all = append(all, page.Buckets...)

		// Exhausted when the last page is short or no continuation key is returned.
		if len(page.Buckets) < esGroupPageSize || page.AfterKey == nil {
			break
		}
		if len(all) >= esGroupMaxGroups {
			truncated = true
			break
		}
		afterKey = page.AfterKey
	}

	if truncated {
		ctx.GetLogger().Warn(
			"grouped traces truncated: distinct group count exceeded cap; sort/pagination limited to the first groups fetched",
			"cap", esGroupMaxGroups,
		)
	}
	return all, nil
}

// parseTraceGroupBucket maps one composite bucket to TraceGroupingValues. Both
// readers key on the same logical dimensions, so the parse is identical. error_count
// comes from the error_count filter sub-agg (spans with a 4xx/5xx http status);
// http_status_code is not a key dimension, so it stays empty on the grouped row.
func parseTraceGroupBucket(bucketRaw json.RawMessage) (TraceGroupingValues, bool) {
	var bucket struct {
		Key        map[string]any `json:"key"`
		DocCount   int            `json:"doc_count"`
		ErrorCount struct {
			DocCount int `json:"doc_count"`
		} `json:"error_count"`
		P99Latency struct {
			Values map[string]any `json:"values"`
		} `json:"p99_latency"`
		P95Latency struct {
			Values map[string]any `json:"values"`
		} `json:"p95_latency"`
		MaxLatency struct {
			Value *float64 `json:"value"`
		} `json:"max_latency"`
		AvgLatency struct {
			Value *float64 `json:"value"`
		} `json:"avg_latency"`
	}
	if err := json.Unmarshal(bucketRaw, &bucket); err != nil {
		return TraceGroupingValues{}, false
	}

	return TraceGroupingValues{
		Count:                        bucket.DocCount,
		ErrorCount:                   bucket.ErrorCount.DocCount,
		DurationNS:                   singleValueAgg(bucket.AvgLatency.Value),
		P99Latency:                   percentileValue(bucket.P99Latency.Values, "99.0"),
		P95Latency:                   percentileValue(bucket.P95Latency.Values, "95.0"),
		MaxLatency:                   singleValueAgg(bucket.MaxLatency.Value),
		WorkloadName:                 compositeKeyString(bucket.Key, "workload_name"),
		WorkloadNamespace:            compositeKeyString(bucket.Key, "workload_namespace"),
		DestinationWorkloadName:      compositeKeyString(bucket.Key, "destination_workload_name"),
		DestinationWorkloadNamespace: compositeKeyString(bucket.Key, "destination_workload_namespace"),
		Resource:                     compositeKeyString(bucket.Key, "resource"),
		SpanName:                     compositeKeyString(bucket.Key, "span_name"),
		HTTPStatusCode:               compositeKeyString(bucket.Key, "http_status_code"),
	}, true
}

// sortAndPaginateTraceGroups sorts the full group set by the requested order_by
// column (default count desc, matching the ClickHouse groupings default) and then
// applies offset/limit in Go — composite has no server-side metric order or offset.
func sortAndPaginateTraceGroups(results []TraceGroupingValues, req TracesV3Request) []TraceGroupingValues {
	col := "count"
	ascending := false
	if len(req.QueryRequest.OrderBy) > 0 {
		col = req.QueryRequest.OrderBy[0].Column
		o := req.QueryRequest.OrderBy[0].Order
		ascending = o == query.Asc || o == query.AscNullsFirst || o == query.AscNullsLast
	}

	sort.SliceStable(results, func(i, j int) bool {
		var less bool
		switch col {
		case "error_count":
			less = results[i].ErrorCount < results[j].ErrorCount
		case "duration_ns":
			less = results[i].DurationNS < results[j].DurationNS
		case "p95_latency":
			less = results[i].P95Latency < results[j].P95Latency
		case "p99_latency":
			less = results[i].P99Latency < results[j].P99Latency
		case "max_latency":
			less = results[i].MaxLatency < results[j].MaxLatency
		default: // "count" and anything else
			less = results[i].Count < results[j].Count
		}
		if ascending {
			return less
		}
		return !less
	})

	offset := req.QueryRequest.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(results) {
		return []TraceGroupingValues{}
	}
	end := len(results)
	if limit := req.QueryRequest.Limit; limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return results[offset:end]
}

// compositeKeyString reads a composite key value as a string ("" when the source
// bucketed as missing/null).
func compositeKeyString(key map[string]any, name string) string {
	if v, ok := key[name]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func percentileValue(values map[string]any, k string) int64 {
	if v, ok := values[k]; ok {
		if f, ok := v.(float64); ok {
			return int64(f)
		}
	}
	return 0
}

// singleValueAgg reads a single-value numeric agg (avg/max) as int64 nanoseconds.
func singleValueAgg(v *float64) int64 {
	if v != nil {
		return int64(*v)
	}
	return 0
}
