package api

import (
	"log/slog"
	"strings"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/audit"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// budgetActionToEventType maps a budget.ChangeAction to its audit EventType.
var budgetActionToEventType = map[string]audit.EventType{
	budget.ChangeActionCreate:   audit.EventTypeBudgetConfigCreate,
	budget.ChangeActionDelete:   audit.EventTypeBudgetConfigDelete,
	budget.ChangeActionIncrease: audit.EventTypeBudgetConfigIncrease,
	budget.ChangeActionDecrease: audit.EventTypeBudgetConfigDecrease,
	budget.ChangeActionEnable:   audit.EventTypeBudgetConfigEnable,
	budget.ChangeActionDisable:  audit.EventTypeBudgetConfigDisable,
}

// budgetActionToEventAction maps a budget.ChangeAction to the coarse
// CRUD-style EventAction the existing Audit Log action filter understands.
var budgetActionToEventAction = map[string]audit.EventAction{
	budget.ChangeActionCreate:   audit.EventActionCreate,
	budget.ChangeActionDelete:   audit.EventActionDelete,
	budget.ChangeActionIncrease: audit.EventActionUpdate,
	budget.ChangeActionDecrease: audit.EventActionUpdate,
	budget.ChangeActionEnable:   audit.EventActionUpdate,
	budget.ChangeActionDisable:  audit.EventActionUpdate,
}

// emitBudgetHistoryAudit diffs before/after budget config rows and batches one
// audit entry per changed budget type into a single relay call. Audit failures
// are logged, not returned — the config write already succeeded, so a history
// recording hiccup must not surface to the caller as a failed save.
func emitBudgetHistoryAudit(ctx *security.RequestContext, before, after *budget.BudgetConfig, entityType, entityID, tenantId, module, userId string, logger *slog.Logger) {
	changes := budget.DiffBudgetConfig(before, after)
	if len(changes) == 0 {
		return
	}

	accountId := ""
	if entityType == budget.EntityTypeAccount {
		accountId = entityID
	}

	now := time.Now().UTC()
	entries := make([]audit.Audit, 0, len(changes))
	for _, change := range changes {
		// EventState is required by the ingest endpoint; a Delete has no new
		// value, so substitute the same placeholder used elsewhere for
		// delete-type audits (e.g. chat conversation delete).
		newValue := any(change.NewValue)
		if change.NewValue == nil {
			newValue = map[string]any{"deleted": true}
		}
		entries = append(entries, audit.Audit{
			UserId:         userId,
			TenantId:       tenantId,
			AccountId:      accountId,
			EventTime:      now,
			EventCategory:  audit.EventCategoryBudget,
			EventType:      budgetActionToEventType[change.Action],
			EventPrevState: change.PrevValue,
			EventState:     newValue,
			EventActor:     audit.EventActorUiService,
			EventTarget:    entityID,
			EventAction:    budgetActionToEventAction[change.Action],
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{"budget_type": change.BudgetType, "module": module},
		})
	}

	if err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: entries}); err != nil {
		logger.Error("api: failed to record budget change history", "error", err, "entity_type", entityType, "entity_id", entityID)
	}
}

// actionError returns an error response in the format RPC action webhooks expect: { "message": "..." }
func actionError(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"message": msg})
}

// requireBudgetAdminTenant gates the three budget-config endpoints and returns
// the tenant they must operate inside, or "" after writing the error response.
//
// The tenant scope binds EVERY caller, super admins included. A super-admin
// session still carries the tenant it is viewing (x-tenant-id / session
// variables), and the Usage & Limits screen these endpoints serve is
// single-tenant: it labels every row with the session tenant's name and reads
// usage back for the session tenant only. Letting a super admin see or write
// another tenant's rows both leaked those rows and made an edit land on a row
// the usage panel never reads — the config showed the new value while the
// usage section kept the old one (#37020).
func requireBudgetAdminTenant(c *gin.Context, sc *security.SecurityContext) string {
	if !sc.IsSuperAdmin() && !sc.IsTenantAdmin() {
		actionError(c, 403, "api: admin access required")
		return ""
	}
	tenantId := sc.GetTenantId()
	if tenantId == "" {
		actionError(c, 400, "api: unable to identify tenant")
		return ""
	}
	return tenantId
}

// canAccessAccountBudget reports whether the caller may act on accountId's
// budget config. HasAccountAccess alone is not enough: it returns true for ANY
// account id once the caller is a super admin, so the tenant-membership check
// is what keeps a super-admin session inside the tenant it is viewing.
func canAccessAccountBudget(sc *security.SecurityContext, tenantId, accountId string, access security.SecurityAccessType) bool {
	return security.IsAccountInTenant(accountId, tenantId) && sc.HasAccountAccess(accountId, access)
}

// handleBudgetStatusApi registers all budget endpoints (all POST for RPC action compatibility)
func handleBudgetStatusApi(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/budget")

	// POST /v1/budget/status — get current usage + limits
	group.POST("/status", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("budget_status")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}

		var request budget.BudgetStatusRequest
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			actionError(c, 400, "api: "+err.Error())
			return
		}

		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			if reqMap, ok := actionRequestPayload["request"].(map[string]any); ok {
				actionRequestPayload = reqMap
			}
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}

		logger := slog.With("account_id", request.AccountID)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		// Empty account_id → tenant-wide read (Settings opened from the
		// global sidebar). RBAC then degrades to tenant-level: a valid
		// session is enough since the service skips the per-account
		// branch and returns only tenant-scope data.
		if request.AccountID != "" {
			if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountID, security.SecurityAccessTypeRead) {
				actionError(c, 403, errorUserAccessMessage)
				return
			}
		}

		tenantId := agentContext.GetSecurityContext().GetTenantId()
		budgetStatus, err := budget.GetBudgetStatus(tenantId, request.AccountID, logger)
		if err != nil {
			logger.Error("api: failed to get budget status", "error", err)
			actionError(c, 500, "api: failed to retrieve budget status")
			return
		}

		c.JSON(200, buildApiResponse(budgetStatus, nil))
	})

	// POST /v1/budget/config/list — list budget configs with optional filters
	group.POST("/config/list", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("budget_config_list")

		requestMap := make(map[string]any)
		_ = c.ShouldBindJSON(&requestMap)

		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)

		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			if reqMap, ok := actionRequestPayload["request"].(map[string]any); ok {
				actionRequestPayload = reqMap
			}
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}

		logger := slog.Default()
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		sc := agentContext.GetSecurityContext()
		tenantId := requireBudgetAdminTenant(c, sc)
		if tenantId == "" {
			return
		}

		var listReq budget.BudgetConfigListRequest
		_ = common.DecodeMapToStruct(actionRequestPayload, &listReq)

		entityType := listReq.EntityType
		entityID := listReq.EntityID
		module := listReq.Module

		switch entityType {
		case budget.EntityTypeTenant:
			if entityID != "" && entityID != tenantId {
				actionError(c, 403, errorUserAccessMessage)
				return
			}
			entityID = tenantId
		case budget.EntityTypeAccount:
			if entityID != "" && !canAccessAccountBudget(sc, tenantId, entityID, security.SecurityAccessTypeRead) {
				actionError(c, 403, errorUserAccessMessage)
				return
			}
			// entityID="" means list every account config IN THIS TENANT — the
			// tenant filter is pushed into SQL below.
		default:
			// No entity_type specified — show tenant configs for the caller's tenant
			entityType = budget.EntityTypeTenant
			entityID = tenantId
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			actionError(c, 500, "api: database error")
			return
		}

		listAllTenantAccounts := entityType == budget.EntityTypeAccount && entityID == ""

		var configs []budget.BudgetConfig
		if listAllTenantAccounts {
			configs, err = budget.ListAccountBudgetConfigsForTenant(dbManager, tenantId, module)
		} else {
			configs, err = budget.ListBudgetConfigs(dbManager, entityType, entityID, module)
		}
		if err != nil {
			slog.Error("api: failed to list budget configs", "error", err)
			actionError(c, 500, "api: failed to list budget configs")
			return
		}

		// Narrow the tenant's account configs to the accounts this caller may
		// actually read (a scoped account admin sees only their own).
		if listAllTenantAccounts {
			filtered := make([]budget.BudgetConfig, 0, len(configs))
			for _, cfg := range configs {
				if sc.HasAccountAccess(cfg.EntityID, security.SecurityAccessTypeRead) {
					filtered = append(filtered, cfg)
				}
			}
			configs = filtered
		}

		c.JSON(200, gin.H{"data": configs})
	})

	// POST /v1/budget/config/upsert — create or update budget config
	group.POST("/config/upsert", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("budget_config_upsert")

		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}

		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}

		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			if reqMap, ok := actionRequestPayload["request"].(map[string]any); ok {
				actionRequestPayload = reqMap
			}
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}

		var request budget.BudgetConfigUpsertRequest
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			actionError(c, 400, "api: "+err.Error())
			return
		}

		logger := slog.With("entity_type", request.EntityType, "entity_id", request.EntityID, "module", request.Module)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		sc := agentContext.GetSecurityContext()

		if request.EntityType == "" || request.EntityID == "" || request.Module == "" {
			actionError(c, 400, "api: entity_type, entity_id, and module are required")
			return
		}

		tenantId := requireBudgetAdminTenant(c, sc)
		if tenantId == "" {
			return
		}

		// budget_disabled field requires super_admin to SET (enable bypass)
		// Non-super-admins can implicitly CLEAR it by saving any config (re-enables enforcement)
		if request.BudgetDisabled != nil && *request.BudgetDisabled && !sc.IsSuperAdmin() {
			actionError(c, 403, "api: only super admins can disable budget checks")
			return
		}

		// Non-super-admin saving config: auto-clear budget_disabled to re-enable enforcement
		if !sc.IsSuperAdmin() && request.BudgetDisabled == nil {
			disabled := false
			request.BudgetDisabled = &disabled
		}

		// The caller's own tenant is the only valid target for a tenant-scoped
		// upsert, whatever entity_id the form posted (the Add-Budget form posts
		// the tenant NAME, not its id). Matches /config/list behaviour and the
		// rest of the llm-server API surface, e.g. functions.go:161,182.
		if request.EntityType == budget.EntityTypeTenant {
			request.EntityID = tenantId
			logger = logger.With("entity_id", request.EntityID)
		}
		if request.EntityType == budget.EntityTypeAccount &&
			!canAccessAccountBudget(sc, tenantId, request.EntityID, security.SecurityAccessTypeCreate) {
			actionError(c, 403, errorUserAccessMessage)
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			actionError(c, 500, "api: database error")
			return
		}

		// Snapshot the pre-write row for change history (nil when this call
		// creates a new row). UpsertBudgetConfig does its own equivalent fetch
		// internally to decide insert-vs-update; this is a second, harmless
		// read kept local to the handler so UpsertBudgetConfig's signature and
		// behavior stay unchanged. A fetch error here must not be treated as
		// "row doesn't exist" — that would misrecord an update to an existing
		// row as a Create with a blank previous value — so skip history for
		// this save rather than guess.
		before, beforeErr := budget.GetBudgetConfig(dbManager, request.EntityType, request.EntityID, request.Module)
		if beforeErr != nil {
			logger.Error("api: failed to fetch pre-write budget config; skipping change-history recording for this save", "error", beforeErr)
		}

		updatedBy := sc.GetUserId()
		result, err := budget.UpsertBudgetConfig(dbManager, &request, updatedBy)
		if err != nil {
			logger.Error("api: failed to upsert budget config", "error", err)
			statusCode := 400
			if strings.Contains(err.Error(), "insert failed") || strings.Contains(err.Error(), "update failed") || strings.Contains(err.Error(), "failed to check existing") {
				statusCode = 500
			}
			actionError(c, statusCode, err.Error())
			return
		}

		if beforeErr == nil {
			emitBudgetHistoryAudit(agentContext, before, result, request.EntityType, request.EntityID, tenantId, request.Module, updatedBy, logger)
		}

		c.JSON(200, gin.H{"data": result})
	})

	// POST /v1/budget/config/delete — delete config (revert to system defaults)
	group.POST("/config/delete", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("budget_config_delete")

		requestMap := make(map[string]any)
		_ = c.ShouldBindJSON(&requestMap)

		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)

		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			if reqMap, ok := actionRequestPayload["request"].(map[string]any); ok {
				actionRequestPayload = reqMap
			}
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}

		logger := slog.Default()
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		sc := agentContext.GetSecurityContext()
		tenantId := requireBudgetAdminTenant(c, sc)
		if tenantId == "" {
			return
		}

		type deleteRequest struct {
			ID string `json:"id" mapstructure:"id"`
		}
		var req deleteRequest
		_ = common.DecodeMapToStruct(actionRequestPayload, &req)

		if req.ID == "" {
			actionError(c, 400, "api: id is required")
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			actionError(c, 500, "api: database error")
			return
		}

		// Fetch config first to validate ownership
		cfg, err := budget.GetBudgetConfigByID(dbManager, req.ID)
		if err != nil {
			actionError(c, 500, "api: failed to fetch budget config")
			return
		}
		if cfg == nil {
			actionError(c, 404, "api: budget config not found")
			return
		}

		// Verify ownership — the row must belong to the caller's tenant.
		if cfg.EntityType == budget.EntityTypeTenant && cfg.EntityID != tenantId {
			actionError(c, 403, errorUserAccessMessage)
			return
		}
		if cfg.EntityType == budget.EntityTypeAccount &&
			!canAccessAccountBudget(sc, tenantId, cfg.EntityID, security.SecurityAccessTypeCreate) {
			actionError(c, 403, errorUserAccessMessage)
			return
		}

		err = budget.DeleteBudgetConfig(dbManager, req.ID)
		if err != nil {
			slog.Error("api: failed to delete budget config", "error", err, "id", req.ID)
			actionError(c, 500, err.Error())
			return
		}

		emitBudgetHistoryAudit(agentContext, cfg, nil, cfg.EntityType, cfg.EntityID, tenantId, cfg.Module, sc.GetUserId(), logger)

		c.JSON(200, gin.H{"data": gin.H{"deleted": req.ID}})
	})

	// POST /v1/budget/system-defaults — read-only system defaults and max caps
	group.POST("/system-defaults", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("budget_system_defaults")

		requestMap := make(map[string]any)
		_ = c.ShouldBindJSON(&requestMap)

		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			actionError(c, 401, err.Error())
			return
		}

		sc := agentContext.GetSecurityContext()
		if !sc.IsSuperAdmin() && !sc.IsTenantAdmin() {
			actionError(c, 403, "api: admin access required")
			return
		}

		defaults := budget.GetSystemDefaults()
		c.JSON(200, gin.H{"data": defaults})
	})
}

// repliesPastCountLimit reports whether a chat request is one the count limits
// must not stop, because it finishes or continues work a person already started:
//
//   - an answer to a waiting prompt (agentId is set), whatever started the
//     conversation, since the run was already admitted and is stranded without it;
//   - a message from a signed-in person in an existing conversation, including one
//     typed into an alert investigation.
//
// An automatic re-run keeps the full check. Alert and automation callers resume a
// session id to reuse a conversation, so an existing conversation alone does not
// make a request a reply; the caller decides. Alert re-runs reach /chat as the
// system user (no user, or the nil UUID), so they never count as a person. The
// automation runner forwards the user it runs as, which can be a real person, so a
// conversation from an automation only passes when it answers a waiting prompt. A
// fresh session has no conversation, so a new investigation is never mistaken for
// a reply.
func repliesPastCountLimit(accountId, sessionId, agentId, callerUserId string, existing *core.Conversation) bool {
	conv := existing
	if conv == nil {
		if sessionId == "" {
			return false
		}
		found, err := core.GetConversationDao().GetConversationBySession(accountId, sessionId)
		if err != nil || found.ID == uuid.Nil {
			return false
		}
		conv = &found
	}
	if agentId != "" {
		return true
	}
	if callerUserId == "" || callerUserId == security.GetSystemUserId() {
		return false
	}
	return conv.Source == nil || *conv.Source != core.ConversationSourceAutomation
}

// checkChatBudgetAndRespond sends a 429 and returns true when the request is over
// budget. A request that repliesPastCountLimit is held to the cost limits only: the
// count limits cap how many conversations are created, so applying them to an
// approval answer or a person's follow-up would strand a run that was already
// admitted. The conversation lookup runs only after the strict check has failed.
func checkChatBudgetAndRespond(c *gin.Context, tenantId, accountId, sessionId, agentId, callerUserId string, existing *core.Conversation, module string, logger *slog.Logger) bool {
	exceeded, message := budget.CheckBudgetLimits(tenantId, accountId, module, logger)
	if exceeded && repliesPastCountLimit(accountId, sessionId, agentId, callerUserId, existing) {
		exceeded, message = budget.CheckBudgetLimitsForReply(tenantId, accountId, module, logger)
	}
	if !exceeded {
		return false
	}
	c.JSON(429, buildApiResponse(nil, []error{common.Error{Message: message}}))
	return true
}
