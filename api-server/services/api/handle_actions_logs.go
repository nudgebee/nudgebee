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

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityLogsQueryTimeout, func() (observability.FetchLogsResult, error) {
			return observability.FetchLogs(ctx, request)
		})
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

		// Only default the source to "agent" when no provider override was sent.
		// When the caller specifies a provider (Query Logs provider switcher),
		// leave the source empty so GetLogsQuery resolves the real integration
		// source — matching the logs_list path. Forcing "agent" here would 400
		// on SaaS providers whose only valid source is "user" (datadog, loggly,
		// newrelic, dynatrace, solarwinds, observe, azure_app_insights, …).
		if request.LogProvider == "" && request.LogProviderSource == "" {
			request.LogProviderSource = "agent"
		}
		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityLogsQueryTimeout, func() (observability.OutputLogQuery, error) {
			return observability.GetLogsQuery(ctx, request)
		})
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
			indexFields, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name+":index", observabilityMetadataActionTimeout, func() ([]observability.OutputLogLabelFields, error) {
				return observability.FetchLogIndexFields(ctx, request)
			})
			if err != nil {
				c.JSON(400, common.ErrorActionBadRequest(err.Error()))
				return
			}
			c.JSON(200, observability.LabelsFromIndexFields(indexFields))
			return
		}

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() ([]observability.OutputLogLabel, error) {
			return observability.FetchLogLabels(ctx, request)
		})
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

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() ([]observability.OutputLogLabelValue, error) {
			return observability.FetchLogLabelValues(ctx, request)
		})
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

		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() (observability.LogGroupOutput, error) {
			return observability.FetchLogGroup(ctx, request)
		})
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
