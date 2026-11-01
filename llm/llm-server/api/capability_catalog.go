package api

import (
	"log/slog"
	"net/http"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// handleCapabilityCatalogApi returns the platform capability catalog for a
// real account. rag-server calls this to validate classifier-suggested
// capability tags. Requires account_id: workflow-server's task list needs a
// real, accessible account — the tenant-less startup sync always 401s there.
func handleCapabilityCatalogApi(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/capabilities")

	group.POST("/catalog", func(c *gin.Context) {
		var req struct {
			AccountId string `json:"account_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.AccountId == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "api: account_id is required"})
			return
		}

		ctx, err := buildContextFromRequestPayload(c.Request.Context(), c, map[string]string{"account_id": req.AccountId}, tracer, meter, slog.Default())
		if err != nil {
			slog.Error("capability_catalog: failed to build security context", "error", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		sc := ctx.GetSecurityContext()
		if sc == nil || !sc.HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		catalog, err := toolcore.GetCapabilityCatalog(ctx, req.AccountId)
		if err != nil {
			slog.Error("capability_catalog: failed to build catalog", "account_id", req.AccountId, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"capabilities": catalog})
	})
}
