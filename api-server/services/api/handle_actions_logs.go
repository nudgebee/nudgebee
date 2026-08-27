package api

import (
	"errors"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"time"

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
	//
	// CanReadAccountData rather than HasAccountAccess: the latter recognises
	// built-in scoped roles ONLY, so a custom-role holder whose `logs:Read`
	// grant had already cleared the gateway was then refused here, with no role
	// they could be given short of account_admin. It adds the dynamic-RBAC arm
	// (tenant-global grant + account-in-tenant, or an account-scoped grant) and
	// still starts from HasAccountAccess, so built-in roles are unchanged.
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
	if !ctx.GetSecurityContext().CanReadAccountData(accountId, "logs") {
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

		start := time.Now()
		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityLogsQueryTimeout, func() (observability.FetchLogsResult, error) {
			return observability.FetchLogs(ctx, request)
		})
		// Recorded before the error branch so failures are captured too — the
		// client cannot do this itself, since a failed query returns no body and
		// Builder mode never had a query string to fall back on. Elapsed time is
		// handler-side, so it excludes the gateway hop and both network legs.
		if shouldRecordUserQueryHistory(request.RecordHistory, actionPayload.SessionVariables) {
			if row, ok := observability.BuildLogQueryHistory(request, resp, err, time.Since(start)); ok {
				recordUserQueryHistoryAsync(c, ctx, row, tracer, meter, logger)
			}
		}
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

		// Optional, privileged: list the fields of the configuration described in the
		// request rather than the one saved for this account, so the integration form can
		// offer real field names before the integration exists.
		//
		// Gated on tenant admin because honouring it makes the SERVER fetch from an
		// endpoint the caller names — read-only for the caller, an outbound request from
		// inside the cluster in effect. Tenant admin is the tier that may create an
		// integration and run Test Connection, i.e. the tier already trusted to hand the
		// server an endpoint and credentials; this grants nothing beyond that. Every other
		// role keeps the saved-integration behaviour, and says so rather than silently
		// ignoring the field.
		labelCtx, status, authErr := logLabelProbeContext(ctx, &request)
		if authErr != nil {
			if status == 403 {
				c.JSON(403, common.ErrorActionForbidden(authErr.Error()))
			} else {
				c.JSON(status, common.ErrorActionBadRequest(authErr.Error()))
			}
			return
		}

		// FetchLogLabelsOrIndexFields owns the fetch_index fork, so the mode is
		// decided in one testable place rather than split across the handler.
		resp, err := runObservabilityActionWithTimeout(ctx, actionPayload.Action.Name, observabilityMetadataActionTimeout, func() ([]observability.OutputLogLabel, error) {
			return observability.FetchLogLabelsOrIndexFields(labelCtx, request)
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

// logLabelProbeContext honours the optional, privileged `integration_config_values` on a
// log-label request: with it, the fields returned describe the configuration IN THE
// REQUEST rather than the one saved for the account, which is what lets the integration
// form list a backend's fields before the integration exists.
//
// Returns the context the lookup should run under, plus the HTTP status to answer with
// when it refuses. Absent values are the overwhelmingly common case and return the
// caller's own context untouched — every in-process caller and every pre-existing client
// takes that path, so behaviour there is unchanged.
//
// The tenant-admin gate is the point of the function. Honouring the field makes the
// SERVER fetch from an endpoint the caller names — read-only for the caller, an outbound
// request from inside the cluster in effect. Tenant admin is the tier that may already
// create an integration and run Test Connection, i.e. the tier trusted to hand the server
// an endpoint and credentials, so this grants nothing beyond what it already has. Lower
// tiers are refused rather than silently downgraded to the saved config: a field list
// that quietly described a different backend than the one asked about would be worse than
// an error.
//
// Extracted from the handler so the rule is unit-testable without the gin stack.
func logLabelProbeContext(ctx *security.RequestContext, request *observability.FetchLogLabelRequest) (*security.RequestContext, int, error) {
	if request == nil || len(request.IntegrationConfigValues) == 0 {
		return ctx, 200, nil
	}

	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.IsTenantAdmin() && !sc.IsSuperAdmin()) {
		return nil, 403, errors.New("integration_config_values requires tenant admin")
	}
	if request.LogProvider == "" {
		return nil, 400, errors.New("log_provider is required with integration_config_values")
	}

	decrypted, err := core.DecryptConfigValues(request.IntegrationConfigValues)
	if err != nil {
		return nil, 400, err
	}
	source := request.LogProviderSource
	if source == "" {
		source = "user"
	}

	probeCtx := core.WithConfigOverride(ctx, core.ConfigOverride{
		AccountId:       request.AccountId,
		IntegrationName: request.LogProvider,
		Source:          source,
		Values:          decrypted,
	})
	// Transport only — the values never travel further than this handler.
	request.IntegrationConfigValues = nil
	return probeCtx, 200, nil
}
