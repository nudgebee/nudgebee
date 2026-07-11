package egressfilter

// OTel metric definitions.
//
// IMPORTANT — cardinality contract. The labels on every metric below are
// *bounded* by construction: provider/model/mode/status/rule_id are all
// enums or curated identifiers in the dozens at worst. Do NOT add per-call
// or per-tenant labels (tenant_id, account_id, user_id, audit_id, matched
// value) — those make this a wide / high-cardinality metric set and will
// blow up the time-series store. Per-call detail belongs in the structured
// audit log line and in the FilterEvent persisted to message metadata,
// NOT here.

import (
	"context"
	"log/slog"
	"nudgebee/llm/config"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	egressfilterMeter metric.Meter

	metricsEgressFilterScans   metric.Int64Counter
	metricsEgressFilterHits    metric.Int64Counter
	metricsEgressFilterBlocks  metric.Int64Counter
	metricsEgressFilterLatency metric.Float64Histogram
	metricsEgressFilterPayload metric.Int64Histogram

	initMetricsOnce sync.Once
)

// egressfilterLatencyBuckets — the in-process scan should sit well under 5 ms p50.
// Bigger buckets exist to surface pathological inputs without bloating the
// histogram dimension.
var egressfilterLatencyBuckets = metric.WithExplicitBucketBoundaries(
	0.0001, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1,
)

// egressfilterPayloadBuckets covers expected sizes of serialized message bodies.
var egressfilterPayloadBuckets = metric.WithExplicitBucketBoundaries(
	256, 1024, 4096, 16384, 65536, 262144, 1048576,
)

// initMetrics wires the OTel instruments. Safe to call repeatedly; only the
// first call has effect.
func initMetrics() {
	initMetricsOnce.Do(func() {
		egressfilterMeter = otel.Meter(config.SERVICE_NAME)
		var err error

		metricsEgressFilterScans, err = egressfilterMeter.Int64Counter(
			"nb_llm_egressfilter_scans_total",
			metric.WithDescription("Number of outbound LLM payloads scanned by the secret egressfilter"),
		)
		if err != nil {
			slog.Error("egressfilter metrics: failed to create scans counter", "error", err)
		}

		metricsEgressFilterHits, err = egressfilterMeter.Int64Counter(
			"nb_llm_egressfilter_hits_total",
			metric.WithDescription("Number of secret rules that fired, labeled by rule_id"),
		)
		if err != nil {
			slog.Error("egressfilter metrics: failed to create hits counter", "error", err)
		}

		metricsEgressFilterBlocks, err = egressfilterMeter.Int64Counter(
			"nb_llm_egressfilter_blocks_total",
			metric.WithDescription("Number of outbound LLM calls blocked by the egressfilter in enforce mode"),
		)
		if err != nil {
			slog.Error("egressfilter metrics: failed to create blocks counter", "error", err)
		}

		metricsEgressFilterLatency, err = egressfilterMeter.Float64Histogram(
			"nb_llm_egressfilter_latency_seconds",
			metric.WithDescription("Wall-clock latency of a egressfilter scan"),
			metric.WithUnit("s"),
			egressfilterLatencyBuckets,
		)
		if err != nil {
			slog.Error("egressfilter metrics: failed to create latency histogram", "error", err)
		}

		metricsEgressFilterPayload, err = egressfilterMeter.Int64Histogram(
			"nb_llm_egressfilter_payload_bytes",
			metric.WithDescription("Size of the serialized outbound LLM payload that was scanned"),
			metric.WithUnit("By"),
			egressfilterPayloadBuckets,
		)
		if err != nil {
			slog.Error("egressfilter metrics: failed to create payload histogram", "error", err)
		}
	})
}

// recordScan records a single scan attempt with its outcome.
//
// All label values are bounded-cardinality strings: provider/model are
// configured statically, mode is one of two values, status is one of four.
// Per-tenant labels are intentionally omitted to avoid metric explosion;
// tenant attribution lives in slog.
func recordScan(ctx context.Context, provider, model string, mode Mode, status string, payloadBytes int, latencySeconds float64, hits []Hit) {
	initMetrics()

	baseAttrs := []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("mode", string(mode)),
		attribute.String("status", status),
	}
	baseSet := metric.WithAttributes(baseAttrs...)

	if metricsEgressFilterScans != nil {
		metricsEgressFilterScans.Add(ctx, 1, baseSet)
	}
	if metricsEgressFilterLatency != nil {
		metricsEgressFilterLatency.Record(ctx, latencySeconds, baseSet)
	}
	if metricsEgressFilterPayload != nil {
		metricsEgressFilterPayload.Record(ctx, int64(payloadBytes), baseSet)
	}

	if metricsEgressFilterHits != nil {
		ruleCounts := make(map[string]int64, len(hits))
		for _, h := range hits {
			ruleCounts[h.RuleID]++
		}
		for ruleID, n := range ruleCounts {
			metricsEgressFilterHits.Add(ctx, n, metric.WithAttributes(
				attribute.String("provider", provider),
				attribute.String("model", model),
				attribute.String("mode", string(mode)),
				attribute.String("rule_id", ruleID),
			))
		}
	}

	if status == "blocked" && metricsEgressFilterBlocks != nil {
		metricsEgressFilterBlocks.Add(ctx, 1, metric.WithAttributes(
			attribute.String("provider", provider),
			attribute.String("model", model),
		))
	}
}
