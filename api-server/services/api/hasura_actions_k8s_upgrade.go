package api

import (
	"log/slog"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/k8s_upgrade"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraUpgradePlanAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	tenantID := ""
	if val, ok := hasuraPayload.SessionVariables["x-hasura-user-tenant-id"].(string); ok {
		tenantID = val
	}

	switch hasuraPayload.Action.Name {
	case "upgrade_plan_create_one":
		var request k8s_upgrade.UpgradePlanTemplate
		err = common.UnmarshalMapToStruct(hasuraPayload.Input, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		response, err := k8s_upgrade.CreateUpgradePlan(ctx, tenantID, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(http.StatusOK, response)
	case "upgrade_plan_fetch_one":
		var request k8s_upgrade.UpgradePlanTemplate
		err = common.UnmarshalMapToStruct(hasuraPayload.Input, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		response, err := k8s_upgrade.GetUpgradePlan(ctx, tenantID, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(http.StatusOK, response)
	case "upgrade_plan_fetch_all":
		var request k8s_upgrade.UpgradePlanTemplate
		err = common.UnmarshalMapToStruct(hasuraPayload.Input, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		response, err := k8s_upgrade.GetAllUpgradePlans(ctx, tenantID, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(http.StatusOK, response)
	case "upgrade_plan_task_upsert_one":
		var request k8s_upgrade.TaskUpsertRequest
		err = common.UnmarshalMapToStruct(hasuraPayload.Input, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		response, err := k8s_upgrade.UpdatePlannedTask(ctx, tenantID, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(http.StatusOK, response)
	case "upgrade_plan_delete_one":
		var request k8s_upgrade.DeleteUpgradePlanRequest
		err = common.UnmarshalMapToStruct(hasuraPayload.Input, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
			return
		}

		err = k8s_upgrade.DeletePlan(ctx, tenantID, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(http.StatusOK, map[string]interface{}{"success": true, "plan_id": request.PlanID})
	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("Unknown action: "+hasuraPayload.Action.Name))
	}

}

func handleHasuraClusterHealthAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	switch hasuraPayload.Action.Name {
	case "check_cluster_health":
		handleCheckClusterHealthAction(hasuraPayload.Input, ctx, c)
	case "post_flight_check":
		handlePostFlightCheckAction(hasuraPayload.Input, ctx, c)
	case "upgrade_pre_flight_check":
		handleUpgradePreFlightCheckAction(hasuraPayload.Input, ctx, c)
	case "upgrade_post_flight_check":
		handleUpgradePostFlightCheckAction(hasuraPayload.Input, ctx, c)
	case "upgrade_execute_command":
		handleExecuteCommandAction(hasuraPayload.Input, ctx, c)
	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("Unknown action: "+hasuraPayload.Action.Name))
	}
}

func handleCheckClusterHealthAction(actionRequest map[string]any, ctx *security.RequestContext, c *gin.Context) {
	var request k8s_upgrade.HealthCheckRequest
	err := common.UnmarshalMapToStruct(actionRequest, &request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
		return
	}

	response, err := k8s_upgrade.PerformHealthCheck(ctx, request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}
	c.JSON(http.StatusOK, response)
}

func handlePostFlightCheckAction(actionRequest map[string]any, ctx *security.RequestContext, c *gin.Context) {
	var request k8s_upgrade.HealthCheckRequest
	err := common.UnmarshalMapToStruct(actionRequest, &request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
		return
	}

	response, err := k8s_upgrade.PerformPostFlightCheck(ctx, request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	c.JSON(http.StatusOK, response)
}

func handleExecuteCommandAction(actionRequest map[string]any, ctx *security.RequestContext, c *gin.Context) {
	var request k8s_upgrade.ExecuteCommandRequest
	err := common.UnmarshalMapToStruct(actionRequest, &request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
		return
	}

	response, err := k8s_upgrade.ExecuteCommand(ctx, request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}
	c.JSON(http.StatusOK, response)
}

func handleUpgradePreFlightCheckAction(actionRequest map[string]any, ctx *security.RequestContext, c *gin.Context) {
	var request k8s_upgrade.PreFlightCheckRequest
	err := common.UnmarshalMapToStruct(actionRequest, &request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest("Invalid request format: "+err.Error()))
		return
	}

	response, err := k8s_upgrade.PerformPreFlightCheckWithStorage(ctx, request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	c.JSON(http.StatusOK, response)
}

func handleUpgradePostFlightCheckAction(actionRequest map[string]any, ctx *security.RequestContext, c *gin.Context) {
	var request k8s_upgrade.PostFlightCheckRequest
	err := common.UnmarshalMapToStruct(actionRequest, &request)
	if err != nil {
		c.JSON(http.StatusBadRequest, []common.Error{
			{
				Message: "Invalid request format: " + err.Error(),
			},
		})
		return
	}

	response, err := k8s_upgrade.PerformPostFlightCheckWithComparison(ctx, request)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	c.JSON(http.StatusOK, response)
}
