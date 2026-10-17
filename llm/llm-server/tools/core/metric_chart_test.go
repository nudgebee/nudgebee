package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testCharts stands in for the tool call rows: conversation -> chart_ref -> spec.
var testCharts = map[string]map[string]json.RawMessage{}

func saveChart(conversationId string, chart MetricChart) (string, error) {
	if testCharts[conversationId] == nil {
		testCharts[conversationId] = map[string]json.RawMessage{}
	}
	return AddMetricChart(testCharts[conversationId], chart)
}

func resolve(conversationId, text string) string {
	return ResolveMetricCharts(text, func(ref string) ([]byte, bool) {
		b, ok := testCharts[conversationId][ref]
		return b, ok
	})
}

func chartSeries(labels map[string]string, start, step int64, values ...float64) MetricChartSeries {
	s := MetricChartSeries{Labels: labels}
	for i, v := range values {
		s.Timestamps = append(s.Timestamps, start+int64(i)*step)
		s.Values = append(s.Values, v)
	}
	return s
}

func TestBuildMetricChart_RanksKeysAndLabels(t *testing.T) {
	// 2026-10-17T10:00:00Z
	const t0 = 1792231200
	var series []MetricChartSeries
	for i := range 8 {
		series = append(series, chartSeries(map[string]string{"__name__": "node_cpu", "node": fmt.Sprintf("n%d", i), "job": "node"}, t0, 300, float64(i), float64(-i*2)))
	}

	chart, ok := BuildMetricChart("cpu", series)

	require.True(t, ok)
	assert.Equal(t, "line", chart.Type)
	assert.Equal(t, "cpu (UTC)", chart.Title)
	assert.Equal(t, []string{"10:00", "10:05"}, chart.Labels)
	require.Len(t, chart.Data, metricChartMaxSeries, "top series only")
	assert.Equal(t, "n7", chart.Data[0].Key, "largest |max| first, keyed by the label that differs")
	assert.Equal(t, []float64{7, -14}, chart.Data[0].Data, "exact backend values")
}

func TestBuildMetricChart_ThinsEvenlyKeepingEnds(t *testing.T) {
	values := make([]float64, 1000)
	for i := range values {
		values[i] = float64(i)
	}
	chart, ok := BuildMetricChart("q", []MetricChartSeries{chartSeries(nil, 1792231200, 15, values...)})

	require.True(t, ok)
	require.Len(t, chart.Labels, metricChartMaxPoints)
	data := chart.Data[0].Data
	assert.Equal(t, 0.0, data[0])
	assert.Equal(t, 999.0, data[len(data)-1])
	for _, v := range data {
		assert.Equal(t, v, float64(int(v)), "points are picked, never averaged")
	}
}

func TestBuildMetricChart_MillisecondsAndLongSpan(t *testing.T) {
	chart, ok := BuildMetricChart("q", []MetricChartSeries{chartSeries(nil, 1792231200000, 2*24*3600*1000, 1, 2)})

	require.True(t, ok)
	assert.Equal(t, []string{"Oct 17 10:00", "Oct 19 10:00"}, chart.Labels)
	assert.Equal(t, "value", chart.Data[0].Key)
}

func TestBuildMetricChart_DropsSeriesRatherThanInventPoints(t *testing.T) {
	big := chartSeries(map[string]string{"pod": "a"}, 100, 60, 9, 9, 9)
	disjoint := chartSeries(map[string]string{"pod": "b"}, 107, 60, 1, 1, 1)

	chart, ok := BuildMetricChart("q", []MetricChartSeries{disjoint, big})

	require.True(t, ok)
	require.Len(t, chart.Data, 1)
	assert.Equal(t, []float64{9, 9, 9}, chart.Data[0].Data)
}

func TestBuildMetricChart_ShortSeriesDoesNotCutTheWindow(t *testing.T) {
	// #40909 E2E: a node with only the last 25 minutes cut a 1-hour chart to 25 minutes.
	full := make([]float64, 60)
	late := make([]float64, 25)
	for i := range full {
		full[i] = 50
	}
	top := chartSeries(map[string]string{"node": "a"}, 1792231200, 60, append(full[:59:59], 99)...)
	other := chartSeries(map[string]string{"node": "b"}, 1792231200, 60, full...)
	short := chartSeries(map[string]string{"node": "c"}, 1792231200+35*60, 60, append(late[:24:24], 98)...)

	chart, ok := BuildMetricChart("q", []MetricChartSeries{short, other, top})

	require.True(t, ok)
	assert.Len(t, chart.Labels, 60, "the whole hour")
	assert.Equal(t, "10:00", chart.Labels[0])
	require.Len(t, chart.Data, 2)
	assert.Equal(t, []string{"a", "b"}, []string{chart.Data[0].Key, chart.Data[1].Key}, "the short series is skipped")
}

func TestResolveMetricCharts_PieKeepsTheRestAsOther(t *testing.T) {
	// #40909 E2E: a pie of the top 6 pods showed each share of those 6, not of all pods.
	var series []MetricChartSeries
	for i := range 8 {
		series = append(series, chartSeries(map[string]string{"pod": fmt.Sprintf("p%d", i)}, 1792231200, 300, 1, float64(10-i)))
	}
	chart, ok := BuildMetricChart("cpu", series)
	require.True(t, ok)
	ref, err := saveChart("conv-pie", chart)
	require.NoError(t, err)

	pie := resolvedSpec(t, resolve("conv-pie", "```nb-chart\n{\"chart_ref\":\""+ref+"\",\"type\":\"pie\"}\n```"))

	assert.Equal(t, []any{"p0", "p1", "p2", "p3", "p4", "p5", "Other"}, pie["labels"])
	assert.Equal(t, []any{10.0, 9.0, 8.0, 7.0, 6.0, 5.0, 7.0}, pie["values"], "Other = 4 + 3")
}

func TestBuildMetricChart_Empty(t *testing.T) {
	_, ok := BuildMetricChart("q", []MetricChartSeries{{Labels: map[string]string{"a": "b"}}})
	assert.False(t, ok)
	_, ok = BuildMetricChart("q", nil)
	assert.False(t, ok)
}

func savedChart(t *testing.T, conversationId string) (string, MetricChart) {
	t.Helper()
	chart, ok := BuildMetricChart("cpu", []MetricChartSeries{
		chartSeries(map[string]string{"node": "a"}, 1792231200, 300, 1, 5),
		chartSeries(map[string]string{"node": "b"}, 1792231200, 300, 2, 3),
	})
	require.True(t, ok)
	ref, err := saveChart(conversationId, chart)
	require.NoError(t, err)
	return ref, chart
}

func resolvedSpec(t *testing.T, text string) map[string]any {
	t.Helper()
	m := nbChartBlockRe.FindStringSubmatch(text)
	require.NotNil(t, m, text)
	var spec map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(m[1])), &spec))
	return spec
}

func TestResolveMetricCharts(t *testing.T) {
	ref, chart := savedChart(t, "conv-1")
	answer := "CPU is flat.\n\n```nb-chart\n{\"chart_ref\":\"" + ref + "\"}\n```\nDone."

	out := resolve("conv-1", answer)

	assert.True(t, strings.HasPrefix(out, "CPU is flat.\n\n```nb-chart\n"))
	assert.True(t, strings.HasSuffix(out, "\n```\nDone."))
	assert.NotContains(t, out, "chart_ref")
	want, _ := json.Marshal(chart)
	assert.Contains(t, out, string(want))
	assert.Equal(t, out, resolve("conv-1", out), "resolving twice is a no-op")
}

func TestResolveMetricCharts_Overrides(t *testing.T) {
	ref, _ := savedChart(t, "conv-2")
	block := func(extra string) string {
		return "```nb-chart\n{\"chart_ref\":\"" + ref + "\"" + extra + "}\n```"
	}

	top := resolvedSpec(t, resolve("conv-2", block(`,"top":1,"type":"area","title":"Node CPU"`)))
	assert.Equal(t, "area", top["type"])
	assert.Equal(t, "Node CPU (UTC)", top["title"])
	assert.Len(t, top["series"], 1)

	pie := resolvedSpec(t, resolve("conv-2", block(`,"type":"pie"`)))
	assert.Equal(t, "pie", pie["type"])
	assert.Equal(t, []any{"a", "b"}, pie["labels"])
	assert.Equal(t, []any{5.0, 3.0}, pie["values"], "each series' latest value")
	assert.Equal(t, "cpu at 10:05 UTC", pie["title"])

	bar := resolvedSpec(t, resolve("conv-2", block(`,"type":"bar"`)))
	assert.Equal(t, []any{map[string]any{"key": "latest", "data": []any{5.0, 3.0}}}, bar["series"])
}

func TestResolveMetricCharts_MissingOrForeign(t *testing.T) {
	ref, _ := savedChart(t, "conv-3")
	block := "```nb-chart\n{\"chart_ref\":\"" + ref + "\"}\n```"

	assert.Equal(t, metricChartMissingNote, resolve("other-conversation", block), "refs are scoped to their conversation")
	assert.Equal(t, metricChartMissingNote, resolve("conv-3", "```nb-chart\n{\"chart_ref\":\"chart_00\"}\n```"))

	finops := "```nb-chart\n{\"type\":\"bar\",\"labels\":[\"a\"],\"series\":[{\"key\":\"x\",\"data\":[1]}]}\n```"
	assert.Equal(t, finops+" chart_ref", resolve("conv-3", finops+" chart_ref"), "blocks without a ref are untouched")
}

func TestStripAndRestoreCharts(t *testing.T) {
	ref, _ := savedChart(t, "conv-4")
	original := resolve("conv-4", "Summary.\n```nb-chart\n{\"chart_ref\":\""+ref+"\"}\n```\nEnd.")

	stripped := StripChartData(original)
	assert.Equal(t, "Summary.\n[chart: cpu (UTC)]\nEnd.", stripped)

	assert.Equal(t, original, RestoreCharts(stripped, original), "back at its placeholder")
	restored := RestoreCharts("Short.", original)
	assert.True(t, strings.HasPrefix(restored, "Short.\n\n```nb-chart\n"), "appended when the rewrite dropped the placeholder")
	assert.Equal(t, "no charts", StripChartData("no charts"))

	finops := "```nb-chart\n{\"type\":\"bar\",\"title\":\"Spend\",\"labels\":[\"a\"],\"series\":[{\"key\":\"x\",\"data\":[1]}]}\n```"
	assert.Equal(t, finops, StripChartData(finops), "hand-written charts stay in history")
	assert.Equal(t, "Short.", RestoreCharts("Short.", finops), "and are not re-added to a rewrite")
}

func TestMetricChartMetadata(t *testing.T) {
	assert.Nil(t, MetricChartMetadata(map[string]json.RawMessage{}), "no metadata without charts")
	charts := map[string]json.RawMessage{}
	ref, err := AddMetricChart(charts, MetricChart{Type: "line", Labels: []string{"a"}})
	require.NoError(t, err)
	b, err := json.Marshal(MetricChartMetadata(charts))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"charts":{"`+ref+`":{"type":"line"`, "saved with the tool call as its metadata")
}

func TestMetricChartRefs(t *testing.T) {
	note := MetricChartNote("chart_ab12", "cpu")
	escaped, _ := json.Marshal(map[string]string{"__chart_note": note + "\n" + MetricChartNote("chart_cd34", "")})

	assert.Equal(t, []string{"chart_ab12", "chart_cd34"}, MetricChartRefs(string(escaped)))
	assert.Equal(t, []string{"chart_ab12"}, MetricChartRefs(note))
	assert.Contains(t, note, "```nb-chart\n{\"chart_ref\":\"chart_ab12\"}\n```")
}
