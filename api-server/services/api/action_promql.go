package api

import (
	"log/slog"
	"nudgebee/services/common"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/prometheus/promql/parser"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type PromQLValidatorInput struct {
	PromQl string `json:"promql"`
}

func handlePromQL(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	promQLValidatorRequest := actionPayload.Input

	if actionPayload.Action.Name == "promql_validator" {
		var promQLRequest PromQLValidatorInput
		err := common.UnmarshalMapToStruct(promQLValidatorRequest, &promQLRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		result := true
		_, err = parser.ParseExpr(promQLRequest.PromQl)
		if err != nil {
			result = false
		}
		c.JSON(200, gin.H{
			"isValid": result,
		})
	} else {
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}
