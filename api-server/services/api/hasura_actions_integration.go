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

func handleHasuraIntegrationAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	switch hasuraPayload.Action.Name {
	case "integrations_create_config":
		var request IntegrationCreateRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := core.CreateIntegrationConfig(ctx, request.IntegrationId, request.IntegrationName, request.IntegrationConfigName, request.IntegrationConfigValues, request.Tags, request.AccountIds, request.SkipValidation, request.Source)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		// Audit publish first — it's the critical path. Cache invalidation is
		// a side effect; even though the helper is fully async (goroutine
		// launch), keeping audit ordered first makes the intent obvious to
		// future readers and answers reviewer feedback (PR #29489 round 1).
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryIntegration,
			EventType:     audit.EventTypeIntegrationCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "integration",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		llm.InvalidateLLMServerCacheForAccounts(ctx, request.AccountIds)
		return
	case "integrations_delete_config":
		var request IntegrationDeleteRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
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
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"status": "success"})
		// Audit before invalidation — same rationale as integrations_create_config.
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryIntegration,
			EventType:     audit.EventTypeIntegrationDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "integration",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		llm.InvalidateLLMServerCacheForAccounts(ctx, affectedAccountIds)
		return
	case "integrations_get_schema":
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err2 := core.IntegrationConfigs(ctx, request["integration_name"], request["source"])

		if err2 != nil {
			logger.Error("integration: error creating access", "error", err2)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err2.Error()))
			return
		}

		c.JSON(200, map[string]any{
			"data": resp,
		})
		return
	case "integration_list_config":
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err2 := core.ListIntegrationConfigs(ctx, request["account_id"], request["integration_name"])

		if err2 != nil {
			logger.Error("integration: error creating access", "error", err2)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err2.Error()))
			return
		}

		c.JSON(200, map[string]any{
			"data": resp,
		})
		return
	case "integrations_update_status":
		var request IntegrationDeleteRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = core.UpdateIntegrationConfigStatus(ctx, request.IntegrationName, request.IntegrationConfigName, request.IntegrationConfigStatus)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"status": "success"})
		// Audit before invalidation — same rationale as integrations_create_config.
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

	case "integrations_test_connection":
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode test connection request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		integrationID := request["integration_id"]
		if integrationID == "" {
			c.JSON(400, common.ErrorHasuraActionBadRequest("integration_id is required"))
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

	case "integrations_test_connection_config":
		var request IntegrationCreateRequest
		err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request)
		if err != nil {
			slog.Error("integrations: failed to decode test connection config request", "error", err)
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		if request.IntegrationName == "" {
			c.JSON(400, common.ErrorHasuraActionBadRequest("integration_name is required"))
			return
		}

		testErr := core.TestIntegrationConnectionByConfig(
			ctx,
			request.IntegrationName,
			request.IntegrationConfigValues,
			request.AccountIds,
			request.Source,
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
		if err := common.UnmarshalMapToStruct(hasuraPayload.Input["request"].(map[string]interface{}), &request); err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := integrations.SyncWebhookSubjectMappings(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return

	case "integrations_trigger_config_push":
		inputRequest, ok := hasuraPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorHasuraActionBadRequest("invalid request input"))
			return
		}
		request := map[string]string{}
		err := common.UnmarshalMapToStruct(inputRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		accountID := request["account_id"]
		if accountID == "" {
			c.JSON(400, common.ErrorHasuraActionBadRequest("account_id is required"))
			return
		}
		// Verify the account belongs to the caller's tenant
		tenantAccounts, accErr := security.GetAccountIdsByTenantId(ctx.GetSecurityContext().GetTenantId())
		if accErr != nil {
			c.JSON(500, common.ErrorHasuraActionBadRequest("failed to validate account ownership"))
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
			c.JSON(403, common.ErrorHasuraActionBadRequest("account does not belong to this tenant"))
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

	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("invalid action name - "+hasuraPayload.Action.Name))
		return
	}
}
