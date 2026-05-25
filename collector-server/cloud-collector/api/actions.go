package api

import (
	"context"
	"log/slog"
	"nudgebee/collector/cloud/account"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type ActionCronRequest struct {
	Comment       string                 `json:"comment"`
	Id            string                 `json:"id"`
	Name          string                 `json:"name"`
	Payload       map[string]interface{} `json:"payload"`
	ScheduledTime string                 `json:"scheduled_time"`
}

func buildContextFromCronPayload(c *gin.Context, h *ActionCronRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) *security.RequestContext {
	var ctx context.Context
	if c != nil && c.Request != nil && c.Request.Context() != nil {
		ctx = c.Request.Context()
	} else {
		ctx = context.Background()
	}
	span := trace.SpanFromContext(ctx)
	childLogger := logger.With("cron_job", h.Name, "cron_id", h.Id, "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, security.NewSecurityContextForSuperAdmin(), childLogger, tracer, meter)
}

func handleHasura(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/hasura")
	groupV2.POST("/hasura-cron", func(c *gin.Context) {
		var actionPayload ActionCronRequest
		err := c.ShouldBindJSON(&actionPayload)
		if err != nil {
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}
		ctx := buildContextFromCronPayload(c, &actionPayload, tracer, meter, logger)
		switch actionPayload.Name {
		case "cloud-account-usage-report":
			go account.StoreDailyUsageReportForAllAccounts(ctx)
			c.JSON(200, gin.H{"status": "ok"})
		case "cloud-account-events":
			go account.StoreEventsForAllAccounts(ctx)
			c.JSON(200, gin.H{"status": "ok"})
		case "cloud-account-webhook-sync":
			go account.SyncGCPMonitoringWebhooks(ctx)
			c.JSON(200, gin.H{"status": "ok"})
		default:
			c.JSON(400, []common.Error{
				{
					Message: "Invalid cron job",
				},
			})
			return
		}
	})
}
