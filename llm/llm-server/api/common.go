package api

import (
	"context"
	"errors"
	"log/slog"
	"nudgebee/llm/security"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const errorBindingMessage = "api: error binding request"
const errorAuditMessage = "api: error creating audit"
const errorUserAccessMessage = "api: user does not have access"

type ApiResponse struct {
	Data   any                `json:"data,omitempty"`
	Errors []ApiResponseError `json:"errors,omitempty"`
}

type ApiResponseError struct {
	Message string `json:"message"`
}

func buildApiResponse(data any, errors []error) ApiResponse {
	apiErrors := make([]ApiResponseError, 0)
	for _, err := range errors {
		apiErrors = append(apiErrors, ApiResponseError{
			Message: err.Error(),
		})
	}

	return ApiResponse{
		Data:   data,
		Errors: apiErrors,
	}
}

func buildContextFromRequestPayload(ctx context.Context, c *gin.Context, request map[string]string, tracer trace.Tracer, meter metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	if c == nil || c.Request == nil {
		return nil, errors.New("api: unable to identify user or tenant")
	}

	span := trace.SpanFromContext(ctx)
	var err error

	var tenantId, userId string
	tenantId = c.Request.Header.Get("x-tenant-id")
	userId = c.Request.Header.Get("x-user-id")
	if tenantId == "" {
		tenantId = c.Request.Header.Get("x-hasura-user-tenant-id")
	}
	if userId == "" {
		userId = c.Request.Header.Get("x-hasura-user-id")
	}

	if tenantId == "" && request["tenant_id"] != "" {
		tenantId = request["tenant_id"]
	}

	if userId == "" && request["user_id"] != "" {
		userId = request["user_id"]
	}

	if tenantId == "" && request["account_id"] != "" {
		tenantId1, err := security.GetTenantIdFromAccountId(request["account_id"])
		tenantId = tenantId1
		if err != nil || tenantId == "" {
			logger.Error("error getting tenant id", "error", err)
			return nil, err
		}
	}

	if tenantId == "" {
		return nil, errors.New("api: unable to identify tenant")
	}

	var securityContext *security.SecurityContext
	if userId == "" || userId == uuid.Nil.String() {
		userId = security.GetSystemUserId()
		securityContext = security.NewSecurityContextForTenantAccountAdmin(tenantId, userId, []string{})
	} else {
		securityContext, err = security.NewSecurityContext(tenantId, userId)
		if err != nil {
			return nil, err
		}

	}

	childLogger := logger.With("trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, securityContext, childLogger, tracer, meter), nil
}

func buildContextFromHasuraPayload(ctx context.Context, c *gin.Context, h *HasuraActionRequest, tracer trace.Tracer, meter metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	// no need to handle role as its calculated by security context
	tenantId := ""
	userId := ""
	hasuraRequestPayload := h.Input
	if hasuraRequestPayload["request"] != nil {
		hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
	}
	if c != nil && c.Request != nil {
		tenantId = c.Request.Header.Get("x-tenant-id")
		userId = c.Request.Header.Get("x-user-id")
		if tenantId == "" {
			tenantId = c.Request.Header.Get("x-hasura-user-tenant-id")
		}
		if userId == "" {
			userId = c.Request.Header.Get("x-hasura-user-id")
		}
	}

	if tenantId == "" && h.SessionVariables["x-hasura-user-tenant-id"] != nil {
		tenantId = h.SessionVariables["x-hasura-user-tenant-id"].(string)
	}

	if userId == "" && h.SessionVariables["x-hasura-user-id"] != nil {
		userId = h.SessionVariables["x-hasura-user-id"].(string)
	}

	if tenantId == "" && hasuraRequestPayload["tenant_id"] != nil {
		tenantId = hasuraRequestPayload["tenant_id"].(string)
	}

	if userId == "" && hasuraRequestPayload["user_id"] != nil {
		userId = hasuraRequestPayload["user_id"].(string)
	}

	var securityContext *security.SecurityContext
	var err error
	if h.SessionVariables["x-hasura-role"] == "admin" && tenantId == "" && userId == "" {
		securityContext = security.NewSecurityContextForSuperAdmin()
	} else if tenantId == "" {
		return nil, errors.New("api: unable to identify tenant")
	} else if userId == "" || userId == uuid.Nil.String() {
		securityContext = security.NewSecurityContextForTenantAccountAdmin(tenantId, uuid.Nil.String(), []string{})
	} else {
		securityContext, err = security.NewSecurityContext(tenantId, userId)
		if err != nil {
			logger.Error("error creating security context", "error", err)
			return nil, err
		}
	}

	// If x-hasura-allowed-roles contains exact super_admin role, mark context as super admin
	allowedRoles, _ := h.SessionVariables["x-hasura-allowed-roles"].(string)
	for _, role := range strings.Split(strings.Trim(allowedRoles, "{}"), ",") {
		if strings.TrimSpace(role) == security.AUTH_SUPER_ADMIN_FULL_ROLE {
			securityContext.AddRole(security.AUTH_SUPER_ADMIN_FULL_ROLE)
			break
		}
	}

	span := trace.SpanFromContext(ctx)
	childLogger := logger.With("tenant_id", tenantId, "user_id", userId, "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, securityContext, childLogger, tracer, meter), nil
}
