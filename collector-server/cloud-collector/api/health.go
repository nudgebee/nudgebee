package api

import (
	"log/slog"
	"nudgebee/collector/cloud/common"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHeathCheckApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	// /health is the readiness probe — it reflects HTTP serving only and must not
	// depend on RabbitMQ (the sync /v1/cloud/* endpoints work without it).
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// /livez is the liveness probe — it goes unhealthy when the MQ heartbeat stops
	// round-tripping while the broker connection is up (a wedged consumer), so Kubernetes
	// restarts the pod and rebuilds every consumer/publisher. A broker outage does not
	// fail it: a restart cannot fix that, and consumers reconnect in-process.
	r.GET("/livez", func(c *gin.Context) {
		if common.MqHealthy() {
			c.JSON(200, gin.H{"status": "ok"})
			return
		}
		c.JSON(503, gin.H{"status": "mq_unhealthy"})
	})
}
