package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/pr_raise"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraPRRaiseAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	prRaiseRequest := hasuraPayload.Input

	if hasuraPayload.Action.Name == "pr_raise" {
		var request pr_raise.PRraiseRequest
		var err error
		if prRaiseRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(prRaiseRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(prRaiseRequest["object"].(map[string]interface{}), &request)
		}

		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		tenantID := ctx.GetSecurityContext().GetTenantId()
		request.TenantId = tenantID

		eventTarget := "pr_raise"
		if request.ResourceId != nil && *request.ResourceId != "" {
			eventTarget = *request.ResourceId
		}

		auditEvent := audit.Audit{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       tenantID,
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventAlertEvent,
			EventTarget:    eventTarget,
			EventType:      audit.EventTypeEventResolve,
			EventState:     request.Data,
			EventPrevState: nil,
			EventActor:     audit.EventActorAutorunbookService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := pr_raise.ApplyResolution(ctx, request)
		defer func() {
			ok := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if ok != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure

			return
		}

		c.JSON(200, resp)
		return
	} else {
		c.JSON(400, common.ErrorHasuraActionBadRequest("invalid action name - "+hasuraPayload.Action.Name))
		return
	}
}
