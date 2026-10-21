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
	// actionPermission is the "<module>:<Class>" grant key the app gateway
	// classified the current RPC action as (empty outside a gateway-forwarded
	// action). It describes this call, not the caller, so it is kept here rather
	// than on the SecurityContext, which is serialized into the cache and passed
	// to other services. The checks themselves live on SecurityContext.
	actionPermission string
}

// SetActionPermission records the current action's classified grant key.
func (rc *RequestContext) SetActionPermission(permission string) {
	rc.actionPermission = permission
}

// CanAccessAccount checks the caller against the current action's grant key.
// See SecurityContext.CanAccessAccount.
func (rc *RequestContext) CanAccessAccount(accountId string, access SecurityAccessType) bool {
	if rc == nil {
		return false
	}
	return rc.securityContext.CanAccessAccount(accountId, access, rc.actionPermission)
}

// CanAccessTenant checks the caller against the current action's grant key.
// See SecurityContext.CanAccessTenant.
func (rc *RequestContext) CanAccessTenant(access SecurityAccessType) bool {
	if rc == nil {
		return false
	}
	return rc.securityContext.CanAccessTenant(access, rc.actionPermission)
}

func (rc *RequestContext) GetSecurityContext() *SecurityContext {
	return rc.securityContext
}

// GetLogger never returns nil. The plain NewRequestContext constructor accepts
// whatever logger it is handed (including nil), and some background/test
// contexts pass one. Falling back to slog.Default() here keeps panic-recovery
// paths — which log via ctx.GetLogger() from inside a recover() block — from
// triggering a second, unrecovered nil-dereference panic that would crash the
// process. The nil-receiver guard covers a nil *RequestContext for the same
// reason.
func (rc *RequestContext) GetLogger() *slog.Logger {
	if rc == nil || rc.logger == nil {
		return slog.Default()
	}
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
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForTenantAdmin(tenantId), logger: logger, tracer: trace, meter: meter}
}

func NewRequestContextForUserTenant(userId string, tenantId string, logger *slog.Logger, trace *trace.Tracer, meter *metric.Meter) *RequestContext {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestContext{context: context.Background(), securityContext: NewSecurityContextForTenantAdminWithUserId(tenantId, userId), logger: logger, tracer: trace, meter: meter}
}
