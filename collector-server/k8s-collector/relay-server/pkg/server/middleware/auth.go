// pkg/server/middleware/auth.go
package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/utils"

	"github.com/gin-gonic/gin"
)

const (
	CtxAccountID = "accountID"
	CtxAgentType = "agentType"
)

// AgentAuthMiddleware protects agent endpoints (e.g. /register) using Basic‑auth.
// It accepts `Authorization: Basic <base64(accessKey:secret)>` (scheme is
// case-insensitive) as well as a bare `Authorization: <base64(accessKey:secret)>`
// with no scheme prefix — the nudgebee-agent runner's relay client sends the
// credential without the "Basic " prefix, and requiring the prefix 401s every
// deployed runner. Validates via store.ValidateAgent and sets the account ID
// in the context.
func AgentAuthMiddleware(store db.AgentStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "missing or malformed authorization header"),
			)
			return
		}

		// Accept a bare base64 token (no scheme) as well as "Basic <token>"
		// with a case-insensitive scheme. Trim first so leading/trailing
		// whitespace can't corrupt the split.
		trimmedAuth := strings.TrimSpace(authHeader)
		encoded := trimmedAuth
		if scheme, rest, found := strings.Cut(trimmedAuth, " "); found {
			if !strings.EqualFold(scheme, "basic") {
				c.AbortWithStatusJSON(http.StatusUnauthorized,
					utils.BuildError(401, "malformed authorization header"),
				)
				return
			}
			encoded = strings.TrimSpace(rest)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "invalid authorization header encoding"),
			)
			return
		}

		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "invalid authorization header format"),
			)
			return
		}
		accessKey, secret := parts[0], parts[1]

		ok, accountID, agentType, err := store.ValidateAgent(c.Request.Context(), accessKey, secret)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError,
				utils.BuildError(500, "error validating credentials"),
			)
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "invalid credentials"),
			)
			return
		}

		// Store accountID and agentType for downstream handlers
		c.Set(CtxAccountID, accountID)
		c.Set(CtxAgentType, agentType)
		c.Next()
	}
}

// ClientAuthMiddleware protects client endpoints (e.g. /ws, /request, /grafana)
// using a static secret key header X-SECRET-KEY.
func ClientAuthMiddleware(secretKey string) gin.HandlerFunc {
	// Hash both sides before constant-time compare — ConstantTimeCompare
	// itself short-circuits on length mismatch, which would leak the
	// expected secret's length to a timing attacker.
	expectedHash := sha256.Sum256([]byte(secretKey))
	return func(c *gin.Context) {
		providedHash := sha256.Sum256([]byte(c.GetHeader("X-SECRET-KEY")))
		if subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "invalid secret key"),
			)
			return
		}
		c.Next()
	}
}

// PrometheusAuthMiddleware protects Prometheus API endpoints.
// It expects `Authorization: Bearer <secretKey>` and an `X-Scope-OrgID` header.
// The OrgID is treated as the accountID and set in the context.
func PrometheusAuthMiddleware(secretKey string) gin.HandlerFunc {
	expectedHash := sha256.Sum256([]byte(secretKey))
	return func(c *gin.Context) {
		// Validate Bearer token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "missing authorization header"),
			)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "malformed authorization header"),
			)
			return
		}

		providedHash := sha256.Sum256([]byte(parts[1]))
		if subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "invalid token"),
			)
			return
		}

		// Extract and set accountID from X-Scope-OrgID
		orgID := c.GetHeader("X-Scope-OrgID")
		if orgID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				utils.BuildError(401, "missing X-Scope-OrgID header"),
			)
			return
		}

		// Assuming the OrgID is the accountID.
		c.Set(CtxAccountID, orgID)
		c.Next()
	}
}
