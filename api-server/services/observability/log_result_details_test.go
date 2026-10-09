package observability

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/relay"
	"nudgebee/services/security"
)

func decodeJSONMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m))
	return m
}

// The acceptance question: error lines per pod per hour. Terms on the pod, then an
// hourly date_histogram — one series per pod, one point per hour.
const esPodHourlyAggsDef = `{"by_pod": {"terms": {"field": "kubernetes.pod.name", "size": 50},
	"aggs": {"per_hour": {"date_histogram": {"field": "@timestamp", "fixed_interval": "1h"}}}}}`

const esPodHourlyResponse = `{
	"took": 12, "timed_out": false,
	"hits": {"total": {"value": 1234, "relation": "eq"}, "hits": []},
	"aggregations": {"by_pod": {"buckets": [
		{"key": "web-2", "doc_count": 4, "per_hour": {"buckets": [
			{"key_as_string": "2026-10-09T10:00:00Z", "key": 1791540000000, "doc_count": 1},
			{"key_as_string": "2026-10-09T11:00:00Z", "key": 1791543600000, "doc_count": 3}]}},
		{"key": "api-1", "doc_count": 7, "per_hour": {"buckets": [
			{"key_as_string": "2026-10-09T10:00:00Z", "key": 1791540000000, "doc_count": 5},
			{"key_as_string": "2026-10-09T11:00:00Z", "key": 1791543600000, "doc_count": 2}]}}
	]}}
}`

func TestEsSeriesFromAggregations_Shapes(t *testing.T) {
	const endMillis = int64(1791547200000)

	t.Run("terms then date_histogram: a series per group, a point per bucket", func(t *testing.T) {
		resp := decodeJSONMap(t, esPodHourlyResponse)
		series, _, ok := esSeriesFromAggregations(decodeJSONMap(t, esPodHourlyAggsDef), resp["aggregations"].(map[string]any), endMillis)
		require.True(t, ok)
		require.Len(t, series, 2)
		// Sorted by labels, not by the provider's count order.
		assert.Equal(t, map[string]string{"kubernetes.pod.name": "api-1"}, series[0].Metric)
		assert.Equal(t, []int64{1791540000, 1791543600}, series[0].Timestamps, "epoch seconds")
		assert.Equal(t, []float64{5, 2}, series[0].Values)
		assert.Equal(t, map[string]string{"kubernetes.pod.name": "web-2"}, series[1].Metric)
		assert.Equal(t, []float64{1, 3}, series[1].Values)
	})

	t.Run("date_histogram only: one series with no labels", func(t *testing.T) {
		def := decodeJSONMap(t, `{"per_hour": {"date_histogram": {"field": "@timestamp", "calendar_interval": "1h"}}}`)
		aggs := decodeJSONMap(t, `{"per_hour": {"buckets": [{"key": 1791540000000, "doc_count": 9}, {"key": 1791543600000, "doc_count": 0}]}}`)
		series, _, ok := esSeriesFromAggregations(def, aggs, endMillis)
		require.True(t, ok)
		require.Len(t, series, 1)
		assert.Empty(t, series[0].Metric)
		assert.Equal(t, []float64{9, 0}, series[0].Values)
	})

	t.Run("two terms levels, no histogram: one point at the end of the window", func(t *testing.T) {
		def := decodeJSONMap(t, `{"ns": {"terms": {"field": "kubernetes.namespace"}, "aggs": {"pod": {"terms": {"field": "kubernetes.pod.name"}}}}}`)
		aggs := decodeJSONMap(t, `{"ns": {"buckets": [
			{"key": "prod", "doc_count": 5, "pod": {"buckets": [{"key": "a", "doc_count": 3}, {"key": "b", "doc_count": 2}]}},
			{"key": "dev", "doc_count": 1, "pod": {"buckets": [{"key": "c", "doc_count": 1}]}}]}}`)
		series, _, ok := esSeriesFromAggregations(def, aggs, endMillis)
		require.True(t, ok)
		require.Len(t, series, 3)
		assert.Equal(t, map[string]string{"kubernetes.namespace": "dev", "kubernetes.pod.name": "c"}, series[0].Metric)
		for _, s := range series {
			assert.Equal(t, []int64{endMillis / 1000}, s.Timestamps)
		}
		assert.Equal(t, []float64{1}, series[0].Values)
	})

	t.Run("a terms level that left groups out is reported", func(t *testing.T) {
		def := decodeJSONMap(t, `{"pod": {"terms": {"field": "pod"}}}`)
		aggs := decodeJSONMap(t, `{"pod": {"sum_other_doc_count": 17, "buckets": [{"key": "a", "doc_count": 3}]}}`)
		series, omitted, ok := esSeriesFromAggregations(def, aggs, endMillis)
		require.True(t, ok)
		assert.Len(t, series, 1)
		assert.True(t, omitted)

		r := FetchLogsResult{}
		applyLogQueryDetails(&r, LogQueryDetails{HasCounts: true, Series: series, GroupsOmitted: omitted}, 0, 0, false, false)
		assert.Contains(t, r.SeriesNote, "raise that terms aggregation's size")
	})

	t.Run("numeric terms keys print without a fraction", func(t *testing.T) {
		def := decodeJSONMap(t, `{"status": {"terms": {"field": "http.status"}}}`)
		aggs := decodeJSONMap(t, `{"status": {"buckets": [{"key": 500, "doc_count": 4}]}}`)
		series, _, ok := esSeriesFromAggregations(def, aggs, endMillis)
		require.True(t, ok)
		assert.Equal(t, "500", series[0].Metric["http.status"])
	})

	for name, def := range map[string]string{
		"two aggregations at one level":      `{"a": {"terms": {"field": "x"}}, "b": {"terms": {"field": "y"}}}`,
		"metric sub-aggregation":             `{"a": {"terms": {"field": "x"}, "aggs": {"avg": {"avg": {"field": "latency"}}}}}`,
		"date_histogram that is not last":    `{"h": {"date_histogram": {"field": "@timestamp"}, "aggs": {"p": {"terms": {"field": "pod"}}}}}`,
		"another bucket type":                `{"r": {"range": {"field": "latency", "ranges": [{"to": 10}]}}}`,
		"terms on a script, with no field":   `{"a": {"terms": {"script": "doc['x'].value"}}}`,
		"two terms levels on one field":      `{"a": {"terms": {"field": "x"}, "aggs": {"b": {"terms": {"field": "x"}}}}}`,
		"terms with a second bucket keyword": `{"a": {"terms": {"field": "x"}, "date_histogram": {"field": "@timestamp"}}}`,
	} {
		t.Run("does not fit: "+name, func(t *testing.T) {
			_, _, ok := esSeriesFromAggregations(decodeJSONMap(t, def), map[string]any{}, endMillis)
			assert.False(t, ok)
		})
	}

	t.Run("does not fit: keyed buckets", func(t *testing.T) {
		def := decodeJSONMap(t, `{"h": {"date_histogram": {"field": "@timestamp", "keyed": true}}}`)
		aggs := decodeJSONMap(t, `{"h": {"buckets": {"2026-10-09T10:00:00Z": {"key": 1791540000000, "doc_count": 1}}}}`)
		_, _, ok := esSeriesFromAggregations(def, aggs, endMillis)
		assert.False(t, ok)
	})
}

func TestEsLogQueryDetails(t *testing.T) {
	t.Run("counts-only query", func(t *testing.T) {
		d := esLogQueryDetails([]byte(esPodHourlyResponse), decodeJSONMap(t, esPodHourlyAggsDef), float64(0), 1791547200000, false)
		require.NotNil(t, d.Total)
		assert.Equal(t, int64(1234), *d.Total)
		assert.Equal(t, "eq", d.TotalRelation)
		assert.True(t, d.NoLines)
		assert.True(t, d.HasCounts)
		assert.Len(t, d.Series, 2)
		assert.Nil(t, d.AggregationsRaw, "raw fragments only with include_raw")
		assert.Nil(t, d.TotalRaw)
	})

	t.Run("include_raw carries the provider's fragments verbatim", func(t *testing.T) {
		d := esLogQueryDetails([]byte(esPodHourlyResponse), decodeJSONMap(t, esPodHourlyAggsDef), float64(0), 1791547200000, true)
		assert.JSONEq(t, `{"value": 1234, "relation": "eq"}`, string(d.TotalRaw))
		raw := decodeJSONMap(t, string(d.AggregationsRaw))
		assert.Contains(t, raw, "by_pod")
	})

	t.Run("lower-bound total", func(t *testing.T) {
		d := esLogQueryDetails([]byte(`{"hits": {"total": {"value": 10000, "relation": "gte"}, "hits": []}}`), nil, float64(100), 0, false)
		require.NotNil(t, d.Total)
		assert.Equal(t, "gte", d.TotalRelation)
		assert.False(t, d.NoLines)
		assert.False(t, d.HasCounts)
	})

	t.Run("ES 6 bare-number total is exact", func(t *testing.T) {
		d := esLogQueryDetails([]byte(`{"hits": {"total": 42, "hits": []}}`), nil, nil, 0, false)
		require.NotNil(t, d.Total)
		assert.Equal(t, int64(42), *d.Total)
		assert.Equal(t, "eq", d.TotalRelation)
	})

	t.Run("no total reported", func(t *testing.T) {
		d := esLogQueryDetails([]byte(`{"hits": {"hits": []}}`), nil, nil, 0, true)
		assert.Nil(t, d.Total)
		assert.Nil(t, d.TotalRaw)
	})

	t.Run("counts that do not fit series stay in the raw fragment", func(t *testing.T) {
		def := decodeJSONMap(t, `{"lat": {"percentiles": {"field": "latency"}}}`)
		d := esLogQueryDetails([]byte(`{"hits": {"total": {"value": 3, "relation": "eq"}}, "aggregations": {"lat": {"values": {"50.0": 12}}}}`), def, float64(0), 0, true)
		assert.True(t, d.HasCounts)
		assert.Empty(t, d.Series)
		assert.NotEmpty(t, d.AggregationsRaw)
	})
}

func TestApplyLogQueryDetails_Truncation(t *testing.T) {
	total := func(n int64) *int64 { return &n }
	lines := func(n int) []OutputLog { return make([]OutputLog, n) }

	cases := []struct {
		name      string
		logs      int
		details   LogQueryDetails
		limit     int
		offset    int
		partial   bool
		truncated bool
	}{
		{name: "exact total above returned", logs: 100, details: LogQueryDetails{Total: total(250), TotalRelation: "eq"}, limit: 100, truncated: true},
		{name: "unparseable lines dropped are not a cut", logs: 3, details: LogQueryDetails{Total: total(5), TotalRelation: "eq", Returned: 5}, limit: 100, truncated: false},
		{name: "exact total equals returned", logs: 100, details: LogQueryDetails{Total: total(100), TotalRelation: "eq"}, limit: 100, truncated: false},
		{name: "fallback: returned equals limit may be cut off", logs: 100, limit: 100, truncated: true},
		{name: "fallback: fewer than limit", logs: 40, limit: 100, truncated: false},
		{name: "fallback: more than limit means the provider ignored it (Loki metric query)", logs: 3365, limit: 50, truncated: false},
		{name: "fallback: no limit", logs: 40, limit: 0, truncated: false},
		{name: "counts-only query is never cut off", logs: 0, details: LogQueryDetails{Total: total(1234), NoLines: true}, limit: 100, truncated: false},
		{name: "last page of an offset request is not cut off", logs: 10, details: LogQueryDetails{Total: total(100), TotalRelation: "eq"}, limit: 10, offset: 90, truncated: false},
		{name: "middle page of an offset request is cut off", logs: 10, details: LogQueryDetails{Total: total(100), TotalRelation: "eq"}, limit: 10, offset: 50, truncated: true},
		{name: "partial is cut off", logs: 10, limit: 100, partial: true, truncated: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := FetchLogsResult{Logs: lines(tc.logs)}
			applyLogQueryDetails(&r, tc.details, tc.limit, tc.offset, tc.partial, false)
			assert.Equal(t, tc.truncated, r.Truncated)
			assert.Equal(t, tc.details.Total, r.Total)
		})
	}
}

func TestApplyLogQueryDetails_SeriesAndRaw(t *testing.T) {
	details := LogQueryDetails{
		HasCounts:       true,
		AggregationsRaw: json.RawMessage(`{"lat": {}}`),
		TotalRaw:        json.RawMessage(`3`),
	}

	r := FetchLogsResult{Suggestion: "unknown label name"}
	applyLogQueryDetails(&r, details, 0, 0, false, false)
	assert.Equal(t, "unknown label name", r.Suggestion, "suggestion keeps its meaning; series notes go elsewhere")
	assert.Nil(t, r.AggregationsRaw, "raw fragments only with include_raw")
	assert.Contains(t, r.SeriesNote, "set include_raw")

	r = FetchLogsResult{}
	applyLogQueryDetails(&r, details, 0, 0, false, true)
	assert.JSONEq(t, `{"lat": {}}`, string(r.AggregationsRaw))
	assert.Contains(t, r.SeriesNote, "they are in aggregations_raw")

	// Without the new fields set, the JSON a UI caller gets is today's plus truncated.
	b, err := json.Marshal(FetchLogsResult{Logs: []OutputLog{}, Query: "q", Provider: "ES"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"logs": [], "query": "q", "provider": "ES", "truncated": false}`, string(b))
}

func TestAnnotateLogLabelKinds(t *testing.T) {
	t.Run("Loki: a target the backend does not report is never named", func(t *testing.T) {
		labels := []OutputLogLabel{{Label: "message"}, {Label: "pod"}, {Label: "stream"}}
		discovered := []OutputLogLabel{{Label: "pod"}, {Label: "stream"}}
		annotateLogLabelKinds(labels, map[string]string{"message": "log"}, discovered, &LokiSource{})

		assert.Equal(t, OutputLogLabel{Label: "message", Kind: "alias"}, labels[0])
		assert.Equal(t, "field", labels[1].Kind)
		assert.Equal(t, "field", labels[2].Kind)
	})

	t.Run("Elasticsearch: an alias points at the shipper spelling the index has", func(t *testing.T) {
		labels := []OutputLogLabel{{Label: "message"}, {Label: "pod"}, {Label: "namespace"}, {Label: "kubernetes.pod.name"}, {Label: "log"}}
		discovered := []OutputLogLabel{{Label: "kubernetes.pod.name"}, {Label: "log"}}
		annotateLogLabelKinds(labels, map[string]string{}, discovered, &ElasticSaasSource{})

		assert.Equal(t, "alias", labels[0].Kind)
		assert.Equal(t, "log", labels[0].Field)
		assert.Equal(t, "alias", labels[1].Kind)
		assert.Equal(t, "kubernetes.pod.name", labels[1].Field)
		assert.Equal(t, OutputLogLabel{Label: "namespace", Kind: "alias"}, labels[2], "no spelling of namespace in this index")
		assert.Equal(t, "field", labels[3].Kind)
		assert.Equal(t, "field", labels[4].Kind)
	})

	t.Run("an operator mapping wins over the shipper candidates", func(t *testing.T) {
		labels := []OutputLogLabel{{Label: "pod"}}
		discovered := []OutputLogLabel{{Label: "kubernetes.pod.name"}, {Label: "custom.pod"}}
		annotateLogLabelKinds(labels, map[string]string{"pod": "custom.pod"}, discovered, &ElasticSource{})
		assert.Equal(t, "custom.pod", labels[0].Field)
	})

	t.Run("a name the mapping leaves unchanged is a field", func(t *testing.T) {
		labels := []OutputLogLabel{{Label: "level"}, {Label: "stream"}}
		annotateLogLabelKinds(labels, map[string]string{"level": "level"}, []OutputLogLabel{{Label: "stream"}}, &LokiSource{})
		assert.Equal(t, "field", labels[0].Kind)
	})

	t.Run("nothing reported by the backend: kinds are left unset, not guessed", func(t *testing.T) {
		labels := []OutputLogLabel{{Label: "namespace"}, {Label: "message"}}
		annotateLogLabelKinds(labels, map[string]string{"message": "log"}, nil, &LokiSource{})
		assert.Equal(t, []OutputLogLabel{{Label: "namespace"}, {Label: "message"}}, labels)
	})

	t.Run("fields_only keeps the names a native query understands", func(t *testing.T) {
		got := fieldLabelsOnly([]OutputLogLabel{{Label: "pod", Kind: "alias"}, {Label: "kubernetes.pod.name", Kind: "field"}})
		assert.Equal(t, []string{"kubernetes.pod.name"}, labelNames(got))
	})
}

// The in-cluster source used to overwrite the body's size with the limit, turning an
// aggregation-only query back into a line fetch.
func TestElasticSource_QueryLogsDetailed_KeepsBodySizeAndReportsCounts(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	var sent map[string]any
	patches.ApplyFunc(relay.Execute, func(req relay.RelayExecuteRequest) (map[string]any, error) {
		body := req.Body
		sent = body.ActionParams
		return map[string]any{"data": map[string]any{"data": esPodHourlyResponse}}, nil
	})

	src := &ElasticSource{}
	logs, details, err := src.QueryLogsDetailed(mockRequestContext(), FetchLogRequest{
		AccountId: "acc",
		Query:     `{"size": 0, "query": {"match": {"log": "error"}}, "aggs": ` + esPodHourlyAggsDef + `}`,
		Limit:     500,
		EndTime:   1791547200000,
		Request:   map[string]any{"index": "logs-*"},
	})
	require.NoError(t, err)
	assert.Empty(t, logs)
	assert.Equal(t, float64(0), sent["size"], "the body's size wins over the limit")
	assert.True(t, details.NoLines)
	require.NotNil(t, details.Total)
	assert.Equal(t, int64(1234), *details.Total)
	assert.Len(t, details.Series, 2)

	// Without a size in the body, the limit still applies.
	_, _, err = src.QueryLogsDetailed(mockRequestContext(), FetchLogRequest{
		AccountId: "acc", Query: `{"query": {"match_all": {}}}`, Limit: 500, Request: map[string]any{"index": "logs-*"},
	})
	require.NoError(t, err)
	assert.Equal(t, 500, sent["size"])
}

func TestElasticSaasSource_QueryLogsDetailed_ReportsCounts(t *testing.T) {
	var sentBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &sentBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(esPodHourlyResponse))
	}))
	defer srv.Close()

	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(GetElasticsearchConfig, func(_ *security.RequestContext, _ string) (*ElasticsearchConfig, error) {
		return &ElasticsearchConfig{Url: srv.URL, LogIndex: "logs-*"}, nil
	})

	logs, details, err := (&ElasticSaasSource{}).QueryLogsDetailed(mockRequestContext(), FetchLogRequest{
		AccountId:  "acc",
		Query:      `{"size": 0, "query": {"match": {"log": "error"}}, "aggs": ` + esPodHourlyAggsDef + `}`,
		Limit:      500,
		StartTime:  1791525600000,
		EndTime:    1791547200000,
		IncludeRaw: true,
	})
	require.NoError(t, err)
	assert.Empty(t, logs)
	assert.Equal(t, float64(0), sentBody["size"])
	assert.Contains(t, sentBody, "aggs", "the caller's aggregations reach the cluster")
	assert.True(t, details.NoLines)
	assert.Len(t, details.Series, 2)
	assert.NotEmpty(t, details.AggregationsRaw)
}

func TestCountsQueryMatched(t *testing.T) {
	total := func(n int64) *int64 { return &n }
	series := []Result{{Metric: map[string]string{"pod": "a"}}}
	cases := []struct {
		name    string
		details LogQueryDetails
		want    bool
	}{
		{name: "line query", details: LogQueryDetails{Total: total(5)}, want: false},
		{name: "counts query with a positive total", details: LogQueryDetails{NoLines: true, Total: total(5)}, want: true},
		{name: "counts query that matched nothing keeps the diagnosis", details: LogQueryDetails{HasCounts: true, Total: total(0)}, want: false},
		{name: "no total reported, but series came back", details: LogQueryDetails{HasCounts: true, Series: series}, want: true},
		{name: "no total and no series: no evidence of a match", details: LogQueryDetails{NoLines: true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, countsQueryMatched(tc.details))
		})
	}
}
