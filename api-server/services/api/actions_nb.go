package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/crawl"
	"nudgebee/services/nb"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleNbAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	switch actionPayload.Action.Name {
	case "nb_versions", "nudgebee_list_versions":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		versions, err := nb.GetVersions(ctx)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, versions)
	case "k8s_versions", "k8s_list_versions":
		versions, err := crawl.FetchKubernetesVersionsWithDates()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, versions)
	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}
