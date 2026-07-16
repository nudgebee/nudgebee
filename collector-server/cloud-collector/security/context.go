package security

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type RequestContext struct {
	securityContext *SecurityContext
	logger          *slog.Logger
	tracer          *trace.Tracer
	meter           *metric.Meter
	context         context.Context
}

func (rc *RequestContext) GetSecurityContext() *SecurityContext {
	return rc.securityContext
}

func (rc *RequestContext) GetLogger() *slog.Logger {
	return rc.logger
}

func (rc *RequestContext) GetTracer() *trace.Tracer {
	return rc.tracer
}

func (rc *RequestContext) GetMeter() *metric.Meter {
	return rc.meter
}

func (rc *RequestContext) GetContext() context.Context {
	return rc.context
}

func (rc *RequestContext) GetTraceId() string {
	span := trace.SpanFromContext(rc.context)
	return span.SpanContext().TraceID().String()
}

func NewRequestContext(ctx context.Context, securityContext *SecurityContext, logger *slog.Logger, tracer *trace.Tracer, meter *metric.Meter) *RequestContext {
	if logger == nil {
		logger = slog.Default()
	}
	// Stamp the active trace_id / span_id onto the logger so every line logged
	// through this context correlates with the rest of the platform in Loki.
	// Only when a real span is present — a background context leaves the logger
	// untouched rather than emitting an all-zero trace_id.
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		logger = logger.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
	}
	return &RequestContext{context: ctx, securityContext: securityContext, logger: logger, tracer: tracer, meter: meter}
}

func NewRequestContextForTenantAdmin(tenantId string) *RequestContext {
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForSuperAdminWithTenant(tenantId), logger: slog.Default(), tracer: nil, meter: nil}
}

func NewRequestContextForSuperAdmin() *RequestContext {
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForSuperAdmin(), logger: slog.Default(), tracer: nil, meter: nil}
}
