package api

import (
	"log/slog"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/user"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleUserAction(actionPayload *ActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	tenantRequest := actionPayload.Input

	switch actionPayload.Action.Name {
	case "users_insert_one", "users_create":
		var request user.UserCreateRequest
		var err error
		if tenantRequest["user"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["user"].(map[string]any), &request)
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
		resp, err := user.CreateUser(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "users_create_token":
		var request user.UserTokenCreateRequest
		var err error
		if tenantRequest["user"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["user"].(map[string]any), &request)
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
		resp, err := user.CreateUserAuthToken(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserLoginCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user_auth_token",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "users_delete_token":
		var request user.UserTokenDeleteRequest
		var err error
		if tenantRequest["user"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["user"].(map[string]any), &request)
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
		resp, err := user.DeleteUserAuthToken(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserLoginDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user_auth_token",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "users_list_token":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := user.ListUserAuthTokens(ctx)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "users_delete_one":
		var request user.UserDeleteRequest
		var err error
		if tenantRequest["user"] == nil {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest["user"].(map[string]any), &request)
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
		resp, err := user.DeleteUser(ctx, request.Id)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_tenant_roles", "users_list_tenant_roles":
		var request user.UserTenantRolesRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.ListUserTenantRoles(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "user_sync_roles", "userroles_sync":
		var request user.UserSyncRolesRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.SyncUserRoles(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryRole,
			EventType:     audit.EventTypeRoleUserUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user_role",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_update_accessed", "userauths_update_accessed":
		var request user.UserUpdateAccessedRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.UpdateUserAccessed(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "user_create_auth", "userauths_create":
		var request user.UserCreateAuthRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.CreateUserAuth(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserLoginCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user_auth",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_update_status", "users_update_status":
		var request user.UserUpdateStatusRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.UpdateUserStatus(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_delete_auth", "userauths_delete":
		var request user.UserDeleteAuthRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.DeleteUserAuth(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserLoginDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user_auth",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_update_default_tenant", "users_update_default_tenant":
		var request user.UserUpdateDefaultTenantRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.UpdateDefaultTenant(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_onboard", "signup_complete":
		var request user.UserOnboardRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.OnboardUser(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_update_profile", "users_update_profile":
		var request user.UserUpdateProfileRequest
		var err error
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
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
		resp, err := user.UpdateUserProfile(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryUser,
			EventType:     audit.EventTypeUserUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "user",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "user_status_types_list", "users_list_status_types":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := user.ListStatusTypes(ctx)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "roles_list":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		var request user.RolesListRequest
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		}
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := user.ListRoles(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "user_list_tenants", "users_list_tenants":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		var request user.UserTenantsListRequest
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		}
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := user.ListUserTenants(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	case "check_group_name_exists", "usergroups_check_name_exists":
		ctx, err := buildContextFromPayload(c, actionPayload, tracer, meter, logger)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		var request user.GroupNameExistsRequest
		if tenantRequest["object"] != nil {
			err = common.UnmarshalMapToStruct(tenantRequest["object"].(map[string]any), &request)
		} else {
			err = common.UnmarshalMapToStruct(tenantRequest, &request)
		}
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		resp, err := user.CheckGroupNameExists(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return
	default:
		c.JSON(400, common.ErrorActionBadRequest("invalid action name - "+actionPayload.Action.Name))
		return
	}
}
