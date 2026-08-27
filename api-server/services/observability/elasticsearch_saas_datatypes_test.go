package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/config"
)

func withGenericFallback(t *testing.T) {
	t.Helper()
	prev := config.Config.FeatureESMetricsGenericFallbackEnabled
	config.Config.FeatureESMetricsGenericFallbackEnabled = true
	t.Cleanup(func() { config.Config.FeatureESMetricsGenericFallbackEnabled = prev })
}

// An Elasticsearch `histogram` field is two parallel arrays that only mean anything
// together. The walk skipped arrays entirely, so a deployment storing latency this way
// — every APM installation does — produced no metrics at all.
func TestESHistogramValues(t *testing.T) {
	// 10ms x2, 20ms x3, 50ms x1  ->  6 observations, weighted mean 25ms
	h, ok := esHistogramValues(map[string]any{
		"values": []any{10.0, 20.0, 50.0},
		"counts": []any{2.0, 3.0, 1.0},
	})
	require.True(t, ok)
	assert.InDelta(t, 6.0, h["count"], 1e-9)
	assert.InDelta(t, 130.0, h["sum"], 1e-9)
	assert.InDelta(t, 130.0/6.0, h["avg"], 1e-9)

	// A histogram that observed nothing is a real answer, not an average of zero.
	zero, ok := esHistogramValues(map[string]any{"values": []any{10.0}, "counts": []any{0.0}})
	require.True(t, ok)
	assert.InDelta(t, 0.0, zero["count"], 1e-9)
	_, hasAvg := zero["avg"]
	assert.False(t, hasAvg, "no observations means no average to report")

	// Anything that is not the paired-array form is left to the normal walk.
	for _, notHist := range []map[string]any{
		{"values": []any{1.0, 2.0}, "counts": []any{1.0}}, // lengths disagree
		{"values": []any{1.0}},                            // counts missing
		{"used": 4.0, "free": 8.0},                        // ordinary nested object
		{"values": []any{}, "counts": []any{}},            // empty
	} {
		_, ok := esHistogramValues(notHist)
		assert.False(t, ok, "must not be read as a histogram: %v", notHist)
	}
}

func TestESNumericArrayStats(t *testing.T) {
	s := esNumericArrayStats([]any{4.0, 1.0, 7.0, 2.0})
	assert.InDelta(t, 4.0, s["count"], 1e-9)
	assert.InDelta(t, 14.0, s["sum"], 1e-9)
	assert.InDelta(t, 1.0, s["min"], 1e-9)
	assert.InDelta(t, 7.0, s["max"], 1e-9)
	assert.InDelta(t, 3.5, s["avg"], 1e-9)

	// Tags, IPs and the like are not measurements and must contribute nothing.
	assert.Nil(t, esNumericArrayStats([]any{"preprod", "au"}))
	assert.Nil(t, esNumericArrayStats([]any{}))
}

// An APM aggregated-metrics document: latency as a histogram, plus ordinary scalars
// and identity fields. Nothing about this shape is registered anywhere, so it is
// exactly the "format we have not met" case.
func TestParseESMetricsHits_APMTransactionShape(t *testing.T) {
	withGenericFallback(t)

	body := `{"hits":{"total":{"value":5280},"hits":[
	 {"_index":".ds-metrics-apm.transaction.1m-prod-2026.08.20-000004",
	  "_source":{
	   "@timestamp":"2026-08-27T12:00:00.000Z",
	   "data_stream":{"type":"metrics","dataset":"apm.transaction.1m","namespace":"prod"},
	   "service":{"name":"ehq-api","environment":"preprod"},
	   "transaction":{"name":"GET /health","type":"request",
	     "duration":{"histogram":{"values":[100.0,500.0,2000.0],"counts":[10.0,4.0,1.0]}}},
	   "event":{"outcome":"success"},
	   "_doc_count":15}}]}}`

	res, stats, err := parseESMetricsHitsWithStats([]byte(body), 0)
	require.NoError(t, err)
	require.NotEmpty(t, res, "an APM document must produce metrics, not silence")
	assert.Zero(t, stats.DroppedNoValue)

	by := map[string]float64{}
	var labels map[string]string
	for _, r := range res {
		by[r.Metric["__name__"]] = r.Values[0]
		labels = r.Metric
	}

	// The latency histogram is the point of the document.
	assert.InDelta(t, 15.0, by["transaction.duration.histogram.count"], 1e-9)
	assert.InDelta(t, (100*10+500*4+2000*1)/15.0, by["transaction.duration.histogram.avg"], 1e-9)

	// Identity survives as labels, so the caller can tell which service and route.
	assert.Equal(t, "ehq-api", labels["service.name"])
	assert.Equal(t, "GET /health", labels["transaction.name"])
}

// Regression: an ordinary nested object that happens to sit next to arrays elsewhere
// must still be walked normally rather than mistaken for a histogram.
func TestParseESMetricsHits_ArraysDoNotDisturbNormalNesting(t *testing.T) {
	withGenericFallback(t)

	body := `{"hits":{"total":{"value":1},"hits":[
	 {"_index":".ds-metrics-system.memory-prod-2026.08.20-000001",
	  "_source":{"@timestamp":"2026-08-27T12:00:00.000Z",
	   "host":{"name":"node-1","ip":["10.0.0.1","10.0.0.2"]},
	   "system":{"memory":{"used":{"bytes":4096.0},"free":{"bytes":8192.0}}},
	   "load":[1.5,2.5]}}]}}`

	res, _, err := parseESMetricsHitsWithStats([]byte(body), 0)
	require.NoError(t, err)

	by := map[string]float64{}
	var labels map[string]string
	for _, r := range res {
		by[r.Metric["__name__"]] = r.Values[0]
		labels = r.Metric
	}
	assert.InDelta(t, 4096.0, by["system.memory.used.bytes"], 1e-9)
	assert.InDelta(t, 8192.0, by["system.memory.free.bytes"], 1e-9)
	assert.InDelta(t, 2.0, by["load.avg"], 1e-9, "a numeric array is summarised")
	assert.Equal(t, "node-1", labels["host.name"])
	// host.ip is a string array: not a measurement, contributes nothing.
	_, hasIP := by["host.ip.count"]
	assert.False(t, hasIP)
}

// genericNumericLeafSeries runs once per document — 10,000 times on a full response —
// so its recursive half is a package-level function rather than a closure, which
// cannot be stack allocated. This pins the allocation count against a realistic
// document so a return to the closure form is visible.
func BenchmarkGenericNumericLeafSeries(b *testing.B) {
	src := map[string]any{
		"@timestamp":  "2026-08-27T12:00:00.000Z",
		"data_stream": map[string]any{"dataset": "apm.transaction.1m"},
		"service":     map[string]any{"name": "ehq-api", "environment": "preprod"},
		"host":        map[string]any{"name": "node-1", "ip": []any{"10.0.0.1"}},
		"transaction": map[string]any{
			"name": "GET /health", "type": "request",
			"duration": map[string]any{"histogram": map[string]any{
				"values": []any{100.0, 500.0, 2000.0},
				"counts": []any{10.0, 4.0, 1.0}}}},
		"system": map[string]any{"memory": map[string]any{
			"used": map[string]any{"bytes": 4096.0},
			"free": map[string]any{"bytes": 8192.0}}},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		genericNumericLeafSeries(src)
	}
}

// The document root drops branches that describe the document rather than measure
// anything; nested paths of the same name are real and must survive.
func TestWalkGenericLeaves_RootSkipIsRootOnly(t *testing.T) {
	labels := map[string]string{}
	values := map[string]float64{}
	walkGenericLeaves(map[string]any{
		"event": map[string]any{"duration": 123.0}, // root metadata: dropped
		"system": map[string]any{"event": map[string]any{ // nested "event": kept
			"count": 7.0}},
	}, "", labels, values)

	_, hasRootEvent := values["event.duration"]
	assert.False(t, hasRootEvent, "root metadata branches are dropped")
	assert.InDelta(t, 7.0, values["system.event.count"], 1e-9, "the same name nested is a real measurement")
}

// esNumericArrayStats runs per array leaf per document. It computes in a single pass
// with no temporary slice, so the only allocation is the result map itself; a
// non-numeric array (tags, ip lists) allocates nothing at all, which is the common
// case on documents carrying both.
func BenchmarkESNumericArrayStats(b *testing.B) {
	numeric := []any{4.0, 1.0, 7.0, 2.0, 9.0, 3.0, 5.0, 8.0}
	strings := []any{"preprod", "au", "gd-ehq"}

	b.Run("numeric", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			esNumericArrayStats(numeric)
		}
	})
	b.Run("non-numeric", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			esNumericArrayStats(strings)
		}
	})
}
