package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/integrations/core"
	"nudgebee/services/llm"
	"nudgebee/services/security"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type IntegrationCreateRequest struct {
	IntegrationId           string                        `json:"integration_id,omitempty"`
	IntegrationName         string                        `json:"integration_name"`
	IntegrationConfigName   string                        `json:"integration_config_name"`
	IntegrationConfigValues []core.IntegrationConfigValue `json:"integration_config_values"`
	Tags                    map[string]any                `json:"tags"`
	AccountIds              []string                      `json:"account_ids"`
	SkipValidation          bool                          `json:"skip_validation"`
	Source                  string                        `json:"source,omitempty"`
}

type IntegrationDeleteRequest struct {
	IntegrationName         string `json:"integration_name"`
	IntegrationConfigName   string `json:"integration_config_name"`
	IntegrationConfigStatus string `json:"integration_config_status,omitempty"`
	Source                  string `json:"source,omitempty"`
}

type ValidationResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

func handleIntegrationAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}

	switch actionPayload.Action.Name {
	case "integrations_create_config":
		var request IntegrationCreateRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err := core.CreateIntegrationConfig(ctx, request.IntegrationId, request.IntegrationName, request.IntegrationConfigName, request.IntegrationConfigValues, request.Tags, request.AccountIds, request.SkipValidation, request.Source)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		// Audit is persisted by core.CreateIntegrationConfig (CreateAudit, DB) —
		// no MQ publish here to avoid a duplicate write.
		llm.InvalidateLLMServerCacheForAccounts(ctx, request.AccountIds)
		return
	case "integrations_delete_config":
		var request IntegrationDeleteRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		// Capture linked account IDs BEFORE delete — the junction rows are
		// cascade-deleted by core.DeleteIntegrationConfig, so a post-mutation
		// query would return nothing.
		var affectedAccountIds []string
		if ids, lerr := core.ListLinkedCloudAccountIDs(ctx, request.IntegrationName, request.IntegrationConfigName, request.Source); lerr != nil {
			// Error level (not Warn): a lookup failure here directly produces
			// the silent-staleness bug this PR exists to prevent — the delete
			// proceeds, the response is 200, and every replica's cache stays
			// stale for up to the per-cache TTL. tenant_id is included so a
			// support report ("I deleted X and the agent still uses it") can
			// be correlated to the failing lookup.
			ctx.GetLogger().Error("integrations: failed to list affected accounts before delete (best-effort, cache will stay stale until TTL)",
				"error", lerr,
				"tenant_id", ctx.GetSecurityContext().GetTenantId(),
				"integration_name", request.IntegrationName,
				"integration_config_name", request.IntegrationConfigName)
		} else {
			affectedAccountIds = ids
		}

		err = core.DeleteIntegrationConfig(ctx, request.IntegrationName, request.IntegrationConfigName, request.Source)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"status": "success"})
		// Audit is persisted by core.DeleteIntegrationConfig (CreateAudit, DB) —
		// no MQ publish here to avoid a duplicate write.
		llm.InvalidateLLMServerCacheForAccounts(ctx, affectedAccountIds)
		return
	case "integrations_get_schema":
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err2 := core.IntegrationConfigs(ctx, request["integration_name"], request["source"])

		if err2 != nil {
			logger.Error("integration: error creating access", "error", err2)
			c.JSON(400, common.ErrorActionBadRequest(err2.Error()))
			return
		}

		c.JSON(200, map[string]any{
			"data": resp,
		})
		return
	case "integration_list_config":
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		resp, err2 := core.ListIntegrationConfigs(ctx, request["account_id"], request["integration_name"])

		if err2 != nil {
			logger.Error("integration: error creating access", "error", err2)
			c.JSON(400, common.ErrorActionBadRequest(err2.Error()))
			return
		}

		c.JSON(200, map[string]any{
			"data": resp,
		})
		return
	case "integrations_update_status":
		var request IntegrationDeleteRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		err = core.UpdateIntegrationConfigStatus(ctx, request.IntegrationName, request.IntegrationConfigName, request.IntegrationConfigStatus)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"status": "success"})
		// Audit is persisted by core.UpdateIntegrationConfigStatus (CreateAudit,
		// DB) — no MQ publish here to avoid a duplicate write.
		// integrations_update_status doesn't change which accounts are linked
		// (UpdateIntegrationConfigStatus only mutates integrations.status),
		// so a post-mutation lookup is safe — unlike the delete path. If a
		// future contributor adds account-link side-effects to status
		// updates, this lookup must move pre-mutation.
		if ids, lerr := core.ListLinkedCloudAccountIDs(ctx, request.IntegrationName, request.IntegrationConfigName, ""); lerr != nil {
			// Error level (not Warn): see integrations_delete_config rationale —
			// silent staleness is the failure mode this PR exists to prevent.
			ctx.GetLogger().Error("integrations: failed to list affected accounts after status update (best-effort, cache will stay stale until TTL)",
				"error", lerr,
				"tenant_id", ctx.GetSecurityContext().GetTenantId(),
				"integration_name", request.IntegrationName,
				"integration_config_name", request.IntegrationConfigName)
		} else {
			llm.InvalidateLLMServerCacheForAccounts(ctx, ids)
		}
		return

	case "integrations_test_connection", "integrations_check_connection":
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode test connection request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		integrationID := request["integration_id"]
		if integrationID == "" {
			c.JSON(400, common.ErrorActionBadRequest("integration_id is required"))
			return
		}

		testErr := core.TestIntegrationConnection(ctx, integrationID)
		if testErr != nil {
			c.JSON(200, ValidationResponse{
				Success: false,
				Message: "Connection test failed",
				Error:   testErr.Error(),
			})
			return
		}

		c.JSON(200, ValidationResponse{
			Success: true,
			Message: "Connection successful",
		})
		return

	case "integrations_test_connection_config", "integrations_check_connection_config":
		var request IntegrationCreateRequest
		err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode test connection config request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.IntegrationName == "" {
			c.JSON(400, common.ErrorActionBadRequest("integration_name is required"))
			return
		}

		testErr := core.TestIntegrationConnectionByConfig(
			ctx,
			request.IntegrationName,
			request.IntegrationConfigValues,
			request.AccountIds,
			request.Source,
			request.IntegrationId,
		)
		if testErr != nil {
			c.JSON(200, ValidationResponse{
				Success: false,
				Message: "Connection test failed",
				Error:   testErr.Error(),
			})
			return
		}

		c.JSON(200, ValidationResponse{
			Success: true,
			Message: "Connection successful",
		})
		return

	case "webhook_subject_mappings_sync":
		var request integrations.WebhookSubjectMappingsSyncRequest
		if err := common.UnmarshalMapToStruct(actionPayload.Input["request"].(map[string]interface{}), &request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := integrations.SyncWebhookSubjectMappings(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return

	case "integrations_trigger_config_push":
		inputRequest, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(inputRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		accountID := request["account_id"]
		if accountID == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id is required"))
			return
		}
		// Verify the account belongs to the caller's tenant
		tenantAccounts, accErr := security.GetAccountIdsByTenantId(ctx.GetSecurityContext().GetTenantId())
		if accErr != nil {
			c.JSON(500, common.ErrorActionBadRequest("failed to validate account ownership"))
			return
		}
		accountOwned := false
		for _, a := range tenantAccounts {
			if a == accountID {
				accountOwned = true
				break
			}
		}
		if !accountOwned {
			c.JSON(403, common.ErrorActionBadRequest("account does not belong to this tenant"))
			return
		}
		core.TriggerProxyConfigPush(accountID)
		c.JSON(200, map[string]string{"status": "success"})
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryIntegration,
			EventType:     audit.EventTypeIntegrationUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "integration",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "integrations_autogen_options":
		var req struct {
			AutogenFunc string         `json:"autogen_func"`
			FormValues  map[string]any `json:"form_values"`
		}
		if actionPayload.Input == nil {
			c.JSON(400, common.ErrorActionBadRequest("missing input payload"))
			return
		}
		reqMap, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("missing or invalid request payload"))
			return
		}
		if err := common.UnmarshalMapToStruct(reqMap, &req); err != nil {
			slog.Error("integrations_autogen_options: decode failed", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if req.AutogenFunc == "" {
			c.JSON(400, common.ErrorActionBadRequest("autogen_func is required"))
			return
		}
		handler, ok := core.GetAutoGenHandler(req.AutogenFunc)
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("unknown autogen_func: "+req.AutogenFunc))
			return
		}
		result, err := handler(ctx, req.FormValues)
		if err != nil {
			logger.Warn("integrations_autogen_options: handler error", "autogen_func", req.AutogenFunc, "error", err)
			// Return a soft empty result with the error message so the UI can show
			// it inline. The form remains usable as free-text.
			c.JSON(200, map[string]any{
				"options": []core.AutoGenOption{},
				"message": err.Error(),
			})
			return
		}
		if result.Options == nil {
			result.Options = []core.AutoGenOption{}
		}
		c.JSON(200, map[string]any{
			"options": result.Options,
			"message": result.Message,
		})
		return

	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}
