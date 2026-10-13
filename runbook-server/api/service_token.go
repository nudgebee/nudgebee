package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"nudgebee/runbook/config"

	"github.com/gin-gonic/gin"
)

// serviceTokenHeader carries the shared service token. Same header the app
// gateway and the other services already use.
const serviceTokenHeader = "X-ACTION-TOKEN"

// skipsServiceToken lists the routes that do not take the service token because
// they carry their own credential (approval links: the token in the URL;
// webhooks: the per-workflow secret or the integration token) or expose
// nothing (health, API docs).
func skipsServiceToken(path string) bool {
	return path == "/health" ||
		path == "/openapi.json" ||
		strings.HasPrefix(path, "/swagger/") ||
		strings.HasPrefix(path, "/approvals/") ||
		strings.HasPrefix(path, "/webhook/")
}

// serviceTokenMiddleware rejects requests without the service token. Before it,
// anything that could reach this server could claim any user — or no user, and
// get tenant-admin rights.
func serviceTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if skipsServiceToken(c.Request.URL.Path) {
			c.Next()
			return
		}
		want := config.Config.WorkflowServerToken
		got := c.GetHeader(serviceTokenHeader)
		if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid service token"})
			return
		}
		c.Next()
	}
}
