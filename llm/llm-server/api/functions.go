package api

import (
	"context"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type functionCreateRequest struct {
	AccountId string              `json:"account_id"`
	Function  core.LLMFunctionDto `json:"function"`
}

type functionDeleteRequest struct {
	AccountId  string `json:"account_id"`
	FunctionId string `json:"function_id" validate:"required"`
}

type functionUpdateRequest struct {
	AccountId  string              `json:"account_id"`
	FunctionId string              `json:"function_id" validate:"required"`
	Function   core.LLMFunctionDto `json:"function"`
}

type FunctionCreateResponse struct {
	Success  bool                `json:"success"`
	Function core.LLMFunctionDto `json:"function,omitempty"`
	Message  string              `json:"message,omitempty"`
}

type FunctionDeleteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type FunctionUpdateResponse struct {
	Success  bool                `json:"success"`
	Function core.LLMFunctionDto `json:"function,omitempty"`
	Message  string              `json:"message,omitempty"`
}

func handleFunctionsApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/functions")

	groupV2.POST("", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("functions")
		var hasuraRequest HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			failureResponse := FunctionCreateResponse{
				Success: false,
				Message: "Invalid request format: " + err.Error(),
			}
			c.JSON(400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}}))
			return
		}

		if hasuraRequest.Action.Name == "" {
			failureResponse := FunctionCreateResponse{
				Success: false,
				Message: "Action name is required",
			}
			c.JSON(400, buildApiResponse(failureResponse, []error{common.Error{Message: "Action name is required"}}))
			return
		}

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, slog.With())
		if err != nil {
			failureResponse := FunctionCreateResponse{
				Success: false,
				Message: "Authentication failed: " + err.Error(),
			}
			c.JSON(401, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}}))
			return
		}

		payload := hasuraRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			} else {
				failureResponse := FunctionCreateResponse{
					Success: false,
					Message: "Invalid payload format",
				}
				c.JSON(400, buildApiResponse(failureResponse, []error{common.Error{Message: "Invalid payload format"}}))
				return
			}
		}

		switch hasuraRequest.Action.Name {
		case "ai_create_function":
			common.MetricsApiRequestsTotal("functions_create")
			statusCode, responseData := functionCreateFunction(c, agentContext, payload)
			c.JSON(statusCode, responseData)
			return
		case "ai_delete_function":
			common.MetricsApiRequestsTotal("functions_delete")

			statusCode, responseData := functionDeleteLlmFunction(c, agentContext, payload)
			c.JSON(statusCode, responseData)
			return
		case "ai_update_function":
			common.MetricsApiRequestsTotal("functions_edit")

			statusCode, responseData := functionUpdateLlmFunction(c, agentContext, payload)
			c.JSON(statusCode, responseData)
			return
		default:
			failureResponse := FunctionCreateResponse{
				Success: false,
				Message: "Unsupported action: " + hasuraRequest.Action.Name,
			}
			c.JSON(400, buildApiResponse(failureResponse, []error{common.Error{Message: "Unsupported action: " + hasuraRequest.Action.Name}}))
			return
		}
	})
}

func functionCreateFunction(c *gin.Context, agentContext *security.RequestContext, payload map[string]any) (int, any) {
	request := functionCreateRequest{}
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: "Invalid request data: " + err.Error(),
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	err = common.ValidateStruct(request)
	if err != nil {
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: "Validation failed: " + err.Error(),
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	// Check if user has access to account
	if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: "Access denied: " + errorUserAccessMessage,
		}
		return 403, buildApiResponse(failureResponse, []error{common.Error{Message: errorUserAccessMessage}})
	}

	userId := agentContext.GetSecurityContext().GetUserId()
	tenantId := agentContext.GetSecurityContext().GetTenantId()

	// Validate that we have required IDs
	if userId == "" {
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: "User ID is required but not found in security context",
		}
		return 401, buildApiResponse(failureResponse, []error{common.Error{Message: "User ID is required but not found in security context"}})
	}

	if tenantId == "" {
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: "Tenant ID is required but not found in security context",
		}
		return 401, buildApiResponse(failureResponse, []error{common.Error{Message: "Tenant ID is required but not found in security context"}})
	}

	request.Function.CreatedBy = userId
	request.Function.AccountId = request.AccountId
	request.Function.TenantId = tenantId
	request.Function.Version = 1 // Initial version

	// Check for duplicate function names
	_, err = core.GetLLMFunctionByName(agentContext, request.AccountId, request.Function.Name)
	if err == nil {
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: "A function with this name already exists",
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: "A function with this name already exists"}})
	}

	resp, err := core.CreateLLMFunction(agentContext, request.AccountId, request.Function)
	if err != nil {
		failureResponse := FunctionCreateResponse{
			Success: false,
			Message: err.Error(),
		}
		return 500, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	// Send audit request (async)
	// Create audit entry for function creation
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     request.AccountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "function",
				EventType:     "llm_function_creation",
				EventState:    resp,
				EventActor:    audit.EventActor(agentContext.GetSecurityContext().GetUserId()),
				EventTarget:   resp.Id,
				EventAction:   audit.EventActionCreate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: agentContext.GetTraceId(),
				EventAttr:     map[string]any{"function_name": resp.Name},
				TenantId:      agentContext.GetSecurityContext().GetTenantId(),
			},
		},
	}
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	err = auditWorkerPool.Submit(submissionCtx, func() {
		err := audit.CreateAudit(agentContext, auditReq)
		if err != nil {
			slog.Error("function: failed to create audit", "error", err)
		}
	})
	if err != nil {
		common.MetricsApiRequestsFailedTotal("functions", "timedout")
		slog.Error("function: failed to submit audit task to worker pool", "error", err)
	}

	successResponse := FunctionCreateResponse{
		Success:  true,
		Function: resp,
		Message:  "LLM function created successfully",
	}
	return 200, buildApiResponse(successResponse, nil)
}

func functionDeleteLlmFunction(c *gin.Context, agentContext *security.RequestContext, payload map[string]any) (int, any) {
	request := functionDeleteRequest{}
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		failureResponse := FunctionDeleteResponse{
			Success: false,
			Message: "Invalid request data: " + err.Error(),
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	err = common.ValidateStruct(request)
	if err != nil {
		failureResponse := FunctionDeleteResponse{
			Success: false,
			Message: "Validation failed: " + err.Error(),
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	// Check if user has access to account
	if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeDelete) {
		failureResponse := FunctionDeleteResponse{
			Success: false,
			Message: "Access denied: " + errorUserAccessMessage,
		}
		return 403, buildApiResponse(failureResponse, []error{common.Error{Message: errorUserAccessMessage}})
	}

	userId := agentContext.GetSecurityContext().GetUserId()
	tenantId := agentContext.GetSecurityContext().GetTenantId()

	// Validate that we have required IDs
	if userId == "" {
		failureResponse := FunctionDeleteResponse{
			Success: false,
			Message: "User ID is required but not found in security context",
		}
		return 401, buildApiResponse(failureResponse, []error{common.Error{Message: "User ID is required but not found in security context"}})
	}

	if tenantId == "" {
		failureResponse := FunctionDeleteResponse{
			Success: false,
			Message: "Tenant ID is required but not found in security context",
		}
		return 401, buildApiResponse(failureResponse, []error{common.Error{Message: "Tenant ID is required but not found in security context"}})
	}

	deletedFunction, err := core.DeleteLLMFunction(agentContext, request.AccountId, request.FunctionId)
	if err != nil {
		failureResponse := FunctionDeleteResponse{
			Success: false,
			Message: err.Error(),
		}
		return 500, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	// Send audit request (async)
	// Create audit entry for function deletion
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     request.AccountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "function",
				EventType:     "llm_function_deletion",
				EventState:    deletedFunction,
				EventActor:    audit.EventActor(agentContext.GetSecurityContext().GetUserId()),
				EventTarget:   request.FunctionId,
				EventAction:   audit.EventActionDelete,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: agentContext.GetTraceId(),
				EventAttr:     map[string]any{"function_name": deletedFunction.Name},
				TenantId:      agentContext.GetSecurityContext().GetTenantId(),
			},
		},
	}
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	err = auditWorkerPool.Submit(submissionCtx, func() {
		err := audit.CreateAudit(agentContext, auditReq)
		if err != nil {
			slog.Error("function: failed to create audit", "error", err)
		}
	})
	if err != nil {
		common.MetricsApiRequestsFailedTotal("functions", "timedout")
		slog.Error("function: failed to submit audit task to worker pool", "error", err)
	}

	successResponse := FunctionDeleteResponse{
		Success: true,
		Message: "LLM function deleted successfully",
	}
	return 200, buildApiResponse(successResponse, nil)
}

func functionUpdateLlmFunction(c *gin.Context, agentContext *security.RequestContext, payload map[string]any) (int, any) {
	request := functionUpdateRequest{}
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error(errorBindingMessage, "error", err)
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: "Invalid request data: " + err.Error(),
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	err = common.ValidateStruct(request)
	if err != nil {
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: "Validation failed: " + err.Error(),
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	// Check if user has access to account
	if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: "Access denied: " + errorUserAccessMessage,
		}
		return 403, buildApiResponse(failureResponse, []error{common.Error{Message: errorUserAccessMessage}})
	}

	userId := agentContext.GetSecurityContext().GetUserId()
	tenantId := agentContext.GetSecurityContext().GetTenantId()

	// Validate that we have required IDs
	if userId == "" {
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: "User ID is required but not found in security context",
		}
		return 401, buildApiResponse(failureResponse, []error{common.Error{Message: "User ID is required but not found in security context"}})
	}

	if tenantId == "" {
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: "Tenant ID is required but not found in security context",
		}
		return 401, buildApiResponse(failureResponse, []error{common.Error{Message: "Tenant ID is required but not found in security context"}})
	}

	// Set update metadata
	request.Function.Id = request.FunctionId
	request.Function.UpdatedBy = userId
	request.Function.AccountId = request.AccountId
	request.Function.TenantId = tenantId

	// Check for duplicate function names (exclude current function)
	existingFunction, err := core.GetLLMFunctionByName(agentContext, request.AccountId, request.Function.Name)
	if err == nil && existingFunction.Id != request.FunctionId {
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: "A function with this name already exists",
		}
		return 400, buildApiResponse(failureResponse, []error{common.Error{Message: "A function with this name already exists"}})
	}

	updatedFunction, err := core.UpdateLLMFunction(agentContext, request.AccountId, request.FunctionId, request.Function)
	if err != nil {
		failureResponse := FunctionUpdateResponse{
			Success: false,
			Message: err.Error(),
		}
		return 500, buildApiResponse(failureResponse, []error{common.Error{Message: err.Error()}})
	}

	// Send audit request (async)
	// Create audit entry for function edit
	auditReq := &audit.AuditRequest{
		Audits: []audit.Audit{
			{
				AccountId:     request.AccountId,
				EventTime:     time.Now().UTC(),
				EventCategory: "function",
				EventType:     "llm_function_edit",
				EventState:    updatedFunction,
				EventActor:    audit.EventActor(agentContext.GetSecurityContext().GetUserId()),
				EventTarget:   request.FunctionId,
				EventAction:   audit.EventActionUpdate,
				EventStatus:   audit.EventStatusSuccess,
				TransactionId: agentContext.GetTraceId(),
				EventAttr:     map[string]any{"function_name": updatedFunction.Name},
				TenantId:      agentContext.GetSecurityContext().GetTenantId(),
			},
		},
	}
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	err = auditWorkerPool.Submit(submissionCtx, func() {
		err := audit.CreateAudit(agentContext, auditReq)
		if err != nil {
			slog.Error("function: failed to create audit", "error", err)
		}
	})
	if err != nil {
		common.MetricsApiRequestsFailedTotal("functions", "timedout")
		slog.Error("function: failed to submit audit task to worker pool", "error", err)
	}

	successResponse := FunctionUpdateResponse{
		Success:  true,
		Function: updatedFunction,
		Message:  "LLM function updated successfully",
	}
	return 200, buildApiResponse(successResponse, nil)
}
