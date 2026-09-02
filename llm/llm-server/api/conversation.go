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

// ModelPricingListApiRequest carries nothing: prices are tenant-scoped and the
// tenant comes from the security context. There is deliberately no account —
// a price applies to every account in the tenant, so scoping the read to one
// would only invite the idea that it could differ between them.
type ModelPricingListApiRequest struct{}

// ModelPricingUpsertApiRequest carries the rates a tenant is setting. There is
// deliberately no tenant_id field: taking it from the request body would let a
// caller write another tenant's prices.
type ModelPricingUpsertApiRequest struct {
	Prices []core.ModelPriceInput `json:"prices"`
}

// The tenant is never taken from the request — it comes from the security
// context, so a caller cannot delete another tenant's override by naming it.
type ModelPricingDeleteApiRequest struct {
	ProviderName string `json:"provider_name"`
	ModelName    string `json:"model_name"`
}

type ConversationReferenceListApiRequest struct {
	AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	// MessageIds batches multiple message ids into one call (e.g. the frontend's
	// per-conversation batch fetch). Takes precedence over MessageId when both are set;
	// MessageId stays for existing single-message callers.
	MessageIds []string `json:"message_ids"`
	AgentId    string   `json:"agent_id"`
	Limit      int      `json:"limit"`
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

		if !agentContext.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_models") {
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

		// If conversation provided, check if it has a custom model set.
		// Blanket mode (provider+model) reported as is_custom=true with a
		// scalar pick; per-tier mode reported as is_custom=true with a map
		// of picks under "tier_overrides". Both modes are mutually exclusive
		// at the conversation row level.
		if request.ConversationId != "" {
			convProvider, convModel, tierOverrides, pinnedSource, err := core.GetConversationOverride(request.ConversationId)
			if err == nil && convProvider != "" && convModel != "" {
				response["current"] = map[string]string{
					"provider": convProvider,
					"model":    convModel,
				}
				response["is_custom"] = true
			} else if err == nil && tierOverrides.HasAny() {
				response["tier_overrides"] = tierOverrides.Picks
				response["is_custom"] = true
				response["current"] = response["default"]
			} else {
				// No custom model, current = default
				response["current"] = response["default"]
			}

			// A pinned config source is orthogonal to the provider/model override
			// above — a conversation can carry either, both, or neither — so it's
			// reported separately. The client matches it against the
			// ai_list_models rows to recover the slot's display name.
			if err == nil && pinnedSource != "" {
				response["config_source"] = pinnedSource
				response["is_custom"] = true
			}
		} else {
			// No conversation, current = default
			response["current"] = response["default"]
		}

		logger.Info("api: model config retrieved",
			"conversation_id", request.ConversationId,
			"is_custom", response["is_custom"],
			"config_source", response["config_source"])

		c.JSON(200, buildApiResponse(response, nil))
	})

	// bindPricingRequest folds the action-envelope decoding the RPC handlers all
	// repeat — unwrap input.request, decode into the typed request, then build
	// the security context — so the two pricing handlers below read as their
	// authorization and their query, not as 30 lines of boilerplate each.
	//
	// Returns ok=false having already written the response, so callers just
	// return.
	bindPricingRequest := func(c *gin.Context, tracer trace.Tracer, meter metric.Meter, request any) (*security.RequestContext, bool) {
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return nil, false
		}
		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return nil, false
		}
		payload := actionRequest.Input
		if payload["request"] != nil {
			payload, _ = payload["request"].(map[string]any)
		}
		if payload == nil {
			payload = requestMap
		}
		if err := common.DecodeMapToStruct(payload, request); err != nil {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return nil, false
		}
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return nil, false
		}
		return agentContext, true
	}

	// Tenant-supplied model pricing (V843). Both handlers scope authorization
	// to the account the UI is showing, but read the tenant from the security
	// context — a tenant_id in the payload would let a caller price another
	// tenant's models.
	groupV2.POST("/ai_list_model_pricing", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_list_model_pricing")
		var request ModelPricingListApiRequest
		agentContext, ok := bindPricingRequest(c, tracer, meter, &request)
		if !ok {
			return
		}
		sc := agentContext.GetSecurityContext()
		// Rates are reference data — seeing what a model costs is not privileged
		// within a tenant, and account admins already read cost dashboards built
		// from these numbers. Account roles are included to match the grant in
		// actions.yaml; without them those users reach this handler and get a 403
		// on a tab they can see. Writing rates stays tenant_admin only.
		if !sc.IsTenantAdmin() && !sc.IsTenantReadAdmin() && !sc.IsAccountAdmin() && !sc.IsAccountReadAdmin() &&
			!sc.IsSuperAdmin() && !sc.IsSuperAdminReadonly() {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		prices, err := core.ListModelPricing(dbManager, sc.GetTenantId())
		if err != nil {
			agentContext.GetLogger().Error("api: error listing model pricing", "error", err)
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(200, buildApiResponse(map[string]any{"prices": prices}, nil))
	})

	groupV2.POST("/ai_upsert_model_pricing", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_upsert_model_pricing")
		var request ModelPricingUpsertApiRequest
		agentContext, ok := bindPricingRequest(c, tracer, meter, &request)
		if !ok {
			return
		}
		// A price change applies to every account in the tenant and rewrites
		// what all of them are billed at, so it is a tenant-admin action —
		// account-level write access is not enough.
		if !agentContext.GetSecurityContext().IsTenantAdmin() {
			c.JSON(403, buildApiResponse(nil, []error{errors.New("model pricing can only be changed by a tenant admin")}))
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		sc := agentContext.GetSecurityContext()
		if err := core.UpsertModelPricing(dbManager, sc.GetTenantId(), sc.GetUserId(), request.Prices); err != nil {
			agentContext.GetLogger().Error("api: error saving model pricing", "error", err)
			// Validation failures are the caller's fault, so report them as 400
			// with the message intact rather than a blanket 500.
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(200, buildApiResponse(map[string]any{"saved": len(request.Prices)}, nil))
	})

	groupV2.POST("/ai_delete_model_pricing", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("ai_delete_model_pricing")
		var request ModelPricingDeleteApiRequest
		agentContext, ok := bindPricingRequest(c, tracer, meter, &request)
		if !ok {
			return
		}
		// Removing an override changes what every account in the tenant is
		// billed at, exactly as setting one does, so it carries the same gate.
		if !agentContext.GetSecurityContext().IsTenantAdmin() {
			c.JSON(403, buildApiResponse(nil, []error{errors.New("model pricing can only be changed by a tenant admin")}))
			return
		}

		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		sc := agentContext.GetSecurityContext()
		removed, err := core.DeleteModelPricing(dbManager, sc.GetTenantId(), request.ProviderName, request.ModelName)
		if err != nil {
			agentContext.GetLogger().Error("api: error deleting model pricing", "error", err)
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(200, buildApiResponse(map[string]any{"removed": removed}, nil))
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

		if !agentContext.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_models") {
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

		// Build response. image_support advertises the server's image limits
		// so the UI can enforce them client-side instead of guessing. Image
		// support itself is always on — "enabled" is kept for API
		// compatibility with clients already reading it.
		response := map[string]any{
			// models: flat one-row-per-(slot, model) list. Kept for the
			// workflow node picker, which only reads provider/model.
			"models": models,
			// credentials: the same information collapsed into unique
			// destinations, each with the models reachable through it. Only the
			// server can compute this — endpoints and api keys are never
			// serialized — so clients must not attempt their own grouping.
			"credentials": core.BuildCredentialsFrom(request.AccountId, models),
			"default": map[string]string{
				"provider": defaultProvider,
				"model":    defaultModel,
			},
			"image_support": map[string]any{
				"enabled":            true,
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

		if !agentContext.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_conversations") {
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

		if !agentContext.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_conversations") {
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

		if !agentContext.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_conversations") {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger := agentContext.GetLogger()

		if request.Limit <= 0 || request.Limit > 100 {
			request.Limit = 100
		}

		messageIds := request.MessageIds
		if len(messageIds) == 0 && request.MessageId != "" {
			messageIds = []string{request.MessageId}
		}

		references, err := core.GetConversationDao().ListAgentReferences(request.AccountId, request.ConversationId, messageIds, request.AgentId, request.Limit)
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
