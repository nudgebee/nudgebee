package observability

import (
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func swTestBucket(ts string, v float64) swBucketMeasurement {
	return swBucketMeasurement{Time: ts, Value: &v}
}

// ============================================================================
// Request → measurement query: preGroupBy is the caller's to name
// ============================================================================

func TestSWMeasurementQueryFromRequest_PassesPreGroupBy(t *testing.T) {
	q := swMeasurementQueryFromRequest(map[string]any{
		"aggregateBy":      "sum",
		"groupBy":          "k8s.namespace.name",
		"preGroupBy":       "k8s.pod.name",
		"preGroupByMethod": "avg",
	}, "from", "to")

	assert.Equal(t, "SUM", q.aggregateBy)
	assert.Equal(t, "k8s.pod.name", q.preGroupBy)
	assert.Equal(t, "AVG", q.preGroupByMethod)

	params := swBuildMeasurementParams(q)
	assert.Equal(t, "k8s.pod.name", params["preGroupBy"])
	assert.Equal(t, "AVG", params["preGroupByMethod"])
}

func TestSWMeasurementQueryFromRequest_PreGroupByDefaultsToAvg(t *testing.T) {
	q := swMeasurementQueryFromRequest(map[string]any{"aggregateBy": "SUM", "preGroupBy": "k8s.node.name"}, "", "")
	assert.Equal(t, "AVG", q.preGroupByMethod, "SWO refuses preGroupBy without a method; AVG is the one a gauge needs")
}

// A free-form SUM that does not name its entity is left exactly as asked: the source
// cannot know which entity the caller meant, and guessing would change the meaning
// of every metrics_list query that already sums on purpose.
func TestSWMeasurementQueryFromRequest_SumWithoutEntityUnchanged(t *testing.T) {
	q := swMeasurementQueryFromRequest(map[string]any{"aggregateBy": "SUM"}, "", "")
	params := swBuildMeasurementParams(q)
	assert.Equal(t, "SUM", params["aggregateBy"])
	assert.NotContains(t, params, "preGroupBy")
	assert.NotContains(t, params, "preGroupByMethod")
}

// ============================================================================
// Partial newest bucket (P3)
// ============================================================================

func TestSWDropPartialBuckets_UsesReportedBucketSize(t *testing.T) {
	groupings := []swMeasurementGrouping{{Measurements: []swBucketMeasurement{
		swTestBucket("2026-10-07T10:08:00Z", 1),
		swTestBucket("2026-10-07T10:09:00Z", 2),
		swTestBucket("2026-10-07T10:10:00Z", 3), // ends 10:11 > end−width (10:10:59): still filling
		swTestBucket("2026-10-07T10:11:00Z", 4), // ends after the window
	}}}
	end := time.Date(2026, 10, 7, 10, 11, 59, 0, time.UTC)

	swDropPartialBuckets(groupings, 60, end)

	got := make([]string, 0)
	for _, m := range groupings[0].Measurements {
		got = append(got, m.Time)
	}
	assert.Equal(t, []string{"2026-10-07T10:08:00Z", "2026-10-07T10:09:00Z"}, got)
}

func TestSWDropPartialBuckets_InfersWidthFromTimestamps(t *testing.T) {
	groupings := []swMeasurementGrouping{{Measurements: []swBucketMeasurement{
		swTestBucket("2026-10-07T09:00:00Z", 1),
		swTestBucket("2026-10-07T09:30:00Z", 2),
		swTestBucket("2026-10-07T10:00:00Z", 3),
	}}}
	end := time.Date(2026, 10, 7, 10, 11, 59, 0, time.UTC)

	swDropPartialBuckets(groupings, 0, end)

	require.Len(t, groupings[0].Measurements, 1)
	assert.Equal(t, "2026-10-07T09:00:00Z", groupings[0].Measurements[0].Time)
}

func TestSWDropPartialBuckets_InstantReadSkipsFillingBucket(t *testing.T) {
	groupings := []swMeasurementGrouping{{Measurements: []swBucketMeasurement{
		swTestBucket("2026-10-07T09:01:00Z", 6.68),
		swTestBucket("2026-10-07T09:02:00Z", 7.08),
		swTestBucket("2026-10-07T09:03:00Z", 6.34), // P3: read 7.05 seventy-five seconds later
	}}}
	swDropPartialBuckets(groupings, 60, time.Date(2026, 10, 7, 9, 4, 30, 0, time.UTC))

	qr := swGroupingsToQueryResult(groupings, "k", "m", true)
	require.Len(t, qr.Payload, 1)
	assert.Equal(t, []float64{7.08}, qr.Payload[0].Values)
}

func TestFetchAllMeasurements_ReturnsBucketSize(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var sent map[string]string
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		sent = params
		return []byte(`{"groupings":[{"attributes":[],"measurements":[{"time":"2026-10-07T10:00:00Z","value":1}]}],"bucketSizeInSeconds":300,"pageInfo":{"nextPage":""}}`), http.StatusOK, nil
	}

	groupings, bucket, err := fetchAllMeasurements("tok", swTestBaseURL, "/v1/metrics/m/measurements",
		swMeasurementQuery{aggregateBy: "SUM", preGroupBy: "k8s.node.name", preGroupByMethod: "AVG"})
	require.NoError(t, err)
	assert.Len(t, groupings, 1)
	assert.Equal(t, int64(300), bucket)
	assert.Equal(t, "k8s.node.name", sent["preGroupBy"])
	assert.Equal(t, "AVG", sent["preGroupByMethod"])
}

// ============================================================================
// Collapse over time (P2)
// ============================================================================

func swTestSeries() []QueryResult {
	return []QueryResult{{
		QueryKey: "cpu_usage",
		Payload: []Result{
			{Metric: map[string]string{"workload": "web"}, Timestamps: []int64{0, 60, 120}, Values: []float64{1, 5, 3}},
			{Metric: map[string]string{"workload": "gone"}},
		},
	}}
}

func TestSolarWindsCollapseOverTime(t *testing.T) {
	cases := map[string]struct {
		agg  ParsedAggregate
		step string
		want float64
	}{
		"max":                {ParsedAggregate{Name: AggregateMax}, "", 5},
		"min":                {ParsedAggregate{Name: AggregateMin}, "", 1},
		"avg":                {ParsedAggregate{Name: AggregateAvg}, "", 3},
		"sum":                {ParsedAggregate{Name: AggregateSum}, "", 9},
		"p50":                {ParsedAggregate{Name: "quantile", Quantile: 0.5}, "", 3},
		"count at the width": {ParsedAggregate{Name: AggregateCount}, "60s", 3},
		// Three 60s buckets cover 180s, which is 1.5 intervals of a 120s step: the
		// contract's unit (metrics_aggregate_count.go), not SWO's bucket count.
		"count in caller steps": {ParsedAggregate{Name: AggregateCount}, "120s", 1.5},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			results := swTestSeries()
			solarwindsCollapseOverTime(results, RequestMetadata{Aggregate: tc.agg, Step: tc.step})

			s := results[0].Payload[0]
			assert.Equal(t, []int64{120}, s.Timestamps, "one point, stamped at the last bucket")
			require.Len(t, s.Values, 1)
			assert.InDelta(t, tc.want, s.Values[0], 1e-9)
			assert.Empty(t, results[0].Payload[1].Values, "a series with no data stays empty")
		})
	}
}

func TestSolarWindsCollapseOverTime_NoAggregateLeavesRange(t *testing.T) {
	results := swTestSeries()
	solarwindsCollapseOverTime(results, RequestMetadata{})
	assert.Equal(t, []float64{1, 5, 3}, results[0].Payload[0].Values)
}

func TestSolarWindsCollapseOverTime_MaxIsTheRawPeak(t *testing.T) {
	results := []QueryResult{{QueryKey: "memory_usage", Payload: []Result{
		{Timestamps: []int64{0, 60, 120, 180}, Values: []float64{2, math.NaN(), 9, 4}},
	}}}
	solarwindsCollapseOverTime(results, RequestMetadata{Aggregate: ParsedAggregate{Name: AggregateMax}})
	assert.Equal(t, []float64{9}, results[0].Payload[0].Values)
}

// ============================================================================
// Time-weighted SUM
// ============================================================================

func TestSWDivideBySamplesPerBucket(t *testing.T) {
	values := []QueryResult{{QueryKey: "cpu_request", Payload: []Result{{
		Metric:     map[string]string{"workload": "web"},
		Timestamps: []int64{0, 1800, 3600, 5400, 7200, 9000, 10800},
		// Plain SUMs of every sample: 0.2 cores x 30 samples per full bucket; a
		// rollout where the old and new pod overlapped for two samples; a bucket no
		// pod lived through; a scrape landing twice; then two buckets with no count.
		Values: []float64{6.0, 6.4, 3.0, 6.2, 6.0, 6.0, 6.0},
	}}}}
	counts := []QueryResult{{QueryKey: "cpu_request", Payload: []Result{{
		Metric:     map[string]string{"workload": "web"},
		Timestamps: []int64{0, 1800, 3600, 5400, 9000},
		Values:     []float64{30, 30, 15, 31, 0},
	}}}}

	swDivideBySamplesPerBucket(values, counts)

	s := values[0].Payload[0]
	assert.Equal(t, []int64{0, 1800, 3600, 5400}, s.Timestamps, "no count, or a zero count, yields no point")
	require.Len(t, s.Values, 4)
	assert.InDelta(t, 0.2, s.Values[0], 1e-9)
	assert.InDelta(t, 6.4/30, s.Values[1], 1e-9, "two overlapping pods really did request 0.4 for two samples")
	assert.InDelta(t, 0.1, s.Values[2], 1e-9, "a half-covered bucket is half a full one, not a full one")
	assert.InDelta(t, 0.2, s.Values[3], 1e-9, "a bucket with an extra sample is not read high")
}

func TestSWDivideBySamplesPerBucket_MatchesSeriesByLabels(t *testing.T) {
	values := []QueryResult{{QueryKey: "k", Payload: []Result{
		{Metric: map[string]string{"workload": "a"}, Timestamps: []int64{0}, Values: []float64{10}},
		{Metric: map[string]string{"workload": "b"}, Timestamps: []int64{0}, Values: []float64{10}},
	}}}
	counts := []QueryResult{{QueryKey: "k", Payload: []Result{
		{Metric: map[string]string{"workload": "b"}, Timestamps: []int64{0}, Values: []float64{5}},
		{Metric: map[string]string{"workload": "a"}, Timestamps: []int64{0, 60}, Values: []float64{5, 5}},
	}}}
	swDivideBySamplesPerBucket(values, counts)
	assert.Equal(t, []float64{2}, values[0].Payload[0].Values)
	assert.Equal(t, []float64{2}, values[0].Payload[1].Values)
}

func TestSWDivideBySamplesPerBucket_NoCountMeansNoTotal(t *testing.T) {
	values := []QueryResult{{QueryKey: "k", Payload: []Result{{Timestamps: []int64{0}, Values: []float64{6}}}}}
	swDivideBySamplesPerBucket(values, []QueryResult{{QueryKey: "k"}})
	assert.Empty(t, values[0].Payload, "the raw sum must never be reported as a total")
	require.NotNil(t, values[0].Error)
	assert.NotEmpty(t, values[0].Note)
}

// A value series whose labels have no count series, or whose buckets all lack a count,
// is dropped: "no data" must be zero series, not a series with empty arrays.
func TestSWDivideBySamplesPerBucket_DropsSeriesWithNoCountableBucket(t *testing.T) {
	values := []QueryResult{{QueryKey: "k", Payload: []Result{
		{Metric: map[string]string{"pod": "a"}, Timestamps: []int64{0, 60}, Values: []float64{6, 6}},
		{Metric: map[string]string{"pod": "b"}, Timestamps: []int64{0}, Values: []float64{9}},
		{Metric: map[string]string{"pod": "c"}, Timestamps: []int64{0}, Values: []float64{3}},
	}}}
	counts := []QueryResult{{QueryKey: "k", Payload: []Result{
		{Metric: map[string]string{"pod": "a"}, Timestamps: []int64{0, 60}, Values: []float64{2, 2}},
		{Metric: map[string]string{"pod": "c"}, Timestamps: []int64{60}, Values: []float64{2}},
	}}}
	swDivideBySamplesPerBucket(values, counts)
	require.Len(t, values[0].Payload, 1, "pod b has no count series and pod c no count for its bucket")
	assert.Equal(t, map[string]string{"pod": "a"}, values[0].Payload[0].Metric)
	assert.Equal(t, []float64{3, 3}, values[0].Payload[0].Values)
}

// Counts arrive as floats; near-equal values must land on the same whole count.
func TestSWFullBucketCount_RoundsToWholeCounts(t *testing.T) {
	full, share := swFullBucketCount(QueryResult{Payload: []Result{{Values: []float64{30, 29.9999999, 30.0000001, 22}}}})
	assert.Equal(t, 30.0, full)
	assert.InDelta(t, 0.75, share, 1e-9)
}

func TestSWFullBucketCount_MostCommonTiesToLarger(t *testing.T) {
	full, share := swFullBucketCount(QueryResult{Payload: []Result{{Values: []float64{30, 30, 22, 30, 4}}}})
	assert.Equal(t, 30.0, full)
	assert.InDelta(t, 0.6, share, 1e-9)
	full, _ = swFullBucketCount(QueryResult{Payload: []Result{{Values: []float64{4, 5}}}})
	assert.Equal(t, 5.0, full)
	full, _ = swFullBucketCount(QueryResult{})
	assert.Equal(t, 0.0, full)
}

func TestSWTimeWeightedSum_PlainSumOverMeasuredCount(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var sent []map[string]string
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		sent = append(sent, params)
		if params["preGroupByMethod"] == "COUNT" {
			return []byte(`{"groupings":[{"attributes":[],"measurements":[{"time":"2026-10-07T09:00:00Z","value":30},{"time":"2026-10-07T09:30:00Z","value":30}]}],"bucketSizeInSeconds":1800}`), http.StatusOK, nil
		}
		return []byte(`{"groupings":[{"attributes":[],"measurements":[{"time":"2026-10-07T09:00:00Z","value":6},{"time":"2026-10-07T09:30:00Z","value":6.6}]}],"bucketSizeInSeconds":1800}`), http.StatusOK, nil
	}
	q := swMeasurementQuery{aggregateBy: "SUM", preGroupBy: swEntityContainer, preGroupByMethod: "AVG", filter: "k8s.namespace.name: [shop]", to: "2026-10-07T11:00:00Z"}
	require.True(t, swIsTimeWeightedSum(q))

	qr, err := swTimeWeightedSum("tok", swTestBaseURL, "/v1/metrics/m/measurements", "cpu_request", "m", q, true)
	require.NoError(t, err)

	require.Len(t, sent, 2)
	assert.Equal(t, "SUM", sent[0]["aggregateBy"])
	assert.NotContains(t, sent[0], "preGroupBy", "the value request is the plain SUM of every sample")
	assert.Equal(t, map[string]string{"aggregateBy": "MAX", "preGroupBy": swEntityContainer, "preGroupByMethod": "COUNT",
		"filter": "k8s.namespace.name: [shop]", "endTime": "2026-10-07T11:00:00Z", "pageSize": "100"}, sent[1])
	require.Len(t, qr.Payload, 1)
	assert.InDelta(t, 0.22, qr.Payload[0].Values[0], 1e-9, "instant: the newest complete bucket, time-weighted")
}

func TestSWIsTimeWeightedSum_OnlyForSumWithAnEntity(t *testing.T) {
	assert.False(t, swIsTimeWeightedSum(swMeasurementQuery{aggregateBy: "SUM"}), "no entity named: sent as asked")
	assert.False(t, swIsTimeWeightedSum(swMeasurementQuery{aggregateBy: "AVG", preGroupBy: "k8s.pod.name", preGroupByMethod: "AVG"}))
	assert.False(t, swIsTimeWeightedSum(swMeasurementQuery{aggregateBy: "SUM", preGroupBy: "k8s.pod.name", preGroupByMethod: "MAX"}))
}

// swTestMeasurements is a measurements response with one grouping.
func swTestMeasurements(bucket int, points string) []byte {
	return []byte(`{"groupings":[{"attributes":[],"measurements":[` + points + `]}],"bucketSizeInSeconds":` + strconv.Itoa(bucket) + `}`)
}

// A filter (or pod_names) that matches nothing is an empty answer, not a failed count.
func TestSolarWindsFetchOneMetricQuery_TimeWeightedSumMatchingNothing(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	calls := 0
	solarWindsDoGET = func(_, _, _ string, _ map[string]string) ([]byte, int, error) {
		calls++
		return []byte(`{"groupings":[{"attributes":[],"measurements":[]}],"bucketSizeInSeconds":60}`), http.StatusOK, nil
	}
	q := swMeasurementQueryFromRequest(buildSolarWindsRequestParams(RequestMetadata{
		Kind: "deployment", Namespace: "shop", Name: "web", PodNames: []string{}, PodNamesSupplied: true,
	}, "k8s.namespace.name,k8s.deployment.name", "SUM", swEntityPod), "2026-10-07T10:00:00Z", "2026-10-07T11:00:00Z")

	qr := (&SolarWindsMetricSource{}).fetchOneMetricQuery(mockRequestContext(), "tok", swTestBaseURL, "cpu_usage", "k8s.pod.cpu.usage.seconds.rate", q, false)

	assert.Nil(t, qr.Error)
	assert.Empty(t, qr.Note)
	assert.False(t, swHasPoints(qr))
	assert.Equal(t, 1, calls, "nothing to count when nothing matched")
}

func TestSWTimeWeightedSum_RefusesMismatchedBucketWidths(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		if params["preGroupByMethod"] == "COUNT" {
			return swTestMeasurements(300, `{"time":"2026-10-07T09:00:00Z","value":5}`), http.StatusOK, nil
		}
		return swTestMeasurements(60, `{"time":"2026-10-07T09:00:00Z","value":6}`), http.StatusOK, nil
	}
	q := swMeasurementQuery{aggregateBy: "SUM", preGroupBy: swEntityNode, preGroupByMethod: "AVG",
		from: "2026-10-07T06:00:00Z", to: "2026-10-07T12:00:00Z"}
	_, err := swTimeWeightedSum("tok", swTestBaseURL, "/p", "cpu_real", "m", q, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "60s buckets")
}

func TestSWDivideBySamplesPerBucket_NotesAnUnclearSampleCount(t *testing.T) {
	// The scrape interval changed mid-window: the later buckets hold fewer samples.
	values := []QueryResult{{QueryKey: "k", Payload: []Result{{Timestamps: []int64{0, 60, 120}, Values: []float64{30, 20, 10}}}}}
	counts := []QueryResult{{QueryKey: "k", Payload: []Result{{Timestamps: []int64{0, 60, 120}, Values: []float64{30, 20, 10}}}}}
	swDivideBySamplesPerBucket(values, counts)
	assert.Contains(t, values[0].Note, "scrape interval")

	clear := []QueryResult{{QueryKey: "k", Payload: []Result{{Timestamps: []int64{0, 60, 120}, Values: []float64{10, 10, 5}}}}}
	swDivideBySamplesPerBucket(clear, []QueryResult{{QueryKey: "k", Payload: []Result{{Timestamps: []int64{0, 60, 120}, Values: []float64{10, 10, 5}}}}})
	assert.Equal(t, []float64{1, 1, 0.5}, clear[0].Payload[0].Values, "documented limit: the bucket after the change reads low")
	assert.Empty(t, clear[0].Note, "a two-thirds majority is clear enough")
}

func TestSWTimeWeightedSum_WidensARawSampleWindow(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	var starts []string
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		starts = append(starts, params["startTime"])
		v := "6"
		if params["preGroupByMethod"] == "COUNT" {
			v = "1"
		}
		// 10:44 is before the caller's window, 10:52 inside it.
		return swTestMeasurements(60, `{"time":"2026-10-07T10:44:00Z","value":`+v+`},{"time":"2026-10-07T10:52:00Z","value":`+v+`}`), http.StatusOK, nil
	}
	q := swMeasurementQuery{aggregateBy: "SUM", preGroupBy: swEntityNode, preGroupByMethod: "AVG",
		from: "2026-10-07T10:50:00Z", to: "2026-10-07T11:00:00Z"}

	qr, err := swTimeWeightedSum("tok", swTestBaseURL, "/p", "cpu_real", "m", q, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-10-07T10:44:00Z", "2026-10-07T10:44:00Z"}, starts, "SWO answers 15 minutes or less in 1-second buckets")
	require.Len(t, qr.Payload, 1)
	assert.Equal(t, []int64{time.Date(2026, 10, 7, 10, 52, 0, 0, time.UTC).Unix()}, qr.Payload[0].Timestamps, "trimmed back to the caller's window")
	assert.Equal(t, []float64{6}, qr.Payload[0].Values)
}

func TestSolarWindsCollapseOverTime_CountSingleBucketUsesReportedWidth(t *testing.T) {
	results := []QueryResult{{QueryKey: "cpu_usage", bucketSeconds: 1800, Payload: []Result{{Timestamps: []int64{0}, Values: []float64{3}}}}}
	solarwindsCollapseOverTime(results, RequestMetadata{Aggregate: ParsedAggregate{Name: AggregateCount}, Step: "60s"})
	assert.Equal(t, []float64{30}, results[0].Payload[0].Values, "one 30-minute bucket is 30 one-minute steps")
}

func TestSolarWindsCollapseOverTime_CountRawSampleWindow(t *testing.T) {
	// 15 minutes in 1-second buckets: one reading per 60s scrape.
	ts := make([]int64, 15)
	vals := make([]float64, 15)
	for i := range ts {
		ts[i], vals[i] = int64(i*60+7), 1
	}
	results := []QueryResult{{QueryKey: "cpu_usage", bucketSeconds: 1, Payload: []Result{{Timestamps: ts, Values: vals}}}}
	solarwindsCollapseOverTime(results, RequestMetadata{Aggregate: ParsedAggregate{Name: AggregateCount}, Step: "60s"})
	assert.Equal(t, []float64{15}, results[0].Payload[0].Values, "each reading covers the scrape interval, not one second")
}

func TestSWTimeWeightedSum_InstantShortWindowKeepsNewestCompleteBucket(t *testing.T) {
	orig := solarWindsDoGET
	defer func() { solarWindsDoGET = orig }()
	solarWindsDoGET = func(_, _, _ string, params map[string]string) ([]byte, int, error) {
		v := "6"
		if params["preGroupByMethod"] == "COUNT" {
			v = "1"
		}
		return swTestMeasurements(60, `{"time":"2026-10-07T10:57:00Z","value":`+v+`},{"time":"2026-10-07T10:58:00Z","value":`+v+`},{"time":"2026-10-07T10:59:00Z","value":`+v+`}`), http.StatusOK, nil
	}
	q := swMeasurementQuery{aggregateBy: "SUM", preGroupBy: swEntityNode, preGroupByMethod: "AVG",
		from: "2026-10-07T10:59:30Z", to: "2026-10-07T11:00:30Z"}

	qr, err := swTimeWeightedSum("tok", swTestBaseURL, "/p", "cpu_real", "m", q, true)
	require.NoError(t, err)
	require.Len(t, qr.Payload, 1)
	assert.Equal(t, []int64{time.Date(2026, 10, 7, 10, 58, 0, 0, time.UTC).Unix()}, qr.Payload[0].Timestamps, "the newest complete bucket, though it began before the window")
	assert.Equal(t, []float64{6}, qr.Payload[0].Values)
}

func TestSWTrimBefore_DropsSeriesLeftEmpty(t *testing.T) {
	qr := QueryResult{Payload: []Result{
		{Metric: map[string]string{"node": "a"}, Timestamps: []int64{100, 200}, Values: []float64{1, 2}},
		{Metric: map[string]string{"node": "b"}, Timestamps: []int64{100}, Values: []float64{3}},
	}}
	swTrimBefore(&qr, time.Unix(150, 0))
	require.Len(t, qr.Payload, 1, "a series with no data in the window is not returned")
	assert.Equal(t, "a", qr.Payload[0].Metric["node"])
	assert.Equal(t, []float64{2}, qr.Payload[0].Values)
}
