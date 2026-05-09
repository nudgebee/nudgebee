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

func NewRequestContext(context context.Context, securityContext *SecurityContext, logger *slog.Logger, trace *trace.Tracer, meter *metric.Meter) *RequestContext {
	return &RequestContext{context: context, securityContext: securityContext, logger: logger, tracer: trace, meter: meter}
}

func NewRequestContextForSuperAdmin(logger *slog.Logger, trace *trace.Tracer, meter *metric.Meter) *RequestContext {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForSuperAdmin(), logger: logger, tracer: trace, meter: meter}
}

func NewRequestContextForTenantAdmin(tenantId string, logger *slog.Logger, trace *trace.Tracer, meter *metric.Meter) *RequestContext {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForSuperAdminAndTenant(tenantId), logger: logger, tracer: trace, meter: meter}
}

func NewRequestContextForUserTenant(userId string, tenantId string, logger *slog.Logger, trace *trace.Tracer, meter *metric.Meter) *RequestContext {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForSuperAdminAndTenantAndUserId(tenantId, userId), logger: logger, tracer: trace, meter: meter}
}
