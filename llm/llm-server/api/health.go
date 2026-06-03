package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// HealthResponse is the body returned by the liveness endpoint.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// handleHeathCheckApis registers GET /health.
//
// @Summary      Liveness probe
// @Description  Returns 200 OK when the llm-server process is up. Does not verify Postgres/RabbitMQ/LLM provider.
// @Tags         system
// @Produce      json
// @Success      200  {object}  HealthResponse
// @Router       /health [get]
func handleHeathCheckApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
	})
}
