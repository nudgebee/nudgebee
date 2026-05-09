package api

import (
	"log/slog"
	"nudgebee/services/common"
	_ "nudgebee/services/event/queue"
	_ "nudgebee/services/integrations/core/webhook_queue"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// handleHasuraWebhooks registers the /hasura-webhooks endpoint.
// All event triggers have been migrated to application-level code and PostgreSQL triggers.
// This endpoint is kept as a no-op stub to avoid errors if any stale webhooks arrive.
func handleHasuraWebhooks(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	r.POST("/hasura-webhooks", func(c *gin.Context) {
		var payload struct {
			Trigger struct {
				Name string `json:"name"`
			} `json:"trigger"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid json - "+err.Error()))
			return
		}

		logger.Warn("received webhook for migrated trigger", slog.String("trigger", payload.Trigger.Name))
		c.JSON(200, gin.H{"status": "ok"})
	})
}
