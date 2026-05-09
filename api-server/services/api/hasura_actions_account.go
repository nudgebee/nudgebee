package api

import (
	"fmt"
	"log/slog"
	"nudgebee/services/account"
	"nudgebee/services/audit"
	"nudgebee/services/common"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// extractNestedObject safely extracts the "object" field from accountRequest if present
func extractNestedObject(accountRequest map[string]interface{}) (map[string]interface{}, error) {
	if accountRequest["object"] == nil {
		return accountRequest, nil
	}

	nestedObj, ok := accountRequest["object"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("object field is not a valid map[string]interface{}")
	}
	return nestedObj, nil
}

func handleHasuraAccountAction(hasuraPayload *HasuraActionRequest, c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	accountRequest := hasuraPayload.Input

	ctx, err := buildContextFromHasuraPayload(c, hasuraPayload, tracer, meter, logger)
	if err != nil {
		c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
		return
	}

	switch hasuraPayload.Action.Name {
	case "cloud_accounts_delete":
		var request account.AccountDeleteRequest
		err := common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.DeleteAccount(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "account",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "cloud_accounts_insert_one":
		var request account.AccountCreateRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.CreateAccount(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "account",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return
	case "aws_cloud_formation":
		var request account.AccountCreateRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.AwsOnBoardUrl(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "cloud_update_cloudformation_permissions":
		var request account.CloudUpdateCloudformationPermissionsRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.CloudUpdateCloudformationPermissions(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "gcp_cloud_terraform":
		var request account.AccountCreateRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.GCPOnBoardUrl(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "agent_token_create":
		var request account.RegenerateAgentKeysRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		auditEvent := audit.Audit{
			UserId:         ctx.GetSecurityContext().GetUserId(),
			TenantId:       ctx.GetSecurityContext().GetTenantId(),
			AccountId:      request.AccountId,
			EventTime:      time.Now().UTC(),
			EventCategory:  audit.EventAgentToken,
			EventTarget:    ctx.GetSecurityContext().GetTenantId(),
			EventType:      audit.EventTypeUpdateAgentToken,
			EventState:     request.AccountId,
			EventPrevState: nil,
			EventActor:     audit.EventActorUiService,
			EventAction:    audit.EventActionUpdate,
			EventStatus:    audit.EventStatusSuccess,
			EventAttr:      map[string]any{},
		}
		resp, err := account.RegenerateAgentKeys(ctx, request.AccountId, request.AgentType)
		defer func() {
			err := audit.CreateAudit(ctx, &audit.AuditRequest{Audits: []audit.Audit{auditEvent}})
			if err != nil {
				ctx.GetLogger().Error("failed to create audit event to update token", "error", err)
			}
		}()
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return
	case "aws_org_onboard":
		var request account.AwsOrgOnboardRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.AwsOrgOnboard(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "aws_org",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "aws_org_status":
		resp, err := account.AwsOrgStatus(ctx)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "aws_org_refresh_token":
		resp, err := account.AwsOrgRefreshToken(ctx)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "aws_onboard_status":
		var request account.AwsOnboardStatusRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.AwsOnboardStatus(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "azure_eventgrid_onboard":
		var request account.AzureEventGridOnboardRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.AzureEventGridOnboardUrl(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "azure_eventgrid",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "aws_eventbridge_onboard":
		var request account.AwsEventBridgeOnboardRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.AwsEventBridgeOnboardUrl(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "aws_eventbridge",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "gcp_pubsub_onboard":
		var request account.GcpPubSubOnboardRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.GcpPubSubOnboardUrl(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountCreate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "gcp_pubsub",
			EventAction:   audit.EventActionCreate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "validate_cloud_credentials":
		var request account.ValidateCloudCredentialsRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.ValidateCloudCredentials(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "azure_list_subscriptions":
		var request account.AzureListSubscriptionsRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.ListAzureSubscriptions(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "azure_bulk_onboard":
		var request account.AzureBulkOnboardRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.AzureBulkOnboard(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "gcp_list_projects":
		var request account.GcpListProjectsRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.ListGCPProjects(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "cloud_check_monitoring_permission":
		var request account.GcpCheckMonitoringPermissionRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.CheckGCPMonitoringPermission(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "cloud_setup_monitoring_webhook":
		var request account.GcpMonitoringWebhookSetupRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.SetupGCPMonitoringWebhook(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "gcp_bulk_onboard":
		var request account.GcpBulkOnboardRequest

		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		resp, err := account.GcpBulkOnboard(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}

		c.JSON(200, resp)
		return

	case "messaging_platform_list":
		var request account.MessagingPlatformListRequest
		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := account.ListMessagingPlatforms(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		return

	case "messaging_platform_update":
		var request account.MessagingPlatformUpdateRequest
		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := account.UpdateMessagingPlatform(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryNotifications,
			EventType:     audit.EventTypeMessagingPlatformUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "messaging_platform",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "messaging_platform_delete":
		var request account.MessagingPlatformDeleteRequest
		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := account.DeleteMessagingPlatform(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryNotifications,
			EventType:     audit.EventTypeMessagingPlatformDelete,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "messaging_platform",
			EventAction:   audit.EventActionDelete,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "cloud_account_attrs_upsert":
		var request account.AccountAttrUpsertRequest
		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := account.UpsertAccountAttrs(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "account_attrs",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	case "cloud_account_update":
		var request account.AccountUpdateRequest
		accountRequest, err = extractNestedObject(accountRequest)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		err = common.UnmarshalMapToStruct(accountRequest, &request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		resp, err := account.UpdateAccountByAction(ctx, request)
		if err != nil {
			c.JSON(400, common.ErrorHasuraActionBadRequest(err.Error()))
			return
		}
		c.JSON(200, resp)
		if err := audit.PublishAuditEvent(ctx, audit.Audit{
			TenantId:      ctx.GetSecurityContext().GetTenantId(),
			UserId:        ctx.GetSecurityContext().GetUserId(),
			EventTime:     time.Now(),
			EventCategory: audit.EventCategoryAccount,
			EventType:     audit.EventTypeAccountUpdate,
			EventState:    request,
			EventActor:    audit.EventActorApiService,
			EventTarget:   "account",
			EventAction:   audit.EventActionUpdate,
			EventStatus:   audit.EventStatusSuccess,
		}); err != nil {
			ctx.GetLogger().Error("failed to publish audit event", "error", err)
		}
		return

	default:
		c.JSON(400, common.ErrorHasuraActionBadRequest("invalid action name - "+hasuraPayload.Action.Name))
		return
	}
}
