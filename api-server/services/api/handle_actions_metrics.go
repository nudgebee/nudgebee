package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/observability"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleMetricsAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	switch actionPayload.Action.Name {
	case "metrics_query", "metrics_aggregate", "metrics_list":
		var request observability.FetchMetricsRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetricsQueryTimeout, func() (any, error) {
			return observability.FetchMetricsQuery(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list_names":
		var request observability.FetchMetricsListRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list_names: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() ([]observability.OutputMetrics, error) {
			return observability.FetchMetricsList(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list_labels":
		var request observability.FetchMetricLabelsRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list_labels: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() ([]observability.OutputMetricLabels, error) {
			return observability.FetchMetricLabelsList(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list_label_values":
		var request observability.FetchMetricsLabelValueRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_list_label_values: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() ([]observability.OutputMetricsLabelValues, error) {
			return observability.FetchMetricLabelValues(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_query_utilisation", "metrics_aggregate_utilisation":
		var request observability.GetUtilisationTrendRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("metrics_aggregate_utilisation: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if request.MetricProviderSource == "" {
			request.MetricProviderSource = "agent"
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetricsQueryTimeout, func() (observability.OutputMetricQuery, error) {
			return observability.FetchMetricUtilisation(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_list_series":
		var request observability.FetchMetricSeriesRequest
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("missing or invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
		if err != nil {
			slog.Error("metrics_list_series: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if request.Workload == "" {
			c.JSON(400, common.ErrorActionBadRequest("workload is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() (observability.MetricSeriesResult, error) {
			return observability.FetchMetricSeries(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "metrics_get_query":
		var request observability.FetchMetricsRequest
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("missing or invalid request input"))
			return
		}
		if err := common.UnmarshalMapToStruct(requestInput, &request); err != nil {
			slog.Error("metrics_get_query: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		// Only default the source to "agent" when no provider override was sent;
		// with an override, let GetMetricsQuery resolve the real integration
		// source (matching the metrics_query path) so SaaS providers don't 400.
		if request.MetricProvider == "" && request.MetricProviderSource == "" {
			request.MetricProviderSource = "agent"
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if request.AccountId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		if !ctx.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+request.AccountId))
			return
		}
		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetricsQueryTimeout, func() (any, error) {
			return observability.GetMetricsQuery(ctx, request)
		})
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	default:
		c.JSON(400, []common.Error{
			{
				Message: "invalid action name - " + actionPayload.Action.Name,
			},
		})
		return
	}
}
