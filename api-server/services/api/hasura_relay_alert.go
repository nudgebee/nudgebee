package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/eventrule"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleHasuraEventRuleAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	relayRequest := hasuraPayload.Input

	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}
	auditEvent := audit.Audit{
		UserId:         ctx.GetSecurityContext().GetUserId(),
		TenantId:       ctx.GetSecurityContext().GetTenantId(),
		EventCategory:  audit.EventAlertManagerRelay,
		EventType:      audit.EventTypeAlertManagerCreate,
		EventPrevState: nil,
		EventActor:     audit.EventActorUiService,
		EventStatus:    audit.EventStatusSuccess,
		EventAttr:      map[string]any{},
	}
	switch hasuraPayload.Action.Name {
	case "alertmanager_create_rule":
		var relayAPIRequest eventrule.EventConfig
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}

		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		response, err1 := eventrule.CreateEventRule(ctx, relayAPIRequest)
		defer func() {
			auditEvent.AccountId = relayAPIRequest.AccountID
			auditEvent.EventAction = audit.EventActionCreate
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = relayAPIRequest.Alert
			auditEvent.EventState = relayAPIRequest
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err1 != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err1.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, response)
		return

	case "alertmanager_update_rule":
		var relayAPIRequest eventrule.EventConfig
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}

		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		response, err1 := eventrule.UpdateEventRule(ctx, relayAPIRequest)
		defer func() {
			auditEvent.AccountId = relayAPIRequest.AccountID
			auditEvent.EventAction = audit.EventActionUpdate
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = relayAPIRequest.Alert
			auditEvent.EventState = relayAPIRequest
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event for update alert manager", "error", err)
			}
		}()
		if err1 != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err1.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, response)
		return

	case "alertmanager_disable_rule":
		var relayAPIRequest eventrule.DisableEventConfig
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}

		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		response, err1 := eventrule.DisableEventRule(ctx, relayAPIRequest)
		defer func() {
			auditEvent.AccountId = relayAPIRequest.AccountID
			auditEvent.EventAction = audit.EventActionDelete
			auditEvent.EventTime = time.Now()
			auditEvent.EventTarget = relayAPIRequest.Alert
			auditEvent.EventState = relayAPIRequest
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event to disable alert manager", "error", err)
			}
		}()
		if err1 != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err1.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, response)
		return

	case "alertmanager_list_actions":
		var relayAPIRequest eventrule.ListActionsRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(relayRequest, &relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		actions, err := eventrule.ListAction(ctx, relayAPIRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		response := map[string]any{
			"actions": actions,
		}
		c.JSON(200, response)
		return

	case "list_alert_agent_playbook":
		var agentPlaybookRequest eventrule.ListAgentPlaybookRequest
		if relayRequest["request"] != nil {
			relayRequest = relayRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(relayRequest, &agentPlaybookRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		actions, err := eventrule.ListAlertAgentPlaybook(ctx, agentPlaybookRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}
		c.JSON(200, actions)
		return
	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("invalid action name - "+hasuraPayload.Action.Name))
		return
	}
}
