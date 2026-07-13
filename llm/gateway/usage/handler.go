package usage

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/metering"
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
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	UserID    string `json:"user_id"` // optional drill-down from the Users tab
	Tool      string `json:"tool"`    // optional drill-down from the Tools tab
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

// RegisterRoutes mounts the read-only usage query API under /rpc/usage, guarded by
// the service token (X-ACTION-TOKEN) that the app's RPC gateway forwards. This is a
// separate plane from the NB-PAT passthrough lanes: app → gateway service-to-service.
func RegisterRoutes(r *gin.Engine, pricer *metering.Pricer, token string) {
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
		res, err := ListRequests(c.Request.Context(), db, pricer, ListRequest{
			TenantID: tenantID, StartDate: start, EndDate: end,
			UserID: req.UserID, Tool: req.Tool, Limit: req.Limit, Offset: req.Offset,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "request list failed"})
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
