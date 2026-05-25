package api

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Request DTOs for Global Context operations.
// NOTE: TenantId on the wire is accepted but ignored — tenant is always
// derived from the authenticated security context inside the service layer.
type gcCreateRequest struct {
	TenantId      string `json:"tenant_id"`  // Ignored — derived from auth
	AccountId     string `json:"account_id"` // Required
	GlobalContext struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Data        string `json:"data"`
		Format      string `json:"format"`
		FileName    string `json:"file_name"`
	} `json:"global_context"`
}

type gcGetRequest struct {
	TenantId  string `json:"tenant_id"`  // Ignored — derived from auth
	AccountId string `json:"account_id"` // Required
	Id        string `json:"id"`
}

type gcListRequest struct {
	TenantId  string `json:"tenant_id"`  // Ignored — derived from auth
	AccountId string `json:"account_id"` // Required
}

type gcUpdateRequest struct {
	TenantId      string `json:"tenant_id"`  // Ignored — derived from auth
	AccountId     string `json:"account_id"` // Required
	GlobalContext struct {
		Id          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Data        string `json:"data"`
		Format      string `json:"format"`
		FileName    string `json:"file_name"`
	} `json:"global_context"`
}

type gcDeleteRequest struct {
	TenantId  string `json:"tenant_id"`  // Ignored — derived from auth
	AccountId string `json:"account_id"` // Required
	Id        string `json:"id"`
}

// Error message constants
const (
	errorGCUserAccessMessage = "gc: user doesn't have access to this account"
	errorGCAccountIDRequired = "gc: account_id is required"
	errorGCIDRequired        = "gc: id is required"
	errorGCInvalidPayload    = "gc: invalid payload, action.name is required"
	errorGCUnsupportedAction = "gc: invalid payload, unsupported action"
)

// Handler functions

func gcCreate(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request gcCreateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("gc: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCAccountIDRequired)}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeCreate) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorGCUserAccessMessage)}))
		return
	}

	gc := core.GlobalContext{
		AccountId:    request.AccountId,
		Name:         request.GlobalContext.Name,
		Description:  request.GlobalContext.Description,
		Data:         request.GlobalContext.Data,
		DataFormat:   request.GlobalContext.Format,
		DataFilename: request.GlobalContext.FileName,
	}

	resp, err := core.CreateGlobalContext(context, gc)
	if err != nil {
		slog.Error("gc: failed to create", "error", err, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func gcGet(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request gcGetRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("gc: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCAccountIDRequired)}))
		return
	}
	if request.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCIDRequired)}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorGCUserAccessMessage)}))
		return
	}

	resp, err := core.GetGlobalContext(context, request.AccountId, request.Id)
	if err != nil {
		slog.Error("gc: failed to get", "error", err, "gc_id", request.Id, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func gcList(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request gcListRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("gc: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCAccountIDRequired)}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorGCUserAccessMessage)}))
		return
	}

	resp, err := core.ListGlobalContexts(context, request.AccountId)
	if err != nil {
		slog.Error("gc: failed to list", "error", err, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(resp, nil))
}

func gcUpdate(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request gcUpdateRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("gc: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCAccountIDRequired)}))
		return
	}
	if request.GlobalContext.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCIDRequired)}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorGCUserAccessMessage)}))
		return
	}

	updates := core.GlobalContext{
		Name:         request.GlobalContext.Name,
		Description:  request.GlobalContext.Description,
		Data:         request.GlobalContext.Data,
		DataFormat:   request.GlobalContext.Format,
		DataFilename: request.GlobalContext.FileName,
	}

	err = core.UpdateGlobalContext(context, request.AccountId, request.GlobalContext.Id, updates)
	if err != nil {
		slog.Error("gc: failed to update", "error", err, "gc_id", request.GlobalContext.Id, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"status": "ok", "id": request.GlobalContext.Id}, nil))
}

func gcDelete(c *gin.Context, context *security.RequestContext, payload map[string]any) {
	var request gcDeleteRequest
	err := common.DecodeMapToStruct(payload, &request)
	if err != nil {
		slog.Error("gc: error binding request", "error", err)
		c.JSON(400, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	if request.AccountId == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCAccountIDRequired)}))
		return
	}
	if request.Id == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCIDRequired)}))
		return
	}

	if !context.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeDelete) {
		c.JSON(403, buildApiResponse(nil, []error{errors.New(errorGCUserAccessMessage)}))
		return
	}

	err = core.DeleteGlobalContext(context, request.AccountId, request.Id)
	if err != nil {
		slog.Error("gc: failed to delete", "error", err, "gc_id", request.Id, "account_id", request.AccountId)
		c.JSON(500, buildApiResponse(nil, []error{
			common.Error{Message: err.Error()},
		}))
		return
	}

	c.JSON(200, buildApiResponse(map[string]string{"status": "ok"}, nil))
}

// Route handler registration
func handleGlobalContextApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/global_contexts")

	groupV2.POST("", func(c *gin.Context) {
		var actionRequest ActionRequest
		err := c.ShouldBindJSON(&actionRequest)
		if err != nil {
			slog.Error("gc: error binding hasura request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "gc: " + err.Error()},
			}))
			return
		}

		if actionRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCInvalidPayload)}))
			return
		}

		context, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		payload := actionRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			}
		}

		switch actionRequest.Action.Name {
		case "ai_create_gc":
			common.MetricsApiRequestsTotal("gc_create")
			gcCreate(c, context, payload)
		case "ai_get_gc":
			common.MetricsApiRequestsTotal("gc_get")
			gcGet(c, context, payload)
		case "ai_list_gc":
			common.MetricsApiRequestsTotal("gc_list")
			gcList(c, context, payload)
		case "ai_update_gc":
			common.MetricsApiRequestsTotal("gc_update")
			gcUpdate(c, context, payload)
		case "ai_delete_gc":
			common.MetricsApiRequestsTotal("gc_delete")
			gcDelete(c, context, payload)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New(errorGCUnsupportedAction)}))
		}
	})
}
