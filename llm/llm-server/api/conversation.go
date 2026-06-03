package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type ConversationGetApiRequest struct {
	ConversationId string `json:"conversation_id"`
	SessionId      string `json:"session_id"`
	AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
}

type ConversationListApiRequest struct {
	AccountId string                  `json:"account_id" mapstructure:"required" validate:"required"`
	UserId    string                  `json:"user_id"`
	Limit     int                     `json:"limit"`
	Offset    int                     `json:"offset"`
	Title     string                  `json:"title"`
	Source    core.ConversationSource `json:"source"`
}

type ConversationModelListApiRequest struct {
	AccountId string `json:"account_id" mapstructure:"required" validate:"required"`
}

type ConversationReferenceListApiRequest struct {
	AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	AgentId        string `json:"agent_id"`
	Limit          int    `json:"limit"`
}

func handleConversationApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/conversations")

	// Get model configuration for a conversation
	groupV2.POST("/ai_get_model_config", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_get_model_config")

		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		type ModelConfigRequest struct {
			AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
			ConversationId string `json:"conversation_id,omitempty"`
		}

		var request ModelConfigRequest
		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			actionRequestPayload = actionRequestPayload["request"].(map[string]any)
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}

		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger := agentContext.GetLogger()

		// Get default model (from existing hierarchy)
		defaultProvider := core.GetLLMProvider(agentContext, request.AccountId, "", false, "")
		defaultModel := core.GetLLMModelName(agentContext, request.AccountId, defaultProvider, "", false, "")

		response := map[string]any{
			"default": map[string]string{
				"provider": defaultProvider,
				"model":    defaultModel,
			},
			"is_custom": false,
		}

		// If conversation provided, check if it has a custom model set
		if request.ConversationId != "" {
			convProvider, convModel, err := core.GetConversationOverride(request.ConversationId)
			if err == nil && convProvider != "" && convModel != "" {
				response["current"] = map[string]string{
					"provider": convProvider,
					"model":    convModel,
				}
				response["is_custom"] = true
			} else {
				// No custom model, current = default
				response["current"] = response["default"]
			}
		} else {
			// No conversation, current = default
			response["current"] = response["default"]
		}

		logger.Info("api: model config retrieved",
			"conversation_id", request.ConversationId,
			"is_custom", response["is_custom"])

		c.JSON(200, buildApiResponse(response, nil))
	})

	groupV2.POST("/ai_list_models", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_list_models")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationModelListApiRequest
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			actionRequestPayload = actionRequestPayload["request"].(map[string]any)
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger := agentContext.GetLogger()

		// Get all configured models (ENV + DB, global + agent-specific)
		models, err := core.GetAllConfiguredModels(request.AccountId)
		if err != nil {
			logger.Error("api: error listing models", "error", err)
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		// Get the default model (first in resolution order)
		defaultProvider := core.GetLLMProvider(agentContext, request.AccountId, "", false, "")
		defaultModel := core.GetLLMModelName(agentContext, request.AccountId, defaultProvider, "", false, "")

		// Build response. image_support advertises the server's runtime image
		// capability + limits so the UI can gate the attach affordance and
		// enforce limits client-side instead of guessing (the flag defaults
		// off and was previously invisible to clients).
		response := map[string]any{
			"models": models,
			"default": map[string]string{
				"provider": defaultProvider,
				"model":    defaultModel,
			},
			"image_support": map[string]any{
				"enabled":            core.IsImageSupportEnabled(),
				"max_per_message":    core.GetImageMaxPerMessage(),
				"max_size_mb":        core.GetImageMaxSizeMB(),
				"allowed_mime_types": core.GetAllowedImageMIMETypes(),
			},
		}

		c.JSON(200, buildApiResponse(response, nil))
	})

	groupV2.POST("/ai_get_conversations", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_get_conversations")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		var request ConversationGetApiRequest
		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			actionRequestPayload = actionRequestPayload["request"].(map[string]any)
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		if request.ConversationId == "" && request.SessionId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: conversationId or sessionId is required",
				},
			}))
			return
		}

		var conversation *core.ConversationWithMessages

		logger := slog.Default()
		if request.ConversationId != "" {
			logger = logger.With("conversation_id", request.ConversationId)
		} else {
			logger = logger.With("session_id", request.SessionId)
		}

		// Re-initialize context with resolution cache and rebuild agentContext
		ctx := context.WithValue(c.Request.Context(), core.ContextKeyLLMResolution, core.NewLLMResolutionCache())
		agentContext, err = buildContextFromPayload(ctx, c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		c.Request = c.Request.WithContext(agentContext.GetContext())

		logger = agentContext.GetLogger()
		if request.ConversationId != "" {
			conversation, err = core.GetConversationDao().GetConversationWithMessages(request.ConversationId, request.AccountId)
		} else {
			conversation, err = core.GetConversationDao().GetLatestConversationBySessionIDWithMessages(request.SessionId, request.AccountId)
		}

		if err != nil {
			logger.Error("api: error getting conversation", "error", err)
			statusCode := 400
			if errors.Is(err, core.ErrConversationNotFound) {
				statusCode = 404
			}
			c.JSON(statusCode, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		// Optionally include model configuration info
		var modelConfig *core.LLMConfigResolution
		if len(conversation.Messages) > 0 {
			// Get agent name from first message, or use default
			agentName := "llm"
			if conversation.Messages[0].AgentName != nil && *conversation.Messages[0].AgentName != "" {
				agentName = *conversation.Messages[0].AgentName
			}

			// Resolve model config for this conversation
			modelConfig, err = core.ResolveLLMConfig(agentContext, request.AccountId, agentName, conversation.ID.String())
			if err != nil {
				logger.Warn("api: failed to resolve model config for conversation",
					"conversation_id", conversation.ID.String(),
					"error", err)
				// Don't fail the request, just omit model config
				modelConfig = nil
			}
		}

		// Build response with optional model config
		response := map[string]any{
			"conversation": conversation,
		}
		if modelConfig != nil {
			response["model_config"] = modelConfig
		}

		c.JSON(200, buildApiResponse(response, nil))
	})

	groupV2.POST("/ai_list_conversations", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_list_conversations")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationListApiRequest
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			actionRequestPayload = actionRequestPayload["request"].(map[string]any)
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.Default()

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		if request.Limit == 0 {
			request.Limit = 20
		}

		logger = agentContext.GetLogger()

		conversations, err := core.GetConversationDao().ListConversations(request.AccountId, request.UserId, request.Title, string(request.Source), request.Limit, request.Offset)
		if err != nil {
			logger.Error("api: error listing conversations", "error", err)
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		c.JSON(200, buildApiResponse(conversations, nil))
	})

	groupV2.POST("/ai_list_references", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_list_references")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request ConversationReferenceListApiRequest
		actionRequestPayload := actionRequest.Input
		if actionRequestPayload["request"] != nil {
			actionRequestPayload = actionRequestPayload["request"].(map[string]any)
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}

		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger := agentContext.GetLogger()

		if request.Limit <= 0 || request.Limit > 100 {
			request.Limit = 100
		}

		references, err := core.GetConversationDao().ListAgentReferences(request.AccountId, request.ConversationId, request.MessageId, request.AgentId, request.Limit)
		if err != nil {
			logger.Error("api: error listing references", "error", err)
			c.JSON(500, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}

		c.JSON(200, buildApiResponse(references, nil))
	})
}
