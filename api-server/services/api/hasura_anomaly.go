package api

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/anomoly"
	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraAnomaly(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	hasuraRequest := hasuraPayload.Input
	if hasuraPayload.Action.Name == "anomaly_template_list" {
		var request anomoly.HasuraAnomalyListTemplateRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := anomoly.ListAnomalyTemplate(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	}

	if hasuraPayload.Action.Name == "trigger_anomaly_execute" {
		var request anomoly.TriggerAnomalyExecuteRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &request)
		if err != nil {
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}

		if request.AccountId == "" {
			c.JSON(400, []common.Error{
				{
					Message: "account_id is required",
				},
			})
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(500, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}

		// Create detached context for background execution
		// (gin reuses request contexts, so we must not use ctx inside goroutines)
		bgCtx := security.NewRequestContext(
			context.Background(),
			ctx.GetSecurityContext(),
			ctx.GetLogger(),
			ctx.GetTracer(),
			ctx.GetMeter(),
		)

		// Execute async in goroutine with detached context
		go func() {
			err := anomoly.ExecuteForAccount(bgCtx, request.AccountId)
			if err != nil {
				bgCtx.GetLogger().Error("trigger_anomaly_execute failed", "error", err, "account_id", request.AccountId)
			}
		}()

		c.JSON(200, anomoly.TriggerAnomalyExecuteResponse{
			Status:  "triggered",
			Message: fmt.Sprintf("Anomaly detection started for account %s", request.AccountId),
		})
		return
	}
}
