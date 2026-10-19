package security

import (
	"context"
	"log/slog"
	"strings"

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
	// action). Per-request, so it lives here and never on the cached
	// SecurityContext. Read by CanAccessAccount / CanAccessTenant.
	actionPermission string
}

// SetActionPermission records the current action's classified grant key.
func (rc *RequestContext) SetActionPermission(permission string) {
	rc.actionPermission = permission
}

// CanAccessAccount is the authorization check for an operation on accountId: a
// built-in role that grants `access` on the account (HasAccountAccess), OR a
// dynamic-RBAC custom grant for the current action that applies to the account
// (tenant-wide, or bound to that account).
//
// Use it instead of a bare HasAccountAccess in RPC handlers. The module comes
// from the gateway's classification of the action, so a handler cannot drift
// from the module the role editor grants (the bug class behind 9be4d473bc,
// c6f1820ff2, 3ace9ff156). Outside a gateway-forwarded action it is exactly
// HasAccountAccess. Custom grants never widen data scope: HasScopedPermission
// only admits accounts of the caller's own tenant.
func (rc *RequestContext) CanAccessAccount(accountId string, access SecurityAccessType) bool {
	if rc == nil || rc.securityContext == nil {
		return false
	}
	sc := rc.securityContext
	if sc.HasAccountAccess(accountId, access) {
		return true
	}
	module, class, ok := rc.actionGrant(access)
	if !ok {
		return false
	}
	if sc.HasScopedPermission(accountId, module, class) {
		return true
	}
	// Write implies Read, as at the gateway.
	return class == "Read" && sc.HasScopedPermission(accountId, module, "Write")
}

// CanAccessTenant is the tenant-level counterpart of CanAccessAccount: a
// built-in tenant role (HasTenantAccess — tenant/super admin, read-only flavors
// for reads) OR a TENANT-WIDE custom grant for the current action. An
// account-bound grant never satisfies it — a tenant-level operation has no
// account to bind to.
//
// Never use it for privilege administration (roles, groups, user-role
// assignments): those stay IsTenantAdmin()-only so a custom role cannot escalate
// itself. The gateway classifies those actions as non-grantable, so
// actionPermission is empty for them anyway.
func (rc *RequestContext) CanAccessTenant(access SecurityAccessType) bool {
	if rc == nil || rc.securityContext == nil {
		return false
	}
	sc := rc.securityContext
	if sc.HasTenantAccess(access) {
		return true
	}
	module, class, ok := rc.actionGrant(access)
	if !ok {
		return false
	}
	return sc.HasPermission(module, class) || (class == "Read" && sc.HasPermission(module, "Write"))
}

// actionGrant returns the (module, class) of the current action's grant key when
// it may authorize `access`. A Read-classified action never authorizes a
// mutation: a handler asking for write access under a read classification
// means the classification is wrong, and that fails closed.
func (rc *RequestContext) actionGrant(access SecurityAccessType) (string, string, bool) {
	module, class, found := strings.Cut(rc.actionPermission, ":")
	if !found || module == "" || class == "" {
		return "", "", false
	}
	if class == "Read" && access != SecurityAccessTypeRead {
		return "", "", false
	}
	return module, class, true
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
