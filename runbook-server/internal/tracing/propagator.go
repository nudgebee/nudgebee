// Package tracing carries the W3C trace context through Temporal workflow and
// activity headers, so a distributed trace that enters runbook-server (HTTP or
// RabbitMQ) survives the durable-execution boundary and reaches the activities'
// outbound calls (e.g. the llm.event_investigate MQ publish).
//
// A custom workflow.ContextPropagator is used instead of the SDK's
// contrib/opentelemetry TracingInterceptor deliberately: the interceptor starts
// NEW spans, which are non-recording (invalid span context) under the noop
// tracer provider this service runs with by default (otel_exporter=noop) — so
// propagation would silently break. Carrying the remote span context verbatim,
// the way otelgin and the MQ consumers do, works regardless of exporter.
package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// headerKey is the Temporal header field holding the serialized W3C carrier
// (traceparent / tracestate / baggage).
const headerKey = "nb-trace-context"

// carrierCtxKey stores the carrier map on the workflow.Context between
// ExtractToWorkflow and InjectFromWorkflow. Storing the raw map (not a span)
// keeps workflow code deterministic — the same header bytes are re-carried on
// every replay.
type carrierCtxKey struct{}

// Propagator implements workflow.ContextPropagator for the W3C trace context.
type Propagator struct{}

// NewPropagator returns the trace-context propagator to register on
// client.Options.ContextPropagators (workers created from that client inherit
// it).
func NewPropagator() workflow.ContextPropagator {
	return &Propagator{}
}

// Inject serializes the active trace context into the outgoing Temporal
// header (client → workflow, and activity ctx → child calls). No-op when the
// context carries no span.
func (p *Propagator) Inject(ctx context.Context, hw workflow.HeaderWriter) error {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return p.writeCarrier(map[string]string(carrier), hw)
}

// Extract restores the trace context from the incoming Temporal header into
// the Go context (activity executions, client-side handlers).
func (p *Propagator) Extract(ctx context.Context, hr workflow.HeaderReader) (context.Context, error) {
	if m := p.readCarrier(hr); len(m) > 0 {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(m))
	}
	return ctx, nil
}

// ExtractToWorkflow stashes the raw carrier on the workflow context so
// InjectFromWorkflow can re-emit it towards activities and child workflows.
func (p *Propagator) ExtractToWorkflow(ctx workflow.Context, hr workflow.HeaderReader) (workflow.Context, error) {
	if m := p.readCarrier(hr); len(m) > 0 {
		ctx = workflow.WithValue(ctx, carrierCtxKey{}, m)
	}
	return ctx, nil
}

// InjectFromWorkflow re-emits the stashed carrier into the outgoing header
// (workflow → activity / child workflow).
func (p *Propagator) InjectFromWorkflow(ctx workflow.Context, hw workflow.HeaderWriter) error {
	m, _ := ctx.Value(carrierCtxKey{}).(map[string]string)
	return p.writeCarrier(m, hw)
}

func (p *Propagator) writeCarrier(m map[string]string, hw workflow.HeaderWriter) error {
	if len(m) == 0 {
		return nil
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(m)
	if err != nil {
		// Best-effort: a failed header write must never fail the workflow call.
		return nil
	}
	hw.Set(headerKey, payload)
	return nil
}

// TraceLoggingAttrs returns "trace_id", <id>, "span_id", <id> key-values
// parsed from the carrier stashed by ExtractToWorkflow, for stamping onto
// workflow loggers. Nil when the workflow was started without a propagated
// trace. Deterministic — a pure function of the workflow header — so it is
// safe to call from workflow code under replay.
func TraceLoggingAttrs(ctx workflow.Context) []any {
	m, _ := ctx.Value(carrierCtxKey{}).(map[string]string)
	if len(m) == 0 {
		return nil
	}
	sc := trace.SpanContextFromContext(otel.GetTextMapPropagator().Extract(context.Background(), propagation.MapCarrier(m)))
	if !sc.IsValid() {
		return nil
	}
	return []any{"trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String()}
}

func (p *Propagator) readCarrier(hr workflow.HeaderReader) map[string]string {
	payload, ok := hr.Get(headerKey)
	if !ok || payload == nil {
		return nil
	}
	var m map[string]string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &m); err != nil {
		return nil
	}
	return m
}
