package api

import (
	"context"
	"errors"
	"log/slog"
	"nudgebee/llm/security"

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

	tenantId := c.Request.Header.Get("x-tenant-id")
	userId := c.Request.Header.Get("x-user-id")

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

// sessionString reads a string-valued session_variable by key.
func sessionString(sv map[string]any, key string) string {
	if v, ok := sv[key].(string); ok {
		return v
	}
	return ""
}

// sessionAllowedRoles extracts the allowed-roles JSON array from
// session_variables.
func sessionAllowedRoles(sv map[string]any) []string {
	arr, ok := sv["allowed_roles"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, r := range arr {
		if s, ok := r.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func buildContextFromPayload(ctx context.Context, c *gin.Context, h *ActionRequest, tracer trace.Tracer, meter metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	// no need to handle role as its calculated by security context
	tenantId := ""
	userId := ""
	actionRequestPayload := h.Input
	if actionRequestPayload["request"] != nil {
		actionRequestPayload = actionRequestPayload["request"].(map[string]any)
	}
	if c != nil && c.Request != nil {
		tenantId = c.Request.Header.Get("x-tenant-id")
		userId = c.Request.Header.Get("x-user-id")
	}

	if tenantId == "" {
		tenantId = sessionString(h.SessionVariables, "tenant_id")
	}
	if userId == "" {
		userId = sessionString(h.SessionVariables, "user_id")
	}

	if tenantId == "" && actionRequestPayload["tenant_id"] != nil {
		tenantId = actionRequestPayload["tenant_id"].(string)
	}

	if userId == "" && actionRequestPayload["user_id"] != nil {
		userId = actionRequestPayload["user_id"].(string)
	}

	role := sessionString(h.SessionVariables, "role")

	var securityContext *security.SecurityContext
	var err error
	if role == "admin" && tenantId == "" && userId == "" {
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

	// Extract super-admin roles from allowed-roles. Full and readonly are
	// extracted distinctly so destructive paths can require exact super_admin
	// (via IsSuperAdmin) while read-only paths accept either flavor (via
	// HasTenantAccess / HasAccountAccess with Read).
	for _, role := range sessionAllowedRoles(h.SessionVariables) {
		switch role {
		case security.AUTH_SUPER_ADMIN_FULL_ROLE:
			securityContext.AddRole(security.AUTH_SUPER_ADMIN_FULL_ROLE)
		case security.AUTH_SUPER_ADMIN_READONLY_ROLE:
			securityContext.AddRole(security.AUTH_SUPER_ADMIN_READONLY_ROLE)
		}
	}

	span := trace.SpanFromContext(ctx)
	childLogger := logger.With("tenant_id", tenantId, "user_id", userId, "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, securityContext, childLogger, tracer, meter), nil
}
