package observability

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discoverServer(t *testing.T, reply string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func runDiscover(t *testing.T, url, mode, dataset string) QueryResult {
	t.Helper()
	req := map[string]any{"query_type": "dsl", esDiscoverRequestKey: mode}
	if dataset != "" {
		req["dataset"] = dataset
	}
	out, err := runESMetricsQueries(&ElasticsearchConfig{Url: url}, "metrics-*",
		FetchMetricsRequest{Request: req, Queries: map[string]string{"discover": "{}"}, StartTime: 1791301435000, EndTime: 1791305035000})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)
	return out.Results[0]
}

func TestESDiscoverDatasets_ListsDatasetsWithExampleFields(t *testing.T) {
	reply := `{"timed_out":false,"aggregations":{
	  "by_dataset":{"sum_other_doc_count":0,"buckets":[
	    {"key":"aws.cloudwatch_metrics","doc_count":7560,"doc":{"hits":{"hits":[{"_index":"x","_source":` + cloudwatchStreamDoc + `}]}}},
	    {"key":"kubernetes.pod","doc_count":120,"doc":{"hits":{"hits":[{"_index":"y","_source":{"@timestamp":"2026-10-08T00:00:00Z","kubernetes":{"namespace":"demo","pod":{"name":"api-1","cpu":{"usage":{"nanocores":12345}}}}}}]}}}
	  ]},
	  "by_index":{"buckets":[]}}}`
	srv, bodies := discoverServer(t, reply)

	r := runDiscover(t, srv.URL, "datasets", "")

	assert.Nil(t, r.Error)
	assert.Equal(t, esDiscoverRequestKey, r.QueryKey)
	assert.Contains(t, r.Sample, "aws.cloudwatch_metrics: 7560 docs")
	assert.Contains(t, r.Sample, "kubernetes.pod: 120 docs")
	assert.Contains(t, r.Sample, "kubernetes.pod.cpu.usage.nanocores: 12345", "numeric fields are shown as examples")
	assert.Contains(t, r.Sample, "kubernetes.namespace: demo", "text fields under the same root follow")
	assert.Contains(t, r.Note, "data_stream.dataset")
	require.Len(t, *bodies, 1, "one bounded request")
	assert.Contains(t, (*bodies)[0], `"size":100`)
	assert.Contains(t, (*bodies)[0], `"timeout"`)
	assert.NotContains(t, (*bodies)[0], "terminate_after", "an aggregation must not stop early")
}

func TestESDiscoverDatasets_ReportsWhatIsMissing(t *testing.T) {
	t.Run("more datasets than listed", func(t *testing.T) {
		srv, _ := discoverServer(t, `{"aggregations":{"by_dataset":{"sum_other_doc_count":42,"buckets":[{"key":"a","doc_count":1,"doc":{"hits":{"hits":[]}}}]},"by_index":{"buckets":[]}}}`)
		r := runDiscover(t, srv.URL, "datasets", "")
		assert.Contains(t, r.Note, "More exist than are listed: 42 documents")
	})
	t.Run("timed out with nothing", func(t *testing.T) {
		srv, _ := discoverServer(t, `{"timed_out":true,"aggregations":{"by_dataset":{"buckets":[]},"by_index":{"buckets":[]}}}`)
		r := runDiscover(t, srv.URL, "datasets", "")
		assert.Contains(t, r.Note, "timed out")
		assert.NotContains(t, r.Note, "has no documents", "a timeout is not an empty index")
	})
	t.Run("empty index", func(t *testing.T) {
		srv, _ := discoverServer(t, `{"timed_out":false,"aggregations":{"by_dataset":{"buckets":[]},"by_index":{"buckets":[]}}}`)
		r := runDiscover(t, srv.URL, "datasets", "")
		assert.Contains(t, r.Note, "has no documents")
	})
	t.Run("no data streams falls back to index names", func(t *testing.T) {
		srv, _ := discoverServer(t, `{"aggregations":{"by_dataset":{"buckets":[]},"by_index":{"buckets":[
		  {"key":"remote:.ds-metrics-app-2026.10.08-000001","doc_count":5,"doc":{"hits":{"hits":[]}}},
		  {"key":"remote:.ds-metrics-app-2026.10.08-000002","doc_count":7,"doc":{"hits":{"hits":[]}}}]}}}`)
		r := runDiscover(t, srv.URL, "datasets", "")
		assert.Contains(t, r.Sample, "metrics-app: 12 docs", "backing indices of one stream are folded together")
		assert.Contains(t, r.Note, "no `data_stream.dataset`")
	})
}

func TestESDiscoverSample_ReturnsNewestDocumentsOfOneDataset(t *testing.T) {
	srv, bodies := discoverServer(t, hitsJSON(cloudwatchStreamDoc))

	r := runDiscover(t, srv.URL, "sample", "aws.cloudwatch_metrics")

	assert.Contains(t, r.Sample, "metricset.metric_name: CPUUtilization")
	assert.Contains(t, r.Note, "no time filter")
	require.Len(t, *bodies, 1)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte((*bodies)[0]), &body))
	assert.Contains(t, (*bodies)[0], `"data_stream.dataset":"aws.cloudwatch_metrics"`)
	assert.NotContains(t, (*bodies)[0], `"range"`, "the sample is not limited to a time window")
}

func TestESDiscover_RejectsBadInput(t *testing.T) {
	srv, _ := discoverServer(t, `{}`)
	assert.NotNil(t, runDiscover(t, srv.URL, "sample", "").Error, "sample needs a dataset")
	assert.NotNil(t, runDiscover(t, srv.URL, "everything", "").Error)
}

func TestESExampleFields_NumbersFirstThenTheirSiblings(t *testing.T) {
	var src map[string]any
	require.NoError(t, json.Unmarshal([]byte(cloudwatchStreamDoc), &src))
	got := esExampleFields(src)
	assert.Contains(t, got, "metricset.")
	assert.NotContains(t, got, "@timestamp")
	assert.LessOrEqual(t, strings.Count(got, ", ")+1, esDiscoverFieldsPerDataset)
}

func TestESZeroMatchSample_ExistsOnlyResolvesFieldByName(t *testing.T) {
	enableSample(t, true)
	found := hitsJSON(cloudwatchStreamDoc)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body := string(b)
		if r.Method == http.MethodGet { // field list
			_, _ = w.Write([]byte(`{"idx":{"mappings":{"properties":{"metricset":{"properties":{"dimensions":{"properties":{"DBInstanceIdentifier":{"type":"keyword"}}}}}}}}}`))
			return
		}
		bodies = append(bodies, body)
		if strings.Contains(body, `"track_total_hits":false`) {
			_, _ = w.Write([]byte(found))
			return
		}
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`))
	}))
	defer srv.Close()

	r := runOneESQuery(t, srv.URL, `{"query":{"bool":{"filter":[{"exists":{"field":"DBInstanceIdentifier"}}]}}}`)

	assert.Contains(t, r.Note, "`DBInstanceIdentifier` is not a field in this index")
	assert.Contains(t, r.Note, "metricset.dimensions.DBInstanceIdentifier")
	assert.NotEmpty(t, r.Sample)
	var sampled string
	for _, b := range bodies {
		if strings.Contains(b, `"track_total_hits":false`) {
			sampled = b
		}
	}
	assert.Contains(t, sampled, `"field":"metricset.dimensions.DBInstanceIdentifier"`)
	assert.Contains(t, sampled, "terminate_after", "an unsorted sample still stops each shard early")
}

func TestESResolveFieldName(t *testing.T) {
	fields := []OutputMetricLabels{{Label: "aws.dimensions.DBInstanceIdentifier"}, {Label: "metricset.dimensions.DBInstanceIdentifier.keyword"}, {Label: "x.dbinstanceidentifierx"}}
	exact, m := esResolveFieldName("DBInstanceIdentifier", fields)
	assert.False(t, exact)
	assert.Equal(t, []string{"aws.dimensions.DBInstanceIdentifier", "metricset.dimensions.DBInstanceIdentifier"}, m)
	exact, _ = esResolveFieldName("aws.dimensions.DBInstanceIdentifier", fields)
	assert.True(t, exact, "a path that exists is left alone")
}

func TestESDiscoverSample_MatchesBackingIndicesOfIndexGroupedStreams(t *testing.T) {
	srv, bodies := discoverServer(t, hitsJSON(cloudwatchStreamDoc))
	runDiscover(t, srv.URL, "sample", "metrics-app")
	require.Len(t, *bodies, 1)
	assert.Contains(t, (*bodies)[0], `".ds-metrics-app-*"`, "the listing folds .ds- backing indices into this name")
	assert.Contains(t, (*bodies)[0], `"metrics-app*"`)
	assert.NotContains(t, (*bodies)[0], "terminate_after", "a newest-first sample must see every document")
}

// A skipped remote with track_total_hits:false has no hit count, so readResponse
// cannot flag it; the listing and the sample must not call that index empty.
func TestESDiscover_SkippedRemoteIsNotAnEmptyIndex(t *testing.T) {
	skipped := `"_clusters":{"total":1,"successful":0,"skipped":1,"failed":0,"details":{"prod":{"status":"skipped","failures":[{"reason":{"type":"connect_transport_exception","reason":"connect_exception"}}]}}}`
	t.Run("datasets", func(t *testing.T) {
		srv, _ := discoverServer(t, `{`+skipped+`,"aggregations":{"by_dataset":{"buckets":[]},"by_index":{"buckets":[]}}}`)
		r := runDiscover(t, srv.URL, "datasets", "")
		require.NotNil(t, r.Error)
		assert.Contains(t, *r.Error, "prod (skipped)")
		assert.NotContains(t, r.Note, "has no documents")
	})
	t.Run("sample", func(t *testing.T) {
		srv, _ := discoverServer(t, `{`+skipped+`,"hits":{"hits":[]}}`)
		r := runDiscover(t, srv.URL, "sample", "aws.cloudwatch_metrics")
		require.NotNil(t, r.Error)
		assert.Contains(t, *r.Error, "did not run")
	})
}
