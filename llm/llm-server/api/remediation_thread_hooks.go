package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// The hooks that draw the alert's Slack thread again from agents/core (#40734), which cannot import this package:
// a turn of the alert's conversation ended (an approval raised, answered or the turn's run done), and a waiting
// approval closed unanswered. Both only do something for an alert conversation (event-<alert>) with the account's
// loop posted to Slack, so with the loop off nothing changes. Plus the details a step's approval card shows, and the
// endpoint notifications-server asks to draw the thread again after a click it was refused.

func init() {
	core.AfterConversationTurn = func(ctx *security.RequestContext, request core.NBAgentRequest) {
		syncAlertThreadOfSession(ctx, request.AccountId, request.SessionId)
	}
	core.AfterWaitingClosed = func(ctx *security.RequestContext, accountId, conversationId string) {
		if !remediationSlackOn(accountId) {
			return
		}
		if session := sessionOfConversation(conversationId); session != "" {
			syncAlertThreadOfSession(ctx, accountId, session)
		}
	}
	core.RemediationApprovalDetails = remediationApprovalDetails
}

// sessionOfConversation is a conversation's session, or "". A var so tests can stub it.
var sessionOfConversation = func(conversationId string) string {
	dao := core.GetConversationDao()
	if dao == nil {
		return ""
	}
	conversation, err := dao.GetConversation(conversationId)
	if err != nil {
		return ""
	}
	return conversation.SessionID
}

// remediationApprovalDetails is what a plan step's approval card shows besides its question: recheck when the alert's
// investigation finished after the plan was written (the card then offers Approve anyway and Deny and re-check).
func remediationApprovalDetails(ctx *security.RequestContext, request core.NBAgentRequest, _ core.FollowupRequest) map[string]any {
	alert, ok := strings.CutPrefix(request.SessionId, events.SessionIdPrefixEvent)
	if !ok || alert == "" || !remediationSlackOn(request.AccountId) {
		return nil
	}
	eventId, err := newestEventOfAlert(ctx, request.AccountId, alert)
	if err != nil || eventId == "" {
		return nil
	}
	planEvent, info, err := resolveThreadPlanEvent(ctx, request.AccountId, eventId)
	if err != nil {
		return nil
	}
	plan, _, _ := loadSlackPlan(ctx, request.AccountId, planEvent)
	loop := readThreadLoop(ctx, request.AccountId, planEvent, info, plan)
	return map[string]any{"recheck": loop != nil && loop.InvestigationAfterPlan != nil}
}

// remediationThreadSyncRequest names the alert whose thread is drawn again.
type remediationThreadSyncRequest struct {
	AccountId string `json:"account_id"`
	EventId   string `json:"event_id"`
}

// processRemediationThreadSync draws an alert's Slack threads again from its state (notifications-server, after a
// click the server refused, so a stale button goes). It changes nothing: read access is enough. 202 once queued.
func processRemediationThreadSync(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request remediationThreadSyncRequest
	var actionRequest ActionRequest
	if err := c.ShouldBindJSON(&actionRequest); err != nil {
		c.JSON(http.StatusBadRequest, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(http.StatusBadRequest, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(request.EventId)); err != nil {
		c.JSON(http.StatusBadRequest, BuildApiResponse(nil, []error{common.Error{Message: "event_id must be an event's id"}}))
		return
	}
	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId)
	ctx, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(http.StatusInternalServerError, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!granted(sc, request.AccountId, moduleAiMisc, "Read", "Write", "Execute")) {
		c.JSON(http.StatusForbidden, BuildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
		return
	}
	if !remediationSlackOn(request.AccountId) {
		c.JSON(http.StatusConflict, BuildApiResponse(nil, []error{common.Error{Message: "remediation: the remediation loop is off for this account"}}))
		return
	}
	syncAlertThread(ctx, request.AccountId, strings.TrimSpace(request.EventId))
	c.JSON(http.StatusAccepted, BuildApiResponse(map[string]string{"status": "queued"}, nil))
}
