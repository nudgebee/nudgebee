package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/productupdates"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleProductUpdates(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	if actionPayload.Action.Name != "product_updates_list" {
		c.JSON(400, common.ErrorActionBadRequest("unsupported action: "+actionPayload.Action.Name))
		return
	}

	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	resp, err := productupdates.ListProductUpdates(ctx)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	c.JSON(200, map[string]any{"data": resp})
}
