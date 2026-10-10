package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"nudgebee/services/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cloudwatchStreamDoc is one document in the layout a CloudWatch metric stream lands in
// through Firehose and the Elastic Serverless Forwarder: a flat metricset copy plus a
// per-service copy, the namespace as an array, and a forwarder envelope. Resource names
// are synthetic.
const cloudwatchStreamDoc = `{
  "@timestamp": "2026-10-06T16:45:03.232Z",
  "@version": "1",
  "aws": {"s3": {"bucket": {"name": "example-metrics-bucket"}}},
  "cloud": {"provider": "aws", "region": "ap-southeast-2"},
  "data_stream": {"dataset": "aws.cloudwatch_metrics", "namespace": "example-non-prod", "type": "metrics"},
  "tags": ["forwarded"],
  "metricset": {
    "RDS": {"metric_name": "CPUUtilization", "namespace": ["AWS","RDS"], "service_name": "RDS",
            "dimensions": {"DBClusterIdentifier": "example-db-cluster"}, "unit": "Percent",
            "timestamp": "2026-10-06T16:41:00.000Z", "value": {"count": 1, "max": 9.6, "min": 9.6, "sum": 9.6}},
    "metric_name": "CPUUtilization", "namespace": ["AWS","RDS"],
    "dimensions": {"DBClusterIdentifier": "example-db-cluster"}, "unit": "Percent",
    "timestamp": "2026-10-06T16:41:00.000Z", "value": {"count": 1, "max": 9.6, "min": 9.6, "sum": 9.6}
  }
}`

// b0680e8eQuery is the shape of query that matched nothing on the customer's index: a
// namespace written as "AWS/RDS" against an array, and dimension paths that do not exist.
const b0680e8eQuery = `{"query":{"bool":{"filter":[
  {"term":{"metricset.namespace":"AWS/RDS"}},
  {"wildcard":{"metricset.dimensions.DBInstanceIdentifier":{"value":"example-db-*"}}},
  {"terms":{"metricset.metric_name":["CPUUtilization","DatabaseConnections"]}},
  {"exists":{"field":"metricset.value"}},
  {"range":{"@timestamp":{"gte":"now-1h","format":"strict_date_optional_time"}}}
]}}}`

func TestESQueryLiterals_CollectsValuesNotFieldsOrOptions(t *testing.T) {
	var q map[string]any
	require.NoError(t, json.Unmarshal([]byte(b0680e8eQuery), &q))

	got := esQueryLiterals(q)

	assert.ElementsMatch(t, []string{"AWS/RDS", "example-db-*", "CPUUtilization", "DatabaseConnections"}, got.Literals)
	assert.Empty(t, got.Raw)
	for _, v := range got.Literals {
		assert.NotContains(t, v, "metricset.", "field names are keys, never values")
		assert.NotEqual(t, "now-1h", v, "range bounds are not field values")
	}
}

func TestESQueryLiterals_NumbersBooleansAndQueryText(t *testing.T) {
	var q map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"bool":{"filter":[
	  {"term":{"cloud.account.id":123456789012}},
	  {"term":{"http.response.status_code":500.5}},
	  {"term":{"event.success":false}},
	  {"query_string":{"query":"status:500 AND host:foo","default_field":"*"}},
	  {"nested":{"path":"spans","score_mode":"avg","query":{"term":{"spans.name":"checkout"}}}},
	  {"wildcard":{"any.field":"*"}}
	]}}`), &q))

	got := esQueryLiterals(q)

	assert.ElementsMatch(t, []string{"123456789012", "500.5", "false", "checkout"}, got.Literals,
		"numbers and booleans are values; nested path, score_mode and a bare * are not")
	assert.Equal(t, []string{"status:500 AND host:foo"}, got.Raw, "query-string text is kept as written")
	assert.Equal(t, `"123456789012" OR "500.5" OR "false" OR "checkout" OR (status:500 AND host:foo)`,
		esQueryValues{Literals: []string{"123456789012", "500.5", "false", "checkout"}, Raw: got.Raw}.queryString())
}

func TestESQueryStringTerm(t *testing.T) {
	assert.Equal(t, `"AWS/RDS"`, esQueryStringTerm("AWS/RDS"), "plain values are quoted phrases")
	assert.Equal(t, `"say \"hi\""`, esQueryStringTerm(`say "hi"`))
	assert.Equal(t, `example\-db\-*`, esQueryStringTerm("example-db-*"), "wildcards keep * and escape the rest")
	assert.Equal(t, `a\:b\/c*`, esQueryStringTerm("a:b/c*"))
}

// fakeESForSample answers the original query with zero hits, the mapping lookup with an
// empty mapping, and the two sample searches with the given bodies (or a 500 when nil).
func fakeESForSample(t *testing.T, valueSearch, anySearch *string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		bodies = append(bodies, body)
		var reply *string
		switch {
		case strings.Contains(body, `"query_string"`):
			reply = valueSearch
		case strings.Contains(body, `"match_all"`) && strings.Contains(body, `"track_total_hits":false`):
			reply = anySearch
		default:
			empty := `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`
			reply = &empty
		}
		if reply == nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		_, _ = w.Write([]byte(*reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func hitsJSON(docs ...string) string {
	parts := make([]string, 0, len(docs))
	for _, d := range docs {
		parts = append(parts, fmt.Sprintf(`{"_index":".ds-metrics-aws.cloudwatch_metrics-example-000001","_source":%s}`, d))
	}
	return `{"timed_out":false,"hits":{"hits":[` + strings.Join(parts, ",") + `]}}`
}

func enableSample(t *testing.T, on bool) {
	t.Helper()
	prev := config.Config.ESMetricsZeroMatchSampleEnabled
	config.Config.ESMetricsZeroMatchSampleEnabled = on
	t.Cleanup(func() { config.Config.ESMetricsZeroMatchSampleEnabled = prev })
}

func runOneESQuery(t *testing.T, url, dsl string) QueryResult {
	t.Helper()
	out, err := runESMetricsQueries(&ElasticsearchConfig{Url: url}, "metrics-aws.cloudwatch_metrics-example",
		FetchMetricsRequest{
			Request:   map[string]any{"query_type": "dsl", "zero_match_sample": true},
			Queries:   map[string]string{"q": dsl},
			StartTime: 1791301435000, EndTime: 1791305035000,
		})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)
	return out.Results[0]
}

func TestESZeroMatchSample_ValueSearchShowsRealPaths(t *testing.T) {
	enableSample(t, true)
	found := hitsJSON(cloudwatchStreamDoc)
	srv, bodies := fakeESForSample(t, &found, nil)

	r := runOneESQuery(t, srv.URL, b0680e8eQuery)

	require.NotNil(t, r.DocsMatched)
	assert.Zero(t, *r.DocsMatched)
	// The two things the agent got wrong are now on screen as they really are.
	assert.Contains(t, r.Sample, `metricset.namespace: ["AWS","RDS"]`)
	assert.Contains(t, r.Sample, "metricset.dimensions.DBClusterIdentifier: example-db-cluster")
	assert.Contains(t, r.Sample, "metricset.RDS.service_name: RDS")
	assert.Contains(t, r.Note, "contain one or more of the values the query used")
	assert.Contains(t, r.Note, "time window")
	assert.NotContains(t, r.Note, "no documents at all")

	var valueSearch string
	for _, b := range *bodies {
		if strings.Contains(b, "query_string") {
			valueSearch = b
		}
	}
	require.NotEmpty(t, valueSearch, "a value search must run")
	assert.Contains(t, valueSearch, `"default_field":"*"`)
	assert.Contains(t, valueSearch, `example\\-db\\-*`)
	assert.Contains(t, valueSearch, `"range"`, "the value search stays in the query's time window")
	assert.Contains(t, valueSearch, `"size":3`)
	assert.Contains(t, valueSearch, `"timeout"`)
}

func TestESZeroMatchSample_FallsBackToAnyDocumentWithoutTimeFilter(t *testing.T) {
	enableSample(t, true)
	none := `{"hits":{"hits":[]}}`
	any := hitsJSON(cloudwatchStreamDoc)
	srv, bodies := fakeESForSample(t, &none, &any)

	r := runOneESQuery(t, srv.URL, b0680e8eQuery)

	assert.Contains(t, r.Sample, "metricset.metric_name: CPUUtilization")
	assert.Contains(t, r.Note, "has data")
	assert.Contains(t, r.Note, "No document in the time window contains")
	assert.NotContains(t, r.Note, "no documents at all")
	for _, b := range *bodies {
		if strings.Contains(b, `"match_all"`) && strings.Contains(b, `"track_total_hits":false`) {
			assert.NotContains(t, b, `"range"`, "the fallback must not filter on time")
		}
	}
}

func TestESZeroMatchSample_EmptyIndexIsTheOnlyAbsence(t *testing.T) {
	enableSample(t, true)
	none := `{"hits":{"hits":[]}}`
	srv, _ := fakeESForSample(t, &none, &none)

	r := runOneESQuery(t, srv.URL, b0680e8eQuery)

	assert.Empty(t, r.Sample)
	assert.Contains(t, r.Note, "has no documents at all")
}

func TestESZeroMatchSample_FailedReadIsNotAbsence(t *testing.T) {
	enableSample(t, true)
	srv, _ := fakeESForSample(t, nil, nil)

	r := runOneESQuery(t, srv.URL, b0680e8eQuery)

	assert.Empty(t, r.Sample)
	assert.Contains(t, r.Note, "did not run")
	assert.NotContains(t, r.Note, "no documents at all")
}

func TestESZeroMatchSample_ValueSearchFailureStillSamples(t *testing.T) {
	enableSample(t, true)
	any := hitsJSON(cloudwatchStreamDoc)
	srv, _ := fakeESForSample(t, nil, &any)

	r := runOneESQuery(t, srv.URL, b0680e8eQuery)

	assert.NotEmpty(t, r.Sample)
	assert.Contains(t, r.Note, "did not run", "a failed value search is reported, not read as no match")
	assert.Contains(t, r.Note, "has data")
}

func TestESZeroMatchSample_OffSwitchAndBatch(t *testing.T) {
	found := hitsJSON(cloudwatchStreamDoc)

	t.Run("disabled", func(t *testing.T) {
		enableSample(t, false)
		srv, bodies := fakeESForSample(t, &found, &found)
		r := runOneESQuery(t, srv.URL, b0680e8eQuery)
		assert.Empty(t, r.Sample)
		for _, b := range *bodies {
			assert.NotContains(t, b, `"track_total_hits":false`, "no sample search when switched off")
		}
	})

	t.Run("batch callers", func(t *testing.T) {
		enableSample(t, true)
		srv, bodies := fakeESForSample(t, &found, &found)
		out, err := runESMetricsQueries(&ElasticsearchConfig{Url: srv.URL}, "metrics-x",
			FetchMetricsRequest{
				Request: map[string]any{"query_type": "dsl", "batch": true, "zero_match_sample": true},
				Queries: map[string]string{"q": b0680e8eQuery},
			})
		require.NoError(t, err)
		assert.Empty(t, out.Results[0].Sample)
		for _, b := range *bodies {
			assert.NotContains(t, b, `"track_total_hits":false`)
		}
	})
}

func TestESZeroMatchSample_NotRunWhenSomethingMatched(t *testing.T) {
	enableSample(t, true)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":".ds-metrics-aws.cloudwatch_metrics-x-000001","_source":` + cloudwatchStreamDoc + `}]}}`))
	}))
	defer srv.Close()

	r := runOneESQuery(t, srv.URL, b0680e8eQuery)

	assert.Empty(t, r.Sample)
	for _, b := range bodies {
		assert.NotContains(t, b, `"track_total_hits":false`)
	}
}

func TestRenderESSample_BudgetKeepsMatchingFieldsFirst(t *testing.T) {
	src := map[string]any{"wanted": "CPUUtilization"}
	for i := 0; i < 300; i++ {
		src[fmt.Sprintf("aaa.envelope.field_%03d", i)] = strings.Repeat("x", 40)
	}
	out := renderESSample([]esSampleHit{{Index: "i", Source: src}, {Index: "i", Source: src}}, []string{"CPUUtilization"})

	assert.LessOrEqual(t, len(out), esSampleBudget)
	assert.Contains(t, out, "wanted: CPUUtilization", "the matching line survives the cut although it sorts last")
	assert.Contains(t, out, "more field(s) not shown")
}

func TestRenderESSample_SecondDocumentShowsOnlyDifferences(t *testing.T) {
	a := map[string]any{"env": "shared", "metric": "CPUUtilization"}
	b := map[string]any{"env": "shared", "metric": "FreeableMemory"}
	out := renderESSample([]esSampleHit{{Index: "i", Source: a}, {Index: "i", Source: b}}, nil)

	assert.Equal(t, 1, strings.Count(out, "env: shared"))
	assert.Contains(t, out, "metric: FreeableMemory")
}

func TestFlattenESSource_KeepsArraysWholeAndCutsLongValues(t *testing.T) {
	lines := flattenESSource(map[string]any{
		"a": map[string]any{"ns": []any{"AWS", "RDS"}, "empty": map[string]any{}},
		"m": strings.Repeat("y", 200),
	})
	assert.Contains(t, lines, `a.ns: ["AWS","RDS"]`)
	assert.Contains(t, lines, `a.empty: {}`)
	for _, l := range lines {
		assert.LessOrEqual(t, len(l), len("m: ")+esSampleValueLen+len("…"))
	}
}

func TestESZeroMatchSample_NoWindowIsNotClaimed(t *testing.T) {
	enableSample(t, true)
	found := hitsJSON(cloudwatchStreamDoc)
	srv, _ := fakeESForSample(t, &found, nil)

	out, err := runESMetricsQueries(&ElasticsearchConfig{Url: srv.URL}, "metrics-x",
		FetchMetricsRequest{Request: map[string]any{"query_type": "dsl", "zero_match_sample": true}, Queries: map[string]string{"q": b0680e8eQuery}})
	require.NoError(t, err)

	assert.Contains(t, out.Results[0].Note, "the query had no time window")
}

func TestESZeroMatchSample_CappedPerRequest(t *testing.T) {
	enableSample(t, true)
	found := hitsJSON(cloudwatchStreamDoc)
	srv, bodies := fakeESForSample(t, &found, &found)

	out, err := runESMetricsQueries(&ElasticsearchConfig{Url: srv.URL}, "metrics-x", FetchMetricsRequest{
		Request: map[string]any{"query_type": "dsl", "zero_match_sample": true},
		Queries: map[string]string{"a": b0680e8eQuery, "b": b0680e8eQuery, "c": b0680e8eQuery, "d": b0680e8eQuery},
	})
	require.NoError(t, err)

	withSample := 0
	for _, r := range out.Results {
		if r.Sample != "" {
			withSample++
		} else {
			assert.Contains(t, r.Note, "earlier queries in this request already returned them")
		}
	}
	assert.Equal(t, esSampleMaxPerRequest, withSample)
	searches := 0
	for _, b := range *bodies {
		if strings.Contains(b, `"track_total_hits":false`) {
			searches++
		}
	}
	assert.Equal(t, esSampleMaxPerRequest, searches, "one value search per sampled query, none for the rest")
}

func TestFlattenESSource_DoesNotSplitMultiByteCharacters(t *testing.T) {
	lines := flattenESSource(map[string]any{"m": strings.Repeat("a", esSampleValueLen-1) + "é" + "tail"})
	require.Len(t, lines, 1)
	assert.True(t, utf8.ValidString(lines[0]), "cut inside a character: %q", lines[0])
}

// Pages, dashboard panels and playbook actions send the same request without asking for
// a sample. A miss there must cost no extra searches.
func TestESZeroMatchSample_OnlyWhenRequested(t *testing.T) {
	enableSample(t, true)
	found := hitsJSON(cloudwatchStreamDoc)
	srv, bodies := fakeESForSample(t, &found, &found)

	out, err := runESMetricsQueries(&ElasticsearchConfig{Url: srv.URL}, "metrics-x", FetchMetricsRequest{
		Request: map[string]any{"query_type": "dsl", "metric_name": "metrics-x"},
		Queries: map[string]string{"q": b0680e8eQuery},
	})
	require.NoError(t, err)

	assert.Empty(t, out.Results[0].Sample)
	for _, b := range *bodies {
		assert.NotContains(t, b, `"track_total_hits":false`, "no sample search for a caller that did not ask")
	}
}

func TestESSampleLineHasValue_WildcardsAndCase(t *testing.T) {
	assert.True(t, esSampleLineHasValue("metricset.metric_name: CPUUtilization", []string{"*cpuutil*"}))
	assert.True(t, esSampleLineHasValue("cloud.service: aws/rds", []string{"AWS/RDS"}))
	assert.False(t, esSampleLineHasValue("metricset.metric_name: CPUUtilization", []string{"*"}))
}

func TestESZeroMatchSample_FallbackSortsByBothTimeFields(t *testing.T) {
	enableSample(t, true)
	none := `{"hits":{"hits":[]}}`
	any := hitsJSON(cloudwatchStreamDoc)
	srv, bodies := fakeESForSample(t, &none, &any)

	runOneESQuery(t, srv.URL, b0680e8eQuery)

	var fallback string
	for _, b := range *bodies {
		if strings.Contains(b, `"match_all"`) && strings.Contains(b, `"track_total_hits":false`) {
			fallback = b
		}
	}
	require.NotEmpty(t, fallback)
	assert.Contains(t, fallback, `"@timestamp":{"order":"desc","unmapped_type":"date"}`)
	assert.Contains(t, fallback, `"time":{"order":"desc","unmapped_type":"date"}`)
}
