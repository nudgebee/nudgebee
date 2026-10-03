package metrics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectHistogram records one value through Init's real instruments and hands
// back the resulting histogram point.
func collectHistogram(t *testing.T, name string, record func(ctx context.Context)) metricdata.HistogramDataPoint[float64] {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	require.NoError(t, Init(provider.Meter("test")))

	record(context.Background())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok, "%s is not a float64 histogram", name)
			require.Len(t, hist.DataPoints, 1)
			return hist.DataPoints[0]
		}
	}
	t.Fatalf("metric %q was not collected", name)
	return metricdata.HistogramDataPoint[float64]{}
}

// The defect this guards: OTel's default boundaries start at 5s, which is
// wider than almost every relay request. Under them 98.9% of production
// traffic landed in one bucket and every quantile below p99 read ~4.75s. A
// sub-second value must therefore fall below the 5s boundary, not at it.
func TestRequestDurationResolvesSubSecondLatency(t *testing.T) {
	dp := collectHistogram(t, "nb_relay_request_duration_seconds", func(ctx context.Context) {
		RequestLatency.Record(ctx, 0.273) // the measured prod mean for prometheus_queries_enricher
	})

	require.Equal(t, requestBuckets, dp.Bounds)

	// 0.273s belongs strictly inside the boundaries, not lumped with everything
	// else under 5s.
	idx := bucketIndex(dp.Bounds, 0.273)
	assert.Equal(t, uint64(1), dp.BucketCounts[idx])
	assert.Less(t, dp.Bounds[idx], 5.0, "sub-second latency must resolve below the old first boundary")
}

func TestRequestBucketsReachTheWriteTimeoutCeiling(t *testing.T) {
	// 180s is RELAY_HTTP_WRITE_TIMEOUT, the longest a request can legitimately
	// take, so it must be representable rather than falling into +Inf.
	assert.Equal(t, 180.0, requestBuckets[len(requestBuckets)-1])
}

func TestAgentRTTAndWSRequestShareRequestBuckets(t *testing.T) {
	rtt := collectHistogram(t, "nb_relay_agent_rtt_seconds", func(ctx context.Context) {
		AgentRTT.Record(ctx, 0.05)
	})
	assert.Equal(t, requestBuckets, rtt.Bounds)

	ws := collectHistogram(t, "nb_relay_ws_request_duration_seconds", func(ctx context.Context) {
		WS_RequestDuration.Record(ctx, 0.05)
	})
	assert.Equal(t, requestBuckets, ws.Bounds)
}

// Sessions live for hours, so they get their own scale — under requestBuckets
// every healthy registration would pile into the final bucket.
func TestSessionDurationUsesSessionBuckets(t *testing.T) {
	dp := collectHistogram(t, "nb_relay_ws_session_duration_seconds", func(ctx context.Context) {
		WS_SessionDuration.Record(ctx, 10097) // measured mean session lifetime on dev
	})

	require.Equal(t, sessionBuckets, dp.Bounds)
	idx := bucketIndex(dp.Bounds, 10097)
	assert.Equal(t, uint64(1), dp.BucketCounts[idx])
	assert.Less(t, idx, len(dp.BucketCounts)-1, "a ~3h session must not land in the overflow bucket")
}

// bucketIndex returns the index into BucketCounts that v falls in, mirroring
// OTel's semantics: bucket i counts values in (bounds[i-1], bounds[i]], and the
// final bucket is everything above the last boundary.
func bucketIndex(bounds []float64, v float64) int {
	for i, b := range bounds {
		if v <= b {
			return i
		}
	}
	return len(bounds)
}
