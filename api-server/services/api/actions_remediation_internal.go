package api

import (
	"net/http"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/triage"

	"github.com/gin-gonic/gin"
)

// The remediation loop's internal actions (#40734). llm-server decides a verdict in code and asks api-server,
// the only writer of events, to apply it. Service-only: neither action is in app/src/lib/actions.yaml, so the
// app gateway never routes them, and each refuses a call that names a user: llm-server calls them with the
// service token and the tenant alone (tools.DoApiServerSystemAction), which is NudgeBee's system identity.

// requireSystemCaller answers 403 unless the caller is NudgeBee itself on a tenant: a tenant context with no user.
func requireSystemCaller(c *gin.Context, ctx *security.RequestContext) bool {
	sc := ctx.GetSecurityContext()
	if sc == nil || sc.GetTenantId() == "" || sc.GetUserId() != "" || sc.IsSuperAdmin() {
		c.JSON(http.StatusForbidden, common.ErrorActionForbidden("this action is internal to NudgeBee"))
		return false
	}
	return true
}

// remediationInput reads the account and event every internal remediation action names, answering 400 when
// either is missing and 403 when the account is not the tenant's.
func remediationInput(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) (accountID, eventID string, ok bool) {
	accountID, _ = h.Input["account_id"].(string)
	eventID, _ = h.Input["event_id"].(string)
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(eventID) == "" {
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest("account_id and event_id are required"))
		return "", "", false
	}
	if !requireTriageAccountAccess(c, ctx, accountID, "events", security.SecurityAccessTypeUpdate) {
		return "", "", false
	}
	return accountID, eventID, true
}

// handleEventsApplyRemediationVerdict resolves an event whose recorded fix held (or that cleared before the
// change), labelled nb_resolved_by, or suppresses it for a mute the person picked. A duplicate's main event
// moves with it.
func handleEventsApplyRemediationVerdict(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	if !requireSystemCaller(c, ctx) {
		return
	}
	accountID, eventID, ok := remediationInput(h, c, ctx)
	if !ok {
		return
	}
	req := triage.RemediationVerdictRequest{AccountID: accountID, EventID: eventID}
	req.NBStatus, _ = h.Input["nb_status"].(string)
	req.ReasonText, _ = h.Input["reason_text"].(string)
	req.ChosenBy, _ = h.Input["chosen_by"].(string)
	req.ResolvedBy, _ = h.Input["resolved_by"].(map[string]any)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal("database connection failed"))
		return
	}
	resp, err := triage.ApplyRemediationVerdict(ctx.GetContext(), dbms.Db, req)
	if err != nil {
		ctx.GetLogger().Error("remediation verdict not applied", "error", err, "event_id", eventID)
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest(err.Error()))
		return
	}
	ctx.GetLogger().Info("remediation verdict applied", "event_id", eventID, "nb_status", req.NBStatus, "changed", len(resp.Events))
	c.JSON(http.StatusOK, resp)
}

// handleEventsUpdateRefireLabel sets the kind of a re-fire label triage wrote (fix_did_not_hold once the new
// chain's plan names the same cause as the plan that resolved the old one).
func handleEventsUpdateRefireLabel(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	if !requireSystemCaller(c, ctx) {
		return
	}
	accountID, eventID, ok := remediationInput(h, c, ctx)
	if !ok {
		return
	}
	kind, _ := h.Input["kind"].(string)
	previousRunID, _ := h.Input["previous_run_id"].(string)
	comparedPlan, _ := h.Input["compared_plan"].(string)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal("database connection failed"))
		return
	}
	updated, err := triage.UpdateRefireLabel(ctx.GetContext(), dbms.Db, accountID, eventID, kind, previousRunID, comparedPlan)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}
