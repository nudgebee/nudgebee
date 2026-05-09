package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/ml"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraMlAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	mlRequest := hasuraPayload.Input

	switch hasuraPayload.Action.Name {
	case "ml_get_metrics":
		var metricsRequest ml.MetricsRequest
		err := common.UnmarshalMapToStruct(mlRequest, &metricsRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := ml.GetMetrics(metricsRequest, ctx)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "ml_get_recommendation":
		var recommendationRequest ml.RecommendationRequest
		err := common.UnmarshalMapToStruct(mlRequest, &recommendationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := ml.GetRecommendation(ctx, recommendationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "generate_node_recommendations":
		var recommendationRequest ml.NodeRecommendationRequest
		err := common.UnmarshalMapToStruct(mlRequest, &recommendationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := ml.GetNodeRecommendation(ctx, recommendationRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("invalid action name - "+hasuraPayload.Action.Name))
		return
	}
}
