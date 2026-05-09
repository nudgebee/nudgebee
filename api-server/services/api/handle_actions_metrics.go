package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/observability"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraMetricsAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	switch hasuraPayload.Action.Name {
	case "metrics_query":
		var request observability.FetchMetricsRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_query: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchMetricsQuery(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list":
		var request observability.FetchMetricsListRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchMetricsList(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list_labels":
		var request observability.FetchMetricLabelsRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list_labels: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchMetricLabelsList(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list_label_values":
		var request observability.FetchMetricsLabelValueRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list_label_values: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchMetricLabelValues(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_query_utilisation":
		var request observability.GetUtilisationTrendRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_query_utilisation: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		if request.MetricProviderSource == "" {
			request.MetricProviderSource = "agent"
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchMetricUtilisation(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_get_query":
		var request observability.FetchMetricsRequest
		requestInput, ok := hasuraPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorHasuraActionBadRequest("missing or invalid request input"))
			return
		}
		if err := common.UnmarshalMapToStruct(requestInput, &request); err != nil {
			slog.Error("metrics_get_query: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		if request.MetricProviderSource == "" {
			request.MetricProviderSource = "agent"
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetMetricsQuery(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	default:
		c.JSON(400, []common.Error{
			{
				Message: "invalid action name - " + hasuraPayload.Action.Name,
			},
		})
		return
	}
}
