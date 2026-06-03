package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/insight"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleInsights(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	actionRequest := actionPayload.Input
	if actionPayload.Action.Name == "list_insights" || actionPayload.Action.Name == "insights_aggregate" {
		var insightRequest insight.InsightListRequest
		if actionRequest["request"] != nil {
			actionRequest = actionRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(actionRequest, &insightRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := insight.GetInsights(ctx, insightRequest.AccountId)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	}
}
