package api

import (
	"fmt"
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/slo"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleSloAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	sloRequest := actionPayload.Input
	switch actionPayload.Action.Name {
	case "slo_config_list":
		var sloConfigListRequest slo.SLOListRequest
		if sloRequest["request"] != nil {
			sloRequest = sloRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(sloRequest, &sloConfigListRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := slo.GetSLOConfig(ctx, sloConfigListRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "slo_config_create":
		var sloConfigRequest slo.SLORequest
		if sloRequest["request"] != nil {
			sloRequest = sloRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(sloRequest, &sloConfigRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		auditEvent := audit.Audit{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       ctx.GetSecurityContext().GetTenantId(),
			AccountId:      sloConfigRequest.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRecommendation,
			EventTarget:    fmt.Sprintf("%s/%s", sloConfigRequest.WorkloadName, sloConfigRequest.Namespace),
			EventType:      audit.EventTypeSLOCreate,
			EventState:     sloConfigRequest,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		resp, err := slo.CreateOrUpdateSLOConfig(ctx, sloConfigRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	case "slo_config_update":
		var sloConfigRequest slo.SLORequest
		if sloRequest["request"] != nil {
			sloRequest = sloRequest["request"].(map[string]interface{})
		}
		err := common.UnmarshalMapToStruct(sloRequest, &sloConfigRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		auditEvent := audit.Audit{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       ctx.GetSecurityContext().GetTenantId(),
			AccountId:      sloConfigRequest.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRecommendation,
			EventTarget:    fmt.Sprintf("%s/%s", sloConfigRequest.WorkloadName, sloConfigRequest.Namespace),
			EventType:      audit.EventTypeSLOUpdate,
			EventState:     sloConfigRequest,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionUpdate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to update audit event", "error", err)
			}
		}()

		resp, err := slo.CreateOrUpdateSLOConfig(ctx, sloConfigRequest)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": resp})
		return
	}
}
