package api

import (
	"errors"
	"log/slog"
	"time"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/dashboard"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// dashboardInput unwraps the conventional {"request": {...}} envelope the
// gateway sends, falling back to the bare input map.
func dashboardInput(actionPayload *ActionRequest) map[string]any {
	in := actionPayload.Input
	if in == nil {
		return map[string]any{}
	}
	if nested, ok := in["request"].(map[string]any); ok {
		return nested
	}
	return in
}

// A dashboard row carries no account — it is a panel DEFINITION, and each panel
// names the account it queries. So the row itself is readable by any member of
// the tenant, and the real access check is per panel: rendering goes through
// metrics_list / logs / traces, which each gate on the panel's own account.
//
// Gating the row on HasPermission would 403 every built-in-role user —
// customPermissions is empty unless a custom role granted something — while
// dashboards_list happily returns the same rows, so the user would click a row
// in the list and get "access denied".
func dashboardReadAllowed(ctx *security.RequestContext) bool {
	sc := ctx.GetSecurityContext()
	return sc.IsTenantAdmin() || sc.IsTenantReadAdmin() ||
		sc.HasPermission("dashboards", "Read") || sc.HasPermission("dashboards", "Write") ||
		len(sc.GetAccountIds()) > 0
}

// dashboardWriteAllowed lets anyone who can write to at least one account author
// dashboards. Requiring tenant admin would mean an account admin could not build
// a dashboard for the very cluster they administer; the panels they may point it
// at are constrained separately by dashboardPanelAccountsAllowed.
func dashboardWriteAllowed(ctx *security.RequestContext) bool {
	sc := ctx.GetSecurityContext()
	if sc.IsTenantAdmin() || sc.HasPermission("dashboards", "Write") {
		return true
	}
	for _, accountId := range sc.GetAccountIds() {
		if sc.HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
			return true
		}
	}
	return false
}

// dashboardPanelAccountsAllowed refuses a definition whose panels point at
// accounts the caller cannot read. Rendering would 403 per panel anyway; failing
// at save keeps the stored document honest rather than silently broken.
//
// CanReadAccountData, not HasAccountAccess: the latter recognises built-in
// scoped roles ONLY, so a `dashboards:Write` grant holder cleared
// dashboardWriteAllowed above and was then refused here for every account —
// i.e. the grant could never save a dashboard. See the same swap at the two
// execute actions below.
func dashboardPanelAccountsAllowed(ctx *security.RequestContext, def dashboard.Definition) (string, bool) {
	sc := ctx.GetSecurityContext()
	for _, accountId := range dashboard.PanelAccountIds(def) {
		if !sc.CanReadAccountData(accountId, "dashboards") {
			return accountId, false
		}
	}
	return "", true
}

func dashboardError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, dashboard.ErrNotFound):
		c.JSON(404, common.ErrorActionBadRequest(err.Error()))
	case errors.Is(err, dashboard.ErrForbidden):
		c.JSON(403, common.ErrorActionForbidden(err.Error()))
	default:
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
	}
}

func handleDashboardAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorActionBadRequest(err.Error()))
		return
	}
	input := dashboardInput(actionPayload)

	switch actionPayload.Action.Name {
	case "dashboards_list":
		var req dashboard.ListRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardReadAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("access denied for dashboards"))
			return
		}
		resp, err := dashboard.List(ctx, req)
		if err != nil {
			dashboardError(c, err)
			return
		}
		c.JSON(200, resp)
		return

	case "dashboards_get":
		var req dashboard.GetRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardReadAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("access denied for dashboard"))
			return
		}
		resp, err := dashboard.Get(ctx, req)
		if err != nil {
			dashboardError(c, err)
			return
		}
		bindings, err := dashboard.ListBindings(ctx, resp.Id)
		if err != nil {
			dashboardError(c, err)
			return
		}
		c.JSON(200, map[string]any{"dashboard": resp, "bindings": bindings})
		return

	case "dashboards_create", "dashboards_update":
		var req dashboard.SaveRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardWriteAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("write access denied for dashboards"))
			return
		}
		if accountId, ok := dashboardPanelAccountsAllowed(ctx, req.Definition); !ok {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+accountId))
			return
		}
		isCreate := req.Id == ""
		// Snapshot the pre-edit state for the audit trail before mutating.
		var prevState any
		if !isCreate {
			if prev, err := dashboard.Get(ctx, dashboard.GetRequest{Id: req.Id}); err == nil {
				prevState = prev
			}
		}

		resp, err := dashboard.Save(ctx, req)
		if err != nil {
			dashboardError(c, err)
			return
		}

		// Audit runs AFTER the service transaction has committed —
		// audit.CreateAudit opens its own connection and must never be called
		// inside a Metastore tx.
		eventType := audit.EventTypeDashboardUpdate
		eventAction := audit.EventActionUpdate
		if isCreate {
			eventType = audit.EventTypeDashboardCreate
			eventAction = audit.EventActionCreate
		}
		// AccountId is intentionally empty: a dashboard belongs to the tenant, and
		// the accounts it touches live on its panels (recorded in EventState).
		auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       ctx.GetSecurityContext().GetTenantId(),
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryDashboard,
			EventTarget:    resp.Title,
			EventType:      eventType,
			EventState:     resp,
			EventPrevState: prevState,
			EventActor:     audit.EventActorUiService,
			EventAction:    eventAction,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{"dashboard_id": resp.Id, "slug": resp.Slug},
		}}})
		if auditErr != nil {
			ctx.GetLogger().Error("failed to create dashboard audit event", "error", auditErr)
		}

		c.JSON(200, resp)
		return

	case "dashboards_delete":
		var req dashboard.DeleteRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardWriteAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("write access denied for dashboard"))
			return
		}
		// Converted rather than rebuilt field by field: the two requests are the
		// same shape, and staticcheck (S1016) rejects the literal form.
		existing, err := dashboard.Get(ctx, dashboard.GetRequest(req))
		if err != nil {
			dashboardError(c, err)
			return
		}
		if err := dashboard.Delete(ctx, req); err != nil {
			dashboardError(c, err)
			return
		}
		auditErr := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{{
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryDashboard,
			EventTarget:   existing.Title,
			EventType:     audit.EventTypeDashboardDelete,
			// EventState is the row that ceased to exist — recording it is the
			// only way a deleted dashboard can be reconstructed from the trail.
			EventState:     existing,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionDelete,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{"dashboard_id": existing.Id, "slug": existing.Slug},
		}}})
		if auditErr != nil {
			ctx.GetLogger().Error("failed to create dashboard audit event", "error", auditErr)
		}
		c.JSON(200, map[string]any{"id": req.Id, "deleted": true})
		return

	case "dashboards_list_versions":
		var req dashboard.GetRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardReadAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("access denied for dashboard"))
			return
		}
		if _, err := dashboard.Get(ctx, req); err != nil {
			dashboardError(c, err)
			return
		}
		resp, err := dashboard.ListVersions(ctx, req.Id)
		if err != nil {
			dashboardError(c, err)
			return
		}
		c.JSON(200, resp)
		return

	// Runs a read-only redis / rabbitmq command for one panel. Unlike the other
	// dashboard actions this reaches into the account's cluster, so it takes the
	// account check directly rather than relying on the per-panel provider call
	// to 403 later.
	case "dashboards_execute_query":
		var req dashboard.ExecuteQueryRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardReadAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("access denied for dashboards"))
			return
		}
		if !ctx.GetSecurityContext().CanReadAccountData(req.AccountId, "dashboards") {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+req.AccountId))
			return
		}
		resp, err := dashboard.ExecuteQuery(ctx, req)
		if err != nil {
			dashboardError(c, err)
			return
		}
		c.JSON(200, resp)
		return

	// Runs a `nudgebee` panel against the internal query engine. The engine
	// applies its own tenant / account / namespace scoping per table, and the
	// account check below is the same one every other account-touching action
	// makes before it gets there.
	//
	// That check is deliberately the COARSE one: it asks "may you read dashboard
	// data for this account at all", and the engine then narrows per table
	// against the table's own PermissionModule. So a `dashboards:Execute` holder
	// still cannot read events without `events:Read` — this gate is not what
	// authorizes the data, only what stops a caller naming an account outside
	// their reach.
	case "dashboards_execute_entity_query":
		var req dashboard.EntityQueryRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !dashboardReadAllowed(ctx) {
			c.JSON(403, common.ErrorActionForbidden("access denied for dashboards"))
			return
		}
		for _, accountId := range req.AccountIds {
			if !ctx.GetSecurityContext().CanReadAccountData(accountId, "dashboards") {
				c.JSON(403, common.ErrorActionForbidden("access denied for account: "+accountId))
				return
			}
		}
		resp, err := dashboard.ExecuteEntityQuery(ctx, req)
		if err != nil {
			dashboardError(c, err)
			return
		}
		c.JSON(200, resp)
		return

	// `list_contextual` rather than `resolve`: the taxonomy classes `resolve` as
	// Execute, but this is a plain read of which dashboards attach to an entity.
	case "dashboards_list_contextual":
		var req dashboard.ResolveRequest
		if err := common.UnmarshalMapToStruct(input, &req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if err := common.ValidateStruct(req); err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if !ctx.GetSecurityContext().CanReadAccountData(req.AccountId, "dashboards") {
			c.JSON(403, common.ErrorActionForbidden("access denied for account: "+req.AccountId))
			return
		}
		resp, err := dashboard.Resolve(ctx, req)
		if err != nil {
			dashboardError(c, err)
			return
		}
		c.JSON(200, resp)
		return
	}

	c.JSON(400, common.ErrorActionBadRequest("unknown dashboard action: "+actionPayload.Action.Name))
}
