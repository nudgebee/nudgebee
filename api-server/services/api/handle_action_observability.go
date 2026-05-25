package api

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/observability"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleAccountProviderAction(
	actionPayload *ActionRequest,
	c *gin.Context,
	tracer *trace.Tracer,
	meter *metric.Meter,
	logger *slog.Logger,
) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	switch actionPayload.Action.Name {
	case "observability_list_provider_capabilities":
		var request observability.ListProviderCapabilitiesRequest
		err := common.UnmarshalMapToStruct(
			actionPayload.Input["request"].(map[string]interface{}),
			&request,
		)
		if err != nil {
			logger.Error("observability_list_provider_capabilities: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.ListProviderCapabilities(ctx, request.AccountId)
		if err != nil {
			c.JSON(500, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return

	case "get_default_provider":
		var request observability.DefaultProvider
		err := common.UnmarshalMapToStruct(
			actionPayload.Input["request"].(map[string]interface{}),
			&request,
		)
		if err != nil {
			logger.Error("get_default_provider: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := observability.GetDefaultProvider(
			ctx,
			request.AccountId,
			request.ProviderType,
			request.ProviderSource,
		)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	default:

		c.JSON(400, gin.H{"message": "Unknown Hasura action: " + actionPayload.Action.Name})
		return
	}
}

func handleAccountUserHistoryAction(
	actionPayload *ActionRequest,
	c *gin.Context,
	tracer *trace.Tracer,
	meter *metric.Meter,
	logger *slog.Logger,
) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	switch actionPayload.Action.Name {
	case "user_history":
		var request observability.UserHistoryRequest
		err := common.UnmarshalMapToStruct(
			actionPayload.Input["request"].(map[string]interface{}),
			&request,
		)
		if err != nil {
			logger.Error("user_history: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.SaveUserHistory(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	default:
		c.JSON(400, []common.Error{
			{Message: "Unknown Hasura action: " + actionPayload.Action.Name},
		})
		return
	}
}
