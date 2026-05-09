package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/insight"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraInsights(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	hasuraRequest := hasuraPayload.Input
	if hasuraPayload.Action.Name == "list_insights" {
		var insightHasuraRequest insight.InsightListRequest
		if hasuraRequest["request"] != nil {
			hasuraRequest = hasuraRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(hasuraRequest, &insightHasuraRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := insight.GetInsights(ctx, insightHasuraRequest.AccountId)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	}
}
