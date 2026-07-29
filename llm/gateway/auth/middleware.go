package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const identityCtxKey = "nb_identity"

// Middleware authenticates a request and stores the resolved Identity in the gin
// context for downstream handlers/metering. On an invalid or (when required)
// missing token it aborts with 401 in a provider-neutral JSON shape.
func Middleware(v Validator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" && v.RequiresToken() {
			abort401(c, "missing NB token")
			return
		}
		id, ok := v.Validate(token)
		if !ok {
			abort401(c, "invalid NB token")
			return
		}
		c.Set(identityCtxKey, id)
		c.Next()
	}
}

// FromContext returns the resolved identity (zero value if unset).
func FromContext(c *gin.Context) Identity {
	if v, ok := c.Get(identityCtxKey); ok {
		if id, ok := v.(Identity); ok {
			return id
		}
	}
	return Identity{}
}

// extractToken pulls the NB token from wherever the client placed it: the
// provider api-key slot (Anthropic x-api-key, Gemini x-goog-api-key or ?key=) or
// an OpenAI-style Authorization: Bearer. The client sends the NB token here; the
// real provider credential is injected server-side, so this is never a provider key.
func extractToken(c *gin.Context) string {
	if v := strings.TrimSpace(c.GetHeader("x-api-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("x-goog-api-key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.GetHeader("Authorization")); v != "" {
		if after, found := strings.CutPrefix(v, "Bearer "); found {
			return strings.TrimSpace(after)
		}
		return v
	}
	if v := strings.TrimSpace(c.Query("key")); v != "" { // Gemini query key
		return v
	}
	return ""
}

func abort401(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{"type": "authentication_error", "message": msg},
	})
}
