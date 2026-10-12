package observability

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #39201: unsorted, a query matching more than its size returned an arbitrary
// slice of the window; it is now sorted newest first unless it has a sort.
func TestESDefaultNewestFirst(t *testing.T) {
	body, err := buildESMetricsQueryBody("dsl", `{"query":{"match_all":{}}}`, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, esNewestFirstSort(), body["sort"])

	builder, err := buildESMetricsQueryBody("", `[]`, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, esNewestFirstSort(), builder["sort"])

	own, err := buildESMetricsQueryBody("dsl", `{"sort":[{"value":"asc"}],"query":{"match_all":{}}}`, 1, 2)
	require.NoError(t, err)
	assert.NotEqual(t, esNewestFirstSort(), own["sort"], "a query's own sort is kept")

	aggOnly, err := buildESMetricsQueryBody("dsl", `{"size":0,"aggs":{"a":{"max":{"field":"v"}}}}`, 1, 2)
	require.NoError(t, err)
	assert.NotContains(t, aggOnly, "sort", "size 0 returns no documents to sort")

	for _, b := range []string{`{"aggs":{"a":{"max":{"field":"v"}}}}`, `{"query":{"match_all":{}},"rescore":{"window_size":10}}`, `{"query":{"match_all":{}},"collapse":{"field":"k"}}`} {
		got, err := buildESMetricsQueryBody("dsl", b, 1, 2)
		require.NoError(t, err)
		assert.NotContains(t, got, "sort", b)
	}
}

func TestESHitsTotalIsLowerBound(t *testing.T) {
	assert.True(t, esHitsTotalIsLowerBound(`{"hits":{"total":{"value":10000,"relation":"gte"},"hits":[]}}`))
	assert.False(t, esHitsTotalIsLowerBound(`{"hits":{"total":{"value":42,"relation":"eq"},"hits":[]}}`))
	assert.False(t, esHitsTotalIsLowerBound(`{"hits":{"total":42}}`))
}

func TestESDocCapNote(t *testing.T) {
	start := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	end := start.Add(6 * time.Hour)
	first, last := end.Add(-29*time.Minute).Unix(), end.Unix()
	sorted := map[string]any{"sort": esNewestFirstSort()}

	assert.Empty(t, esDocCapNote(esParseStats{DocsMatched: 42, HitsReturned: 42}, sorted, start.UnixMilli(), end.UnixMilli()))

	note := esDocCapNote(esParseStats{DocsMatched: 10000, HitsReturned: 10000, TotalIsLowerBound: true, FirstTs: first, LastTs: last},
		sorted, start.UnixMilli(), end.UnixMilli())
	assert.Contains(t, note, "matched more than 10000 documents but only the newest 10000 were read")
	assert.Contains(t, note, "cover only 2026-10-12 14:31 UTC to 2026-10-12 15:00 UTC of the requested 2026-10-12 09:00 UTC to 2026-10-12 15:00 UTC")

	// A deliberate small size is not a gap.
	assert.Empty(t, esDocCapNote(esParseStats{DocsMatched: 500, HitsReturned: 1}, sorted, 0, 0))

	// The query's own sort, exact total above the default cap.
	own := map[string]any{"sort": []any{map[string]any{"value": "asc"}}}
	note = esDocCapNote(esParseStats{DocsMatched: 25000, HitsReturned: 10000, FirstTs: first, LastTs: last}, own, 0, 0)
	assert.Contains(t, note, "matched 25000 documents but only the first (in the query's sort order) 10000 were read")
	assert.NotContains(t, note, "of the requested")

	// Aggregation series cover the whole window: no note.
	assert.Empty(t, esDocCapNote(esParseStats{DocsMatched: 10000, HitsReturned: 10000, TotalIsLowerBound: true},
		map[string]any{"aggs": map[string]any{}}, 0, 0))
}

func TestESRejectedDefaultSort(t *testing.T) {
	body := map[string]any{"sort": esNewestFirstSort()}
	assert.True(t, esRejectedDefaultSort(body, errors.New("metric query: 400: Text fields are not optimised for operations that require per-document field data (fielddata)")))
	assert.True(t, esRejectedDefaultSort(body, errors.New("400: Cannot use [sort] option in conjunction with [rescore]")))
	assert.False(t, esRejectedDefaultSort(body, errors.New("400: index_not_found_exception")))
	assert.False(t, esRejectedDefaultSort(map[string]any{"sort": []any{"x"}}, errors.New("400: sort")), "the query's own sort is not retried")
}

func TestESShardFailures(t *testing.T) {
	raw := `{"_shards":{"total":6,"successful":5,"failed":1,"failures":[{"reason":{"type":"illegal_argument_exception","reason":"match_only_text fields do not support sorting and aggregations"}}]},"hits":{"hits":[]}}`
	assert.True(t, esShardsRejectedDefaultSort(map[string]any{"sort": esNewestFirstSort()}, []byte(raw)))
	assert.False(t, esShardsRejectedDefaultSort(map[string]any{}, []byte(raw)), "no default sort, nothing to retry")
	assert.Equal(t, "1 of 6 shards failed (match_only_text fields do not support sorting and aggregations), so these results are partial: documents on the failed shards were not read.", esShardFailureNote(raw))
	assert.Empty(t, esShardFailureNote(`{"_shards":{"total":6,"successful":6,"failed":0},"hits":{"hits":[]}}`))
	assert.Equal(t, "1 of 2 shards failed (node left), so these results are partial: documents on the failed shards were not read.",
		esShardFailureNote(`{"_shards":{"total":2,"failed":1,"failures":[{"reason":"node left"}]}}`), "a plain-string reason")
	assert.Equal(t, "1 of 2 shards failed, so these results are partial: documents on the failed shards were not read.",
		esShardFailureNote(`{"_shards":{"total":2,"failed":1}}`))
	assert.False(t, esRejectedDefaultSort(map[string]any{"sort": esNewestFirstSort()}, nil))
}

// An aggregation on a field the documents lack returns a null value: the note
// must ask for a corrected query, not say "do not reformulate" (#39201).
func TestESEmptyAggregationNote(t *testing.T) {
	raw := []byte(`{"hits":{"total":{"value":5000,"relation":"eq"},"hits":[]},"aggregations":{"a":{"value":null}}}`)
	payload, stats, err := parseESMetricsHitsWithStats(raw, 0)
	require.NoError(t, err)
	assert.Empty(t, payload)
	assert.True(t, stats.HasAggregations)
	assert.Equal(t, 0, stats.HitsReturned)
	note := esEmptyAggregationNote(stats)
	assert.Contains(t, note, "matched 5000 document(s) but its aggregation returned no buckets")
	assert.Contains(t, note, "rerun with the correct field names")
}

// A metric query must not run unbounded on the customer's cluster (#39201).
func TestESDefaultSearchTimeout(t *testing.T) {
	body, err := buildESMetricsQueryBody("dsl", `{"size":0,"aggs":{"a":{"max":{"field":"v"}}}}`, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, esMetricsSearchTimeout, body["timeout"])
	builder, err := buildESMetricsQueryBody("", `[]`, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, esMetricsSearchTimeout, builder["timeout"])
	own, err := buildESMetricsQueryBody("dsl", `{"timeout":"5s","query":{"match_all":{}}}`, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, "5s", own["timeout"], "a query's own timeout is kept")

	assert.Contains(t, esTimedOutNote(`{"timed_out":true,"hits":{"hits":[]}}`), "stopped this query at its time limit")
	assert.Empty(t, esTimedOutNote(`{"timed_out":false,"hits":{"hits":[]}}`))
}
