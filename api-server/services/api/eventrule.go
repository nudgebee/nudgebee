package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type executePlaybookActionRequest struct {
	TenantId   string         `json:"tenant_id"`
	AccountId  string         `json:"account_id"`
	ActionName string         `json:"action_name"`
	ActionArgs map[string]any `json:"action_args"`
}

func handleEventRuleApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	groupV2 := r.Group("/v1/eventrule")

	//TODO, more apis for storing event_rules && related actions

	groupV2.POST("/execute_action", func(c *gin.Context) {
		common.MetricsApiRequestsTotal(c.Request.Context(), "execute_action")
		var playload executePlaybookActionRequest
		err := c.ShouldBindJSON(&playload)
		if err != nil {
			common.MetricsApiRequestsFailedTotal(c.Request.Context(), "execute_action", "invalid_json")
			logger.Error("eventrule: error binding request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if playload.TenantId == "" || playload.AccountId == "" || playload.ActionName == "" {
			logger.Error("eventrule: missing required fields", "tenantId", playload.TenantId, "accountId", playload.AccountId, "actionName", playload.ActionName)
			c.JSON(400, common.ErrorActionBadRequest("required fields (tenant_id/account_id/action_name) missing"))
			return
		}

		action, ok := playbooks.GetAction(playload.ActionName)
		if !ok {
			logger.Error("eventrule: action not found", "actionName", playload.ActionName)
			c.JSON(400, common.ErrorActionBadRequest("action not found"))
			return
		}
		result, err := action.Execute(playbooks.NewPlaybookActionContext(playload.TenantId, playload.AccountId, logger, playbooks.PlaybookEvent{}), playload.ActionArgs)
		if err != nil {
			logger.Error("eventrule: error executing action", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, result)
	})
}
