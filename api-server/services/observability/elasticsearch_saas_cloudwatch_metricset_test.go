package observability

import (
	"testing"

	"nudgebee/services/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awsCloudwatchSampleBody is a verbatim excerpt of a live response from a customer's
// Elasticsearch (2026-08-26), the shape that returned docs_matched=61 / total_series=0
// on every one of 86 agent queries before esDatasetParsers existed. Kept literal so a
// change to the extractor is checked against real data, not against a paraphrase.
const awsCloudwatchSampleBody = `{
 "hits": {
  "total": {"value": 3257, "relation": "eq"},
  "hits": [
   {"_index": "aircp-es-transport:.ds-metrics-aws.cloudwatch_metrics-gd-ehq-non-prod-2026.08.16-000018",
    "_source": {"@timestamp": "2026-08-26T13:41:25.762Z",
     "metricset": {"dimensions": {"DBClusterIdentifier": "asydd-auroradb"},
      "metric_name": "DatabaseConnections", "timestamp": "2026-08-26T13:38:00.000Z",
      "unit": "Count", "value": {"count": 2.0, "max": 103.0, "min": 6.0, "sum": 109.0}}}},
   {"_index": "aircp-es-transport:.ds-metrics-aws.cloudwatch_metrics-gd-ehq-non-prod-2026.08.16-000018",
    "_source": {"@timestamp": "2026-08-26T13:41:25.753Z",
     "metricset": {"dimensions": {"DBClusterIdentifier": "asydd-auroradb", "Role": "READER"},
      "metric_name": "ActiveTransactions", "timestamp": "2026-08-26T13:38:00.000Z",
      "unit": "Count/Second", "value": {"count": 1.0, "max": 1.53341, "min": 1.53341, "sum": 1.53341}}}},
   {"_index": "aircp-es-transport:.ds-metrics-aws.cloudwatch_metrics-gd-ehq-non-prod-2026.08.16-000018",
    "_source": {"@timestamp": "2026-08-26T13:41:25.757Z",
     "metricset": {"dimensions": {"DBInstanceIdentifier": "asydd-engagementcloud-pg1"},
      "metric_name": "DBLoadNonCPU", "timestamp": "2026-08-26T13:38:00.000Z",
      "unit": "None", "value": {"count": 60.0, "max": 0.0, "min": 0.0, "sum": 0.0}}}}
  ]
 }
}`

func TestESIndexDataset(t *testing.T) {
	cases := map[string]string{
		// Cross-cluster prefix + hidden backing index + date/generation suffix.
		"aircp-es-transport:.ds-metrics-aws.cloudwatch_metrics-gd-ehq-non-prod-2026.08.16-000018": "aws.cloudwatch_metrics",
		".ds-metrics-kubernetes.state_pod-gd-ehq-non-prod-2026.08.16-000004":                      "kubernetes.state_pod",
		"metrics-aws.cloudwatch_metrics-gd-ehq-non-prod":                                          "aws.cloudwatch_metrics",
		// Namespaces contain '-' and must not be mistaken for the dataset.
		"metrics-aws.rds-a-b-c-d": "aws.rds",
		// Legacy concrete indices declare nothing; callers fall back to shape detection.
		"metricbeat-7.17.0-2026.08.16": "",
		"someindex":                    "",
	}
	for index, want := range cases {
		assert.Equal(t, want, esIndexDataset(index), "index %q", index)
	}
}

func TestParseESMetricsHits_AWSCloudwatchMetricset(t *testing.T) {
	results, stats, err := parseESMetricsHitsWithStats([]byte(awsCloudwatchSampleBody))
	require.NoError(t, err)

	// The regression this guards: three documents in, zero series out.
	require.NotEmpty(t, results, "metricset documents must produce series")
	assert.Equal(t, 12, stats.SeriesParsed, "3 documents x avg/max/min/sum")
	assert.Zero(t, stats.DroppedNoValue)

	byKey := map[string]Result{}
	for _, r := range results {
		byKey[r.Metric["__name__"]+"/"+r.Metric["statistic"]] = r
	}

	avg, ok := byKey["DatabaseConnections/avg"]
	require.True(t, ok, "got series: %v", byKey)
	// sum/count, not sum: 109/2. Charting sum would report 109 connections.
	assert.InDelta(t, 54.5, avg.Values[0], 1e-9)
	assert.Equal(t, "asydd-auroradb", avg.Metric["DBClusterIdentifier"])
	assert.Equal(t, "Count", avg.Metric["unit"])

	// metricset.timestamp (13:38:00), NOT @timestamp (13:41:25). Using the ingest
	// time would place every metric of one batch on the same instant.
	assert.Equal(t, int64(1787751480), avg.Timestamps[0])

	maxSeries := byKey["DatabaseConnections/max"]
	assert.InDelta(t, 103.0, maxSeries.Values[0], 1e-9)
	assert.InDelta(t, 6.0, byKey["DatabaseConnections/min"].Values[0], 1e-9)
	// sum is what a Count-unit metric (Deadlocks, Aurora_pq_request_*) actually means.
	assert.InDelta(t, 109.0, byKey["DatabaseConnections/sum"].Values[0], 1e-9)

	// Multi-dimension keys all become labels, so cluster+role is its own series.
	role := byKey["ActiveTransactions/max"]
	assert.Equal(t, "READER", role.Metric["Role"])
	assert.Equal(t, "asydd-auroradb", role.Metric["DBClusterIdentifier"])

	// unit "None" is CloudWatch's placeholder and is not carried as a label.
	_, hasUnit := byKey["DBLoadNonCPU/avg"].Metric["unit"]
	assert.False(t, hasUnit)
}

// A registered dataset whose document does not match the expected shape must fall
// through to the existing detection chain, not be dropped.
func TestParseESMetricsHits_DatasetFallsThroughOnShapeMismatch(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[
	 {"_index":"aircp-es-transport:.ds-metrics-aws.cloudwatch_metrics-gd-ehq-non-prod-2026.08.16-000018",
	  "_source":{"@timestamp":"2026-08-26T13:38:00.000Z","name":"CPUUtilization","value":42.5}}]}}`
	results, stats, err := parseESMetricsHitsWithStats([]byte(body))
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "CPUUtilization", results[0].Metric["__name__"])
	assert.InDelta(t, 42.5, results[0].Values[0], 1e-9)
	assert.Zero(t, stats.DroppedNoValue)
}

// The note must state what was observed. The text it replaced asserted a `_source`
// projection was the cause in every case, which sent the agent in a loop.
func TestESNoSeriesNote_NamesTheObservedCause(t *testing.T) {
	note := esNoSeriesNote(esParseStats{
		DocsMatched:        61,
		DroppedNoValue:     10,
		SampleSourceFields: []string{"@timestamp", "metricset"},
	})
	assert.Contains(t, note, "61 document")
	assert.Contains(t, note, "shape is not supported")
	assert.Contains(t, note, "metricset")
	assert.Contains(t, note, "do not reformulate the query")
}

// Agents narrow queries with `_source`, which strips whole branches of the document.
// Observed on 2026-08-26: `_source` of [@timestamp, metricset.metric_name,
// metricset.dimensions.*, metricset.value.sum, metricset.value.max] — no `count`, so
// no average is computable. The surviving statistics must still be emitted, and the
// dispatch must still work because it keys on `_index`, which `_source` cannot touch.
func TestParseESMetricsHits_AWSCloudwatchPartialSourceProjection(t *testing.T) {
	body := `{"hits":{"total":{"value":61},"hits":[
	 {"_index":"aircp-es-transport:.ds-metrics-aws.cloudwatch_metrics-gd-ehq-non-prod-2026.08.16-000018",
	  "_source":{"@timestamp":"2026-08-26T13:41:25.762Z","metricset":{
	    "metric_name":"CPUUtilization",
	    "dimensions":{"DBInstanceIdentifier":"asydd-auroradb-writer"},
	    "value":{"sum":109.0,"max":103.0}}}}]}}`
	results, stats, err := parseESMetricsHitsWithStats([]byte(body))
	require.NoError(t, err)
	// max and sum are both projected in; avg is not computable without count and must
	// simply be absent rather than guessed.
	require.Len(t, results, 2, "surviving statistics must still be emitted")
	byStat := map[string]Result{}
	for _, r := range results {
		byStat[r.Metric["statistic"]] = r
	}
	assert.InDelta(t, 103.0, byStat["max"].Values[0], 1e-9)
	assert.InDelta(t, 109.0, byStat["sum"].Values[0], 1e-9)
	_, hasAvg := byStat["avg"]
	assert.False(t, hasAvg, "avg needs count; it must be omitted, not invented")
	assert.Equal(t, "asydd-auroradb-writer", byStat["max"].Metric["DBInstanceIdentifier"])
	// No metricset.timestamp in the projection: fall back to the document timestamp
	// rather than dropping the hit.
	assert.Equal(t, int64(1787751685), byStat["max"].Timestamps[0])
	assert.Zero(t, stats.DroppedNoValue)
}

// The system/host metricset shape, seen 5 times in the services-server log of
// 2026-08-26 among the 84,478 dropped documents. Its numbers are numeric leaves under
// `system.*` / `process.*` — structurally the Metricbeat shape, but beatsMetricSeries
// only walks `kubernetes`, so nothing recognised it. The generic fallback must read it
// rather than drop it, even though no dataset parser exists for it.
func TestParseESMetricsHits_GenericFallbackReadsSystemMetricset(t *testing.T) {
	prev := config.Config.FeatureESMetricsGenericFallbackEnabled
	config.Config.FeatureESMetricsGenericFallbackEnabled = true
	t.Cleanup(func() { config.Config.FeatureESMetricsGenericFallbackEnabled = prev })

	body := `{"hits":{"total":{"value":5},"hits":[
	 {"_index":".ds-metrics-system.process-gd-ehq-non-prod-2026.08.16-000003",
	  "_source":{
	   "@timestamp":"2026-08-26T13:38:00.000Z",
	   "@version":"1",
	   "agent":{"type":"metricbeat","version":"8.19.11"},
	   "ecs":{"version":"8.0.0"},
	   "event":{"duration":123456},
	   "data_stream":{"dataset":"system.process"},
	   "host":{"name":"asydd-node-1"},
	   "service":{"name":"postgres"},
	   "system":{"cpu":{"total":{"pct":0.42},"cores":8}},
	   "process":{"memory":{"rss":{"bytes":2048}}},
	   "tags":["preprod"]}}]}}`
	results, stats, err := parseESMetricsHitsWithStats([]byte(body))
	require.NoError(t, err)
	require.NotEmpty(t, results, "an unknown shape must degrade to visible series, not zero")
	assert.Zero(t, stats.DroppedNoValue)

	got := map[string]float64{}
	for _, r := range results {
		got[r.Metric["__name__"]] = r.Values[0]
	}
	assert.InDelta(t, 0.42, got["system.cpu.total.pct"], 1e-9)
	assert.InDelta(t, 8.0, got["system.cpu.cores"], 1e-9)
	assert.InDelta(t, 2048.0, got["process.memory.rss.bytes"], 1e-9)

	// Document metadata is not a measurement: event.duration must not become a metric.
	_, hasEventDuration := got["event.duration"]
	assert.False(t, hasEventDuration, "metadata branches must be skipped, got: %v", got)

	// String leaves outside the skip list become labels.
	assert.Equal(t, "asydd-node-1", results[0].Metric["host.name"])
}

// A registered dataset parser must win over the generic fallback — otherwise AWS
// documents would come back as "metricset.value.sum" with no statistics and the
// ingest timestamp.
func TestParseESMetricsHits_DatasetParserBeatsGenericFallback(t *testing.T) {
	results, _, err := parseESMetricsHitsWithStats([]byte(awsCloudwatchSampleBody))
	require.NoError(t, err)
	for _, r := range results {
		assert.NotContains(t, r.Metric["__name__"], "metricset.value",
			"AWS documents must go through awsCloudwatchMetricsetSeries, not the fallback")
	}
}

// The fallback must not resurrect the flat-zero bug: a document with no number
// anywhere is still dropped and counted, not charted as 0.
func TestParseESMetricsHits_NoNumbersStillDropped(t *testing.T) {
	body := `{"hits":{"total":{"value":1},"hits":[
	 {"_index":".ds-logs-generic.otel-default-2026.08.16-000001",
	  "_source":{"@timestamp":"2026-08-26T13:38:00.000Z","host":{"name":"n1"},"tags":["x"]}}]}}`
	results, stats, err := parseESMetricsHitsWithStats([]byte(body))
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 1, stats.DroppedNoValue)
}

// The fallback is a behaviour change for every existing ES tenant — indices that
// return nothing today would start returning dotted-path series — so it must stay off
// until an environment opts in. The dataset-dispatch path is deliberately unflagged.
func TestParseESMetricsHits_GenericFallbackIsOffByDefault(t *testing.T) {
	prev := config.Config.FeatureESMetricsGenericFallbackEnabled
	config.Config.FeatureESMetricsGenericFallbackEnabled = false
	t.Cleanup(func() { config.Config.FeatureESMetricsGenericFallbackEnabled = prev })

	body := `{"hits":{"total":{"value":1},"hits":[
	 {"_index":".ds-metrics-system.process-gd-ehq-non-prod-2026.08.16-000003",
	  "_source":{"@timestamp":"2026-08-26T13:38:00.000Z",
	   "system":{"cpu":{"total":{"pct":0.42}}}}}]}}`
	results, stats, err := parseESMetricsHitsWithStats([]byte(body))
	require.NoError(t, err)
	assert.Empty(t, results)
	assert.Equal(t, 1, stats.DroppedNoValue)

	// And the registry path must NOT be gated by the same flag: an AWS document still
	// parses with the fallback disabled.
	awsResults, _, err := parseESMetricsHitsWithStats([]byte(awsCloudwatchSampleBody))
	require.NoError(t, err)
	assert.NotEmpty(t, awsResults, "dataset dispatch is unflagged and must still work")
}
