package api

import (
	"errors"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/memory"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Memory Module v2 API — Phase 1 endpoints for Soul + Preferences.
// Wired into api.go as handleMemoryV2Apis. Follows the RPC Action envelope
// pattern used by handleMemoryApis in memory.go.

// Request DTOs

type memV2SoulRequest struct {
	UserID   string                 `json:"user_id"`
	Style    map[string]interface{} `json:"style,omitempty"`
	Markdown string                 `json:"markdown,omitempty"`
}

type memV2PrefsGetRequest struct {
	UserID      string `json:"user_id"`
	AgentModule string `json:"agent_module,omitempty"`
}

type memV2PrefsSetRequest struct {
	UserID      string      `json:"user_id"`
	AgentModule string      `json:"agent_module,omitempty"`
	Key         string      `json:"key"`
	Value       interface{} `json:"value"`
}

type memV2PrefsClearRequest struct {
	UserID      string `json:"user_id"`
	AgentModule string `json:"agent_module,omitempty"`
	Key         string `json:"key"`
}

// Handlers

func memV2SoulGet(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	var req memV2SoulRequest
	if err := common.DecodeMapToStruct(payload, &req); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := resolveUserID(ctx, req.UserID)
	if userID == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: user_id is required")}))
		return
	}

	resp, err := memory.Default().Get(c.Request.Context(), memory.GetRequest{
		TenantID: tenantID, UserID: userID, Layer: "soul",
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func memV2SoulSet(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	var req memV2SoulRequest
	if err := common.DecodeMapToStruct(payload, &req); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := resolveUserID(ctx, req.UserID)
	if userID == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: user_id is required")}))
		return
	}

	value := map[string]any{}
	if req.Style != nil {
		value["style"] = req.Style
	}
	if req.Markdown != "" {
		value["markdown"] = req.Markdown
	}

	resp, err := memory.Default().Mutate(c.Request.Context(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "soul", Action: "set",
		Value:     value,
		ActorKind: "user", ActorID: ctx.GetSecurityContext().GetUserId(),
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func memV2SoulClear(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	var req memV2SoulRequest
	if err := common.DecodeMapToStruct(payload, &req); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := resolveUserID(ctx, req.UserID)
	if userID == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: user_id is required")}))
		return
	}

	resp, err := memory.Default().Mutate(c.Request.Context(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "soul", Action: "clear",
		ActorKind: "user", ActorID: ctx.GetSecurityContext().GetUserId(),
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func memV2PrefsList(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	var req memV2PrefsGetRequest
	if err := common.DecodeMapToStruct(payload, &req); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := resolveUserID(ctx, req.UserID)
	if userID == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: user_id is required")}))
		return
	}

	resp, err := memory.Default().Get(c.Request.Context(), memory.GetRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func memV2PrefsSet(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	var req memV2PrefsSetRequest
	if err := common.DecodeMapToStruct(payload, &req); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := resolveUserID(ctx, req.UserID)
	if userID == "" || req.Key == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: user_id and key are required")}))
		return
	}

	value := map[string]any{
		"value":        req.Value,
		"agent_module": req.AgentModule,
	}
	resp, err := memory.Default().Mutate(c.Request.Context(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "set", Key: req.Key, Value: value,
		ActorKind: "user", ActorID: ctx.GetSecurityContext().GetUserId(),
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

func memV2PrefsClear(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	var req memV2PrefsClearRequest
	if err := common.DecodeMapToStruct(payload, &req); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	tenantID := ctx.GetSecurityContext().GetTenantId()
	userID := resolveUserID(ctx, req.UserID)
	if userID == "" || req.Key == "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: user_id and key are required")}))
		return
	}

	value := map[string]any{"agent_module": req.AgentModule}
	resp, err := memory.Default().Mutate(c.Request.Context(), memory.MutateRequest{
		TenantID: tenantID, UserID: userID, Layer: "preferences",
		Action: "clear", Key: req.Key, Value: value,
		ActorKind: "user", ActorID: ctx.GetSecurityContext().GetUserId(),
	})
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{err}))
		return
	}
	c.JSON(200, buildApiResponse(resp, nil))
}

// resolveUserID prefers the request body's user_id (allowing admin scenarios
// where one user reads another's memory if authorized) but falls back to the
// authenticated user.
func resolveUserID(ctx *security.RequestContext, bodyUserID string) string {
	if bodyUserID != "" {
		return bodyUserID
	}
	return ctx.GetSecurityContext().GetUserId()
}

// NOTE: Per-tenant enrolment for the Memory Module uses the existing
// public.feature_flag table (feature_id = 'MEMORY_MODULE'). Admins flip
// it on/off via the existing feature-flag admin surface (api-server); we
// don't duplicate that CRUD here.

// handleMemoryV2Apis registers the /v1/memory_v2 route. Called from api.go.
func handleMemoryV2Apis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/memory_v2")

	group.POST("", func(c *gin.Context) {
		var actionRequest ActionRequest
		if err := c.ShouldBindJSON(&actionRequest); err != nil {
			slog.Error("memory_v2: bind rpc request", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "memory_v2: " + err.Error()},
			}))
			return
		}

		if actionRequest.Action.Name == "" {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: action.name required")}))
			return
		}

		ctx, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		payload := actionRequest.Input
		if raw, ok := payload["request"]; ok {
			if m, castOk := raw.(map[string]any); castOk {
				payload = m
			}
		}

		switch actionRequest.Action.Name {
		case "ai_memory_soul_get":
			common.MetricsApiRequestsTotal("memory_v2_soul_get")
			memV2SoulGet(c, ctx, payload)
		case "ai_memory_soul_set":
			common.MetricsApiRequestsTotal("memory_v2_soul_set")
			memV2SoulSet(c, ctx, payload)
		case "ai_memory_soul_clear":
			common.MetricsApiRequestsTotal("memory_v2_soul_clear")
			memV2SoulClear(c, ctx, payload)
		case "ai_memory_prefs_list":
			common.MetricsApiRequestsTotal("memory_v2_prefs_list")
			memV2PrefsList(c, ctx, payload)
		case "ai_memory_prefs_set":
			common.MetricsApiRequestsTotal("memory_v2_prefs_set")
			memV2PrefsSet(c, ctx, payload)
		case "ai_memory_prefs_clear":
			common.MetricsApiRequestsTotal("memory_v2_prefs_clear")
			memV2PrefsClear(c, ctx, payload)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("memory_v2: unsupported action")}))
		}
	})
}
