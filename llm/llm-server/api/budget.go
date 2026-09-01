package api

import (
	"log/slog"
	"strings"

	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

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
