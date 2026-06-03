package api

import (
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/tenant"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleTenantAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	tenantRequest := actionPayload.Input

	switch actionPayload.Action.Name {
	case "tenant_insert_one":

		var request tenant.TenantCreateRequest
		var err error
		if tenantRequest["tenant"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["tenant"].(map[string]interface{}), &request)
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

		resp, err := tenant.CreateTenant(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "tenant",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "tenant_user_roles_upsert_one", "userroles_upsert_user":
		var request tenant.TenantUserRoleUpsertRequest
		var err error
		if tenantRequest["role"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["role"].(map[string]any), &request)
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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRole,
			EventTarget:    request.UserId,
			EventType:      audit.EventTypeRoleUserCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := tenant.UpsertTenantUserRole(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "tenant_group_roles_upsert_one", "userroles_upsert_group":
		var request tenant.TenantGroupRoleUpsertRequest
		var err error
		if tenantRequest["role"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["role"].(map[string]any), &request)
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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRole,
			EventTarget:    request.GroupId,
			EventType:      audit.EventTypeRoleGroupCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
		resp, err := tenant.UpsertTenantGroupRole(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "account_user_roles_upsert_one", "userroles_upsert_account_user":
		var request tenant.AccountUserRoleUpsertRequest
		var err error
		if tenantRequest["role"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["role"].(map[string]any), &request)
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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRole,
			EventTarget:    request.UserId,
			EventType:      audit.EventTypeRoleAccountCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
		resp, err := tenant.UpsertAccountUserRole(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "account_group_roles_upsert_one", "userroles_upsert_account_group":
		var request tenant.AccountGroupRoleUpsertRequest
		var err error
		if tenantRequest["role"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["role"].(map[string]any), &request)
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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRole,
			EventTarget:    request.GroupId,
			EventType:      audit.EventTypeRoleAccountCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := tenant.UpsertAccountGroupRole(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "auth_k8saccount_namespace_user_roles_upsert_one", "userroles_upsert_k8s_namespace_user":
		var request tenant.K8sAccountNamespaceUserRoleUpsertRequest
		var err error
		if tenantRequest["role"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["role"].(map[string]any), &request)
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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRole,
			EventTarget:    request.UserId,
			EventType:      audit.EventTypeRoleAccountCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
		resp, err := tenant.UpsertK8sAccountNamespaceUserRole(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "auth_k8saccount_namespace_group_roles_upsert_one", "userroles_upsert_k8s_namespace_group":
		var request tenant.AccountNamespaceGroupRoleUpsertRequest
		var err error
		if tenantRequest["role"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["role"].(map[string]any), &request)
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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryRole,
			EventTarget:    request.GroupId,
			EventType:      audit.EventTypeRoleAccountCreate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionCreate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := tenant.UpsertK8sAccountNamespaceGroupRole(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "tenant_attribute_upsert":
		var request tenant.TenantAttributeUpsertRequest
		var err error
		err = common.UnmarshalMapToStruct(tenantRequest, &request)

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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryTenant,
			EventTarget:    ctx.GetSecurityContext().GetTenantId(),
			EventType:      audit.EventTypeTenantAttributeUpsert,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionUpdate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := tenant.UpsertTenantAttributes(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event tenant attribute", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "delete_feature":
		var request tenant.DeleteFeatureRequest
		var err error
		err = common.UnmarshalMapToStruct(tenantRequest, &request)

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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryTenant,
			EventTarget:    ctx.GetSecurityContext().GetTenantId(),
			EventType:      audit.EventTypeTenantFeature,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorApiService,
			EventAction:    audit.EventActionDelete,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := tenant.DeleteFeature(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event tenant delete feature", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "auth_manage_group_users", "usergroups_update_members":
		var request tenant.ManageGroupUsersRequest
		var err error
		err = common.UnmarshalMapToStruct(tenantRequest, &request)

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
			AccountId:      "",
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventCategoryGroup,
			EventTarget:    request.GroupId,
			EventType:      audit.EventTypeGroupUserUpdate,
			EventState:     request,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionUpdate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}

		resp, err := tenant.ManageGroupUsers(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "tenant_update_name":
		var request tenant.TenantUpdateNameRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryTenant,
			EventTarget:   ctx.GetSecurityContext().GetTenantId(),
			EventType:     audit.EventTypeTenantUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpdateTenantName(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event tenant update name", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "tenant_attribute_delete":
		var request tenant.TenantAttributeDeleteRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryTenant,
			EventTarget:   ctx.GetSecurityContext().GetTenantId(),
			EventType:     audit.EventTypeTenantAttributeUpsert,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.DeleteTenantAttributes(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event tenant attribute delete", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "featureflag_upsert":
		var request tenant.FeatureFlagUpsertRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryTenant,
			EventTarget:   ctx.GetSecurityContext().GetTenantId(),
			EventType:     audit.EventTypeTenantUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpsertFeatureFlags(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event featureflag upsert", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "usergroup_create":
		var request tenant.UserGroupCreateRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryGroup,
			EventTarget:   "",
			EventType:     audit.EventTypeGroupUserUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.CreateUserGroup(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event user group create", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		auditEvent.EventTarget = resp.Id
		c.JSON(200, resp)
		return
	case "usergroup_update":
		var request tenant.UserGroupUpdateRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryGroup,
			EventTarget:   request.Id,
			EventType:     audit.EventTypeGroupUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpdateUserGroup(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event usergroup update", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "integration_update_status_by_pk":
		var request tenant.IntegrationUpdateStatusByPkRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryIntegration,
			EventTarget:   request.Id,
			EventType:     audit.EventTypeIntegrationUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpdateIntegrationStatusByPk(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event integration status update", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "notification_rule_delete":
		var request tenant.NotificationRuleDeleteRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryNotificationRules,
			EventTarget:   request.Id,
			EventType:     audit.EventTypeNotificationRulesDelete,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.DeleteNotificationRule(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event notification rule delete", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "notification_channel_mapping_create":
		var request tenant.NotificationChannelMappingCreateRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryNotifications,
			EventTarget:   "",
			EventType:     audit.EventTypeNotificationSlackChannelCreate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.CreateNotificationChannelMapping(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event notification channel mapping create", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		auditEvent.EventTarget = resp.Id
		c.JSON(200, resp)
		return
	case "notification_channel_mapping_delete":
		var request tenant.NotificationChannelMappingDeleteRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryNotifications,
			EventTarget:   request.Id,
			EventType:     audit.EventTypeNotificationSlackChannelDelete,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.DeleteNotificationChannelMapping(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event notification channel mapping delete", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "notification_channel_mapping_update":
		var request tenant.NotificationChannelMappingUpdateRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryNotifications,
			EventTarget:   request.Id,
			EventType:     audit.EventTypeNotificationSlackChannelUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpdateNotificationChannelMapping(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event notification channel mapping update", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "application_group_create", "applications_create_group":
		var request tenant.ApplicationGroupCreateRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryAccount,
			EventTarget:   "",
			EventType:     audit.EventTypeAccountUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.CreateApplicationGroup(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event application group create", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		auditEvent.EventTarget = resp.Id
		c.JSON(200, resp)
		return
	case "application_group_update", "applications_update_group":
		var request tenant.ApplicationGroupUpdateRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryAccount,
			EventTarget:   request.Id,
			EventType:     audit.EventTypeAccountUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpdateApplicationGroup(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event application group update", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "cloud_resource_attributes_upsert":
		var request tenant.CloudResourceAttributesUpsertRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
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
			UserId:        ctx.GetSecurityContext().GetUserId(),
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			EventTime:     time.Now().UTC(),
			EventCategory: audit.EventCategoryAccount,
			EventTarget:   ctx.GetSecurityContext().GetTenantId(),
			EventType:     audit.EventTypeAccountUpdate,
			EventState:    request,
			EventActor:    audit.EventActorUiService,
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
			EventAttr:     map[string]any{},
		}
		resp, err := tenant.UpsertCloudResourceAttributes(ctx, request)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event cloud resource attributes upsert", "error", err)
			}
		}()
		if err != nil {
			auditEvent.EventStatus = audit.EventStatusFailure
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "tenant_onboarding_delete_by_username", "signup_delete":
		var request tenant.TenantOnboardingDeleteByUsernameRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.DeleteTenantOnboardingByUsername(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantOnboardingDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "tenant_onboarding",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "tenant_onboarding_insert", "signup_create":
		var request tenant.TenantOnboardingInsertRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.InsertTenantOnboarding(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantOnboardingCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "tenant_onboarding",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "tenant_onboarding_update_status", "signup_update_status":
		var request tenant.TenantOnboardingUpdateStatusRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := tenant.UpdateTenantOnboardingStatus(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantOnboardingUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "tenant_onboarding",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "tenant_onboarding_get_by_token", "signup_check_token":
		var request tenant.TenantOnboardingGetByTokenRequest
		err := common.UnmarshalMapToStruct(tenantRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		records, err := tenant.GetTenantOnboardingByToken(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, records)
		return
	case "tenant_list_all":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		// Listing is a read operation — accept full and readonly super admins.
		// Exact-match via security context, not substring on the role string.
		sc := ctx.GetSecurityContext()
		if !sc.IsSuperAdmin() && !sc.IsSuperAdminReadonly() {
			c.JSON(400, common.ErrorActionBadRequest("Not Allowed"))
			return
		}
		tenants, err := tenant.ListTenants(ctx)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, tenants)
		return
	case "tenant_delete":
		var request tenant.TenantDeleteRequest
		var err error
		err = common.UnmarshalMapToStruct(tenantRequest, &request)

		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		// Destructive — exact-match full super admin only. Readonly super
		// admins (IsSuperAdminReadonly) are NOT permitted to delete tenants.
		if !ctx.GetSecurityContext().IsSuperAdmin() {
			c.JSON(400, common.ErrorActionBadRequest("Not Allowed"))
			return
		}

		// Delete all accounts belonging to this tenant first
		// NOTE: Account deletion is non-transactional with tenant table cleanup because
		// account.DeleteAccount manages its own transaction and the account package imports
		// the tenant package (circular dependency prevents co-locating).
		// If tenant deletion fails after accounts are removed, the tenant will remain in a
		// partially deleted state. The deleted account IDs are logged for manual recovery.
		accountIds, err := tenant.GetAccountIdsForTenant(ctx, request.Id)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest("Failed to fetch accounts for tenant: "+err.Error()))
			return
		}

		var deletedAccountIds []string
		for _, accountId := range accountIds {
			_, err := account.DeleteAccount(ctx, account.AccountDeleteRequest{
				Id:        accountId,
				OnlyClean: false,
			})
			if err != nil {
				logger.Error("tenant deletion partially failed: some accounts were already deleted",
					"tenant_id", request.Id,
					"failed_account_id", accountId,
					"deleted_account_ids", deletedAccountIds,
					"remaining_account_ids", accountIds[len(deletedAccountIds):],
				)
				c.JSON(400, common.ErrorActionBadRequest("Failed to delete account "+accountId+": "+err.Error()+". WARNING: accounts already deleted: "+strings.Join(deletedAccountIds, ", ")))
				return
			}
			deletedAccountIds = append(deletedAccountIds, accountId)
		}

		// Now delete tenant-specific data and the tenant itself
		resp, err := tenant.DeleteTenant(ctx, request)
		if err != nil {
			if len(deletedAccountIds) > 0 {
				logger.Error("tenant table cleanup failed after accounts were deleted — partial state",
					"tenant_id", request.Id,
					"deleted_account_ids", deletedAccountIds,
					"error", err,
				)
			}
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryTenant,
			EventType:     audit.EventTypeTenantDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "tenant",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	default:
		// EE-registered actions (billing_*, etc.) dispatch here.
		if dispatchAction(actionPayload, c, tracer, meter, logger) {
			return
		}
		c.JSON(400, []common.Error{
			{
				Message: "invalid action name - " + actionPayload.Action.Name,
			},
		})
		return
	}
}
