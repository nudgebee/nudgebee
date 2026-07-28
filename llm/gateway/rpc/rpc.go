// Package rpc holds the generic service-to-service RPC plumbing shared by the
// gateway's internal query/config planes (app → gateway, guarded by the service
// token the app's RPC gateway forwards). It carries no feature-specific logic: just
// the service-token gate, the envelope-tolerant body binder, and the tenant-from-
// header helper — plus a registration seam so an optional admin plane can mount
// itself without main referencing it directly.
package rpc

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ServiceToken gates a plane on the shared service token. The token is OPTIONAL:
// when configured it is enforced; when unset the plane stays open and relies on the
// app's RPC permission layer + network isolation (these are internal service-to-
// service endpoints, not the public passthrough edge). A missing token therefore
// degrades gracefully rather than dead-ending the caller.
func ServiceToken(expected string) gin.HandlerFunc {
	if expected == "" {
		slog.Warn("rpc: gateway_action_token not set — the RPC plane is unauthenticated " +
			"(relying on the RPC permission layer + network isolation); set it to require a service token")
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if c.GetHeader("X-ACTION-TOKEN") != expected {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"type": "authentication_error", "message": "invalid service token"},
			})
			return
		}
		c.Next()
	}
}

// RequireTenant pulls the tenant from the session-injected header, 400ing if absent.
// Tenant identity comes from the session (the RPC gateway injects x-tenant-id),
// never the body — the caller cannot scope to another tenant.
func RequireTenant(c *gin.Context) (string, bool) {
	tenantID := c.GetHeader("x-tenant-id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing tenant identity (x-tenant-id)"})
		return "", false
	}
	return tenantID, true
}

// CallerRole returns the caller's effective role from the session — the app RPC gateway
// injects x-user-role (the highest-priority role the user holds). Empty for direct
// service-to-service / curl calls that don't set it.
func CallerRole(c *gin.Context) string { return c.GetHeader("x-user-role") }

// tenantAdminRoles may read ANY user's data within their OWN tenant (never
// cross-tenant — tenant scoping still applies). Read-only variants are included:
// viewing is a read.
var tenantAdminRoles = map[string]bool{
	"tenant_admin": true, "tenant_admin_readonly": true,
	"account_admin": true, "account_admin_readonly": true,
	"super_admin": true, "super_admin_readonly": true,
}

// IsTenantAdmin reports whether the caller's role grants tenant-wide read access
// (e.g. viewing another user's captured request body, within the same tenant).
func IsTenantAdmin(c *gin.Context) bool { return tenantAdminRoles[CallerRole(c)] }

// BindAction reads the request args into dst, tolerating both shapes (mirrors the
// app's action handlers): the RPC-gateway envelope — { input: { request: {…} } } or
// { input: {…} } — and a direct flat body, so the endpoints work via the app RPC
// gateway AND direct service-to-service / curl calls.
func BindAction(c *gin.Context, dst any) bool {
	raw := map[string]any{}
	if err := c.ShouldBindJSON(&raw); err != nil {
		return false
	}
	payload := raw
	if input, ok := raw["input"].(map[string]any); ok {
		payload = input
		if inner, ok := input["request"].(map[string]any); ok {
			payload = inner
		}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}
