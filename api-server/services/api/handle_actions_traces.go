package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/observability"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleTracesAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	switch actionPayload.Action.Name {
	case "traces_query", "traces_list":
		var request observability.TracesV3Request
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_list: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetTraces(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "traces_counts":
		var request observability.TracesV3Request
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_counts: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.CountTraces(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "traces_label_values":
		var request observability.TracesV3LabelValuesRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_label_values: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetTracesLabelValues(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "traces_get_query":
		var request observability.TracesV3Request
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_label_values: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetTracesQuery(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "traces_grouping_v3":
		var request observability.TracesV3Request
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_grouping_v3: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetGroupedTraces(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "traces_grouping_count_v3":
		var request observability.TracesV3Request
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_grouping_count_v3: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetGroupedTracesCount(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "traces_heat_map", "traces_get_heatmap":
		var request observability.TracesHeatMapRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("traces_grouping_count_v3: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetTraceHeatMap(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	}

}
