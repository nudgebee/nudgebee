package api

import (
	"log/slog"
	"nudgebee/services/common"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// HealthResponse is the body returned by the liveness endpoint.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// HealthCheck godoc
// @Summary      Liveness probe
// @Description  Returns 200 OK when the service process is up. Does not verify downstream dependencies.
// @Tags         system
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Router       /health [get]
func handleHeathCheckApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	r.GET("/health", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "health")
		c.JSON(200, gin.H{"status": "ok"})
	})
}
