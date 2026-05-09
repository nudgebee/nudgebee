package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func buildContextFromAuditPayload(c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) *security.RequestContext {
	span := trace.SpanFromContext(c.Request.Context())
	childLogger := logger.With("service", "audit", "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(c.Request.Context(), security.NewSecurityContextForSuperAdmin(), childLogger, tracer, meter)
}

func handleAuditApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/audit")
	groupV2.POST("", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "audit")
		var hasuraPayload audit.AuditRequest
		err := c.ShouldBindJSON(&hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "invalid_json")
			logger.Error("audit: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		context := buildContextFromAuditPayload(c, tracer, meter, logger)
		err = audit.CreateAudit(context, &hasuraPayload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "audit", "create_audit_failed")
			logger.Error("audit: error creating audit", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, gin.H{"status": "ok"})
	})
}
