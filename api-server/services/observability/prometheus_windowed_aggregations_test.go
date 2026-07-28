package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ----------------------------------------------------------------------------
// Cluster-level aggregations (cpu_real / p50_cpu / p90_cpu / max_usage_cpu /
// max_usage_mem / cpu_usage_trend). These are windowed by the picker range and the
// CPU percentile/peak queries must sum-across-series BEFORE aggregating over time,
// so the result stays bounded by physical capacity (no >100%).
// ----------------------------------------------------------------------------

func TestBuildPrometheusWorkloadQueries_ClusterAggregationsWindowed(t *testing.T) {
	// Windows as promAggWindow would produce for a 24h picker range.
	meta := RequestMetadata{RangeWindow: "86400s", Step: "288s", RateWindow: "300s"}

	cases := []struct {
		name     string
		metric   string
		expected string
	}{
		{
			name:     "cpu_real averages over the picker range",
			metric:   "cpu_real",
			expected: `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[86400s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[86400s]))`,
		},
		{
			name:     "p90_cpu sums first then quantile_over_time (bounded)",
			metric:   "p90_cpu",
			expected: `quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s])`,
		},
		{
			name:     "p50_cpu sums first then quantile_over_time (bounded)",
			metric:   "p50_cpu",
			expected: `quantile_over_time(0.50, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s]) or quantile_over_time(0.50, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s])`,
		},
		{
			name:     "max_usage_cpu sums first then max_over_time (bounded)",
			metric:   "max_usage_cpu",
			expected: `max_over_time(sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s]) or max_over_time(sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))[86400s:288s])`,
		},
		{
			name:     "max_usage_mem windows the subquery by the picker range",
			metric:   "max_usage_mem",
			expected: `max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[86400s:288s])`,
		},
		{
			name:     "cpu_usage_trend is the instantaneous usage expression (short rate window)",
			metric:   "cpu_usage_trend",
			expected: `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[300s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[300s]))`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPrometheusWorkloadQueries(meta, []string{tc.metric})
			assert.Equal(t, tc.expected, got[tc.metric])
		})
	}
}

func TestBuildPrometheusWorkloadQueries_ClusterAggregationsFallbackTo24h(t *testing.T) {
	// Direct-constructed metadata (as unit tests / non-picker callers use) has no
	// window fields; the builder falls back to a 24h window at 5m resolution.
	got := buildPrometheusWorkloadQueries(RequestMetadata{}, []string{"cpu_real", "p90_cpu"})
	assert.Equal(t, `sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[24h])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[24h]))`, got["cpu_real"])
	assert.Equal(t, `quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[5m]))[24h:5m]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[5m]))[24h:5m])`, got["p90_cpu"])
}

func TestPromAggWindow(t *testing.T) {
	const ms = int64(1000)

	cases := []struct {
		name                        string
		startMs, endMs              int64
		wantRange, wantStep, wantRt string
	}{
		{"zero range falls back to 24h", 0, 0, "86400s", "288s", "300s"},
		{"1h range clamps step to 1m, rate to 5m", 0, 3600 * ms, "3600s", "60s", "300s"},
		{"24h range", 0, 24 * 3600 * ms, "86400s", "288s", "300s"},
		{"7d range clamps step and rate to 30m", 0, 7 * 24 * 3600 * ms, "604800s", "1800s", "1800s"},
		{"inverted range falls back to 24h", 5000, 1000, "86400s", "288s", "300s"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRange, gotStep, gotRate := promAggWindow(tc.startMs, tc.endMs)
			assert.Equal(t, tc.wantRange, gotRange)
			assert.Equal(t, tc.wantStep, gotStep)
			assert.Equal(t, tc.wantRt, gotRate)
		})
	}
}
