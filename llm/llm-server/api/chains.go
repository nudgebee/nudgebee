package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	_ "nudgebee/llm/agents/signoz"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type HasuraActionRequestAction struct {
	Name string `json:"name"`
}

type HasuraActionRequest struct {
	Action           HasuraActionRequestAction `json:"action"`
	Input            map[string]any            `json:"input"`
	RequestQuery     string                    `json:"request_query"`
	SessionVariables map[string]any            `json:"session_variables"`
}

type ConversationApiRequest struct {
	Query          string                   `json:"query" mapstructure:"required" validate:"required"`
	ConversationId string                   `json:"conversation_id"`
	SessionId      string                   `json:"session_id"`
	AccountId      string                   `json:"account_id"`
	UserId         string                   `json:"user_id" mapstructure:"required"`
	MessageId      string                   `json:"message_id"`
	AgentId        string                   `json:"agent_id"`
	Async          *bool                    `json:"async"`
	Config         toolcore.NBQueryConfig   `json:"config"`
	Source         core.ConversationSource  `json:"source"`
	ClientTools    []toolcore.NBToolCommand `json:"client_tools"`
	Capabilities   map[string]any           `json:"capabilities"`
	Images         []core.ImageAttachment   `json:"images,omitempty"`
}

type ConversationTerminateApiRequest struct {
	ConversationId string `json:"conversation_id"`
	AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId         string `json:"user_id" mapstructure:"required"`
}

type ClientToolResultItem struct {
	ToolId string `json:"tool_id" validate:"required"`
	Result string `json:"result" validate:"required"`
	Status string `json:"status" validate:"required"`
}

type ClientToolResultApiRequest struct {
	ConversationId string                 `json:"conversation_id" validate:"required"`
	MessageId      string                 `json:"message_id" validate:"required"`
	AgentId        string                 `json:"agent_id" validate:"required"`
	AccountId      string                 `json:"account_id" validate:"required"`
	Async          *bool                  `json:"async"`
	Results        []ClientToolResultItem `json:"results" validate:"required,dive"`
}

func isAsync(async *bool) bool {
	if async == nil {
		return true
	}
	return *async
}

func handleRequestExecution(
	c *gin.Context,
	agentContext *security.RequestContext,
	async *bool,
	userId string,
	metricsKey string,
	logger *slog.Logger,
	executeFn func(ctx *security.RequestContext) (core.NBAgentResponse, error),
	asyncResponse any,
) {
	startTime := time.Now()
	if isAsync(async) {
		submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncApiTimeoutSeconds)*time.Second)
		defer cancel()

		workerQueue := asyncUserOperationWorkerPool
		if userId == "" || userId == uuid.Nil.String() {
			workerQueue = asyncOperationWorkerPool
		}

		detachedCtx := context.WithoutCancel(agentContext.GetContext())
		// Explicitly propagate the trace context to the detached context
		span := trace.SpanFromContext(agentContext.GetContext())
		detachedCtx = trace.ContextWithSpan(detachedCtx, span)

		asyncAgentContext := security.NewRequestContext(detachedCtx, agentContext.GetSecurityContext(), agentContext.GetLogger(), agentContext.GetTracer(), agentContext.GetMeter())

		err := workerQueue.Submit(submissionCtx, func() {
			executionStartTime := time.Now()
			_, executeErr := executeFn(asyncAgentContext)
			if executeErr != nil {
				logger.Error("api: error executing request asynchronously",
					"metrics_key", metricsKey,
					"latency", time.Since(executionStartTime).String(),
					"queue_wait", time.Since(startTime).String(),
					"error", common.SanitizeErrorMessage(executeErr.Error()),
				)
			} else {
				logger.Info("api: async request completed",
					"metrics_key", metricsKey,
					"latency", time.Since(executionStartTime).String(),
					"queue_wait", time.Since(startTime).String(),
				)
			}
		})
		if err != nil {
			common.MetricsApiRequestsFailedTotal(metricsKey, "timedout")
			c.JSON(http.StatusServiceUnavailable, buildApiResponse(nil, []error{common.Error{Message: "api: unable to queue request, please try again later"}}))
			return
		}
		c.JSON(http.StatusAccepted, buildApiResponse(asyncResponse, nil))
	} else {
		resp, err := executeFn(agentContext)
		if err != nil {
			logger.Error("api: error executing request", "metrics_key", metricsKey, "latency", time.Since(startTime).String(), "error", err)
			c.JSON(http.StatusInternalServerError, buildApiResponse(resp, []error{
				common.Error{
					Message: common.SanitizeErrorMessage(err.Error()),
				},
			}))
			return
		}
		logger.Info("api: request completed", "metrics_key", metricsKey, "latency", time.Since(startTime).String())
		c.JSON(http.StatusOK, buildApiResponse(resp, nil))
	}
}

func handleCompletionApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/completions")

	groupV2.POST("/chat", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if request.Query == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: query is required",
				},
			}))
			return
		}

		if len(request.Query) > 500000 {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: max query size can be 500000 characters",
				},
			}))
			return
		}

		if len(request.Images) > 0 {
			if !core.IsImageSupportEnabled() {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: image attachments are not enabled",
					},
				}))
				return
			}
			if err := core.ValidateImages(c.Request.Context(), request.Images); err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: " + err.Error(),
					},
				}))
				return
			}
		}

		conversationId := uuid.NullUUID{}
		if request.ConversationId != "" {
			parsedUuid, err := uuid.Parse(request.ConversationId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid conversation_id",
					},
				}))
				return
			}
			conversationId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}

		if conversationId.Valid {
			conversation, err := core.GetConversationDao().GetConversation(request.ConversationId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: conversation_id not found",
					},
				}))
				return
			}

			if conversation.AccountID.String() != request.AccountId {
				c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{
					common.Error{
						Message: errorUserAccessMessage,
					},
				}))
				return
			}

			if request.SessionId == "" {
				request.SessionId = conversation.SessionID
			}

			if request.SessionId != conversation.SessionID {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid sessionId",
					},
				}))
				return
			}

			if conversation.Status == core.ConversationStatusInProgress {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: conversation is in progress",
					},
				}))
				return
			}

			// use original source
			if request.Source == "" && conversation.Source != nil {
				request.Source = *conversation.Source
			}
		}

		if request.SessionId == "" {
			request.SessionId = uuid.New().String()
		}

		logger := slog.With("account_id", request.AccountId, "session_id", request.SessionId, "user_id", request.UserId)

		if request.ConversationId != "" {
			logger = logger.With("conversation_id", request.ConversationId)
		}
		ctx := context.WithoutCancel(c.Request.Context())
		// Optimization: Add a thread-safe resolution cache to the context to prevent redundant DB calls for LLM config
		ctx = context.WithValue(ctx, core.ContextKeyLLMResolution, core.NewLLMResolutionCache())

		agentContext, err := buildContextFromHasuraPayload(ctx, c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}
		c.Request = c.Request.WithContext(agentContext.GetContext())

		var agent core.NBAgent
		isNewConversation := false
		if request.ConversationId == "" {
			if request.SessionId != "" {
				// PARALLELIZE: Fetch conversation and history concurrently to save sequential DB tax
				var conv core.Conversation
				var chatHistory []map[string]string
				var wg sync.WaitGroup
				wg.Add(2)

				go func() {
					defer wg.Done()
					conv, _ = core.GetConversationDao().GetConversationBySession(request.AccountId, request.SessionId)
				}()

				go func() {
					defer wg.Done()
					// Pre-load history for InferAgentOrHelp optimization
					// Scope to the account and user to ensure fast lookup
					chatHistory, _ = core.GetConversationDao().LoadConversationMessages(request.AccountId, "", request.UserId, core.MessageTypeRoute, 1)
				}()

				wg.Wait()

				if conv.ID != uuid.Nil {
					request.ConversationId = conv.ID.String()
					// Check if we can reuse the last agent from the pre-loaded history
					if len(chatHistory) > 0 && chatHistory[0]["response"] != "" {
						lastAgentName := strings.TrimSpace(chatHistory[0]["response"])
						if lastAgentName != "" && lastAgentName != core.RouterAgentName {
							if agent1, found := core.GetNBAgent(agentContext, lastAgentName, request.AccountId, core.AgentStatusEnabled); found {
								agent = agent1
								logger.Info("api: reusing last active agent from pre-loaded history", "agent_name", lastAgentName)
							}
						}
					}
				} else {
					isNewConversation = true
				}
			} else {
				isNewConversation = true
			}
			if request.ConversationId == "" {
				request.ConversationId = common.GenerateUUID()
			}
		}

		// Update conversationId variable for HandleConversationSessionRequest calls
		if request.ConversationId != "" {
			conversationId = uuid.NullUUID{
				UUID:  uuid.MustParse(request.ConversationId),
				Valid: true,
			}
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		} else if request.UserId != "" && request.UserId != agentContext.GetSecurityContext().GetUserId() && !agentContext.GetSecurityContext().IsSuperAdmin() {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}
		if agentContext.GetSecurityContext().IsSuperAdmin() {
			request.UserId = security.GetSystemUserId()
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Check budget limits for tenant and account
		module := budget.ModuleUserInvestigation
		if strings.HasPrefix(request.SessionId, events.SessionIdPrefixEvent) {
			module = budget.ModuleInvestigation
		}

		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, module, logger) {
			return
		}

		//TODO validate message_id and agent_id and if they are applicable && correct
		messageId := uuid.NullUUID{}
		if request.MessageId != "" {
			parsedUuid, err := uuid.Parse(request.MessageId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid message_id",
					},
				}))
				return
			}
			messageId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}
		agentId := uuid.NullUUID{}
		if request.AgentId != "" {
			parsedUuid, err := uuid.Parse(request.AgentId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid agent_id",
					},
				}))
				return
			}
			agentId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}
		//if agentId is available, then populate that agent
		if agentId.Valid {
			agents, err := core.GetConversationDao().ListConversationAgents("", agentId.UUID.String())
			if err != nil {
				logger.Error("api: error getting router chain", "error", err)
				c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to complete request, Please try again later",
					},
				}))
				return
			}
			if len(agents) == 0 {
				logger.Error("api: agent not found", "agent_id", request.AgentId)
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: agent not found",
					},
				}))
				return
			}
			agentDto := agents[0]
			agent1, found := core.GetNBAgent(agentContext, agentDto.AgentName, request.AccountId, core.AgentStatusEnabled)
			if !found {
				logger.Error("api: agent not found", "agent_name", agentDto.AgentName)
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: agent not found",
					},
				}))
				return
			}
			agent = agent1
		}

		source := core.ConversationSourceUserInvestigation
		if request.Source != "" {
			source = request.Source
		}
		// Handle user conversation question to add context to the original question
		if agent == nil {
			agent, err = agents.InferAgentOrHelp(agentContext, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithIsNewConversation(isNewConversation))
			if err != nil {
				logger.Error("api: error getting router chain", "error", err)
				c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to complete request, Please try again later",
					},
				}))
				return
			}
		}

		if agent == nil {
			logger.Info("api: unable to identify intent", "error", err)
			c.JSON(http.StatusOK, buildApiResponse(core.NBAgentResponse{
				Response: []string{"Unable to identify intent. My expertise is focused on Kubernetes, Helm, Events, Logs, Metrics, Recommendations, Software Development. Could you please provide more details or rephrase your question so I can better assist you?"},
				Status:   core.ConversationStatusFailed,
			}, nil))
			return
		}

		// Pre-check integrations only when the user explicitly invoked an agent
		// via @<name>. LLM-routed agents can be picked tentatively and the
		// planner already has its own missing-config error path; the @-mention
		// path is the one where the user expects a definitive yes/no upfront.
		if config.Config.AgentIntegrationPrecheckEnabled &&
			strings.HasPrefix(strings.TrimSpace(request.Query), "@") {
			if err := agents.EnsureAgentIntegrations(agentContext, agent, request.AccountId); err != nil {
				var missing *agents.MissingIntegrationError
				if errors.As(err, &missing) {
					logger.Info("api: agent missing required integration",
						"agent", missing.AgentName, "tools", missing.MissingTools)
					c.JSON(http.StatusOK, buildApiResponse(core.NBAgentResponse{
						Response: []string{fmt.Sprintf(
							"@%s needs an integration to run. Configure one of: %s. Open Settings → Integrations to connect it.",
							missing.AgentName, strings.Join(missing.MissingTools, ", "))},
						AgentName:      agent.GetName(),
						Query:          request.Query,
						ConversationId: request.ConversationId,
						SessionId:      request.SessionId,
						Status:         core.ConversationStatusFailed,
					}, nil))
					return
				}
			}
		}

		// Execute the agent
		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_chat", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, agent, request.UserId, request.AccountId, request.SessionId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConversationId(conversationId), core.ConversationSessionRequestWithMessageId(messageId), core.ConversationSessionRequestWithAgentId(agentId), core.ConversationSessionRequestWithConfig(request.Config), core.ConversationSessionRequestWithClientTools(request.ClientTools), core.ConversationSessionRequestWithCapabilities(request.Capabilities), core.ConversationSessionRequestWithIsNewConversation(isNewConversation), core.ConversationSessionRequestWithImages(request.Images))
		}, core.NBAgentResponse{
			Response:       []string{"Your request has been received and will be processed asynchronously."},
			Query:          request.Query,
			AgentName:      agent.GetName(),
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	})

	// /chat/auto - third-party endpoint that auto-resolves account_id from query text.
	// When account_id is provided, it delegates directly to the main /chat flow.
	// When account_id is absent, it resolves the account via conversation context
	// or query text matching, then continues with the standard execution flow.
	groupV2.POST("/chat/auto", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat_auto")
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
		var request ConversationApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if request.Query == "" {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: query is required",
				},
			}))
			return
		}

		if len(request.Query) > 500000 {
			c.JSON(400, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: max query size can be 500000 characters",
				},
			}))
			return
		}

		conversationId := uuid.NullUUID{}
		if request.ConversationId != "" {
			parsedUuid, err := uuid.Parse(request.ConversationId)
			if err != nil {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid conversation_id",
					},
				}))
				return
			}
			conversationId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}

		// Auto-resolve account_id when not provided
		if request.AccountId == "" {
			earlyCtx := context.WithoutCancel(c.Request.Context())
			earlyAgentContext, earlyErr := buildContextFromHasuraPayload(earlyCtx, c, &hasuraRequest, tracer, meter, slog.Default())
			if earlyErr != nil {
				slog.Error("chat_auto: failed to build security context", "error", earlyErr)
				c.JSON(401, buildApiResponse(nil, []error{
					common.Error{
						Message: earlyErr.Error(),
					},
				}))
				return
			}

			// Strategy 1: Inherit account from existing conversation
			if conversationId.Valid {
				conversation, convErr := core.GetConversationDao().GetConversation(request.ConversationId)
				if convErr == nil && conversation.AccountID != uuid.Nil {
					request.AccountId = conversation.AccountID.String()
					slog.Info("chat_auto: resolved from conversation", "account_id", request.AccountId)
				}
			}

			// Strategy 2: Resolve from query text via string matching
			if request.AccountId == "" {
				result, resolveErr := resolveAccountForRequest(earlyAgentContext, request.Query)
				if resolveErr != nil {
					c.JSON(resolveErr.StatusCode, buildApiResponse(nil, []error{
						common.Error{
							Message: resolveErr.Message,
						},
					}))
					return
				}
				if result.Followup != nil {
					c.JSON(200, buildApiResponse(*result.Followup, nil))
					return
				}
				request.AccountId = result.AccountId
			}

			if request.AccountId == "" {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to resolve account from query",
					},
				}))
				return
			}
		}

		// From here, account_id is resolved — standard chat execution flow
		if conversationId.Valid {
			conversation, err := core.GetConversationDao().GetConversation(request.ConversationId)
			if err != nil {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: conversation_id not found",
					},
				}))
				return
			}

			if conversation.AccountID.String() != request.AccountId {
				c.JSON(403, buildApiResponse(nil, []error{
					common.Error{
						Message: errorUserAccessMessage,
					},
				}))
				return
			}

			if request.SessionId == "" {
				request.SessionId = conversation.SessionID
			}

			if request.SessionId != conversation.SessionID {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid sessionId",
					},
				}))
				return
			}

			if conversation.Status == core.ConversationStatusInProgress {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: conversation is in progress",
					},
				}))
				return
			}

			// use original source
			if request.Source == "" && conversation.Source != nil {
				request.Source = *conversation.Source
			}
		}

		if request.ConversationId == "" {
			if request.SessionId != "" {
				conversation, err := core.GetConversationDao().GetConversationBySession(request.AccountId, request.SessionId)
				if err == nil && conversation.ID != uuid.Nil {
					request.ConversationId = conversation.ID.String()
				}
			}
			if request.ConversationId == "" {
				request.ConversationId = common.GenerateUUID()
			}
		}

		if request.SessionId == "" {
			request.SessionId = uuid.New().String()
		}

		logger := slog.With("account_id", request.AccountId, "session_id", request.SessionId, "user_id", request.UserId)

		if request.ConversationId != "" {
			logger = logger.With("conversation_id", request.ConversationId)
		}
		ctx := context.WithoutCancel(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		// Update conversationId variable for HandleConversationSessionRequest calls
		if request.ConversationId != "" {
			conversationId = uuid.NullUUID{
				UUID:  uuid.MustParse(request.ConversationId),
				Valid: true,
			}
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		} else if request.UserId != "" && request.UserId != agentContext.GetSecurityContext().GetUserId() {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(403, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Check budget limits for tenant and account
		module := budget.ModuleUserInvestigation
		if strings.HasPrefix(request.SessionId, events.SessionIdPrefixEvent) {
			module = budget.ModuleInvestigation
		}

		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, module, logger) {
			return
		}

		//TODO validate message_id and agent_id and if they are applicable && correct
		messageId := uuid.NullUUID{}
		if request.MessageId != "" {
			parsedUuid, err := uuid.Parse(request.MessageId)
			if err != nil {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid message_id",
					},
				}))
				return
			}
			messageId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}
		agentId := uuid.NullUUID{}
		if request.AgentId != "" {
			parsedUuid, err := uuid.Parse(request.AgentId)
			if err != nil {
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid agent_id",
					},
				}))
				return
			}
			agentId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}
		var agent core.NBAgent
		//if agentId is available, then populate that agent
		if agentId.Valid {
			agents, err := core.GetConversationDao().ListConversationAgents("", agentId.UUID.String())
			if err != nil {
				logger.Error("api: error getting router chain", "error", err)
				c.JSON(500, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to complete request, Please try again later",
					},
				}))
				return
			}
			if len(agents) == 0 {
				logger.Error("api: agent not found", "agent_id", request.AgentId)
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: agent not found",
					},
				}))
				return
			}
			agentDto := agents[0]
			agent1, found := core.GetNBAgent(agentContext, agentDto.AgentName, request.AccountId, core.AgentStatusEnabled)
			if !found {
				logger.Error("api: agent not found", "agent_name", agentDto.AgentName)
				c.JSON(400, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: agent not found",
					},
				}))
				return
			}
			agent = agent1
		}

		source := core.ConversationSourceUserInvestigation
		if request.Source != "" {
			source = request.Source
		}
		// Handle user conversation question to add context to the original question
		if agent == nil {
			agent, err = agents.InferAgentOrHelp(agentContext, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source))
			if err != nil {
				logger.Error("api: error getting router chain", "error", err)
				c.JSON(500, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to complete request, Please try again later",
					},
				}))
				return
			}
		}

		if agent == nil {
			logger.Info("api: unable to identify intent", "error", err)
			c.JSON(200, buildApiResponse(core.NBAgentResponse{
				Response: []string{"Unable to identify intent. My expertise is focused on Kubernetes, Helm, Events, Logs, Metrics, Recommendations, Software Development. Could you please provide more details or rephrase your question so I can better assist you?"},
				Status:   core.ConversationStatusFailed,
			}, nil))
			return
		}

		// Execute the agent
		if *request.Async {
			submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncApiTimeoutSeconds)*time.Second)
			defer cancel()

			workerQueue := asyncUserOperationWorkerPool
			if request.UserId == "" || request.UserId == uuid.Nil.String() {
				workerQueue = asyncOperationWorkerPool
			}

			err = workerQueue.Submit(submissionCtx, func() {
				_, executeErr := core.HandleConversationSessionRequest(agentContext, agent, request.UserId, request.AccountId, request.SessionId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConversationId(conversationId), core.ConversationSessionRequestWithMessageId(messageId), core.ConversationSessionRequestWithAgentId(agentId), core.ConversationSessionRequestWithConfig(request.Config), core.ConversationSessionRequestWithClientTools(request.ClientTools), core.ConversationSessionRequestWithCapabilities(request.Capabilities))
				if executeErr != nil {
					logger.Error("api: error executing chain asynchronously", "error", executeErr)
				} else {
					logger.Info("api: async request completed")
				}
			})
			if err != nil {
				common.MetricsApiRequestsFailedTotal("chains_chat_auto", "timedout")
				c.JSON(503, buildApiResponse(nil, []error{common.Error{Message: "api: unable to queue request, please try again later"}}))
				return
			}
			c.JSON(202, buildApiResponse(core.NBAgentResponse{
				Response:       []string{"Your request has been received and will be processed asynchronously."},
				Query:          request.Query,
				AgentName:      agent.GetName(),
				ConversationId: request.ConversationId,
				SessionId:      request.SessionId,
				Status:         core.ConversationStatusInProgress,
			}, nil))
		} else {
			chainResponse, executeErr := core.HandleConversationSessionRequest(agentContext, agent, request.UserId, request.AccountId, request.SessionId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConversationId(conversationId), core.ConversationSessionRequestWithMessageId(messageId), core.ConversationSessionRequestWithAgentId(agentId), core.ConversationSessionRequestWithConfig(request.Config), core.ConversationSessionRequestWithClientTools(request.ClientTools), core.ConversationSessionRequestWithCapabilities(request.Capabilities))
			if executeErr != nil {
				logger.Error("api: error executing chain", "error", executeErr)
				c.JSON(500, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to complete request, please try again later",
					},
				}))
				return
			}
			c.JSON(200, buildApiResponse(chainResponse, nil))
		}
	})

	groupV2.POST("/chat_suggestions", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat_suggestions")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request core.ConversationSuggestionRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		_, err = uuid.Parse(request.ConversationId)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: invalid conversation_id",
				},
			}))
			return
		}

		_, err = uuid.Parse(request.MessageId)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: invalid message_id",
				},
			}))
			return
		}

		_, err = uuid.Parse(request.AccountId)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: invalid account_id",
				},
			}))
			return
		}

		message, err := core.GetConversationDao().GetConversationMessage(request.MessageId, request.AccountId, request.ConversationId)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: message_id not found",
				},
			}))
			return
		}

		if message.Status == core.ConversationStatusInProgress {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: message is in progress",
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		//TODO validate message_id and agent_id and if they are applicable && correct
		_, err = uuid.Parse(request.MessageId)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: invalid message_id",
				},
			}))
			return
		}

		// Check budget limits for tenant and account
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		suggestionResponse, executeErr := core.HandleConversationSuggestionRequest(agentContext, request)
		if executeErr != nil {
			logger.Error("api: error executing chain", "error", executeErr)
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: unable to complete request, please try again later",
				},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(suggestionResponse, nil))
	})

	groupV2.POST("/chat_stop", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat_stop")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationTerminateApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		if request.ConversationId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: messageId is required",
				},
			}))
			return
		}

		err = core.GetConversationDao().TerminateConversation(agentContext, request.AccountId, request.ConversationId)
		if err != nil {
			logger.Error("api: error terminating conversation message", "error", err)
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		c.JSON(http.StatusOK, buildApiResponse(map[string]string{"status": "terminated"}, nil))
	})

	groupV2.POST("/prometheus-query", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_prometheus_query")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if request.Query == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: query is required",
				},
			}))
			return
		}

		if request.ConversationId == "" {
			request.ConversationId = uuid.New().String()
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)
		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		source := core.ConversationSourcePrometheusQuery
		if request.Source != "" {
			source = request.Source
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))

		var prometheusChain = &agents.PrometheusAgent{}
		// Check budget limits for tenant and account
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		if request.SessionId == "" {
			request.SessionId = request.ConversationId
		}

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_prometheus_query", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, prometheusChain, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source))
		}, core.NBAgentResponse{
			Response:       []string{},
			Query:          request.Query,
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	})

	groupV2.POST("/loki-query", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_loki_query")
		var request ConversationApiRequest
		var hasuraRequest HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.ConversationId == "" {
			request.ConversationId = uuid.New().String()
		}
		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)
		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		}

		// Call the HasAccess method
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))

		source := core.ConversationSourceLokiQuery
		if request.Source != "" {
			source = request.Source
		}

		var logChain = &agents.LokiAgent{}
		// Check budget limits for tenant and account
		module := budget.ModuleUserInvestigation
		if strings.HasPrefix(request.SessionId, events.SessionIdPrefixEvent) {
			module = budget.ModuleInvestigation
		}

		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, module, logger) {
			return
		}

		if request.SessionId == "" {
			request.SessionId = request.ConversationId
		}

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_loki_query", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, logChain, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source))
		}, core.NBAgentResponse{
			Response:       []string{},
			Query:          request.Query,
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	})

	groupV2.POST("/elastic-search-query", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_elastic_search_query")
		var request ConversationApiRequest
		var hasuraRequest HasuraActionRequest
		err := c.ShouldBindJSON(&hasuraRequest)
		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)
		if err != nil {
			logger.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.ConversationId == "" {
			request.ConversationId = uuid.New().String()
		}

		queryBody := map[string]string{}
		err = common.UnmarshalJson([]byte(request.Query), &queryBody)
		if err != nil {
			slog.Error("elastic: unable unmarshal)", "error", err.Error())
		}
		request.Query = queryBody["query"]
		request.Config = toolcore.NBQueryConfig{} // ES index is handled by the tool directly

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Check budget limits for tenant and account
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		source := core.ConversationSourceESQuery
		if request.Source != "" {
			source = request.Source
		}

		esChain := agents.ESLogAgent{}
		request.Query = "Generate an Elastic Search DSL query for: " + request.Query

		if request.SessionId == "" {
			request.SessionId = request.ConversationId
		}

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_elastic_search_query", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, esChain, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConfig(request.Config))
		}, core.NBAgentResponse{
			Response:       []string{},
			Query:          request.Query,
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	})

	groupV2.POST("/workflow-generate", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_workflow_generate")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if request.Query == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: query is required",
				},
			}))
			return
		}

		conversationId := uuid.NullUUID{}
		if request.ConversationId != "" {
			parsedUuid, err := uuid.Parse(request.ConversationId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid conversation_id",
					},
				}))
				return
			}
			conversationId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}

		if conversationId.Valid {
			conversation, err := core.GetConversationDao().GetConversation(request.ConversationId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: conversation_id not found",
					},
				}))
				return
			}

			if conversation.AccountID.String() != request.AccountId {
				c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{
					common.Error{
						Message: errorUserAccessMessage,
					},
				}))
				return
			}

			if request.SessionId == "" {
				request.SessionId = conversation.SessionID
			}

			if request.SessionId != conversation.SessionID {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid sessionId",
					},
				}))
				return
			}

			if conversation.Status == core.ConversationStatusInProgress {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: conversation is in progress",
					},
				}))
				return
			}

			// use original source
			if request.Source == "" && conversation.Source != nil {
				request.Source = *conversation.Source
			}
		}

		messageId := uuid.NullUUID{}
		if request.MessageId != "" {
			parsedUuid, err := uuid.Parse(request.MessageId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid message_id",
					},
				}))
				return
			}
			messageId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}
		agentId := uuid.NullUUID{}
		if request.AgentId != "" {
			parsedUuid, err := uuid.Parse(request.AgentId)
			if err != nil {
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: invalid agent_id",
					},
				}))
				return
			}
			agentId = uuid.NullUUID{
				UUID:  parsedUuid,
				Valid: true,
			}
		}

		if request.SessionId == "" {
			request.SessionId = uuid.New().String()
		}

		logger := slog.With("account_id", request.AccountId, "session_id", request.SessionId, "user_id", request.UserId)
		if request.ConversationId != "" {
			logger = logger.With("conversation_id", request.ConversationId)
		}

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		if request.UserId == "" {
			request.UserId = agentContext.GetSecurityContext().GetUserId()
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		source := core.ConversationSourceWorkflowBuilder
		if request.Source != "" {
			source = request.Source
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))

		chain, found := core.GetNBAgent(agentContext, agents.WorkflowBuilderAgentName, request.AccountId, core.AgentStatusEnabled)
		if !found {
			logger.Error("api: agent not found", "agent_name", agents.WorkflowBuilderAgentName)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: agent not found",
				},
			}))
			return
		}

		// Check budget limits for tenant and account
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_workflow_generate", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, chain, request.UserId, request.AccountId, request.SessionId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConversationId(conversationId), core.ConversationSessionRequestWithMessageId(messageId), core.ConversationSessionRequestWithAgentId(agentId), core.ConversationSessionRequestWithEnableQueryRefinement(false), core.ConversationSessionRequestWithConfig(request.Config))
		}, core.NBAgentResponse{
			Response:       []string{"Your request has been received and will be processed asynchronously."},
			Query:          request.Query,
			AgentName:      chain.GetName(),
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	})

	groupV2.POST("/prompt-refinement", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_prompt_refinement")
		var request ConversationApiRequest
		err := c.ShouldBindJSON(&request)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)
		agentContext, err := buildContextFromRequestPayload(c.Request.Context(), c, map[string]string{"account_id": request.AccountId, "user_id": request.UserId}, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Check budget limits for tenant and account
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))
		chain, found := core.GetNBAgent(agentContext, agents.PromptRefinementAgentName, request.AccountId, core.AgentStatusEnabled)
		if !found {
			logger.Error("api: agent not found", "agent_name", agents.PromptRefinementAgentName)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: agent not found",
				},
			}))
			return
		}

		source := core.ConversationSourceUserInvestigation
		if request.Source != "" {
			source = request.Source
		}
		resp, err := core.HandleConversationSessionRequest(agentContext, chain, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source))
		if err != nil {
			logger.Error(errorAuditMessage, "error", err)
			c.JSON(http.StatusInternalServerError, buildApiResponse(resp, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		c.JSON(http.StatusOK, buildApiResponse(resp, nil))
	})

	groupV2.POST("/conversation-usage-metrics", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_conversation_usage_metrics")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request core.ConversationUsageMetricsRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.ConversationId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: conversationId is required",
				},
			}))
			return
		}

		data, err := core.HandleConversationUsageMetricsApi(agentContext, request)

		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	groupV2.POST("/conversation-time-aggregates", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_conversation_time_aggregates")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		var request core.ConversationTimeAggregatesRequest
		var hasuraRequest HasuraActionRequest
		if err = common.DecodeMapToStruct(requestMap, &hasuraRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(hasuraRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "user_id", request.UserId)

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		// account_id is intentionally optional. The handler falls back to every
		// account the caller's session is permitted to read, matching the
		// multi-account roll-up the troubleshoot widget needs. Authorization
		// of any explicit account_id happens inside the handler.
		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		data, err := core.HandleConversationTimeAggregatesApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	groupV2.POST("/chat_get", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat_get")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		var request ConversationGetApiRequest
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
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

		logger := agentContext.GetLogger()
		if request.ConversationId != "" {
			logger = logger.With("conversation_id", request.ConversationId)
			conversation, err = core.GetConversationDao().GetConversationWithMessages(request.ConversationId, request.AccountId)
		} else {
			logger = logger.With("session_id", request.SessionId)
			conversation, err = core.GetConversationDao().GetLatestConversationBySessionIDWithMessages(request.SessionId, request.AccountId)
		}

		if err != nil {
			logger.Error("api: error getting conversation", "error", err)
			statusCode := http.StatusBadRequest
			if errors.Is(err, core.ErrConversationNotFound) {
				statusCode = http.StatusNotFound
			}
			c.JSON(statusCode, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		c.JSON(http.StatusOK, buildApiResponse(conversation, nil))
	})

	groupV2.POST("/chat_list", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat_list")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		var request ConversationListApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.Default()

		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		if request.Limit == 0 {
			request.Limit = 20
		}

		logger = agentContext.GetLogger()

		conversations, err := core.GetConversationDao().ListConversations(request.AccountId, request.UserId, request.Title, string(request.Source), request.Limit, request.Offset)
		if err != nil {
			logger.Error("api: error listing conversations", "error", err)
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		c.JSON(http.StatusOK, buildApiResponse(conversations, nil))
	})

	groupV2.POST("/client-tool-result", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_client_tool_result")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		var request ClientToolResultApiRequest
		var hasuraRequest HasuraActionRequest
		err = common.DecodeMapToStruct(requestMap, &hasuraRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		hasuraRequestPayload := hasuraRequest.Input
		if hasuraRequestPayload["request"] != nil {
			hasuraRequestPayload = hasuraRequestPayload["request"].(map[string]any)
		}
		if hasuraRequestPayload == nil {
			hasuraRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(hasuraRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		err = common.ValidateStruct(request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "agent_id", request.AgentId)
		agentContext, err := buildContextFromHasuraPayload(c.Request.Context(), c, &hasuraRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeUpdate) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Check if conversation is already in progress to prevent race conditions
		conv, err := core.GetConversationDao().GetConversation(request.ConversationId)
		if err == nil && conv.Status == core.ConversationStatusInProgress {
			logger.Warn("api: conversation already in progress, rejecting tool result submission")
			c.JSON(http.StatusConflict, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: conversation is currently in progress, please wait",
				},
			}))
			return
		}

		// Check for already submitted tools
		for _, r := range request.Results {
			_, status, err := core.GetConversationDao().GetConversationToolResponse(r.ToolId, request.MessageId, request.ConversationId, request.AccountId)
			if err == nil {
				// If status is success or error, it means we already have a final result
				if strings.EqualFold(string(status), string(toolcore.NBToolResponseStatusSuccess)) || strings.EqualFold(string(status), string(toolcore.NBToolResponseStatusError)) {
					logger.Warn("api: tool response already submitted", "tool_id", r.ToolId, "status", status)
					c.JSON(http.StatusConflict, buildApiResponse(nil, []error{
						common.Error{
							Message: fmt.Sprintf("api: tool response for %s already submitted", r.ToolId),
						},
					}))
					return
				}
			}
		}

		// Update tool response in DB
		for _, r := range request.Results {
			err = core.GetConversationDao().UpdateConversationToolResponse(r.ToolId, request.MessageId, request.ConversationId, request.AccountId, r.Result, toolcore.NBToolResponseStatus(r.Status))
			if err != nil {
				logger.Error("api: error updating tool response", "tool_id", r.ToolId, "error", err)
				c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to update tool response for " + r.ToolId,
					},
				}))
				return
			}
		}

		// Resume agent execution
		handleRequestExecution(c, agentContext, request.Async, agentContext.GetSecurityContext().GetUserId(), "chains_client_tool_result", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationMessageRequest(request.AccountId, request.ConversationId, request.MessageId, request.AgentId)
		}, map[string]string{"status": "in_progress"})
	})
}
