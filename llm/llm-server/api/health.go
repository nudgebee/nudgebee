package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHeathCheckApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})
}
