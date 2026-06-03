package api

import (
	"errors"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// buildContextForAuditActor builds the RequestContext used to persist a single
// relayed audit. The /v1/audit endpoint is a service-to-service ingest pipe whose
// payload carries each audit's own actor, so the context is derived from that
// actor (NOT a blanket super-admin). This matters: CreateAudit skips super-admin
// contexts, and the prior super-admin context here silently dropped every relayed
// audit (notification rules, messaging-platform config, llm/runbook).
func buildContextForAuditActor(c *gin.Context, tenantId string, userId string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) *security.RequestContext {
	span := trace.SpanFromContext(c.Request.Context())
	childLogger := logger.With("service", "audit", "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(c.Request.Context(), security.NewSecurityContextForAuditRelay(tenantId, userId), childLogger, tracer, meter)
}

func handleAuditApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/audit")
	groupV2.POST("", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "audit")
		var actionPayload audit.AuditRequest
		err := c.ShouldBindJSON(&actionPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "invalid_json")
			logger.Error("audit: error binding request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if len(actionPayload.Audits) == 0 {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "invalid_json")
			c.JSON(400, common.ErrorActionBadRequest("audit: audits is required"))
			return
		}

		// Persist each audit under a context derived from its own actor so the
		// super-admin skip is evaluated against the real actor, not the relay.
		var errs []error
		for i := range actionPayload.Audits {
			a := actionPayload.Audits[i]
			// An audit with no tenant can't be attributed or surfaced in any
			// tenant-scoped view; skip it rather than persist an orphan row.
			if a.TenantId == "" {
				logger.Warn("audit: skipping relayed audit with empty tenant_id", "event_type", a.EventType)
				continue
			}
			actorCtx := buildContextForAuditActor(c, a.TenantId, a.UserId, tracer, meter, logger)
			if err := audit.CreateAudit(actorCtx, &audit.AuditRequest{Audits: []audit.Audit{a}}); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			joined := errors.Join(errs...)
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "create_audit_failed")
			logger.Error("audit: error creating audit", "error", joined)
			c.JSON(400, common.ErrorActionBadRequest(joined.Error()))
			return
		}

		c.JSON(200, gin.H{"status": "ok"})
	})
}
