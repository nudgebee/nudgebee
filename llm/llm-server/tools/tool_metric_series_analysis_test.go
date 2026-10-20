package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

var msTestStart = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

func msTestSeries(step time.Duration, vals []float64) msSeries {
	s := msSeries{name: "test"}
	for i, v := range vals {
		s.points = append(s.points, msPoint{at: msTestStart.Add(time.Duration(i) * step), v: v})
	}
	return s
}

func msLinear(n int, base, perPoint, wobble float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = base + perPoint*float64(i) + wobble*math.Sin(float64(i)*1.7)
	}
	return out
}

func msNoisy(n int, level, spread float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = level + (r.Float64()-0.5)*spread
	}
	return out
}

func msStairs() []float64 {
	out := make([]float64, 0, 60)
	for i := 0; i < 60; i++ {
		level := 10.0
		switch {
		case i >= 40:
			level = 4
		case i >= 20:
			level = 7
		}
		out = append(out, level+0.05*math.Sin(float64(i)))
	}
	return out
}

func TestAnalyzeMetricSeries_Scenarios(t *testing.T) {
	gapped := msTestSeries(5*time.Minute, msLinear(73, 50, 0, 1))
	gapped.points = append(append([]msPoint{}, gapped.points[:30]...), gapped.points[42:]...)

	tests := []struct {
		name       string
		series     msSeries
		thresholds []msThreshold
		check      func(t *testing.T, a seriesAnalysis)
	}{
		{
			name:       "linear rise toward a threshold is forecast",
			series:     msTestSeries(time.Hour, msLinear(48, 10, 0.5, 0.2)),
			thresholds: []msThreshold{{Value: 50, Direction: "above", Label: "limit"}},
			check: func(t *testing.T, a seriesAnalysis) {
				require.NotNil(t, a.Trend)
				assert.True(t, a.Trend.Steady)
				assert.Equal(t, "rising", a.Trend.Direction)
				assert.InDelta(t, 0.5, a.Trend.RatePerHour, 0.01)
				assert.InDelta(t, 12, a.Trend.RatePerDay, 0.2)
				assert.Greater(t, a.Trend.R2, 0.99)
				th := a.Thresholds[0]
				require.NotNil(t, th.TimeToReach, th.NotForecastReason)
				// Fit at the last point is ~10 + 0.5*47 = 33.5, so ~33 h to 50.
				assert.InDelta(t, 33, th.TimeToReach.HoursEstimate, 1.5)
				require.NotNil(t, th.TimeToReach.HoursHigh)
				assert.LessOrEqual(t, th.TimeToReach.HoursLow, th.TimeToReach.HoursEstimate)
				assert.GreaterOrEqual(t, *th.TimeToReach.HoursHigh, th.TimeToReach.HoursEstimate)
				assert.Equal(t, 0, th.Crossings)
				assert.False(t, th.CurrentlyPast)
				assert.Empty(t, a.StepChanges)
				assert.Contains(t, a.Summary, "Rising steadily")
				assert.Contains(t, a.Summary, "reaches limit in ~")
			},
		},
		{
			name:       "flat noisy series gets no forecast",
			series:     msTestSeries(time.Hour, msNoisy(481, 7.6, 0.5, 7)),
			thresholds: []msThreshold{{Value: 5, Direction: "below", Label: "P1 · 5%"}},
			check: func(t *testing.T, a seriesAnalysis) {
				require.NotNil(t, a.Trend)
				assert.False(t, a.Trend.Steady)
				assert.Equal(t, "flat", a.Trend.Direction)
				assert.Less(t, a.Trend.R2, 0.2)
				th := a.Thresholds[0]
				assert.Nil(t, th.TimeToReach)
				assert.Contains(t, th.NotForecastReason, "no steady trend (r2=")
				assert.Zero(t, th.PctTimePast)
				assert.Empty(t, a.StepChanges, "noise is not a level shift")
				assert.True(t, strings.HasPrefix(a.Summary, "Flat: "), a.Summary)
				assert.Contains(t, a.Summary, "over 20 d")
				assert.Contains(t, a.Summary, "no forecast to P1 · 5%")
				logMetricSeriesExample(t, a)
			},
		},
		{
			name:       "two step drops are steps, not a steady decline",
			series:     msTestSeries(time.Hour, msStairs()),
			thresholds: []msThreshold{{Value: 2, Direction: "below", Label: "floor"}},
			check: func(t *testing.T, a seriesAnalysis) {
				require.Len(t, a.StepChanges, 2)
				for i, st := range a.StepChanges {
					assert.InDelta(t, -3, st.Delta, 0.15, i)
				}
				assert.Equal(t, msTestStart.Add(20*time.Hour).Format(time.RFC3339), a.StepChanges[0].At)
				assert.Equal(t, msTestStart.Add(40*time.Hour).Format(time.RFC3339), a.StepChanges[1].At)
				require.NotNil(t, a.Trend)
				assert.Equal(t, "falling", a.Trend.Direction)
				assert.False(t, a.Trend.Steady)
				assert.Contains(t, a.Trend.Note, "level shift")
				th := a.Thresholds[0]
				assert.Nil(t, th.TimeToReach)
				assert.Contains(t, th.NotForecastReason, "level shift")
				assert.True(t, strings.HasPrefix(a.Summary, "Stepped down in 2 level shift(s)"), a.Summary)
				logMetricSeriesExample(t, a)
			},
		},
		{
			name:   "gap in the data is reported",
			series: gapped,
			check: func(t *testing.T, a seriesAnalysis) {
				assert.Equal(t, 1, a.GapCount)
				require.Len(t, a.Gaps, 1)
				assert.Equal(t, msTestStart.Add(29*5*time.Minute).Format(time.RFC3339), a.Gaps[0].From)
				assert.Equal(t, msTestStart.Add(42*5*time.Minute).Format(time.RFC3339), a.Gaps[0].To)
				assert.Equal(t, 65.0, a.Gaps[0].Minutes)
				assert.Equal(t, 300.0, a.MedianStepSeconds)
				assert.Contains(t, a.Summary, "1 gap(s)")
			},
		},
		{
			name:       "too little history is refused",
			series:     msTestSeries(15*time.Minute, []float64{1, 2, 3, 4, 5}),
			thresholds: []msThreshold{{Value: 10, Direction: "above", Label: "limit"}},
			check: func(t *testing.T, a seriesAnalysis) {
				th := a.Thresholds[0]
				assert.Nil(t, th.TimeToReach)
				assert.True(t, strings.HasPrefix(th.NotForecastReason, "insufficient history: 5 points over 1 h"), th.NotForecastReason)
			},
		},
		{
			name:       "already past the threshold",
			series:     msTestSeries(time.Hour, msLinear(48, 10, 0.5, 0.2)),
			thresholds: []msThreshold{{Value: 20, Direction: "above", Label: "warn"}},
			check: func(t *testing.T, a seriesAnalysis) {
				th := a.Thresholds[0]
				assert.True(t, th.CurrentlyPast)
				assert.Equal(t, "already past", th.NotForecastReason)
				assert.Nil(t, th.TimeToReach)
				assert.Equal(t, 1, th.Crossings)
				assert.InDelta(t, 56, th.PctTimePast, 3)
				assert.Contains(t, a.Summary, "past warn now")
			},
		},
		{
			name:       "threshold inside the normal band",
			series:     msTestSeries(time.Hour, msNoisy(481, 7.6, 0.5, 7)),
			thresholds: []msThreshold{{Value: 7.55, Direction: "above", Label: "P3"}},
			check: func(t *testing.T, a seriesAnalysis) {
				th := a.Thresholds[0]
				assert.Greater(t, th.PctTimePast, 40.0)
				assert.Less(t, th.PctTimePast, 80.0)
				assert.Greater(t, th.Crossings, 50, "flapping across a threshold inside normal operation")
				assert.Contains(t, a.Summary, "of samples")
			},
		},
		{
			name:       "moving away from the threshold",
			series:     msTestSeries(time.Hour, msLinear(48, 40, -0.5, 0.2)),
			thresholds: []msThreshold{{Value: 60, Direction: "above", Label: "limit"}},
			check: func(t *testing.T, a seriesAnalysis) {
				assert.Equal(t, "moving away from the threshold", a.Thresholds[0].NotForecastReason)
			},
		},
		{
			name:       "falling toward a below threshold is forecast",
			series:     msTestSeries(time.Hour, msLinear(48, 40, -0.5, 0.2)),
			thresholds: []msThreshold{{Value: 5, Direction: "below", Label: "floor"}},
			check: func(t *testing.T, a seriesAnalysis) {
				th := a.Thresholds[0]
				require.NotNil(t, th.TimeToReach, th.NotForecastReason)
				// Fit at the end is ~40 - 23.5 = 16.5, so ~23 h to 5.
				assert.InDelta(t, 23, th.TimeToReach.HoursEstimate, 1.5)
			},
		},
		{
			name:   "small fractions keep their precision",
			series: msTestSeries(time.Hour, msLinear(24, 0.0033, 0.00001, 0)),
			check: func(t *testing.T, a seriesAnalysis) {
				assert.Equal(t, 0.0033, a.Baseline.First)
				assert.Equal(t, 0.00353, a.Baseline.Last)
				assert.InDelta(t, 0.00001, a.Trend.RatePerHour, 1e-9)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, analyzeMetricSeries(msNormalize(tc.series), tc.thresholds))
		})
	}
}

func logMetricSeriesExample(t *testing.T, a seriesAnalysis) {
	t.Helper()
	b, err := json.MarshalIndent(a, "", "  ")
	require.NoError(t, err)
	t.Logf("example output:\n%s", b)
}

func TestAnalyzeMetricSeries_OneSampleSpikeIsNotAStep(t *testing.T) {
	vals := msNoisy(60, 10, 0.2, 3)
	vals[30] = 30
	a := analyzeMetricSeries(msTestSeries(time.Minute, vals), nil)
	assert.Empty(t, a.StepChanges)
}

func TestParseMetricSeriesFile_Shapes(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		statistic  string
		wantCount  int
		wantName   string
		wantStat   string
		wantValues []float64
		wantNote   string
	}{
		{
			name: "cloudwatch get-metric-data newest first with account prefix",
			content: "[account: prod-main]\n" + `{"MetricDataResults":[{"Id":"m1","Label":"CPUUtilization",` +
				`"Timestamps":["2026-10-20T10:10:00+00:00","2026-10-20T10:05:00+00:00","2026-10-20T10:00:00+00:00"],` +
				`"Values":[30,20,10],"StatusCode":"PartialData"}],"Messages":[]}`,
			wantCount: 1, wantName: "CPUUtilization (m1)", wantValues: []float64{10, 20, 30}, wantNote: "PartialData",
		},
		{
			name: "cloudwatch get-metric-statistics unordered, default Average",
			content: `{"Label":"CPUUtilization","Datapoints":[` +
				`{"Timestamp":"2026-10-20T10:05:00+00:00","Average":20,"Maximum":25,"Unit":"Percent"},` +
				`{"Timestamp":"2026-10-20T10:10:00+00:00","Average":30,"Maximum":35,"Unit":"Percent"},` +
				`{"Timestamp":"2026-10-20T10:00:00+00:00","Average":10,"Maximum":15,"Unit":"Percent"}]}`,
			wantCount: 1, wantName: "CPUUtilization", wantStat: "Average", wantValues: []float64{10, 20, 30},
		},
		{
			name: "cloudwatch get-metric-statistics requested Maximum",
			content: `{"Label":"CPUUtilization","Datapoints":[` +
				`{"Timestamp":"2026-10-20T10:05:00+00:00","Average":20,"Maximum":25,"Unit":"Percent"},` +
				`{"Timestamp":"2026-10-20T10:00:00+00:00","Average":10,"Maximum":15,"Unit":"Percent"}]}`,
			statistic: "maximum", wantCount: 1, wantStat: "Maximum", wantValues: []float64{15, 25},
		},
		{
			name: "azure monitor metrics list skips null points",
			content: `{"cost":0,"interval":"PT1M","namespace":"Microsoft.Compute/virtualMachines","value":[{` +
				`"id":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1/providers/Microsoft.Insights/metrics/Percentage CPU",` +
				`"name":{"localizedValue":"Percentage CPU","value":"Percentage CPU"},"type":"Microsoft.Insights/metrics","unit":"Percent",` +
				`"timeseries":[{"metadatavalues":[],"data":[` +
				`{"timeStamp":"2026-10-20T10:02:00Z","average":12.5,"maximum":20},` +
				`{"timeStamp":"2026-10-20T10:01:00Z","average":null,"maximum":null},` +
				`{"timeStamp":"2026-10-20T10:00:00Z","average":10.5,"maximum":18}]}]}]}`,
			wantCount: 1, wantName: "Percentage CPU", wantStat: "average", wantValues: []float64{10.5, 12.5},
		},
		{
			name: "azure dimension metadata names the series",
			content: `{"value":[{"name":{"value":"Http5xx"},"timeseries":[{"metadatavalues":[{"name":{"value":"Instance"},"value":"i-1"}],` +
				`"data":[{"timeStamp":"2026-10-20T10:00:00Z","total":3},{"timeStamp":"2026-10-20T10:01:00Z","total":4}]}]}]}`,
			wantCount: 1, wantName: "Http5xx{Instance=i-1}", wantStat: "total", wantValues: []float64{3, 4},
		},
		{
			name: "gcp time-series list newest first",
			content: `[{"metric":{"labels":{"instance_name":"vm-1"},"type":"compute.googleapis.com/instance/cpu/utilization"},` +
				`"resource":{"labels":{"instance_id":"123","zone":"us-central1-a"},"type":"gce_instance"},"metricKind":"GAUGE","valueType":"DOUBLE",` +
				`"points":[{"interval":{"endTime":"2026-10-20T10:02:00.000Z","startTime":"2026-10-20T10:02:00.000Z"},"value":{"doubleValue":0.3}},` +
				`{"interval":{"endTime":"2026-10-20T10:01:00Z"},"value":{"doubleValue":0.2}},` +
				`{"interval":{"endTime":"2026-10-20T10:00:00Z"},"value":{"doubleValue":0.1}}]},` +
				`{"metric":{"type":"custom.googleapis.com/queue_depth"},"resource":{"type":"global"},` +
				`"points":[{"interval":{"endTime":"2026-10-20T10:00:00Z"},"value":{"int64Value":"5"}}]}]`,
			wantCount: 2, wantName: "compute.googleapis.com/instance/cpu/utilization{instance_id=123,instance_name=vm-1,resource_type=gce_instance,zone=us-central1-a}",
			wantValues: []float64{0.1, 0.2, 0.3},
		},
		{
			name: "prometheus tool file",
			content: `{"sum by (pod) (rate(container_cpu_usage_seconds_total[5m]))":[` +
				`{"metric":{"pod":"api-1"},"timestamps":[1760954520000,1760954460000,1760954400000],"values":[0.3,0.2,0.1],"stats":{"min":0.1,"max":0.3}},` +
				`{"metric":{"pod":"api-2"},"timestamps":[1760954400000,1760954460000,1760954520000],"values":["0.4","0.5","NaN"]}]}`,
			wantCount: 2, wantName: "sum by (pod) (rate(container_cpu_usage_seconds_total[5m])){pod=api-1}", wantValues: []float64{0.1, 0.2, 0.3},
		},
		{
			name: "event evidence file, nested generic series",
			content: `{"event_id":"e1","evidence":{"metrics":[{"name":"memory_working_set_bytes","alerting":true,` +
				`"timestamps":["2026-10-20T10:00:00Z","2026-10-20T10:01:00Z"],"values":[100,200]}]}}`,
			wantCount: 1, wantName: "memory_working_set_bytes", wantValues: []float64{100, 200},
		},
		{
			name:      "prometheus matrix value pairs",
			content:   `{"result":[{"metric":{"__name__":"up","job":"api"},"values":[[1760954460,"0"],[1760954400,"1"]]}]}`,
			wantCount: 1, wantName: "up{job=api}", wantValues: []float64{1, 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			series, notes, err := parseMetricSeriesFile([]byte(tc.content), tc.statistic)
			require.NoError(t, err)
			require.Len(t, series, tc.wantCount)
			if tc.wantName != "" {
				assert.Equal(t, tc.wantName, series[0].name)
			}
			assert.Equal(t, tc.wantStat, series[0].statistic)
			got := make([]float64, 0, len(series[0].points))
			for i, p := range series[0].points {
				got = append(got, p.v)
				if i > 0 {
					assert.True(t, series[0].points[i-1].at.Before(p.at), "points sorted oldest first")
				}
			}
			assert.Equal(t, tc.wantValues, got)
			if tc.wantNote != "" {
				assert.Contains(t, strings.Join(notes, "; "), tc.wantNote)
			}
		})
	}
}

func TestParseMetricSeriesFile_Errors(t *testing.T) {
	tests := []struct {
		name, content, statistic, want string
	}{
		{"not json", "hello world", "", "Supported shapes"},
		{"json without series", `{"a":1,"b":[1,2]}`, "", "no time series found"},
		{"missing statistic", `{"Label":"x","Datapoints":[{"Timestamp":"2026-10-20T10:00:00Z","Average":1}]}`, "p99", `statistic "p99" not present (available: Average)`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseMetricSeriesFile([]byte(tc.content), tc.statistic)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestMsTime_UnitDetection(t *testing.T) {
	want := time.Date(2025, 10, 9, 8, 53, 20, 0, time.UTC) // unix 1760000000
	tests := []struct {
		name string
		in   any
	}{
		{"unix seconds", float64(1760000000)},
		{"unix milliseconds", float64(1760000000000)},
		{"unix microseconds", float64(1760000000000000)},
		{"unix seconds as string", "1760000000"},
		{"unix ms as json.Number", json.Number("1760000000000")},
		{"RFC3339", "2025-10-09T08:53:20Z"},
		{"RFC3339 with offset", "2025-10-09T14:23:20+05:30"},
		{"RFC3339 nanos", "2025-10-09T08:53:20.000Z"},
		{"no zone", "2025-10-09T08:53:20"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := msTime(tc.in)
			require.True(t, ok)
			assert.True(t, want.Equal(got), "got %s", got)
		})
	}
	for _, bad := range []any{nil, "yesterday", math.NaN(), float64(-5), true} {
		_, ok := msTime(bad)
		assert.False(t, ok, "%v", bad)
	}
}

func TestMsNumber(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want float64
		ok   bool
	}{
		{1.5, 1.5, true}, {"2.5", 2.5, true}, {json.Number("3"), 3, true},
		{"NaN", 0, false}, {"+Inf", 0, false}, {nil, 0, false}, {"abc", 0, false}, {math.Inf(1), 0, false},
	} {
		got, ok := msNumber(tc.in)
		assert.Equal(t, tc.ok, ok, "%v", tc.in)
		assert.Equal(t, tc.want, got, "%v", tc.in)
	}
}

const msPromFixture = `{"sum by (pod) (container_memory_working_set_bytes)":[` +
	`{"metric":{"pod":"api-1","namespace":"prod"},"timestamps":[1760954400,1760954460,1760954520],"values":[1,2,3]},` +
	`{"metric":{"pod":"api-2","namespace":"prod"},"timestamps":[1760954400,1760954460,1760954520],"values":[4,5,6]},` +
	`{"metric":{"pod":"worker-1","namespace":"jobs"},"timestamps":[1760954400,1760954460,1760954520],"values":[7,8,9]}]}`

func TestMsMatches(t *testing.T) {
	series, _, err := parseMetricSeriesFile([]byte(msPromFixture), "")
	require.NoError(t, err)
	tests := []struct {
		match string
		want  []string
	}{
		{"", []string{"api-1", "api-2", "worker-1"}},
		{"pod=api-2", []string{"api-2"}},
		{"POD=API", []string{"api-1", "api-2"}},
		{"namespace=jobs", []string{"worker-1"}},
		{"worker", []string{"worker-1"}},
		{"pod=nothing", nil},
	}
	for _, tc := range tests {
		t.Run(tc.match, func(t *testing.T) {
			var got []string
			for _, s := range series {
				if msMatches(s, tc.match) {
					got = append(got, s.labels["pod"])
				}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// callAnalyzeMetricSeries runs the tool with args given as JSON, so argument
// types match what the executor passes.
func callAnalyzeMetricSeries(t *testing.T, argsJSON string, files map[string]string) (core.NBToolResponse, map[string]any) {
	t.Helper()
	orig := readMetricSeriesFile
	t.Cleanup(func() { readMetricSeriesFile = orig })
	readMetricSeriesFile = func(_ core.NbToolContext, path string, _ int64) ([]byte, error) {
		if path == "huge.json" {
			return nil, errMetricSeriesFileTooLarge
		}
		content, ok := files[path]
		if !ok {
			return nil, errors.New("workspace: file not found")
		}
		return []byte(content), nil
	}
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(argsJSON), &args))
	resp, err := MetricSeriesAnalysisTool{}.Call(core.NbToolContext{}, core.NBToolCallRequest{Arguments: args})
	require.NoError(t, err)
	var out map[string]any
	if resp.Status == core.NBToolResponseStatusSuccess {
		require.Equal(t, core.NBToolResponseTypeJson, resp.Type)
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprint(resp.Data)), &out))
	}
	return resp, out
}

func TestAnalyzeMetricSeriesTool_Call(t *testing.T) {
	var many strings.Builder
	many.WriteString(`{"q":[`)
	for i := 0; i < 12; i++ {
		if i > 0 {
			many.WriteString(",")
		}
		fmt.Fprintf(&many, `{"metric":{"pod":"p%d"},"timestamps":[1760954400,1760954460],"values":[1,2]}`, i)
	}
	many.WriteString(`]}`)
	files := map[string]string{"metrics_prometheus_1.json": msPromFixture, "many.json": many.String(), "bad.txt": "no json here"}

	tests := []struct {
		name      string
		args      string
		wantError string
		check     func(t *testing.T, out map[string]any)
	}{
		{
			name: "file_ref with match and thresholds",
			args: `{"file_ref":"metrics_prometheus_1.json","match":"pod=api-2","thresholds":[{"value":5.5,"direction":"above","label":"limit"}]}`,
			check: func(t *testing.T, out map[string]any) {
				assert.Equal(t, "metrics_prometheus_1.json", out["source"])
				assert.EqualValues(t, 3, out["series_found"])
				assert.EqualValues(t, 1, out["series_matched"])
				assert.EqualValues(t, 1, out["series_analyzed"])
				s := out["series"].([]any)[0].(map[string]any)
				assert.Contains(t, s["name"], "pod=api-2")
				th := s["thresholds"].([]any)[0].(map[string]any)
				assert.Equal(t, true, th["currently_past"])
				assert.Equal(t, "already past", th["not_forecast_reason"])
			},
		},
		{
			name: "thresholds sent as a JSON string",
			args: `{"file_ref":"metrics_prometheus_1.json","match":"worker","thresholds":"[{\"value\":100,\"label\":\"cap\"}]"}`,
			check: func(t *testing.T, out map[string]any) {
				th := out["series"].([]any)[0].(map[string]any)["thresholds"].([]any)[0].(map[string]any)
				assert.Equal(t, "above", th["direction"])
				assert.Equal(t, "cap", th["label"])
			},
		},
		{
			name: "at most 10 series analyzed",
			args: `{"file_ref":"many.json"}`,
			check: func(t *testing.T, out map[string]any) {
				assert.EqualValues(t, 12, out["series_matched"])
				assert.EqualValues(t, 10, out["series_analyzed"])
				assert.EqualValues(t, 2, out["series_skipped"])
				assert.Contains(t, fmt.Sprint(out["notes"]), "first 10 of 12")
			},
		},
		{
			name: "inline points pairs",
			args: `{"points":[[1760954400,1],[1760954460,"2"],["2025-10-20T10:02:00Z",3]]}`,
			check: func(t *testing.T, out map[string]any) {
				assert.Equal(t, "points", out["source"])
				s := out["series"].([]any)[0].(map[string]any)
				assert.EqualValues(t, 3, s["points"])
			},
		},
		{
			name: "inline points object as a JSON string",
			args: `{"points":"{\"timestamps\":[1760954400000,1760954460000],\"values\":[1,2]}"}`,
			check: func(t *testing.T, out map[string]any) {
				assert.EqualValues(t, 2, out["series"].([]any)[0].(map[string]any)["points"])
			},
		},
		{name: "neither file_ref nor points", args: `{}`, wantError: "Pass file_ref"},
		{name: "unknown file", args: `{"file_ref":"metrics_prometheus_9.json"}`, wantError: "do not guess a name"},
		{name: "oversized file", args: `{"file_ref":"huge.json"}`, wantError: "larger than 20 MB"},
		{name: "unparseable file", args: `{"file_ref":"bad.txt"}`, wantError: "Supported shapes"},
		{name: "match selects nothing", args: `{"file_ref":"metrics_prometheus_1.json","match":"pod=zzz"}`, wantError: `No series matched "pod=zzz" among 3 series`},
		{name: "bad threshold direction", args: `{"points":[[1760954400,1]],"thresholds":[{"value":1,"direction":"sideways"}]}`, wantError: `direction must be "above" or "below"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := callAnalyzeMetricSeries(t, tc.args, files)
			if tc.wantError != "" {
				assert.Equal(t, core.NBToolResponseStatusError, resp.Status)
				assert.Contains(t, fmt.Sprint(resp.Data), tc.wantError)
				return
			}
			require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status, resp.Data)
			tc.check(t, out)
		})
	}
}

func TestAnalyzeMetricSeriesTool_Registration(t *testing.T) {
	tool, ok := core.GetNBTool("00000000-0000-0000-0000-000000000001", ToolAnalyzeMetricSeries)
	require.True(t, ok)
	assert.Equal(t, ToolAnalyzeMetricSeries, tool.Name())
	assert.Equal(t, core.NBToolTypeTool, tool.GetType())
	assert.Equal(t, core.ToolApprovalNever, core.ApprovalMode(tool))
	_, declared := core.AccountScopeOf(ToolAnalyzeMetricSeries)
	assert.True(t, declared)
	desc := tool.Description()
	for _, want := range []string{"getting worse", "next threshold", "inside normal operation", "never guess", "Quote only numbers it returns", "cite this step"} {
		assert.Contains(t, desc, want)
	}
	schema := tool.InputSchema()
	for _, key := range []string{"file_ref", "points", "thresholds", "match", "statistic"} {
		assert.Contains(t, schema.Properties, key)
	}
}

// Gemini is the strictest provider schema validator; the nested item schemas
// of points and thresholds must convert.
func TestAnalyzeMetricSeriesTool_SchemaConvertsForGemini(t *testing.T) {
	schema := MetricSeriesAnalysisTool{}.InputSchema()
	props := map[string]any{}
	for k, p := range schema.Properties {
		prop := map[string]any{"type": string(p.Type), "description": p.Description}
		if len(p.Items) > 0 {
			prop["items"] = p.Items
		}
		props[k] = prop
	}
	params := map[string]any{"type": "object", "properties": props, "required": []string{}}
	err := googleai.ValidateTools([]llms.Tool{{Type: "function", Function: &llms.FunctionDefinition{
		Name: ToolAnalyzeMetricSeries, Description: "d", Parameters: params,
	}}})
	assert.NoError(t, err)
}
