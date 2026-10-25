package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"nudgebee/services/triage"

	"github.com/gin-gonic/gin"
)

// handleEventsApplyRemediationResolve resolves an event whose run the remediation loop judged likely fixed, because
// the person calling confirmed it on the Remediation tab (#40734). A person only: write access on the account, and
// a user identity (NudgeBee itself never resolves a fix). The nb_resolved_by label names the person, the run, its
// verdict and the plan's cause key, as read from the run.
func handleEventsApplyRemediationResolve(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	sc := ctx.GetSecurityContext()
	if sc == nil || strings.TrimSpace(sc.GetUserId()) == "" {
		c.JSON(http.StatusForbidden, common.ErrorActionForbidden("a person confirms a fix"))
		return
	}
	accountID, eventID, ok := remediationInput(h, c, ctx)
	if !ok {
		return
	}
	runID, _ := h.Input["run_id"].(string)
	if strings.TrimSpace(runID) == "" {
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest("run_id is required"))
		return
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal("database connection failed"))
		return
	}
	resp, err := triage.ConfirmRemediationResolve(ctx.GetContext(), dbms.Db, accountID, eventID, strings.TrimSpace(runID), sc.GetUserId())
	if errors.Is(err, triage.ErrNoVerdictToConfirm) || errors.Is(err, triage.ErrFiredAgainAfterVerdict) {
		c.JSON(http.StatusConflict, common.ErrorActionBadRequest(err.Error()))
		return
	}
	if err != nil {
		ctx.GetLogger().Error("remediation resolve not applied", "error", err, "event_id", eventID, "run_id", runID)
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest(err.Error()))
		return
	}
	ctx.GetLogger().Info("remediation resolve confirmed", "event_id", eventID, "run_id", runID, "changed", len(resp.Events))
	c.JSON(http.StatusOK, resp)

	// Audited as the event status change it is, as event_update_nb_status does.
	if err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{{
		TenantId:      sc.GetTenantId(),
		UserId:        sc.GetUserId(),
		AccountId:     accountID,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryTriage,
		EventType:     audit.EventTypeTriageStatusUpdate,
		EventState:    resp,
		EventActor:    audit.EventActorApiService,
		EventTarget:   "event_status",
		EventAction:   audit.EventActionUpdate,
		EventStatus:   audit.EventStatusSuccess,
		EventAttr:     map[string]any{"event_id": eventID, "nb_status": triage.NBStatusResolved, "run_id": runID},
	}}}); err != nil {
		ctx.GetLogger().Error("failed to create audit event", "error", err)
	}
}
