package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleIntegrationApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/integration")

	groupV2.POST("/list_integration_config", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "integration_list_config")
		payload := map[string]string{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_list_config", "invalid_json")
			logger.Error("integration: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		tenantId := c.Request.Header.Get("x-tenant-id")
		if tenantId == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_list_config", "missing_tenant_id")
			c.JSON(400, common.ErrorHasuraActionBadRequest("tenant id is required"))
			return
		}
		resp, err := core.ListIntegrationConfigs(security.NewRequestContextForTenantAdmin(tenantId, logger, tracer, meter), payload["account_id"], payload["integration_name"])
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_list_config", "list_config_failed")
			logger.Error("integration: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]any{
			"data": resp,
		})
	})

	groupV2.POST("/webhook_request", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "integration_webhook_request")
		payload := map[string]any{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_webhook_request", "invalid_json")
			logger.Error("integration: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		if payload["url"] == nil || payload["headers"] == nil || payload["payload"] == nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_webhook_request", "invalid_payload")
			logger.Error("integration: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid request payload"))
			return
		}

		err = core.ProcessEventWebook(security.NewRequestContextForSuperAdmin(logger, tracer, meter), payload["url"].(string), payload["headers"].(map[string]string), payload["payload"].(string))
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_webhook_request", "process_webhook_failed")
			logger.Error("integration: error creating access", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"message": "success"})
	})

	groupV2.POST("/replay_webhook", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "integration_replay_webhook")
		payload := map[string]string{}
		err := c.ShouldBindJSON(&payload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_replay_webhook", "invalid_json")
			logger.Error("integration: error binding request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		if payload["webhook_event_id"] == "" {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_replay_webhook", "missing_webhook_event_id")
			logger.Error("integration: webhook_event_id is required")
			c.JSON(400, common.ErrorHasuraActionBadRequest("webhook_event_id is required"))
			return
		}

		err = core.ReplayWebhookEvent(security.NewRequestContextForSuperAdmin(logger, tracer, meter), payload["webhook_event_id"])
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "integration_replay_webhook", "replay_webhook_failed")
			logger.Error("integration: error replaying webhook event", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"message": "webhook event replayed successfully"})
	})

}
