package tools

import (
	"context"
	"encoding/json"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #40909: the chart a prometheus_execute result points at carries the query's own values.
func TestPromChartNotes_ChartIsTheQueriedData(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContext(context.Background(), nil, nil, nil, nil)}
	seriesByQuery := map[string][]any{
		"node_cpu": {
			map[string]any{"metric": map[string]any{"node": "a"}, "timestamps": []any{1792231200.0, 1792231500.0}, "values": []float64{0.25, 0.75}},
			map[string]any{"metric": map[string]any{"node": "b"}, "timestamps": []any{1792231200.0, 1792231500.0}, "values": []float64{0.5, 0.1}},
		},
		"empty": {},
	}

	notes, charts := promChartNotes(ctx, []string{"node_cpu", "empty"}, seriesByQuery)

	require.Len(t, notes, 1, "no chart for a query without data")
	refs := core.MetricChartRefs(notes[0])
	require.Len(t, refs, 1)
	require.Len(t, charts, 1)
	out := core.ResolveMetricCharts("```nb-chart\n{\"chart_ref\":\""+refs[0]+"\"}\n```", func(ref string) ([]byte, bool) {
		b, ok := charts[ref]
		return b, ok
	})
	body := strings.TrimSuffix(strings.TrimPrefix(out, "```nb-chart\n"), "\n```")
	var spec struct {
		Labels []string `json:"labels"`
		Series []struct {
			Key  string    `json:"key"`
			Data []float64 `json:"data"`
		} `json:"series"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &spec), out)
	assert.Equal(t, []string{"10:00", "10:05"}, spec.Labels)
	require.Len(t, spec.Series, 2)
	assert.Equal(t, "a", spec.Series[0].Key)
	assert.Equal(t, []float64{0.25, 0.75}, spec.Series[0].Data)
	assert.Equal(t, []float64{0.5, 0.1}, spec.Series[1].Data)
}

// Review #41537: values the stats pass left as []any still chart.
func TestPromChartNotes_UnconvertedValues(t *testing.T) {
	ctx := core.NbToolContext{Ctx: security.NewRequestContext(context.Background(), nil, nil, nil, nil)}
	seriesByQuery := map[string][]any{"q": {
		map[string]any{"metric": map[string]any{}, "timestamps": []any{1792231200.0, 1792231500.0}, "values": []any{1.5, 2.5}},
	}}

	notes, charts := promChartNotes(ctx, []string{"q"}, seriesByQuery)

	require.Len(t, notes, 1)
	for _, c := range charts {
		assert.Contains(t, string(c), `"data":[1.5,2.5]`)
	}
}

// Units come from the metric name only when nothing in the query changes them.
func TestPromChartFormat(t *testing.T) {
	for query, want := range map[string]string{
		`container_memory_working_set_bytes{namespace="x"}`:                                     "bytes",
		`sum by (pod) (container_memory_working_set_bytes{namespace="x"})`:                      "bytes",
		`rate(container_network_receive_bytes_total[5m])`:                                       "bytes_per_second",
		`sum(rate(container_cpu_usage_seconds_total{pod="p"}[5m])) by (pod)`:                    "number", // cores
		`histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[5m])) by (le))`: "seconds",
		`process_cpu_seconds_total`:                                                             "seconds",
		`100 - (avg by (instance) (irate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`:      "number",
		`rate(a_bytes_total[5m]) / rate(b_bytes_total[5m])`:                                     "number",
		`count(container_memory_working_set_bytes)`:                                             "number",
		`count_over_time(container_memory_working_set_bytes[1h])`:                               "number",
		`kube_pod_container_status_restarts_total`:                                              "number",
		`not promql at all`: "number",
	} {
		assert.Equal(t, want, promChartFormat(query), query)
	}
}
