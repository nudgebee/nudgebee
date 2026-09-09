package api

import (
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/recommendation"
	"nudgebee/services/scan_orchestrator"
	"nudgebee/services/security"
	"nudgebee/services/vmpackage"
	vmqueue "nudgebee/services/vmpackage/queue"
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
	case "security_scan_vm":
		var request vmpackage.ScanRequest
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
			EventTarget:    request.CloudResourceId,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := vmpackage.ScanPackages(ctx, request)
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
	case "security_scan_vm_account":
		var request vmpackage.ScanAccountRequest
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
			EventTarget:    request.AccountId,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := scanVmAccount(ctx, request)
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
	case "recommendations_create_ticket_resolution":
		var request recommendation.RecommendationTicketResolutionRequest
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else if objMap, ok := mlRequest["object"].(map[string]interface{}); ok {
			err = common.UnmarshalMapToStruct(objMap, &request)
		} else {
			c.JSON(400, []common.Error{
				{
					Message: "invalid 'object' field type: expected map",
				},
			})
			return
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
			EventTarget:    request.RecommendationId,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{"action": "ticket_resolution"},
		}

		resp, err := recommendation.RecordTicketResolution(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
			auditEvent.EventStatus = audit.EventStatusFailure
			return
		}

		c.JSON(200, resp)
		return
	case "recommendations_update_dismissal":
		var request recommendation.RecommendationDismissalRequest
		var err error
		if mlRequest["object"] == nil {
			err = common.UnmarshalMapToStruct(mlRequest, &request)
		} else if objMap, ok := mlRequest["object"].(map[string]interface{}); ok {
			err = common.UnmarshalMapToStruct(objMap, &request)
		} else {
			c.JSON(400, []common.Error{
				{
					Message: "invalid 'object' field type: expected map",
				},
			})
			return
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
			EventTarget:    request.RecommendationId,
			EventType:      audit.EventTypeRecommendationApply,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionUpdate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{"action": "dismissal"},
		}

		resp, err := recommendation.UpdateRecommendationDismissal(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, []common.Error{
				{
					Message: err.Error(),
				},
			})
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

// scanVmAccount queues an on-demand scan for every discovery datasource that
// runs in or targets req.AccountId — the manual-trigger counterpart to
// cron.go's daily "VM Vulnerability Scan" job, scoped to one account instead
// of every tenant. Lives in the api package (not vmpackage) because vmpackage/queue
// already imports vmpackage (see consumer.go), so vmpackage importing
// vmpackage/queue back would be a cycle; cron.go's handler is the existing
// precedent for this same list-then-publish glue.
func scanVmAccount(ctx *security.RequestContext, req vmpackage.ScanAccountRequest) (vmpackage.ScanAccountResponse, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeCreate) {
		return vmpackage.ScanAccountResponse{}, common.ErrorUnauthorized("unauthorized")
	}

	a, err := account.GetAccount(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("scanVmAccount: error getting account", "error", err)
		return vmpackage.ScanAccountResponse{}, err
	}
	if a.Id == "" {
		return vmpackage.ScanAccountResponse{}, fmt.Errorf("scanVmAccount: account not found - %s", req.AccountId)
	}
	if a.Tenant != ctx.GetSecurityContext().GetTenantId() {
		return vmpackage.ScanAccountResponse{}, fmt.Errorf("scanVmAccount: account not found in tenant - %s", req.AccountId)
	}

	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return vmpackage.ScanAccountResponse{}, err
	}

	datasources, err := vmpackage.ListDiscoveryDatasourcesForAccount(dbManager, req.AccountId)
	if err != nil {
		return vmpackage.ScanAccountResponse{}, err
	}
	if len(datasources) == 0 {
		return vmpackage.ScanAccountResponse{}, common.ErrorBadRequest("no discovery agent is configured to scan this account")
	}

	queued, failed := 0, 0
	for _, ds := range datasources {
		if err := vmqueue.PublishVMScan(ctx.GetContext(), ds.IntegrationID, ds.TenantID, ds.AccountID, "manual"); err != nil {
			ctx.GetLogger().Error("scanVmAccount: failed to publish vm scan", "integration_id", ds.IntegrationID, "error", err)
			failed++
			continue
		}
		queued++
	}
	ctx.GetLogger().Info("scanVmAccount: vm scan queued", "account_id", req.AccountId, "queued", queued, "failed", failed)

	if queued == 0 {
		return vmpackage.ScanAccountResponse{}, common.ErrorBadRequest(fmt.Sprintf("failed to queue a scan for any of the %d discovery datasource(s) configured for this account", len(datasources)))
	}

	return vmpackage.ScanAccountResponse{
		Data: []map[string]any{{"account_id": req.AccountId, "status": "started", "datasources_queued": queued}},
	}, nil
}
