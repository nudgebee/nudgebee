package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveAggResponse is the verbatim Elasticsearch reply to the aggregation shape the
// agent wrote unaided and we discarded — `aggregations` was absent from the response
// struct, so encoding/json dropped it and the caller was told `total_series: 0`.
// Captured on dev against metrics-aws.cloudwatch_metrics-dev-test, 2026-08-27.
const liveAggResponse = `{
 "took":31,"hits":{"total":{"value":8,"relation":"eq"},"hits":[]},
 "aggregations":{"inst":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[
   {"key":"db-instance-1","doc_count":4,"m":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[
     {"key":"CPUUtilization","doc_count":1,"avg_v":{"value":107.0},"max_v":{"value":92.9}},
     {"key":"Deadlocks","doc_count":1,"avg_v":{"value":5.0},"max_v":{"value":3.0}},
     {"key":"ReadIOPS","doc_count":1,"avg_v":{"value":11441.0},"max_v":{"value":6875.1}}]}}]}}}`

func TestParseESMetrics_AggregationResponseIsRead(t *testing.T) {
	res, stats, err := parseESMetricsHitsWithStats([]byte(liveAggResponse), 1787800000000)
	require.NoError(t, err)

	// The regression: 8 documents matched, zero series returned, and a note blaming
	// a `_source` projection that was never set.
	require.Len(t, res, 6, "3 metrics x 2 metric aggs")
	assert.Equal(t, 6, stats.SeriesParsed)

	by := map[string]Result{}
	for _, r := range res {
		by[r.Metric["__name__"]+"/"+r.Metric["m"]] = r
	}
	assert.InDelta(t, 92.9, by["max_v/CPUUtilization"].Values[0], 1e-9)
	assert.InDelta(t, 6875.1, by["max_v/ReadIOPS"].Values[0], 1e-9)
	assert.InDelta(t, 5.0, by["avg_v/Deadlocks"].Values[0], 1e-9)

	// Bucket keys become labels, keyed by the aggregation's own name.
	assert.Equal(t, "db-instance-1", by["max_v/CPUUtilization"].Metric["inst"])
	// No date_histogram, so the query end stamps the reading.
	assert.Equal(t, int64(1787800000), by["max_v/CPUUtilization"].Timestamps[0])
}

// A date_histogram bucket is the timestamp, not a label — otherwise a time series
// collapses into one label set with the epoch as its value.
func TestParseESAggregations_DateHistogramBecomesTimestamps(t *testing.T) {
	body := `{"hits":{"total":{"value":2},"hits":[]},"aggregations":{
	 "over_time":{"buckets":[
	  {"key_as_string":"2026-08-27T05:00:00.000Z","key":1787799600000,"doc_count":1,"cpu":{"value":10.0}},
	  {"key_as_string":"2026-08-27T06:00:00.000Z","key":1787803200000,"doc_count":1,"cpu":{"value":20.0}}]}}}`
	res, _, err := parseESMetricsHitsWithStats([]byte(body), 0)
	require.NoError(t, err)
	require.Len(t, res, 2)
	for _, r := range res {
		assert.Equal(t, "cpu", r.Metric["__name__"])
		_, isLabel := r.Metric["over_time"]
		assert.False(t, isLabel, "a time bucket must not also become a label")
	}
	assert.ElementsMatch(t, []int64{1787799600, 1787803200},
		[]int64{res[0].Timestamps[0], res[1].Timestamps[0]})
}

// A terms aggregation over a NUMERIC field also has numeric keys. It has no
// key_as_string, so it must stay a label rather than be read as a timestamp.
func TestParseESAggregations_NumericTermsIsNotATimeBucket(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[]},"aggregations":{
	 "port":{"buckets":[{"key":8080,"doc_count":1,"hits_total":{"value":42.0}}]}}}`
	res, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "8080", res[0].Metric["port"], "numeric terms key must be a label")
	assert.Equal(t, int64(1787800000), res[0].Timestamps[0])
}

// stats/extended_stats carry several numbers under one name; they are separated by the
// same `statistic` label the CloudWatch document parser uses.
func TestParseESAggregations_StatsSplitsByStatistic(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[]},"aggregations":{
	 "s":{"count":2,"min":6.0,"max":103.0,"avg":54.5,"sum":109.0}}}`
	res, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.NoError(t, err)
	by := map[string]float64{}
	for _, r := range res {
		by[r.Metric["statistic"]] = r.Values[0]
	}
	assert.InDelta(t, 54.5, by["avg"], 1e-9)
	assert.InDelta(t, 103.0, by["max"], 1e-9)
	assert.InDelta(t, 6.0, by["min"], 1e-9)
	assert.InDelta(t, 109.0, by["sum"], 1e-9)
}

// `filters` renders buckets as a keyed object rather than an array.
func TestParseESAggregations_KeyedBuckets(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[]},"aggregations":{
	 "env":{"buckets":{"prod":{"doc_count":1,"c":{"value":7.0}}}}}}`
	res, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "prod", res[0].Metric["env"])
	assert.InDelta(t, 7.0, res[0].Values[0], 1e-9)
}

// An aggregation we executed but cannot read is OUR defect. It must be an error naming
// the shape — never an empty result, which reads as "you have no data" and is what
// sent one investigation through 86 queries against data that was present.
func TestParseESAggregations_UnsupportedShapeIsAnError(t *testing.T) {
	body := `{"hits":{"total":{"value":5},"hits":[]},"aggregations":{
	 "pcts":{"values":{"95.0":12.3,"99.0":45.6}},
	 "ok_one":{"value":1.0}}}`
	res, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pcts", "the error must name the aggregation we could not read")
	assert.Contains(t, err.Error(), "terms", "and list what is supported")
	// Whatever WAS readable is still returned rather than discarded.
	require.Len(t, res, 1)
	assert.Equal(t, "ok_one", res[0].Metric["__name__"])
}

// A null value means the aggregation matched no documents. That is a real answer, not
// an unreadable shape, and must not be reported as an error.
func TestParseESAggregations_NullValueIsNotAnError(t *testing.T) {
	body := `{"hits":{"total":{"value":0},"hits":[]},"aggregations":{"m":{"value":null}}}`
	res, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.NoError(t, err)
	assert.Empty(t, res)
}

// Aggregations must not disturb the raw-document path when both are present.
func TestParseESMetrics_AggregationsAndHitsCoexist(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[
	 {"_index":"metricbeat-8.19.11","_source":{"@timestamp":"2026-08-27T05:19:00.000Z",
	   "name":"cpu","value":3.5}}]},
	 "aggregations":{"total":{"value":9.0}}}`
	res, stats, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.NoError(t, err)
	require.Len(t, res, 2)
	assert.Equal(t, 2, stats.SeriesParsed)
	names := []string{res[0].Metric["__name__"], res[1].Metric["__name__"]}
	assert.ElementsMatch(t, []string{"total", "cpu"}, names)
}

// A present, non-null, non-numeric value is a shape we do not understand — not an
// aggregation that matched nothing. Conflating them would reproduce this change's own
// defect one level up.
func TestParseESAggregations_NonNumericValueIsUnsupported(t *testing.T) {
	body := `{"hits":{"total":{"value":3},"hits":[]},"aggregations":{
	 "odd":{"value":"not-a-number"}}}`
	_, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "odd")
}

// Series order must be stable: callers diff them, and map iteration is random.
func TestParseESAggregations_OrderIsStable(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[]},"aggregations":{
	 "inst":{"buckets":[
	   {"key":"b","doc_count":1,"v":{"value":2.0}},
	   {"key":"a","doc_count":1,"v":{"value":1.0}},
	   {"key":"c","doc_count":1,"v":{"value":3.0}}]}}}`
	var first []string
	for i := 0; i < 5; i++ {
		res, _, err := parseESMetricsHitsWithStats([]byte(body), 1787800000000)
		require.NoError(t, err)
		var got []string
		for _, r := range res {
			got = append(got, r.Metric["inst"])
		}
		if i == 0 {
			first = got
			continue
		}
		assert.Equal(t, first, got, "series order must not vary between runs")
	}
}

// A snapshot-restored backing index carries a restore prefix before ".ds-". Seen on a
// customer estate as partial-restored-.ds-metrics-aws.metrics-forwarder-…; stripping
// ".ds-" as a prefix missed it, so the dataset was lost and dispatch never fired.
func TestESIndexDataset_RestoredBackingIndex(t *testing.T) {
	assert.Equal(t, "aws.metrics", esIndexDataset(
		"partial-restored-.ds-metrics-aws.metrics-forwarder-gd-ehq-non-prod-2025.05.01-000014"))
	assert.Equal(t, "aws.cloudwatch_metrics", esIndexDataset(
		"restored-.ds-metrics-aws.cloudwatch_metrics-prod-2026.08.16-000018"))
}
