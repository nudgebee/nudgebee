package api

import (
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/integrations"
	"nudgebee/services/integrations/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"
	"nudgebee/services/user"
	"nudgebee/services/vmpackage"
	"strings"
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

type IntegrationDiagnosisResponse struct {
	Success           bool      `json:"success"`
	TestStatus        string    `json:"test_status"`
	Health            string    `json:"health"`
	Stage             string    `json:"stage"`
	ReasonCode        string    `json:"reason_code"`
	Summary           string    `json:"summary"`
	RecommendedAction string    `json:"recommended_action"`
	CheckedAt         time.Time `json:"checked_at"`
}

var (
	listDiagnosisIntegrationAccountIDs = core.ListLinkedCloudAccountIDsByIntegrationID
	testDiagnosisIntegrationConnection = core.DiagnoseIntegrationConnectionForAccount
)

var errIntegrationNotFound = errors.New("integration not found")

// ESIndexesResponse is returned by integrations_list_es_indexes: the cluster's
// queryable index targets (data-stream / index names) for the ES index picker.
type ESIndexesResponse struct {
	Indexes []string `json:"indexes"`
	Error   string   `json:"error,omitempty"`
}

// notifyGoogleChatBinding asks notifications-server to post a bind/unbind notice
// into the space (and have the bot leave it on unbind). Best-effort.
func notifyGoogleChatBinding(tenantId, spaceId, event string) {
	if tenantId == "" || config.Config.NotificationServiceUrl == "" {
		return
	}
	headers := map[string]string{
		"Content-Type": "application/json",
	}
	// Attach the optional X-ACTION-TOKEN when configured.
	if config.Config.NotificationServiceToken != "" {
		headers["X-ACTION-TOKEN"] = config.Config.NotificationServiceToken
	}
	resp, err := common.HttpPost(
		fmt.Sprintf("%s/api/integrations/google-chat/notify", config.Config.NotificationServiceUrl),
		common.HttpWithTimeout(10*time.Second),
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(map[string]any{"space_id": spaceId, "event": event, "tenant_id": tenantId}),
	)
	if err != nil {
		slog.Warn("integrations: google chat notice failed (best-effort)", "error", err, "space_id", spaceId, "event", event)
		return
	}
	defer func() { _ = resp.Body.Close() }()
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
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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

		// Capture the accounts linked BEFORE the save. CreateIntegrationConfig doubles
		// as the update path and unlinks every account absent from request.AccountIds,
		// so those accounts' cached provider/tool state goes stale — and they are, by
		// definition, the accounts the request does not name. Post-mutation their link
		// rows are gone, so this has to run first. Looked up by id, not name, because
		// the same call can rename the integration. Mirrors the pre-mutation lookup in
		// integrations_delete_config.
		var preSaveAccountIds []string
		if ids, lerr := core.ListLinkedCloudAccountIDsByIntegrationID(ctx, request.IntegrationId); lerr != nil {
			// Error level (not Warn): see integrations_delete_config rationale —
			// silent staleness is the failure mode this lookup exists to prevent.
			ctx.GetLogger().Error("integrations: failed to list linked accounts before save (best-effort, cache will stay stale until TTL)",
				"error", lerr,
				"tenant_id", ctx.GetSecurityContext().GetTenantId(),
				"integration_id", request.IntegrationId,
				"integration_name", request.IntegrationName)
		} else {
			preSaveAccountIds = ids
		}

		resp, err := core.CreateIntegrationConfig(ctx, request.IntegrationId, request.IntegrationName, request.IntegrationConfigName, request.IntegrationConfigValues, request.Tags, request.AccountIds, request.SkipValidation, request.Source)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		// Audit is persisted by core.CreateIntegrationConfig (CreateAudit, DB) —
		// no MQ publish here to avoid a duplicate write.
		onIntegrationConfigChanged(ctx, mergeAccountIds(request.AccountIds, preSaveAccountIds))
		// Only announce on a genuine new binding (create). A config update of an
		// existing space (e.g. toggling is_default) carries an IntegrationId and
		// must not re-post the "now connected" card into the space.
		if request.IntegrationName == integrations.IntegrationGoogleChatSpace && request.IntegrationId == "" {
			notifyGoogleChatBinding(ctx.GetSecurityContext().GetTenantId(), request.IntegrationConfigName, "bound")
		}
		return
	case "integrations_delete_config":
		var request IntegrationDeleteRequest
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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
		onIntegrationConfigChanged(ctx, affectedAccountIds)
		if request.IntegrationName == integrations.IntegrationGoogleChatSpace {
			notifyGoogleChatBinding(ctx.GetSecurityContext().GetTenantId(), request.IntegrationConfigName, "unbound")
		}
		return
	case "integrations_get_schema":
		request := map[string]string{}
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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

		// The form offers the Log Label Mapping editor iff the property survives here,
		// so the decision lives in one place and the frontend needs no provider list of
		// its own. Category alone is too coarse — jaeger and otel_clickhouse carry a log
		// category but serve traces only, and an editor there would be dead config.
		// Accepting the key stays broader than offering it (see CreateIntegrationConfig):
		// an already-saved mapping must not become unsaveable.
		resp = withLogLabelMappingOffer(resp, request["integration_name"], request["source"])

		c.JSON(200, map[string]any{
			"data": resp,
		})
		return
	case "integration_list_config":
		request := map[string]string{}
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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

		// Disabling an integration: immediately clear its discovered identity accounts
		// so the Integration Profiles UI reflects the change now rather than after the
		// next identity-sync run. Best-effort — sweepDisabledIntegrations is the
		// backstop, so a failure here only delays cleanup, never loses a manual mapping.
		if strings.EqualFold(request.IntegrationConfigStatus, string(core.IntegrationStatusDisabled)) {
			if perr := user.PurgeDisabledIntegrationAccounts(ctx, request.IntegrationName, request.IntegrationConfigName); perr != nil {
				ctx.GetLogger().Error("integrations: failed to purge identity accounts after disable (best-effort, next sync will reconcile)",
					"error", perr,
					"tenant_id", ctx.GetSecurityContext().GetTenantId(),
					"integration_name", request.IntegrationName,
					"integration_config_name", request.IntegrationConfigName)
			}
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
			onIntegrationConfigChanged(ctx, ids)
		}
		return

	case "integrations_test_connection", "integrations_check_connection":
		request := map[string]string{}
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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

	case "integrations_diagnose_connection":
		request := map[string]string{}
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		if err := common.UnmarshalMapToStruct(requestInput, &request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		integrationID := strings.TrimSpace(request["integration_id"])
		if integrationID == "" {
			c.JSON(400, common.ErrorActionBadRequest("integration_id is required"))
			return
		}

		diagnosis, diagnosisErr := diagnoseIntegrationConnection(ctx, integrationID)
		if errors.Is(diagnosisErr, errIntegrationNotFound) {
			c.JSON(404, common.ErrorActionBadRequest("integration not found"))
			return
		}
		if diagnosisErr != nil {
			ctx.GetLogger().Error("integrations: diagnosis failed", "integration_id", integrationID, "error", diagnosisErr)
			c.JSON(500, common.ErrorActionInternal("integration diagnosis failed"))
			return
		}
		c.JSON(200, diagnosis)
		return

	case "integrations_test_connection_config", "integrations_check_connection_config":
		var request IntegrationCreateRequest
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		err := common.UnmarshalMapToStruct(requestInput, &request)
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

	case "integrations_list_es_indexes":
		// Lists the Elasticsearch cluster's queryable index targets from raw config
		// values (add flow: the integration isn't saved yet), for the per-account
		// index picker. Mirrors integrations_check_connection_config's request shape;
		// the edit form sends stored ciphertext + is_encrypted for untyped secrets.
		reqInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		var request IntegrationCreateRequest
		if err := common.UnmarshalMapToStruct(reqInput, &request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		decrypted, decErr := core.DecryptConfigValues(request.IntegrationConfigValues)
		if decErr != nil {
			c.JSON(200, ESIndexesResponse{Error: decErr.Error()})
			return
		}
		cfg := observability.BuildElasticsearchConfigFromValues(decrypted)
		if cfg.Url == "" {
			c.JSON(200, ESIndexesResponse{Error: "Elasticsearch URL is required"})
			return
		}
		indexes, listErr := observability.ListAllESIndexTargets(cfg)
		if listErr != nil {
			c.JSON(200, ESIndexesResponse{Error: listErr.Error()})
			return
		}
		c.JSON(200, ESIndexesResponse{Indexes: indexes})
		return

	case "webhook_subject_mappings_sync":
		// Tenant admins and super admins only. The sync rewrites the tenant-wide
		// webhook_subject_mappings table from every resolved incident in the
		// connected Datadog / PagerDuty / Zenduty account, and that table decides
		// which subject future alerts are attributed to — a bad sync silently
		// misroutes the tenant's incidents. actions.yaml lists the same two roles;
		// this is the half that holds if the action is ever reached another way.
		if sc := ctx.GetSecurityContext(); !sc.IsTenantAdmin() && !sc.IsSuperAdmin() {
			c.JSON(403, common.ErrorActionForbidden("only tenant admins can sync webhook subject mappings"))
			return
		}
		var request integrations.WebhookSubjectMappingsSyncRequest
		requestInput, ok := actionPayload.Input["request"].(map[string]interface{})
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid request input"))
			return
		}
		if err := common.UnmarshalMapToStruct(requestInput, &request); err != nil {
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

	case "integrations_upsert_discovery_target":
		reqMap, ok := actionPayload.Input["request"].(map[string]any)
		if !ok {
			c.JSON(400, common.ErrorActionBadRequest("invalid or missing request payload"))
			return
		}
		var request vmpackage.DiscoveryTargetRequest
		if err := common.UnmarshalMapToStruct(reqMap, &request); err != nil {
			slog.Error("integrations: failed to decode request", "error", err)
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if err := common.ValidateStruct(request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if err := vmpackage.UpsertDiscoveryTarget(ctx, request); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, map[string]string{"status": "success"})
		return

	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}

func diagnoseIntegrationConnection(ctx *security.RequestContext, integrationID string) (IntegrationDiagnosisResponse, error) {
	accountIDs, err := listDiagnosisIntegrationAccountIDs(ctx, integrationID)
	if err != nil {
		return IntegrationDiagnosisResponse{}, err
	}
	securityContext := ctx.GetSecurityContext()
	authorizedAccountID := ""
	for _, accountID := range accountIDs {
		if securityContext.HasAccountAccess(accountID, security.SecurityAccessTypeRead) {
			authorizedAccountID = accountID
			break
		}
	}
	if authorizedAccountID == "" {
		// Use the same response for missing, cross-tenant and inaccessible
		// integrations so callers cannot enumerate integration identifiers.
		return IntegrationDiagnosisResponse{}, errIntegrationNotFound
	}

	checkedAt := time.Now().UTC()
	if testErr := testDiagnosisIntegrationConnection(ctx, integrationID, authorizedAccountID); testErr != nil {
		if errors.Is(testErr, core.ErrConnectionTestNotSupported) {
			return IntegrationDiagnosisResponse{
				Success: false, TestStatus: "not_supported", Health: "unknown", Stage: "connection",
				ReasonCode:        "CONNECTION_TEST_NOT_SUPPORTED",
				Summary:           "This integration does not provide an active connection test.",
				RecommendedAction: "Verify that expected data or events are arriving after the integration is enabled.", CheckedAt: checkedAt,
			}, nil
		}
		stage, reasonCode, summary, recommendedAction := classifyIntegrationDiagnosisError(testErr)
		ctx.GetLogger().Warn("integrations: connection diagnosis test failed",
			"integration_id", integrationID,
			"stage", stage,
			"reason_code", reasonCode,
		)
		return IntegrationDiagnosisResponse{
			Success: false, TestStatus: "failed", Health: "unhealthy", Stage: stage,
			ReasonCode: reasonCode, Summary: summary,
			RecommendedAction: recommendedAction, CheckedAt: checkedAt,
		}, nil
	}
	return IntegrationDiagnosisResponse{
		Success: true, TestStatus: "passed", Health: "healthy", Stage: "connection",
		ReasonCode: "CONNECTION_SUCCEEDED", Summary: "The integration connection test succeeded.",
		RecommendedAction: "No connection remediation is required.", CheckedAt: checkedAt,
	}, nil
}

func classifyIntegrationDiagnosisError(err error) (stage, reasonCode, summary, recommendedAction string) {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "no such host"), strings.Contains(message, "dns"):
		return "dns", "DNS_RESOLUTION_FAILED", "The integration host could not be resolved.", "Verify the configured hostname and DNS resolution from the Nudgebee connection path."
	case strings.Contains(message, "x509"), strings.Contains(message, "certificate"), strings.Contains(message, "tls"):
		return "tls", "TLS_VALIDATION_FAILED", "The integration endpoint failed TLS certificate validation.", "Verify the endpoint certificate, hostname, validity period, and trusted certificate authority."
	case strings.Contains(message, "401"), strings.Contains(message, "unauthorized"), strings.Contains(message, "authentication"), strings.Contains(message, "credential"):
		return "authentication", "AUTHENTICATION_FAILED", "The integration endpoint rejected authentication.", "Update or re-authorize the integration credentials, then retry the connection test."
	case strings.Contains(message, "403"), strings.Contains(message, "forbidden"), strings.Contains(message, "permission denied"):
		return "authorization", "AUTHORIZATION_FAILED", "The configured identity is not authorized to access the integration endpoint.", "Grant the configured identity the required provider permissions, then retry."
	case strings.Contains(message, "no accounts associated"), strings.Contains(message, "configuration"), strings.Contains(message, "url is required"), strings.Contains(message, "endpoint is required"):
		return "configuration", "CONFIGURATION_INVALID", "The integration configuration is incomplete or invalid.", "Review the integration settings and linked accounts, then retry the connection test."
	case strings.Contains(message, "deadline exceeded"), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return "connectivity", "CONNECTION_TIMEOUT", "The integration endpoint did not respond before the connection test timed out.", "Verify endpoint availability, routing, firewall rules, and proxy settings."
	case strings.Contains(message, "connection refused"), strings.Contains(message, "network is unreachable"), strings.Contains(message, "no route to host"), strings.Contains(message, "dial tcp"):
		return "connectivity", "ENDPOINT_UNREACHABLE", "The integration endpoint could not be reached.", "Verify the endpoint address, service availability, routing, and firewall rules."
	default:
		return "connection", "CONNECTION_FAILED", "The integration connection test failed.", "Review the integration configuration and provider availability, then retry."
	}
}

// withLogLabelMappingOffer decides whether the integration form shows the Log Label
// Mapping editor, by removing the auto-injected config property from the schema when
// this provider has no log source. Lives in the api layer because it is the only one
// that may import both the integration registry and the observability source registry.
//
// Returns the schema with a cloned Properties map — ConfigSchema() implementations
// commonly hand back a shared/static map, and deleting a key in place would strip the
// property from every later caller.
func withLogLabelMappingOffer(schema core.IntegrationSchema, integrationName, source string) core.IntegrationSchema {
	if _, offered := schema.Properties[core.LogLabelMappingsConfigName]; !offered {
		return schema
	}
	if source == "" {
		source = "user"
	}
	if observability.SupportsLogSource(integrationName, source) {
		return schema
	}

	cloned := make(map[string]core.IntegrationSchemaProperty, len(schema.Properties))
	for k, v := range schema.Properties {
		if k == core.LogLabelMappingsConfigName {
			continue
		}
		cloned[k] = v
	}
	schema.Properties = cloned
	return schema
}
