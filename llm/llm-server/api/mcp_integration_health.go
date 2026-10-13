package api

import (
	"errors"
	"log/slog"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolscore "nudgebee/llm/tools/core"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type mcpHealthCheckInput struct {
	IntegrationID string `json:"integration_id" mapstructure:"integration_id"`
}

// canCheckMCPIntegration is the Check now gate (#40994). The action is classified
// integrations:Execute and is not on the gateway's ACCOUNT_ENFORCED_ACTIONS, so
// only tenant-wide callers reach it: tenant admins, full super admins and
// tenant-global grant holders.
func canCheckMCPIntegration(sc *security.SecurityContext) bool {
	if sc == nil {
		return false
	}
	return sc.IsSuperAdmin() || sc.CanManage(moduleIntegrations, "Execute")
}

// handleMCPIntegrationApis registers integrations_execute_mcp_health_check.
func handleMCPIntegrationApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/mcp-integrations")

	// POST /v1/mcp-integrations/health-check — integrations_execute_mcp_health_check
	group.POST("/health-check", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("mcp_integrations_health_check")

		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}
		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}
		var request mcpHealthCheckInput
		if err := common.DecodeMapToStruct(unwrapHasuraInput(actionRequest, requestMap), &request); err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}
		if _, err := uuid.Parse(request.IntegrationID); err != nil {
			actionError(c, 400, "api: integration_id must be a valid UUID")
			return
		}

		logger := slog.With("integration_id", request.IntegrationID)
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}
		sc := agentContext.GetSecurityContext()
		if !canCheckMCPIntegration(sc) {
			actionError(c, 403, "api: not permitted to check this integration")
			return
		}

		target, err := toolscore.LoadMCPCheckTarget(request.IntegrationID)
		// Another tenant's integration is reported as missing, not as forbidden.
		if errors.Is(err, toolscore.ErrMCPIntegrationNotFound) || (err == nil && target.TenantID != sc.GetTenantId()) {
			actionError(c, 404, "api: MCP integration not found")
			return
		}
		if err != nil {
			logger.Error("api: failed to load MCP integration", "error", err)
			actionError(c, 500, "api: failed to load MCP integration")
			return
		}

		health, err := toolscore.CheckMCPIntegration(target)
		switch {
		case errors.Is(err, toolscore.ErrMCPIntegrationNotEnabled), errors.Is(err, toolscore.ErrMCPIntegrationNoAccounts):
			actionError(c, 400, "api: "+err.Error())
			return
		case errors.Is(err, toolscore.ErrMCPCheckInconclusive):
			actionError(c, 503, "api: "+err.Error())
			return
		case err != nil:
			logger.Error("api: MCP health check failed", "error", err)
			actionError(c, 500, "api: MCP health check failed")
			return
		}
		c.JSON(200, gin.H{"integration_id": request.IntegrationID, "health": health})
	})
}
