package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- buildMetricDQL ----

func TestBuildMetricDQL_PlainSelector(t *testing.T) {
	s := &DynatraceMetricSource{}
	dql, err := s.buildMetricDQL("builtin:host.cpu.usage", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", "1m", nil, nil)
	assert.NoError(t, err)
	assert.Contains(t, dql, "timeseries")
	assert.Contains(t, dql, "avg(`builtin:host.cpu.usage`)")
	assert.Contains(t, dql, "from:")
	assert.Contains(t, dql, "to:")
	assert.Contains(t, dql, "interval:")
}

func TestBuildMetricDQL_ContainsAlias(t *testing.T) {
	s := &DynatraceMetricSource{}
	dql, err := s.buildMetricDQL("my.metric", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z", "5m", nil, nil)
	assert.NoError(t, err)
	assert.Contains(t, dql, "val = avg(`my.metric`)")
}

func TestBuildMetricDQL_PassthroughTimeseries(t *testing.T) {
	s := &DynatraceMetricSource{}
	raw := `timeseries val = avg(builtin:host.cpu.usage), from: "now-1h", to: "now", interval: 5m`
	dql, err := s.buildMetricDQL(raw, "ignored-from", "ignored-to", "ignored-resolution", nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, raw, dql, "pre-formed timeseries query without placeholders should pass through unchanged")
}

func TestBuildMetricDQL_PlaceholderSubstitution(t *testing.T) {
	s := &DynatraceMetricSource{}
	template := `timeseries val = avg(` + "`builtin:host.cpu.usage`" + `), from: "` + dtFromPlaceholder + `", to: "` + dtToPlaceholder + `", interval: ` + dtIntervalPlaceholder
	from := "2024-06-01T00:00:00Z"
	to := "2024-06-01T01:00:00Z"
	interval := "5m"
	dql, err := s.buildMetricDQL(template, from, to, interval, nil, nil)
	assert.NoError(t, err)
	assert.Contains(t, dql, from)
	assert.Contains(t, dql, to)
	assert.Contains(t, dql, interval)
	assert.NotContains(t, dql, dtFromPlaceholder)
	assert.NotContains(t, dql, dtToPlaceholder)
	assert.NotContains(t, dql, dtIntervalPlaceholder)
}

func TestBuildMetricDQL_LeadingWhitespace(t *testing.T) {
	// TrimSpace check — leading whitespace should still detect "timeseries"
	s := &DynatraceMetricSource{}
	raw := "   timeseries val = avg(x)"
	dql, err := s.buildMetricDQL(raw, "f", "t", "1m", nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, raw, dql)
}

func TestBuildMetricDQL_ContainsFromTo(t *testing.T) {
	s := &DynatraceMetricSource{}
	from := "2024-06-01T00:00:00Z"
	to := "2024-06-01T01:00:00Z"
	dql, err := s.buildMetricDQL("my.metric", from, to, "2m", nil, nil)
	assert.NoError(t, err)
	assert.Contains(t, dql, from)
	assert.Contains(t, dql, to)
}

// ---- buildResolution ----

func TestBuildResolution_Zero(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "1m", s.buildResolution(0))
}

func TestBuildResolution_Negative(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "1m", s.buildResolution(-1))
}

func TestBuildResolution_LessThan60(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "1m", s.buildResolution(30))
}

func TestBuildResolution_Exactly60(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "1m", s.buildResolution(60))
}

func TestBuildResolution_120Seconds(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "2m", s.buildResolution(120))
}

func TestBuildResolution_3600Seconds(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "1h", s.buildResolution(3600))
}

func TestBuildResolution_7200Seconds(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Equal(t, "2h", s.buildResolution(7200))
}

func TestBuildResolution_5400Seconds(t *testing.T) {
	// 5400s = 90 min → 1h (90/60 = 1 — integer division truncates)
	s := &DynatraceMetricSource{}
	assert.Equal(t, "1h", s.buildResolution(5400))
}

// ---- extractTimestamps ----

func TestExtractTimestamps_NilResult(t *testing.T) {
	s := &DynatraceMetricSource{}
	assert.Nil(t, s.extractTimestamps(nil))
}

func TestExtractTimestamps_NilMetadata(t *testing.T) {
	s := &DynatraceMetricSource{}
	gr := &grailResult{Metadata: nil}
	assert.Nil(t, s.extractTimestamps(gr))
}

func TestExtractTimestamps_NilMetricsMap(t *testing.T) {
	s := &DynatraceMetricSource{}
	gr := &grailResult{
		Metadata: &grailMetadata{Metrics: nil},
	}
	assert.Nil(t, s.extractTimestamps(gr))
}

func TestExtractTimestamps_MissingAlias(t *testing.T) {
	s := &DynatraceMetricSource{}
	gr := &grailResult{
		Metadata: &grailMetadata{
			Metrics: map[string]*grailMetricMeta{
				"other": {Timestamps: []string{"2024-01-01T00:00:00Z"}},
			},
		},
	}
	assert.Nil(t, s.extractTimestamps(gr))
}

func TestExtractTimestamps_RFC3339(t *testing.T) {
	s := &DynatraceMetricSource{}
	ts := "2024-01-01T00:00:00Z"
	expected, _ := time.Parse(time.RFC3339, ts)
	gr := &grailResult{
		Metadata: &grailMetadata{
			Metrics: map[string]*grailMetricMeta{
				grailTimeseriesAlias: {Timestamps: []string{ts}},
			},
		},
	}
	result := s.extractTimestamps(gr)
	require.Len(t, result, 1)
	assert.Equal(t, expected.Unix(), result[0])
}

func TestExtractTimestamps_RFC3339Nano(t *testing.T) {
	s := &DynatraceMetricSource{}
	ts := "2024-01-01T00:00:00.123456789Z"
	expected, _ := time.Parse(time.RFC3339Nano, ts)
	gr := &grailResult{
		Metadata: &grailMetadata{
			Metrics: map[string]*grailMetricMeta{
				grailTimeseriesAlias: {Timestamps: []string{ts}},
			},
		},
	}
	result := s.extractTimestamps(gr)
	require.Len(t, result, 1)
	assert.Equal(t, expected.Unix(), result[0])
}

func TestExtractTimestamps_MalformedSkipped(t *testing.T) {
	s := &DynatraceMetricSource{}
	gr := &grailResult{
		Metadata: &grailMetadata{
			Metrics: map[string]*grailMetricMeta{
				grailTimeseriesAlias: {
					Timestamps: []string{
						"2024-01-01T00:00:00Z",
						"not-a-date",
						"2024-01-01T01:00:00Z",
					},
				},
			},
		},
	}
	result := s.extractTimestamps(gr)
	assert.Len(t, result, 2, "malformed timestamp should be skipped")
}

func TestExtractTimestamps_EmptyList(t *testing.T) {
	s := &DynatraceMetricSource{}
	gr := &grailResult{
		Metadata: &grailMetadata{
			Metrics: map[string]*grailMetricMeta{
				grailTimeseriesAlias: {Timestamps: []string{}},
			},
		},
	}
	result := s.extractTimestamps(gr)
	assert.Empty(t, result)
}

// ---- extractDimensions ----

func TestExtractDimensions_NameField(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{"k8s.namespace": "prod"}
	dims := s.extractDimensions(record, "my.metric")
	assert.Equal(t, "my.metric", dims["__name__"])
}

func TestExtractDimensions_SkipsAlias(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		grailTimeseriesAlias: []any{1.0, 2.0},
		"k8s.namespace":      "prod",
	}
	dims := s.extractDimensions(record, "q")
	assert.NotContains(t, dims, grailTimeseriesAlias)
	assert.Equal(t, "prod", dims["k8s.namespace"])
}

func TestExtractDimensions_SkipsTimeframe(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		"timeframe": "2024-01-01T00:00:00Z/2024-01-01T01:00:00Z",
		"host":      "my-host",
	}
	dims := s.extractDimensions(record, "q")
	assert.NotContains(t, dims, "timeframe")
	assert.Equal(t, "my-host", dims["host"])
}

func TestExtractDimensions_NonStringSkipped(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		"count":   float64(42),
		"host":    "my-host",
		"enabled": true,
	}
	dims := s.extractDimensions(record, "q")
	assert.NotContains(t, dims, "count")
	assert.NotContains(t, dims, "enabled")
	assert.Equal(t, "my-host", dims["host"])
}

func TestExtractDimensions_OnlyAlias(t *testing.T) {
	// Record has only the timeseries alias (no dimensions)
	s := &DynatraceMetricSource{}
	record := map[string]any{
		grailTimeseriesAlias: []any{1.0},
	}
	dims := s.extractDimensions(record, "my.metric")
	assert.Equal(t, "my.metric", dims["__name__"])
	assert.Len(t, dims, 1)
}

// ---- extractValues ----

func TestExtractValues_MissingKey(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{"other": "value"}
	assert.Nil(t, s.extractValues(record))
}

func TestExtractValues_NonArray(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{grailTimeseriesAlias: "not-an-array"}
	assert.Nil(t, s.extractValues(record))
}

func TestExtractValues_Float64(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		grailTimeseriesAlias: []any{float64(1.5), float64(2.5), float64(3.0)},
	}
	vals := s.extractValues(record)
	require.Len(t, vals, 3)
	assert.Equal(t, 1.5, vals[0])
	assert.Equal(t, 2.5, vals[1])
	assert.Equal(t, 3.0, vals[2])
}

func TestExtractValues_Int64(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		grailTimeseriesAlias: []any{int64(100), int64(200)},
	}
	vals := s.extractValues(record)
	require.Len(t, vals, 2)
	assert.Equal(t, float64(100), vals[0])
	assert.Equal(t, float64(200), vals[1])
}

func TestExtractValues_NilEntry(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		grailTimeseriesAlias: []any{float64(1.0), nil, float64(3.0)},
	}
	vals := s.extractValues(record)
	require.Len(t, vals, 3)
	assert.Equal(t, 0.0, vals[1], "nil entry should become 0")
}

func TestExtractValues_Mixed(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{
		grailTimeseriesAlias: []any{float64(1.0), int64(2), nil, float64(4.0)},
	}
	vals := s.extractValues(record)
	require.Len(t, vals, 4)
	assert.Equal(t, 1.0, vals[0])
	assert.Equal(t, 2.0, vals[1])
	assert.Equal(t, 0.0, vals[2])
	assert.Equal(t, 4.0, vals[3])
}

func TestExtractValues_EmptyArray(t *testing.T) {
	s := &DynatraceMetricSource{}
	record := map[string]any{grailTimeseriesAlias: []any{}}
	vals := s.extractValues(record)
	assert.Empty(t, vals)
}

// ---- getTimeRange (metrics) ----

func TestMetricGetTimeRange_BothZero(t *testing.T) {
	s := &DynatraceMetricSource{}
	from, to := s.getTimeRange(0, 0)
	assert.NotEmpty(t, from)
	assert.NotEmpty(t, to)
	_, errFrom := time.Parse(time.RFC3339, from)
	_, errTo := time.Parse(time.RFC3339, to)
	assert.NoError(t, errFrom)
	assert.NoError(t, errTo)
}

func TestMetricGetTimeRange_MillisInput(t *testing.T) {
	s := &DynatraceMetricSource{}
	startMs := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	endMs := time.Date(2024, 3, 1, 1, 0, 0, 0, time.UTC).UnixMilli()
	from, to := s.getTimeRange(startMs, endMs)
	assert.Contains(t, from, "2024-03-01")
	assert.Contains(t, to, "2024-03-01")
}

// ---- extractBaseMetricID ----

func TestExtractBaseMetricID_NoTransformation(t *testing.T) {
	assert.Equal(t, "builtin:host.cpu.usage", extractBaseMetricID("builtin:host.cpu.usage"))
}

func TestExtractBaseMetricID_FilterSuffix(t *testing.T) {
	result := extractBaseMetricID(`builtin:host.cpu.usage:filter(in("dt.entity.host",entitySelector("type(HOST)")))`)
	assert.Equal(t, "builtin:host.cpu.usage", result)
}

func TestExtractBaseMetricID_SplitBySuffix(t *testing.T) {
	result := extractBaseMetricID("builtin:kubernetes.workload.cpu.usage:splitBy(k8s.namespace)")
	assert.Equal(t, "builtin:kubernetes.workload.cpu.usage", result)
}

func TestExtractBaseMetricID_FoldSuffix(t *testing.T) {
	result := extractBaseMetricID("builtin:host.disk.used:fold(max)")
	assert.Equal(t, "builtin:host.disk.used", result)
}

func TestExtractBaseMetricID_EmptyString(t *testing.T) {
	assert.Equal(t, "", extractBaseMetricID(""))
}

func TestExtractBaseMetricID_MultipleNamespaceSegments(t *testing.T) {
	result := extractBaseMetricID("builtin:kubernetes.node.requests.cpu.cores")
	assert.Equal(t, "builtin:kubernetes.node.requests.cpu.cores", result)
}

func TestExtractBaseMetricID_NoColon(t *testing.T) {
	assert.Equal(t, "somemetric", extractBaseMetricID("somemetric"))
}

// ---- fetchDTMetricDimensionsViaAutocomplete ----

func TestFetchDTMetricDimensionsViaAutocomplete_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/platform/storage/query/v1/query:autocomplete", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"suggestions":[
			{"suggestion":"dt.entity.kubernetes_node","parts":[{"type":"SIMPLE_IDENTIFIER","suggestion":"dt.entity.kubernetes_node"}]},
			{"suggestion":"k8s.node.name","parts":[{"type":"SIMPLE_IDENTIFIER","suggestion":"k8s.node.name"}]},
			{"suggestion":"metric.key","parts":[{"type":"SIMPLE_IDENTIFIER","suggestion":"metric.key"}]},
			{"suggestion":"dt.system.bucket","parts":[{"type":"SIMPLE_IDENTIFIER","suggestion":"dt.system.bucket"}]},
			{"suggestion":"}","parts":[{"type":"BRACE_CLOSE","suggestion":"}"}]}
		]}`))
	}))
	defer srv.Close()

	output, err := fetchDTMetricDimensionsViaAutocomplete(srv.URL, "test-token", "dt.kubernetes.node.cpu_allocatable")
	require.NoError(t, err)
	require.Len(t, output, 2, "metric.key, dt.system.bucket, and syntactic tokens should be filtered")
	assert.Equal(t, "dt.entity.kubernetes_node", output[0].Label)
	assert.Equal(t, "k8s.node.name", output[1].Label)
}

func TestFetchDTMetricDimensionsViaAutocomplete_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"suggestions":[]}`))
	}))
	defer srv.Close()

	output, err := fetchDTMetricDimensionsViaAutocomplete(srv.URL, "tok", "some:metric")
	require.NoError(t, err)
	assert.Empty(t, output)
}

func TestFetchDTMetricDimensionsViaAutocomplete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	_, err := fetchDTMetricDimensionsViaAutocomplete(srv.URL, "tok", "some:metric")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// ---- fetchDTMetricLabelValues ----

func TestFetchDTMetricLabelValues_DistinctValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[
			{"val":[1.0],"timeframe":{},"interval":"3600000000000","k8s.namespace":"prod"},
			{"val":[2.0],"timeframe":{},"interval":"3600000000000","k8s.namespace":"staging"},
			{"val":[3.0],"timeframe":{},"interval":"3600000000000","k8s.namespace":"prod"}
		]}}`))
	}))
	defer srv.Close()

	output, err := fetchDTMetricLabelValues(srv.URL, "tok", "builtin:host.cpu.usage",
		"k8s.namespace", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	require.Len(t, output, 2, "should deduplicate 'prod'")
	values := []string{output[0].Value, output[1].Value}
	assert.ElementsMatch(t, []string{"prod", "staging"}, values)
}

func TestFetchDTMetricLabelValues_LabelNotInRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[
			{"val":[1.0],"k8s.namespace":"prod"}
		]}}`))
	}))
	defer srv.Close()

	output, err := fetchDTMetricLabelValues(srv.URL, "tok", "builtin:host.cpu.usage",
		"nonexistent.key", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Empty(t, output)
}

func TestFetchDTMetricLabelValues_EmptyRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[]}}`))
	}))
	defer srv.Close()

	output, err := fetchDTMetricLabelValues(srv.URL, "tok", "builtin:host.cpu.usage",
		"k8s.namespace", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Empty(t, output)
}

func TestFetchDTMetricLabelValues_SystemFieldSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[
			{"val":[1.0],"k8s.namespace":"prod"}
		]}}`))
	}))
	defer srv.Close()

	output, err := fetchDTMetricLabelValues(srv.URL, "tok", "builtin:host.cpu.usage",
		"val", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.NoError(t, err)
	assert.Empty(t, output, "system field 'val' should yield no label values")
}

func TestFetchDTMetricLabelValues_DQLFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	_, err := fetchDTMetricLabelValues(srv.URL, "tok", "builtin:host.cpu.usage",
		"k8s.namespace", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query failed")
}

func TestFetchDTMetricLabelValues_FilteredSelectorStripped(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"SUCCEEDED","result":{"records":[]}}`))
	}))
	defer srv.Close()

	_, _ = fetchDTMetricLabelValues(srv.URL, "tok",
		`builtin:host.cpu.usage:filter(in("dt.entity.host",entitySelector("type(HOST)")))`,
		"k8s.namespace", "2024-01-01T00:00:00Z", "2024-01-01T01:00:00Z")

	assert.Contains(t, string(capturedBody), "`builtin:host.cpu.usage`")
	assert.NotContains(t, string(capturedBody), ":filter(")
	assert.Contains(t, string(capturedBody), "by: {k8s.namespace}")
}

// TestFetchMetricList_Platform403_ReturnsEmpty verifies that when the platform token gets
// a 403 from autocomplete, FetchMetricList returns empty results with no error.
func TestFetchMetricList_Platform403_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	s := &DynatraceMetricSource{
		getAllConfigs: func(_ *security.RequestContext, _ string) (string, string, error) {
			return "platform-tok", srv.URL, nil
		},
	}

	metrics, err := s.FetchMetricList(nil, FetchMetricsListRequest{AccountId: "test"})
	assert.NoError(t, err)
	assert.Empty(t, metrics)
}

// TestFetchMetricsLabels_Platform403_ReturnsEmpty verifies that when the platform token gets
// a 403 from the classic metric descriptor API, FetchMetricsLabels degrades gracefully.
func TestFetchMetricsLabels_Platform403_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	s := &DynatraceMetricSource{
		getAllConfigs: func(_ *security.RequestContext, _ string) (string, string, error) {
			return "platform-tok", srv.URL, nil
		},
	}

	labels, err := s.FetchMetricsLabels(nil, FetchMetricLabelsRequest{AccountId: "test", MetricName: "builtin:host.cpu.usage"})
	assert.NoError(t, err)
	assert.Empty(t, labels)
}
