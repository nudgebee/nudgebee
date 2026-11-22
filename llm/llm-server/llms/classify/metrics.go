package classify

import (
	"context"
	"log/slog"
	"sync"

	"nudgebee/llm/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Classification metrics. Same meter and names as when they were created with
// agents/core's metrics, so dashboards and alerts are unaffected.
var (
	metricsClassificationDecision        metric.Int64Counter
	metricsClassificationDecisionLatency metric.Float64Histogram
	initMetricsOnce                      sync.Once
)

// classificationDecisionLatencyBuckets spans a fast decision-model answer through a
// slow LLM call with retries (production p95 for workflow intent was ~7 s).
var classificationDecisionLatencyBuckets = metric.WithExplicitBucketBoundaries(
	0.1, 0.25, 0.5, 1, 2, 3, 5, 8, 13, 20,
)

func initMetrics() {
	initMetricsOnce.Do(func() {
		meter := otel.Meter(config.SERVICE_NAME)
		var err error
		metricsClassificationDecision, err = meter.Int64Counter(
			"nb_llm_classification_decisions",
			metric.WithDescription("Classification decisions, labeled by task, backend, valid and ok"),
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_classification_decisions metric", "error", err)
		}
		metricsClassificationDecisionLatency, err = meter.Float64Histogram(
			"nb_llm_classification_decision_latency",
			metric.WithDescription("Classification decision latency, labeled by task and backend"),
			metric.WithUnit("s"),
			classificationDecisionLatencyBuckets,
		)
		if err != nil {
			slog.Error("metrics: failed to create nb_llm_classification_decision_latency metric", "error", err)
		}
	})
}

// MetricsClassificationDecision records one Classify outcome.
func MetricsClassificationDecision(task, backend string, valid, ok bool, seconds float64) {
	initMetrics()
	if metricsClassificationDecision != nil {
		metricsClassificationDecision.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("task", task),
			attribute.String("backend", backend),
			attribute.Bool("valid", valid),
			attribute.Bool("ok", ok),
		))
	}
	if metricsClassificationDecisionLatency != nil {
		metricsClassificationDecisionLatency.Record(context.Background(), seconds, metric.WithAttributes(
			attribute.String("task", task),
			attribute.String("backend", backend),
		))
	}
}
