package api

import (
	"log/slog"
	"nudgebee/services/application"
	"nudgebee/services/common"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// handleError writes an error response and returns true if there was an error
func handleError(c *gin.Context, err error, statusCode int) bool {
	if err != nil {
		c.JSON(statusCode, common.ErrorHasuraActionBadRequest(err.Error()))
		return true
	}
	return false
}

func handleApplicationEvent(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	request := hasuraPayload.Input
	if request["request"] != nil {
		request = request["request"].(map[string]any)
	}
	switch hasuraPayload.Action.Name {
	case "application_metrics":
		var applicationMetricsRequest application.ApplicationMetricsRequest

		err := common.UnmarshalMapToStruct(request, &applicationMetricsRequest)
		if handleError(c, err, 400) {
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if handleError(c, err, 500) {
			return
		}

		resp, err := application.GetApplicationMetrics(ctx, applicationMetricsRequest)
		if handleError(c, err, 500) {
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "application_deployment_compare":
		var applicationRequest application.ApplicationDeploymentCompareRequest

		err := common.UnmarshalMapToStruct(request, &applicationRequest)
		if handleError(c, err, 400) {
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if handleError(c, err, 500) {
			return
		}

		resp, err := application.CompareApplicationDeployment(ctx, applicationRequest)
		if handleError(c, err, 500) {
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "application_profile_convert":
		var applicationprofileRequest application.ApplicationProfileConvertRequest

		err := common.UnmarshalMapToStruct(request, &applicationprofileRequest)
		if handleError(c, err, 400) {
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if handleError(c, err, 500) {
			return
		}

		resp, err := application.ConvertProfile(ctx, applicationprofileRequest)
		if handleError(c, err, 500) {
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "application_profile":
		var applicationprofileRequest application.ApplicationProfileRequest

		err := common.UnmarshalMapToStruct(request, &applicationprofileRequest)
		if handleError(c, err, 400) {
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if handleError(c, err, 500) {
			return
		}

		resp, err := application.AppplicationProfile(ctx, applicationprofileRequest)
		if handleError(c, err, 500) {
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "application_get_profile":
		var applicationprofileRequest application.GetApplicationProfileRequest

		err := common.UnmarshalMapToStruct(request, &applicationprofileRequest)
		if handleError(c, err, 400) {
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if handleError(c, err, 500) {
			return
		}

		resp, err := application.GetProfileStatus(ctx, &applicationprofileRequest)
		if handleError(c, err, 500) {
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	case "traces_service_map":
		var traceServiceMapRequest application.TraceServiceMapRequest

		err := common.UnmarshalMapToStruct(request, &traceServiceMapRequest)
		if handleError(c, err, 400) {
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if handleError(c, err, 500) {
			return
		}

		resp, err := application.GetTraceServiceMap(ctx, traceServiceMapRequest)
		if handleError(c, err, 500) {
			return
		}

		c.JSON(200, map[string]any{"data": resp})
		return
	}
}
