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

func handleLogsAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	// Every logs action is an account-scoped read carrying account_id in the
	// request payload; enforce account access once up front. tenant_admin
	// passes for any account in the tenant; account_admin only for its
	// assigned accounts.
	reqInput, ok := actionPayload.Input["request"].(map[string]interface{})
	if !ok {
		c.JSON(400, common.ErrorActionBadRequest("missing or invalid request input"))
		return
	}
	accountId, _ := reqInput["account_id"].(string)
	if accountId == "" {
		c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
		return
	}
	if !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
		c.JSON(403, common.ErrorActionForbidden("access denied for account: "+accountId))
		return
	}

	switch actionPayload.Action.Name {
	case "logs_query", "logs_list":
		var request observability.FetchLogRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("logs_list: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchLogs(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "logs_get_query":
		var request observability.FetchLogRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("logs_list: failed to decode request", "error", err) // Original message was "logs_list", kept for consistency
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.LogProviderSource == "" {
			request.LogProviderSource = "agent"
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.GetLogsQuery(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "logs_list_labels":
		var request observability.FetchLogLabelRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)

		if err != nil {
			slog.Error("logs_list_labels: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.FetchIndex {
			indexFields, err := observability.FetchLogIndexFields(ctx, request)
			if err != nil {
				c.JSON(400, common.ErrorActionBadRequest(err.Error()))
				return
			}
			labels := make([]observability.OutputLogLabel, len(indexFields))
			for i, f := range indexFields {
				labels[i] = observability.OutputLogLabel{
					Label:      f.Field,
					Attributes: f.Attributes,
				}
			}
			c.JSON(200, labels)
			return
		}

		resp, err := observability.FetchLogLabels(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "logs_list_label_values":
		var request observability.FetchLogLabelValuesRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("logs_list_label_values: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)

		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchLogLabelValues(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "log_group":
		var request observability.FetchLogGroupRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("log_group: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)

		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := observability.FetchLogGroup(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}
