package api

import (
	"log/slog"
	"nudgebee/services/common"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHeathCheckApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	r.GET("/health", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "health")
		c.JSON(200, gin.H{"status": "ok"})
	})
}
