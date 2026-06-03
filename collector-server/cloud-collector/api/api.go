package api

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type apiResponse struct {
	Data   any                `json:"data"`
	Errors []ApiResponseError `json:"errors,omitempty"`
}

type ApiResponseError struct {
	Message string `json:"message"`
}

func buildApiResponse(data any, errors ...error) apiResponse {
	apiErrors := make([]ApiResponseError, 0)
	for _, err := range errors {
		apiErrors = append(apiErrors, ApiResponseError{
			Message: err.Error(),
		})
	}
	return apiResponse{
		Data:   data,
		Errors: apiErrors,
	}
}

func ConfigureRoutes(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	handleHeathCheckApis(r, tracer, meter, logger)
	handleCloudProviderApis(r, tracer, meter, logger)
	handleRpc(r, tracer, meter, logger)
}
