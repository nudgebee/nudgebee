package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/recommendation"
	"nudgebee/services/scan_orchestrator"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleRecommendationAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	mlRequest := actionPayload.Input

	switch actionPayload.Action.Name {
	case "apply_recommendations", "recommendations_apply", "recommendation_resolve":
		var request recommendation.RecommendationApplyRequest
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(mlRequest["object"].(map[string]interface{}), &request)
		}

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
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRecommendation,
			EventTarget:    request.RecommendationId,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request.Data,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := recommendation.ApplyRecommendation(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}

		c.JSON(200, resp)
		return
	case "security_scan_image", "recommendation_security_scan_image":
		var request recommendation.RecommendationScanImageRequest
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(mlRequest["object"].(map[string]interface{}), &request)
		}

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
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRecommendation,
			EventTarget:    request.Workload,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := recommendation.ScanImage(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}

		c.JSON(200, resp)
		return
	case "recommendation_job_create":
		var request recommendation.RecommendationJobCreateRequest
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(mlRequest["object"].(map[string]interface{}), &request)
		}

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
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRecommendation,
			EventTarget:    request.JobName,
			EventType:      audit.EventTypeRecommendationJobCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := recommendation.CreateRecommendationJob(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}

		c.JSON(200, resp)
		return
	case "recommendation_resolution_retry", "retry_recommendation_resolution":
		var request recommendation.RetryRecommendationResolutionRequest
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(mlRequest["object"].(map[string]interface{}), &request)
		}

		if err != nil {
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(500, []common.Error{
				{
					Message: err.Error(),
				},
			})
			return
		}

		auditEvent := audit.Audit{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       ctx.GetSecurityContext().GetTenantId(),
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRecommendation,
			EventTarget:    request.ResolutionId,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionUpdate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{"action": "retry"},
		}

		resp, err := recommendation.RetryRecommendationResolution(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(500, []common.Error{
				{
					Message: err.Error(),
				},
			})
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}

		c.JSON(200, resp)
		return
	case "recommendation_rule_list":
		var request struct {
			RuleName         string `json:"rule_name" mapstructure:"rule_name"`
			Category         string `json:"category" mapstructure:"category"`
			RecommendationId string `json:"recommendation_id" mapstructure:"recommendation_id"`
		}
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(mlRequest["object"].(map[string]interface{}), &request)
		}
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		if request.RuleName != "" {
			cloudProvider := ""
			if request.RecommendationId != "" {
				ctx, ctxErr := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
				if ctxErr == nil {
					rec, recErr := recommendation.GetRecommendation(ctx, request.RecommendationId)
					if recErr == nil {
						// Resolve cloud_provider for provider-aware metadata lookup
						if rec.CloudAccountId != "" {
							dbm, dbErr := database.GetDatabaseManager(database.Metastore)
							if dbErr == nil {
								_ = dbm.Db.QueryRowx("SELECT cloud_provider FROM cloud_accounts WHERE id = $1", rec.CloudAccountId).Scan(&cloudProvider)
							}
						}

						metadata, err := recommendation.GetRuleMetadataByNameAndProvider(request.RuleName, cloudProvider)
						if err != nil {
							c.JSON(404, common.ErrorActionBadRequest("rule not found: "+err.Error()))
							return
						}

						if rec.Recommendation.IsObject() {
							if recData, ok := rec.Recommendation.Object().(map[string]any); ok {
								resourceId := ""
								if rec.ResourceId != nil {
									resourceId = *rec.ResourceId
								}
								vars := recommendation.BuildVariableMap(recData, resourceId, "")
								metadata.Mitigations = recommendation.InterpolateMitigationsJson(metadata.Mitigations, vars)
							}
						}

						c.JSON(200, map[string]any{"data": []any{metadata}})
						return
					}
				}
			}

			metadata, err := recommendation.GetRuleMetadataByNameAndProvider(request.RuleName, cloudProvider)
			if err != nil {
				c.JSON(404, common.ErrorActionBadRequest("rule not found: "+err.Error()))
				return
			}
			c.JSON(200, map[string]any{"data": []any{metadata}})
			return
		}

		results, err := recommendation.GetAllRuleMetadataByCategory(request.Category)
		if err != nil {
			c.JSON(500, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, map[string]any{"data": results})
		return
	case "scanner_heartbeat_evaluate":
		var request struct {
			AccountId string `json:"account_id" mapstructure:"account_id"`
			TenantId  string `json:"tenant_id" mapstructure:"tenant_id"`
		}
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(mlRequest["object"].(map[string]interface{}), &request)
		}
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		if request.AccountId == "" || request.TenantId == "" {
			c.JSON(400, common.ErrorActionBadRequest("account_id and tenant_id are required"))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(500, common.ErrorActionBadRequest(err.Error()))
			return
		}
		result := scan_orchestrator.EvaluateHeartbeat(ctx, request.AccountId, request.TenantId)
		c.JSON(202, result)
		return
	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}
