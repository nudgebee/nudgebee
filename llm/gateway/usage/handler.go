package usage

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/rpc"
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
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	UserID    string   `json:"user_id"`   // optional; Users-tab drill-down or the User filter
	Providers []string `json:"providers"` // optional; empty = all providers
	Models    []string `json:"models"`    // optional; empty = all (routed) models
	Status    string   `json:"status"`    // optional; "success" (2xx) | "error" (everything else)
	Tool      string   `json:"tool"`      // optional drill-down from the Tools tab
	// Governance-tab drill-ins (each maps a Governance count to the rows behind it).
	RoutingReason string `json:"routing_reason"` // optional; e.g. substitute | fallback | deprecated
	RejectReason  string `json:"reject_reason"`  // optional; e.g. rate_limited | secret_blocked
	Dlp           bool   `json:"dlp"`            // optional; requests that tripped the egress filter
	SessionID     string `json:"session_id"`     // optional; one session/conversation (drill-in)
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
}

// apiSessionsRequest is the body for the paginated session list (Sessions tab).
type apiSessionsRequest struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	UserID    string `json:"user_id"` // optional; scope to one user
	Search    string `json:"search"`  // optional; session_id contains
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

// RegisterRoutes mounts the read-only usage query API under /rpc/usage, guarded by
// the service token (X-ACTION-TOKEN) that the app's RPC gateway forwards. This is a
// separate plane from the NB-PAT passthrough lanes: app → gateway service-to-service.
func RegisterRoutes(r *gin.Engine, token string) {
	g := r.Group("/rpc/usage", rpc.ServiceToken(token))

	g.POST("/aggregate", func(c *gin.Context) {
		var req apiRequest
		if !rpc.BindAction(c, &req) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		tenantID, ok := rpc.RequireTenant(c)
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
		res, err := Aggregate(c.Request.Context(), db, Request{
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
		if !rpc.BindAction(c, &req) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		tenantID, ok := rpc.RequireTenant(c)
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
		res, err := ListRequests(c.Request.Context(), db, ListRequest{
			TenantID: tenantID, StartDate: start, EndDate: end,
			UserID: req.UserID, Providers: req.Providers, Models: req.Models, Status: req.Status,
			Tool: req.Tool, RoutingReason: req.RoutingReason, RejectReason: req.RejectReason, Dlp: req.Dlp,
			SessionID:     req.SessionID,
			CallerUserID:  c.GetHeader("x-user-id"),
			CallerIsAdmin: rpc.IsTenantAdmin(c),
			Limit:         req.Limit, Offset: req.Offset,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "request list failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})

	g.POST("/sessions", func(c *gin.Context) {
		var req apiSessionsRequest
		if !rpc.BindAction(c, &req) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		tenantID, ok := rpc.RequireTenant(c)
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
		res, err := ListSessions(c.Request.Context(), db, ListSessionsRequest{
			TenantID: tenantID, StartDate: start, EndDate: end,
			UserID: req.UserID, Search: req.Search,
			CallerUserID:  c.GetHeader("x-user-id"),
			CallerIsAdmin: rpc.IsTenantAdmin(c),
			Limit:         req.Limit, Offset: req.Offset,
		})
		if err != nil {
			slog.Error("usage: session list failed", "error", err, "tenant", tenantID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "session list failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": res})
	})
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
