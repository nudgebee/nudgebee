package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

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

// formatValidationError converts go-playground/validator errors into human-readable messages.
func formatValidationError(err error) string {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return err.Error()
	}

	messages := make([]string, 0, len(ve))
	for _, fe := range ve {
		field := fe.Field()
		switch fe.Tag() {
		case "required":
			messages = append(messages, fmt.Sprintf("%s is required", field))
		case "workflowname":
			messages = append(messages, fmt.Sprintf("%s must be 3-50 characters long, start and end with alphanumeric, and contain only letters, numbers, spaces, hyphens, or underscores", field))
		case "taskid":
			messages = append(messages, fmt.Sprintf("%s must be 3-64 characters of letters, numbers, hyphens, or underscores", field))
		case "workflowversion":
			messages = append(messages, fmt.Sprintf("%s must be 'v1'", field))
		case "workflowtrigger":
			messages = append(messages, fmt.Sprintf("%s has an invalid trigger type", field))
		default:
			messages = append(messages, fmt.Sprintf("%s failed validation: %s", field, fe.Tag()))
		}
	}
	return strings.Join(messages, "; ")
}

// handleServiceError writes the appropriate HTTP error response for Hasura
// action handlers. Returns true if an error was handled, false if err is nil.
// Status is clamped to 4xx because Hasura wraps 5xx responses in
// `extensions.internal`, which leaks the response body to callers. Internal
// failures are therefore surfaced as 400 with a Hasura-action-shaped payload.
func handleServiceError(c *gin.Context, err error, genericMessage string) bool {
	if err == nil {
		return false
	}
	var commonErr common.Error
	if errors.As(err, &commonErr) {
		statusCode := commonErr.Code
		if statusCode == 0 || statusCode >= http.StatusInternalServerError {
			statusCode = http.StatusBadRequest
		}
		c.JSON(statusCode, common.ErrorHasuraActionBadRequest(commonErr.Message))
		return true
	}
	c.JSON(http.StatusBadRequest, common.ErrorHasuraActionInternal(fmt.Sprintf("%s: %s", genericMessage, err.Error())))
	return true
}

func buildContextFromRequestPayload(ctx context.Context, c *gin.Context, request map[string]string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
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

type HasuraActionRequestAction struct {
	Name string `json:"name"`
}

type HasuraActionRequest struct {
	Action           HasuraActionRequestAction `json:"action"`
	Input            map[string]any            `json:"input"`
	RequestQuery     string                    `json:"request"`
	SessionVariables map[string]any            `json:"session_variables"`
}

// SecurityContextBuilder defines an interface for building security contexts.
type SecurityContextBuilder interface {
	BuildContextFromRequestPayload(ctx context.Context, c *gin.Context, request map[string]string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error)
	BuildContextFromHasuraPayload(ctx context.Context, c *gin.Context, h *HasuraActionRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error)
}

// DefaultSecurityContextBuilder is the default implementation of SecurityContextBuilder.
type DefaultSecurityContextBuilder struct{}

// BuildContextFromRequestPayload implements SecurityContextBuilder.
func (d *DefaultSecurityContextBuilder) BuildContextFromRequestPayload(ctx context.Context, c *gin.Context, request map[string]string, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	return buildContextFromRequestPayload(ctx, c, request, tracer, meter, logger)
}

// BuildContextFromHasuraPayload implements SecurityContextBuilder.
func (d *DefaultSecurityContextBuilder) BuildContextFromHasuraPayload(ctx context.Context, c *gin.Context, h *HasuraActionRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
	return buildContextFromHasuraPayload(ctx, c, h, tracer, meter, logger)
}

func (s *Server) getHasuraRequestDetails(c *gin.Context) (*security.RequestContext, map[string]any, string, bool) {
	var hasuraReq HasuraActionRequest
	if err := c.ShouldBindJSON(&hasuraReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request format: " + err.Error()})
		return nil, nil, "", false
	}

	sc, err := s.securityContextBuilder.BuildContextFromHasuraPayload(c.Request.Context(), c, &hasuraReq, s.tracer, s.meter, s.logger)
	if err != nil || sc == nil {
		c.JSON(http.StatusUnauthorized, common.ErrorHasuraActionBadRequest(err.Error()))
		return nil, nil, "", false
	}

	args := hasuraReq.Input
	if input, ok := hasuraReq.Input["input"].(map[string]any); ok {
		args = input
	} else if arg, ok := hasuraReq.Input["arg1"].(map[string]any); ok {
		args = arg
	}

	accountID, _ := args["account_id"].(string)
	if accountID == "" {
		// Try session variable or top level
		if acc, ok := hasuraReq.SessionVariables["x-hasura-user-account-id"].(string); ok {
			accountID = acc
		}
	}

	if accountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "account_id missing in API"})
		return nil, nil, "", false
	}

	if !sc.GetSecurityContext().HasAccountAccess(accountID, security.SecurityAccessTypeRead) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, nil, "", false
	}

	return sc, args, accountID, true
}

func buildContextFromHasuraPayload(ctx context.Context, c *gin.Context, h *HasuraActionRequest, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) (*security.RequestContext, error) {
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
		tenantId = h.Input["tenant_id"].(string)
	}

	if userId == "" && hasuraRequestPayload["user_id"] != nil {
		userId = h.Input["user_id"].(string)
	}

	var securityContext *security.SecurityContext
	var err error
	if h.SessionVariables["x-hasura-role"] == "admin" && tenantId == "" && userId == "" {
		securityContext = security.NewSecurityContextForSuperAdmin()
	} else if h.SessionVariables["x-hasura-role"] == "admin" && tenantId != "" {
		securityContext = security.NewSecurityContextForTenantAccountAdmin(tenantId, userId, []string{})
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

	// If x-hasura-allowed-roles contains super_admin, mark context as super admin
	if allowedRoles, ok := h.SessionVariables["x-hasura-allowed-roles"].(string); ok {
		for _, role := range strings.Split(strings.Trim(allowedRoles, "{}"), ",") {
			if strings.TrimSpace(role) == "super_admin" {
				securityContext.AddRole(security.AUTH_SUPER_ADMIN_ROLE)
				break
			}
		}
	}

	span := trace.SpanFromContext(ctx)
	childLogger := logger.With("tenant_id", tenantId, "user_id", userId, "trace_id", span.SpanContext().TraceID().String())
	return security.NewRequestContext(ctx, securityContext, childLogger, tracer, meter), nil
}
