package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"nudgebee/llm-gateway/auth"

	"github.com/gin-gonic/gin"
)

// Middleware rejects requests that exceed a configured quota (429). It runs after
// auth (needs identity) and before the provider call. Requests/tokens/cost limits
// with no configured value are simply absent, so this is a no-op for unlimited
// callers. Token/cost usage is reconciled post-response by the handler.
func Middleware(l *Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.Enabled() {
			c.Next()
			return
		}
		id := auth.FromContext(c)
		if ex := l.Check(c.Request.Context(), Scope{TenantID: id.TenantID, UserID: id.UserID}); ex != nil {
			reset := ResetAt(ex.Period, time.Now())
			if secs := int(time.Until(reset).Seconds()) + 1; secs > 0 {
				c.Header("Retry-After", strconv.Itoa(secs))
			}
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"type": "rate_limit_exceeded",
					"message": fmt.Sprintf("%s limit exceeded for %s (%s window); resets at %s UTC",
						ex.Metric, ex.Scope, ex.Period, reset.Format("2006-01-02 15:04")),
				},
			})
			return
		}
		c.Next()
	}
}
