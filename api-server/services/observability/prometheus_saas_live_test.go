//go:build livePrometheus

// Package-level live verification against a REAL Prometheus, not an httptest
// fake. It proves the pieces the unit tests stub: that the URLs this source
// builds are ones Prometheus actually serves, that a real PromQL response parses
// into the Result shape the charts consume, and that the save-time probe in the
// integrations package succeeds against a genuine engine.
//
// Run with a Prometheus reachable at PROMETHEUS_LIVE_URL, e.g.
//
//	docker run -d -p 19090:9090 prom/prometheus:v3.0.0
//	PROMETHEUS_LIVE_URL=http://localhost:19090 \
//	  go test -tags livePrometheus ./observability/ -run TestLivePrometheus -v
package observability

import (
	"os"
	"testing"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func liveURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("PROMETHEUS_LIVE_URL")
	if url == "" {
		t.Skip("PROMETHEUS_LIVE_URL not set")
	}
	return url
}

// pointSourceAtLive makes the source resolve to the live endpoint without a database.
func pointSourceAtLive(t *testing.T, url string) {
	t.Helper()
	original := loadPrometheusUserConfig
	loadPrometheusUserConfig = func(*security.RequestContext, string) (integrations.PrometheusUserConfig, error) {
		return integrations.PrometheusUserConfig{URL: url, AuthType: integrations.PrometheusAuthNone}, nil
	}
	t.Cleanup(func() { loadPrometheusUserConfig = original })
}

// The save/test-connection path against a real engine.
func TestLivePrometheus_ValidateConfigProbe(t *testing.T) {
	url := liveURL(t)
	errs := (integrations.Prometheus{}).ValidateConfig(nil, []core.IntegrationConfigValue{
		{Name: integrations.PrometheusURLKey, Value: url},
		{Name: integrations.PrometheusAuthTypeKey, Value: integrations.PrometheusAuthNone},
	}, "acc")
	assert.Empty(t, errs, "probe against a live Prometheus must succeed")
}

func TestLivePrometheus_FetchMetricList(t *testing.T) {
	pointSourceAtLive(t, liveURL(t))

	metrics, err := (&PrometheusSaasMetricSource{}).FetchMetricList(&security.RequestContext{}, FetchMetricsListRequest{AccountId: "acc"})
	require.NoError(t, err)
	require.NotEmpty(t, metrics, "a self-scraping Prometheus exposes metric families")

	names := make([]string, 0, len(metrics))
	for _, m := range metrics {
		names = append(names, m.Metric)
	}
	assert.Contains(t, names, "up")
	t.Logf("live: %d metric families, e.g. %v", len(names), names[:min(5, len(names))])
}

func TestLivePrometheus_FetchMetricsQuery_RangeAndInstant(t *testing.T) {
	url := liveURL(t)
	pointSourceAtLive(t, url)

	now := nowMillisForLiveTest()
	source := &PrometheusSaasMetricSource{}

	t.Run("range", func(t *testing.T) {
		out, err := source.FetchMetricsQuery(&security.RequestContext{}, FetchMetricsRequest{
			AccountId:    "acc",
			Queries:      map[string]string{"up": "up"},
			StartTime:    now - 5*60*1000,
			EndTime:      now,
			StepInterval: 15,
		})
		require.NoError(t, err)
		require.Len(t, out.Results, 1)
		require.Nil(t, out.Results[0].Error, "live range query must not error")
		require.NotEmpty(t, out.Results[0].Payload, "expected samples for up")

		point := out.Results[0].Payload[0]
		assert.Equal(t, "up", point.Metric["__name__"])
		require.NotEmpty(t, point.Timestamps)
		// Unix SECONDS: a seconds-vs-milliseconds mix-up is exactly what makes
		// charts render empty, so assert the magnitude explicitly.
		assert.Greater(t, point.Timestamps[0], int64(1_000_000_000))
		assert.Less(t, point.Timestamps[0], int64(100_000_000_000))
		t.Logf("live range: %d series, first ts=%d value=%v", len(out.Results[0].Payload), point.Timestamps[0], point.Values[0])
	})

	t.Run("instant with aggregator", func(t *testing.T) {
		out, err := source.FetchMetricsQuery(&security.RequestContext{}, FetchMetricsRequest{
			AccountId:  "acc",
			Queries:    map[string]string{"up": "up"},
			QueryItems: map[string]QueryItem{"up": {Metric: "up", AggregateOperator: "sum"}},
			EndTime:    now,
			Instant:    true,
		})
		require.NoError(t, err)
		require.Len(t, out.Results, 1)
		require.Nil(t, out.Results[0].Error)
		assert.Equal(t, "sum(up)", out.Results[0].Query)
		require.NotEmpty(t, out.Results[0].Payload)
		t.Logf("live instant: sum(up)=%v", out.Results[0].Payload[0].Values)
	})

	// A bad query must come back as a per-query error, not a transport failure.
	t.Run("invalid promql is reported per query", func(t *testing.T) {
		out, err := source.FetchMetricsQuery(&security.RequestContext{}, FetchMetricsRequest{
			AccountId: "acc",
			Queries:   map[string]string{"bad": "sum(("},
			StartTime: now - 60*1000,
			EndTime:   now,
		})
		require.NoError(t, err)
		require.Len(t, out.Results, 1)
		require.NotNil(t, out.Results[0].Error)
		t.Logf("live error passthrough: %s", *out.Results[0].Error)
	})
}

func TestLivePrometheus_LabelsAndValues(t *testing.T) {
	pointSourceAtLive(t, liveURL(t))
	source := &PrometheusSaasMetricSource{}

	labels, err := source.FetchMetricsLabels(&security.RequestContext{}, FetchMetricLabelsRequest{AccountId: "acc", MetricName: "up"})
	require.NoError(t, err)
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Label)
	}
	assert.Contains(t, names, "job", "up carries a job label")

	values, err := source.FetchMetricLabelValues(&security.RequestContext{}, FetchMetricsLabelValueRequest{
		AccountId: "acc",
		Label:     "job",
		Request:   map[string]any{"metric": "up"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, values)
	t.Logf("live labels for up: %v; job values: %v", names, values)
}

func nowMillisForLiveTest() int64 {
	return time.Now().UnixMilli()
}
