package usage

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/metering"
)

// apiRequest is the JSON body from the app's RPC action. The tenant is NOT taken
// from the body — it comes from the session via the x-tenant-id header the RPC
// gateway injects (so a caller can't query another tenant's usage). account_ids is
// accepted for parity with the cost-analyser contract; the gateway scopes by tenant.
type apiRequest struct {
	AccountIds  []string `json:"account_ids"`
	StartDate   string   `json:"start_date"`
	EndDate     string   `json:"end_date"`
	Granularity string   `json:"granularity"`
}

// apiListRequest is the body for the paginated recent-request list (Requests tab).
type apiListRequest struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	UserID    string `json:"user_id"` // optional drill-down from the Users tab
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

// RegisterRoutes mounts the read-only usage query API under /rpc/usage, guarded by
// the service token (X-ACTION-TOKEN) that the app's RPC gateway forwards. This is a
// separate plane from the NB-PAT passthrough lanes: app → gateway service-to-service.
func RegisterRoutes(r *gin.Engine, pricer *metering.Pricer, token string) {
	g := r.Group("/rpc/usage", serviceToken(token))

	g.POST("/aggregate", func(c *gin.Context) {
		var req apiRequest
		if !bindAction(c, &req) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		tenantID, ok := requireTenant(c)
		if !ok {
			return
		}
		start, end, ok := parseWindow(c, req.StartDate, req.EndDate)
		if !ok {
			return
		}
		db, err := common.GetDatabaseManager(common.MeteringSink)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metering store unavailable"})
			return
		}
		res, err := Aggregate(c.Request.Context(), db, pricer, Request{
			TenantID: tenantID, StartDate: start, EndDate: end, Granularity: req.Granularity,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "aggregation failed"})
			return
		}
		// {"data": …} matches the cost-analyser RPC envelope the frontend reads.
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	g.POST("/requests", func(c *gin.Context) {
		var req apiListRequest
		if !bindAction(c, &req) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		tenantID, ok := requireTenant(c)
		if !ok {
			return
		}
		start, end, ok := parseWindow(c, req.StartDate, req.EndDate)
		if !ok {
			return
		}
		db, err := common.GetDatabaseManager(common.MeteringSink)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metering store unavailable"})
			return
		}
		res, err := ListRequests(c.Request.Context(), db, pricer, ListRequest{
			TenantID: tenantID, StartDate: start, EndDate: end,
			UserID: req.UserID, Limit: req.Limit, Offset: req.Offset,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "request list failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})
}

// requireTenant pulls the tenant from the session-injected header, 400ing if absent.
// Tenant identity comes from the session (RPC gateway injects x-tenant-id), never the
// body — the caller cannot scope to another tenant.
func requireTenant(c *gin.Context) (string, bool) {
	tenantID := c.GetHeader("x-tenant-id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing tenant identity (x-tenant-id)"})
		return "", false
	}
	return tenantID, true
}

// parseWindow parses the RFC3339 start/end bounds, 400ing on a bad value.
func parseWindow(c *gin.Context, startStr, endStr string) (time.Time, time.Time, bool) {
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date must be RFC3339"})
		return time.Time{}, time.Time{}, false
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_date must be RFC3339"})
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// bindAction reads the request args into dst, tolerating both shapes (mirrors
// llm-server's action handlers): the RPC-gateway envelope — { input: { request: {…} } }
// or { input: {…} } — and a direct flat body, so the endpoints work via the app RPC
// gateway AND direct service-to-service / curl calls.
func bindAction(c *gin.Context, dst any) bool {
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

// serviceToken gates the query plane on the shared service token. The token is
// OPTIONAL: when configured it is enforced; when unset the plane stays open and
// relies on the app's RPC permission layer + network isolation (this is an internal
// service-to-service endpoint, not the public passthrough edge). A missing token
// therefore degrades gracefully rather than dead-ending the dashboard.
func serviceToken(expected string) gin.HandlerFunc {
	if expected == "" {
		slog.Warn("usage: GATEWAY_ACTION_TOKEN not set — usage query plane is unauthenticated " +
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
