package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nudgebee/relay-server/pkg/server/health"
)

// Liveness reports whether this relay is still able to serve tenant traffic.
//
// It is deliberately separate from /status, which stays an unconditional OK:
// /status is what the ingress exposes and what external load balancers probe,
// and it also backs the readiness probe. Readiness must not start failing here —
// removing the pod from the Service endpoints does not restart anything, and at
// replicaCount: 1 it would turn one stuck tenant into a total outage. Restarting
// is what actually clears the condition, so this backs the liveness probe only.
func Liveness(tracker *health.Tracker, restartAfter time.Duration, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if tracker == nil {
			c.String(http.StatusOK, "OK")
			return
		}
		if wedged, reason := tracker.Wedged(restartAfter); wedged {
			// The reason names a queue, which embeds an account UUID. The
			// kubelet copies a failing probe's response body verbatim into the
			// Pod's "Liveness probe failed" event, so putting it in the body
			// would publish tenant identifiers into cluster events and anything
			// that scrapes them. The probe only needs the status code; the
			// detail belongs in our own log, which is access-controlled.
			logger.Error("liveness failing; relay cannot serve a tenant", "reason", reason)
			c.String(http.StatusServiceUnavailable, "unhealthy")
			return
		}
		c.String(http.StatusOK, "OK")
	}
}
