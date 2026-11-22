package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A step and a rate window are different quantities, and they stop agreeing once the
// step grows. A caller sizing a bytes-per-day bucket sends a one-day step; letting that
// become the rate window made cpu_usage_trend `rate(node_cpu_seconds_total[1d])`, which
// on an autoscaling cluster extrapolates across every node that came and went during
// the day. Measured against a dev cluster over 7 days: 136.9 cores on an 80-core
// cluster — a utilisation tile rendering 171% — where a 30-minute window reads 15.1.
func TestResolveAggWindows_RateWindowIsCappedButTheStepIsNot(t *testing.T) {
	const day = 24 * 3600 * 1000

	// Rightsizing's 75s step is below the ceiling and must survive exactly: a
	// percentile taken over 5-minute averages is a different number from one taken
	// over 75-second averages, which is why the rate window follows the step at all.
	_, stepW, rateW := resolveAggWindows(0, 14*day, 75)
	assert.Equal(t, "75s", stepW)
	assert.Equal(t, "75s", rateW, "a step under the ceiling is the rate window")

	// A one-day step keeps its step — Elasticsearch sizes its date_histogram buckets
	// from it and returns too_many_buckets_exception without one — while the rate
	// window stops at the ceiling.
	_, stepW, rateW = resolveAggWindows(0, 7*day, 86400)
	assert.Equal(t, "86400s", stepW, "the sampling interval is the caller's to choose")
	assert.Equal(t, "1800s", rateW, "a rate over a whole day extrapolates across node churn")

	// Exactly at the ceiling, nothing is clamped.
	_, _, rateW = resolveAggWindows(0, 7*day, promMaxRateWindowSeconds)
	assert.Equal(t, "1800s", rateW)
}

// OpenObserve is the other consumer of the rate window: its cluster aggregates render
// `sum(rate(<selector>[<rateWindow>]))` from meta.RateWindow, so the cap has to reach
// it too. Its dev integration cannot be decrypted locally, so this pins the wiring that
// a live check could not.
func TestOpenObserveClusterQuery_UsesTheCappedRateWindow(t *testing.T) {
	_, _, rateW := resolveAggWindows(0, 7*24*3600*1000, 86400)

	candidate := OpenObserveClusterCandidates[ooRoleCPUUsage][0]
	query := buildOpenObserveClusterQuery(candidate, rateW)

	assert.Contains(t, query, "[1800s]",
		"OpenObserve rates over the capped window, not the caller's day-long step")
	assert.NotContains(t, query, "[86400s]")
}

// The workload latency p99 rates over the request's window, not a fixed 5m. The anomaly
// detector asks for 7 days at a 10m step; at a 5m window that query read every sample
// of a high-cardinality histogram 2.5 times and, at the old 2m step, held 8M samples in
// memory for one workload — the hourly burst OOM-killed Prometheus (#43354). A chart
// with no explicit step must keep the 5m window it has always had.
func TestWorkloadHTTPLatencyP99_RatesOverTheRequestWindow(t *testing.T) {
	const hour = 3600 * 1000
	latency := func(startMs, endMs int64, step int) string {
		meta := RequestMetadata{Kind: "deployment", Name: "web", Namespace: "shop"}
		meta.RangeWindow, meta.Step, meta.RateWindow = resolveAggWindows(startMs, endMs, step)
		return buildPrometheusWorkloadQueries(meta, []string{string(KeyWorkloadHTTPLatencyP99)})[string(KeyWorkloadHTTPLatencyP99)]
	}

	anomaly := latency(0, 7*24*hour, 600)
	assert.Contains(t, anomaly, "[600s]", "the detector's 10m step is the rate window")
	assert.NotContains(t, anomaly, "[5m]")

	assert.Contains(t, latency(0, hour, 0), "[300s]", "an evidence chart keeps its 5m window")
}
