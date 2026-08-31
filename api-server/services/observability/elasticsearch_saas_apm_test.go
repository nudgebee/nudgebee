package observability

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verbatim customer documents from metrics-apm.*-gd-ehq-non-prod. APM aggregated
// metrics are the shape no registered parser covers, so they exercise the generic
// reader end to end.
const (
	apmTransactionDoc = `{"hits":{"total":{"value":1446},"hits":[
	 {"_index":"partial-restored-.ds-metrics-apm.transaction.1m-gd-ehq-non-prod-2025.04.30-000001",
	  "_source":{"@timestamp":"2025-04-30T17:30:00.000Z","_doc_count":4,
	   "agent":{"name":"nodejs"},
	   "data_stream":{"dataset":"apm.transaction.1m","namespace":"gd-ehq-non-prod","type":"metrics"},
	   "event":{"agent_id_status":"missing","ingested":"2025-04-30T17:30:33Z","outcome":"success",
	     "success_count":{"sum":4,"value_count":4}},
	   "metricset":{"interval":"1m","name":"transaction"},
	   "service":{"environment":"production","name":"preprod_au_govd-ehq_fe","version":"12.3.4"},
	   "transaction":{"duration":{
	       "histogram":{"values":[9855,11711,21631,72703],"counts":[1,1,1,1]},
	       "summary":{"sum":115900,"value_count":4}},
	     "name":"PATCH unknown route","result":"HTTP 2xx","root":true,"type":"request"}}}]}}`

	apmServiceDestinationDoc = `{"hits":{"total":{"value":2654},"hits":[
	 {"_index":"partial-restored-.ds-metrics-apm.service_destination.1m-gd-ehq-non-prod-2025.04.30-000001",
	  "_source":{"@timestamp":"2025-04-30T17:30:00.000Z","_doc_count":16,
	   "event":{"ingested":"2025-04-30T17:30:33Z","outcome":"success"},
	   "data_stream":{"dataset":"apm.service_destination.1m","namespace":"gd-ehq-non-prod","type":"metrics"},
	   "metricset":{"interval":"1m","name":"service_destination"},
	   "service":{"name":"preprod_au_govd-ehq_fe","target":{"name":"dep.example:443","type":"http"}},
	   "span":{"destination":{"service":{"resource":"dep.example:443",
	     "response_time":{"count":16,"sum":{"us":3001262}}}},
	     "name":"GET dep.example"}}}]}}`
)

func apmSeriesByName(t *testing.T, body string) (map[string]float64, map[string]string) {
	t.Helper()
	res, stats, err := parseESMetricsHitsWithStats([]byte(body), 0)
	require.NoError(t, err)
	require.NotEmpty(t, res, "an APM document must produce metrics, not silence")
	assert.Zero(t, stats.DroppedNoValue)
	by := map[string]float64{}
	for _, r := range res {
		by[r.Metric["__name__"]] = r.Values[0]
	}
	return by, res[0].Metric
}

// The weighted sum computed from the histogram must equal the summary Elastic
// computed independently on the same document — the strongest available check that
// the histogram maths is right.
func TestAPM_TransactionHistogramMatchesElasticsSummary(t *testing.T) {
	by, labels := apmSeriesByName(t, apmTransactionDoc)

	assert.InDelta(t, 4, by["transaction.duration.histogram.count"], 1e-9)
	assert.InDelta(t, 115900, by["transaction.duration.histogram.sum"], 1e-9)
	assert.InDelta(t, 115900.0/4, by["transaction.duration.histogram.avg"], 1e-9)

	assert.InDelta(t, by["transaction.duration.summary.sum"], by["transaction.duration.histogram.sum"], 1e-9,
		"our weighted sum must agree with the summary Elastic wrote alongside it")
	assert.InDelta(t, by["transaction.duration.summary.value_count"], by["transaction.duration.histogram.count"], 1e-9)

	// Identity is what makes latency actionable: which service, which route.
	assert.Equal(t, "preprod_au_govd-ehq_fe", labels["service.name"])
	assert.Equal(t, "PATCH unknown route", labels["transaction.name"])
	assert.Equal(t, "HTTP 2xx", labels["transaction.result"])
}

// event.success_count is a real APM measurement that sat inside a branch skipped
// wholesale as metadata, so it was dropped with event.duration and event.ingested.
func TestAPM_EventBranchKeepsItsMeasurements(t *testing.T) {
	by, labels := apmSeriesByName(t, apmTransactionDoc)

	assert.InDelta(t, 4, by["event.success_count.sum"], 1e-9,
		"successful transactions are a measurement, not document metadata")
	assert.InDelta(t, 4, by["event.success_count.value_count"], 1e-9)
	assert.Equal(t, "success", labels["event.outcome"], "a low-cardinality string is a useful label")
}

// event.ingested is unique per document. As a label it would put every document in
// its own series, which is why the branch was being skipped by name. Dropping it by
// VALUE keeps the branch and its measurements.
func TestAPM_TimestampStringsAreNotLabels(t *testing.T) {
	_, labels := apmSeriesByName(t, apmTransactionDoc)
	for _, k := range []string{"event.ingested", "@timestamp"} {
		_, present := labels[k]
		assert.False(t, present, "%q is unique per document and must not become a label", k)
	}
}

func TestESLooksLikeTimestamp(t *testing.T) {
	for _, ts := range []string{
		"2025-04-30T17:30:33Z", "2026-08-26T13:41:25.762Z", "2025-04-30T17:30:33+10:00",
	} {
		assert.True(t, esLooksLikeTimestamp(ts), "%q is an instant", ts)
	}
	for _, notTs := range []string{
		"preprod_au_govd-ehq_fe", "PATCH unknown route", "8.12.2", "HTTP 2xx",
		"dep.example:443", "", "nodejs",
	} {
		assert.False(t, esLooksLikeTimestamp(notTs), "%q is not an instant", notTs)
	}
}

// _doc_count is Elasticsearch bookkeeping on aggregated documents, and APM rollups
// carry one per bucket. It is not a measurement anyone asked for.
func TestAPM_ElasticsearchInternalsAreNotMetrics(t *testing.T) {
	for _, body := range []string{apmTransactionDoc, apmServiceDestinationDoc} {
		by, _ := apmSeriesByName(t, body)
		_, present := by["_doc_count"]
		assert.False(t, present, "_doc_count is bookkeeping, not a metric")
	}
}

// The second APM shape carries no histogram: a plain count and a summed duration.
func TestAPM_ServiceDestinationShape(t *testing.T) {
	by, labels := apmSeriesByName(t, apmServiceDestinationDoc)

	assert.InDelta(t, 16, by["span.destination.service.response_time.count"], 1e-9)
	assert.InDelta(t, 3001262, by["span.destination.service.response_time.sum.us"], 1e-9)

	// Caller and callee both need to be identifiable for a dependency metric.
	assert.Equal(t, "preprod_au_govd-ehq_fe", labels["service.name"])
	assert.Equal(t, "dep.example:443", labels["span.destination.service.resource"])
	assert.Equal(t, "GET dep.example", labels["span.name"])
}

// Guard the label set against growth: a per-document-unique label is the cardinality
// failure mode, so the set must stay small and stable.
func TestAPM_LabelSetStaysBounded(t *testing.T) {
	_, labels := apmSeriesByName(t, apmTransactionDoc)
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	assert.LessOrEqual(t, len(keys), 20, "label set should identify the series, not describe the document: %v", keys)
}

// data_stream.dataset names which dataset a series came from — the question the
// whole discovery path exists to answer — and it is bounded (64 datasets on the
// estate this document came from). It was dropped as metadata along with the rest
// of the branch; agent.* stays dropped because agent.id and agent.ephemeral_id are
// per-agent and per-restart, which is the cardinality failure a label must avoid.
func TestAPM_DatasetIdentityIsKeptAsLabel(t *testing.T) {
	_, labels := apmSeriesByName(t, apmServiceDestinationDoc)
	assert.Equal(t, "apm.service_destination.1m", labels["data_stream.dataset"],
		"a series should say which dataset produced it")
	assert.Equal(t, "gd-ehq-non-prod", labels["data_stream.namespace"])
}
