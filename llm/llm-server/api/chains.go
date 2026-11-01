package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	_ "nudgebee/llm/agents/signoz"
	"nudgebee/llm/audit"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
	toolcore "nudgebee/llm/tools/core"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type ActionRequestAction struct {
	Name string `json:"name"`
}

type ActionRequest struct {
	Action           ActionRequestAction `json:"action"`
	Input            map[string]any      `json:"input"`
	RequestQuery     string              `json:"request_query"`
	SessionVariables map[string]any      `json:"session_variables"`
}

type ConversationApiRequest struct {
	// QueueOnly forbids falling through to foreground async execution.
	QueueOnly      bool                       `json:"queue_only"`
	Query          string                     `json:"query" mapstructure:"required" validate:"required"`
	ConversationId string                     `json:"conversation_id"`
	SessionId      string                     `json:"session_id"`
	AccountId      string                     `json:"account_id"`
	UserId         string                     `json:"user_id" mapstructure:"required"`
	MessageId      string                     `json:"message_id"`
	AgentId        string                     `json:"agent_id"`
	Async          *bool                      `json:"async"`
	Config         toolcore.NBQueryConfig     `json:"config"`
	Source         core.ConversationSource    `json:"source"`
	ClientTools    []toolcore.NBToolCommand   `json:"client_tools"`
	Capabilities   toolcore.AgentCapabilities `json:"capabilities"`
	Images         []core.ImageAttachment     `json:"images,omitempty"`
	// ChannelContext carries conversation from a watched messaging channel.
	// Callers must never merge it into Query — keeping it separate is what lets
	// the agent treat it as reference material rather than as a request.
	ChannelContext string `json:"channel_context,omitempty"`
	// ChannelContextRefs is structured provenance for ChannelContext — which
	// channel and which retained messages the block was assembled from. Stored
	// as a channel_context conversation reference so the UI can show what the
	// answer drew on. Opaque to the agent; never rendered into the prompt.
	ChannelContextRefs map[string]any `json:"channel_context_refs,omitempty"`
	// LogProvider optionally pins /log-query resolution to a specific log
	// provider (e.g. "loki") instead of the account's default — mirrors the
	// logs tab's "Log Provider:" dropdown override sent as log_provider on
	// observability.fetchLogs. Ignored by every other handler.
	LogProvider string `json:"log_provider,omitempty"`
	// ReplyRef is an opaque per-question correlator the caller attaches and
	// gets back verbatim on the /llm/response webhook (see
	// conversation_notification.go:sendReplyToNotificationServer). It exists
	// because SessionId is deliberately reused across many distinct questions
	// in some callers (notifications-server's bound incident channels route
	// every future @mention to the same SessionId so they share one LLM
	// conversation) — SessionId alone can no longer identify which specific
	// question a response answers. Never read or interpreted here; passed
	// through unchanged.
	ReplyRef string `json:"reply_ref,omitempty"`
	// Index optionally pins query generation to a specific Elasticsearch index —
	// the "Select an Index" dropdown selection on the logs and metrics tabs. It
	// scopes the field list the query generator is shown (fields are per-index)
	// and is the index the generated query is resolved against. Honoured by
	// /log-query and /prometheus-query (the latter only when the account's
	// metrics provider is Elasticsearch). Ignored for backends with no index
	// concept (Loki, Prometheus, …) and by every other handler.
	Index string `json:"index,omitempty"`
	// Resolution optionally resolves a pending follow-up without answering
	// it. Only "dismiss" is supported (soft-skip the question, then end the
	// conversation with a structured marker — see handleFollowupCancel for
	// why a hard terminate isn't duplicated here). Empty means answer
	// normally. Gated by FollowupCancelEnabled.
	Resolution string `json:"resolution,omitempty"`
	// Reason is an optional user-supplied note recorded with a "dismiss"
	// Resolution. Ignored otherwise.
	Reason string `json:"reason,omitempty"`
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

// acceptTimer records how long each step before a chat turn is accepted takes, so
// the accept path's latency can be broken down from one log line per request.
type acceptTimer struct {
	start, last time.Time
	steps       []any
}

func newAcceptTimer() *acceptTimer {
	now := time.Now()
	return &acceptTimer{start: now, last: now}
}

// mark attributes the time since the previous mark to step.
func (t *acceptTimer) mark(step string) {
	now := time.Now()
	t.steps = append(t.steps, step+"_ms", now.Sub(t.last).Milliseconds())
	t.last = now
}

// log reports every marked step and the total, including time spent after the
// last mark (submitting the turn, or an early exit such as queueing a message).
func (t *acceptTimer) log(logger *slog.Logger, endpoint string) {
	args := append([]any{"endpoint", endpoint, "total_ms", time.Since(t.start).Milliseconds()}, t.steps...)
	logger.Info("api: chat accept timing", args...)
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
			// EgressFilter block: surface the user-safe message verbatim with a 400.
			// The error string is constructed in the egressfilter package to avoid
			// echoing any matched value or detector internals; SanitizeErrorMessage
			// would be a no-op on it but we skip it for clarity.
			if scrubErr, ok := egressfilter.AsError(err); ok {
				logger.Warn("api: outbound LLM call blocked by egressfilter", "metrics_key", metricsKey, "latency", time.Since(startTime).String(), "audit_id", scrubErr.AuditID, "rule_ids", scrubErr.RuleIDs)
				c.JSON(http.StatusBadRequest, buildApiResponse(resp, []error{
					common.Error{Message: scrubErr.Error()},
				}))
				return
			}
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

// rejectIfConversationLocked rejects IN_PROGRESS, and rejects KILLED unless
// allowFollowupCompletion — shouldSkipResumeForTerminalConversation always lets a
// pending followup's cancel/resume through regardless of conversation status, so
// this must too. KILLED today comes only from the stranded-conversation sweep
// (MarkInProgressConversationAsKilled), sticky by design in shouldSkipSaveBack: an
// unexempted retry would run a full turn for real and then have it silently
// discarded. See #37486.
func rejectIfConversationLocked(c *gin.Context, endpoint string, conversation core.Conversation, allowFollowupCompletion bool) bool {
	var message string
	switch {
	case conversation.Status == core.ConversationStatusInProgress:
		message = "This conversation is in progress."
	case conversation.Status == core.ConversationStatusKilled && !allowFollowupCompletion:
		message = "This conversation was terminated and cannot be resumed. Please start a new conversation."
	default:
		return false
	}
	slog.Warn("chains: rejecting request for locked conversation",
		"endpoint", endpoint, "conversation_id", conversation.ID.String(),
		"account_id", conversation.AccountID.String(), "status", conversation.Status)
	c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
		common.Error{Message: message},
	}))
	return true
}

func isSecurityContextAdmin(sc *security.SecurityContext, accountID string) bool {
	if sc == nil {
		return false
	}
	return sc.IsSuperAdmin() || sc.IsTenantAdmin() || sc.HasScopedRole(security.AUTH_ACCOUNT_ADMIN_ROLE, accountID)
}

type QueuedMessageDeleteRequest struct {
	AccountId      string `json:"account_id"`
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	UserId         string `json:"user_id"`
}

type QueuedMessageUpdateRequest struct {
	AccountId      string `json:"account_id"`
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	Message        string `json:"message"`
	UserId         string `json:"user_id"`
}

type QueuedMessageForceRequest struct {
	AccountId      string `json:"account_id"`
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	UserId         string `json:"user_id"`
}

func queueConversationMessage(
	conversation core.Conversation,
	request *ConversationApiRequest,
	agentName string,
) (uuid.UUID, error) {
	effectiveAgent := agentName
	if len(request.ClientTools) > 0 {
		request.Config.ClientTools = request.ClientTools
	}
	if !request.Capabilities.IsEmpty() {
		request.Config.Capabilities = request.Capabilities
	}
	queuedMessageId := uuid.New()
	// Attachment refs intentionally have no message FK. Persist them before
	// publishing QUEUED so a completion cannot claim a partially saved request.
	if len(request.Images) > 0 {
		dao := core.GetAttachmentDAO()
		if dao == nil {
			return uuid.Nil, errors.New("attachment storage unavailable")
		}
		if _, err := dao.SaveAttachments(queuedMessageId.String(), conversation.ID.String(), conversation.AccountID.String(), request.Images); err != nil {
			return uuid.Nil, err
		}
	}
	_, err := core.GetConversationDao().SaveConversationMessage(
		queuedMessageId.String(),
		conversation.ID.String(),
		conversation.AccountID.String(),
		request.UserId,
		core.MessageRoleHuman,
		core.MessageTypeGeneration,
		request.Query,
		"",
		effectiveAgent,
		uuid.Nil,
		request.Config,
		request.ChannelContext,
		"",
		"",
		core.ConversationStatusQueued,
	)
	if err != nil {
		return uuid.Nil, err
	}
	return queuedMessageId, nil
}

// queueAdmits reports whether a chat request should wait in the queue. A
// request naming a message on a WAITING conversation is that turn's own retry
// or resume, not a new message, so it is left to run.
func queueAdmits(conversation core.Conversation, request *ConversationApiRequest) bool {
	if conversation.Status == core.ConversationStatusInProgress {
		return true
	}
	return request.MessageId == "" && core.QueueAcceptsStatus(conversation.Status)
}

// Called after authorized queue admission: a queue-only request must never
// become an independent asynchronous foreground request when status changes.
func rejectUnfulfilledQueueRequest(c *gin.Context, request *ConversationApiRequest) bool {
	if !request.QueueOnly {
		return false
	}
	c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: "Conversation is not ready to queue a message. Please retry your message."}}))
	return true
}

func tryQueueConversationMessage(
	c *gin.Context,
	conversation core.Conversation,
	request *ConversationApiRequest,
	agentName string,
) bool {
	// Text-only admission needs no preliminary read: capacity is checked
	// atomically by the DAO. Keep the image preflight to avoid unnecessary uploads.
	if len(request.Images) > 0 {
		maxQueued := config.Config.MaxQueuedMessagesPerConversation
		if maxQueued <= 0 {
			maxQueued = 5
		}
		existingQueued, err := core.GetConversationDao().GetQueuedConversationMessages(conversation.ID.String())
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: "unable to read queued messages"}}))
			return true
		}
		if len(existingQueued) >= maxQueued {
			slog.Warn("chains: maximum queued messages limit reached",
				"conversation_id", conversation.ID.String(),
				"queued_count", len(existingQueued),
				"max_limit", maxQueued,
			)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: fmt.Sprintf("Maximum queued messages limit reached (max: %d). Please wait for queued messages to process or cancel an existing queued message.", maxQueued),
				},
			}))
			return true
		}

	}

	queuedMessageId, err := queueConversationMessage(conversation, request, agentName)
	if err != nil {
		if errors.Is(err, core.ErrQueueFull) || errors.Is(err, core.ErrQueueInactive) {
			c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return true
		}
		slog.Error("chains: failed to queue message", "error", err, "conversation_id", conversation.ID.String())
		c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
			common.Error{Message: "failed to queue message: " + err.Error()},
		}))
		return true
	}
	slog.Info("chains: message queued for in-progress conversation", "conversation_id", conversation.ID.String(), "message_id", queuedMessageId.String())
	c.JSON(http.StatusAccepted, buildApiResponse(core.NBAgentResponse{
		Response:       []string{"Your request has been queued and will be processed after the current task finishes."},
		Query:          request.Query,
		ConversationId: conversation.ID.String(),
		SessionId:      conversation.SessionID,
		MessageId:      queuedMessageId.String(),
		Status:         core.ConversationStatusQueued,
	}, nil))
	return true
}

// dispatchQueuedMessage is shared by FIFO completion and explicit Send Now.
// Persist failures as terminal messages so a live worker never strands a claim.
func dispatchQueuedMessage(parent *security.RequestContext, conversation core.Conversation, msg core.ConversationMessage) error {
	// Callers may hand over a request-scoped context; queued work outlives the
	// request, so detach it once here. The turn itself runs on a fresh context below.
	parent = security.NewRequestContext(context.WithoutCancel(parent.GetContext()), parent.GetSecurityContext(), parent.GetLogger(), parent.GetTracer(), parent.GetMeter())
	fail := func(err error) {
		parent.GetLogger().Error("chains: queued execution failed", "error", err, "message_id", msg.ID)
		applied, saveErr := core.GetConversationDao().FinalizeConversationMessage(msg.ConversationID.String(), msg.ID.String(), "Queued message could not be executed. Please retry this message.", core.ConversationStatusFailed)
		if saveErr != nil {
			parent.GetLogger().Error("chains: failed to persist queue failure", "error", saveErr)
			return
		}
		if !applied {
			return
		}
		// A failed dispatch may never reach the turn-completion path that claims the
		// next message, so claim here too or the rest of the queue is stranded. If that
		// path already claimed, this claim finds a turn in progress and returns nil.
		// Each failure consumes one queued message, bounded by the queue capacity.
		claimCtx, cancelClaim := context.WithTimeout(parent.GetContext(), 10*time.Second)
		defer cancelClaim()
		next, claimErr := core.GetConversationDao().ClaimNextQueuedMessage(claimCtx, msg.ConversationID.String())
		if claimErr != nil {
			parent.GetLogger().Error("chains: failed to claim next queued message", "error", claimErr, "conversation_id", msg.ConversationID)
			return
		}
		if next == nil {
			return
		}
		// Dispatch on its own goroutine so a run of failures neither nests calls nor
		// holds this worker while the next submission waits for a pool slot.
		logger := parent.GetLogger()
		go func(next core.ConversationMessage) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("chains: panic dispatching next queued message", "panic", recovered, "message_id", next.ID, "stack", string(debug.Stack()))
				}
			}()
			if dispatchErr := dispatchQueuedMessage(parent, conversation, next); dispatchErr != nil {
				logger.Error("chains: failed to dispatch next queued message", "error", dispatchErr, "message_id", next.ID)
			}
		}(*next)
	}
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncApiTimeoutSeconds)*time.Second)
	defer cancel()
	workerQueue := asyncUserOperationWorkerPool
	if msg.UserID == uuid.Nil {
		workerQueue = asyncOperationWorkerPool
	}
	err := workerQueue.Submit(submissionCtx, func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				fail(fmt.Errorf("queued execution panic: %v", recovered))
			}
		}()
		sc, err := security.NewSecurityContext(conversation.TenantID.String(), msg.UserID.String())
		if err != nil {
			fail(err)
			return
		}
		if sc == nil || (!sc.HasAccountAccess(msg.AccountID.String(), security.SecurityAccessTypeRead) && !grantedRun(sc, msg.AccountID.String(), moduleAiMisc)) {
			fail(errors.New(errorUserAccessMessage))
			return
		}
		logger := slog.With("account_id", msg.AccountID, "conversation_id", msg.ConversationID, "message_id", msg.ID, "user_id", msg.UserID)
		ctx := security.NewRequestContext(context.Background(), sc, logger, parent.GetTracer(), parent.GetMeter())
		module := budget.ModuleUserInvestigation
		if strings.HasPrefix(conversation.SessionID, events.SessionIdPrefixEvent) {
			module = budget.ModuleInvestigation
		}
		if exceeded, reason := budget.CheckBudgetLimits(sc.GetTenantId(), msg.AccountID.String(), module, logger); exceeded {
			fail(errors.New(reason))
			return
		}
		var msgConfig toolcore.NBQueryConfig
		if msg.MessageConfig != nil && *msg.MessageConfig != "" {
			if err := json.Unmarshal([]byte(*msg.MessageConfig), &msgConfig); err != nil {
				fail(err)
				return
			}
		}
		source := core.ConversationSourceUserInvestigation
		if conversation.Source != nil && *conversation.Source != "" {
			source = *conversation.Source
		}
		var agent core.NBAgent
		if msg.AgentName != nil && *msg.AgentName != "" {
			agent, _ = core.GetNBAgent(ctx, *msg.AgentName, msg.AccountID.String(), core.AgentStatusEnabled)
		} else {
			agent, err = agents.InferAgentOrHelp(ctx, msg.UserID.String(), msg.AccountID.String(), msg.ConversationID.String(), msg.Message, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConfig(msgConfig))
		}
		if err != nil || agent == nil {
			fail(fmt.Errorf("queued message agent unavailable: %v", err))
			return
		}
		var images []core.ImageAttachment
		if dao := core.GetAttachmentDAO(); dao != nil {
			attachments, loadErr := dao.LoadAttachments(msg.ID.String(), msg.AccountID.String())
			if loadErr != nil {
				fail(loadErr)
				return
			}
			for _, attachment := range attachments {
				images = append(images, core.ImageAttachment{Data: attachment.Data, MIMEType: attachment.MIMEType})
			}
		}
		channelContext := ""
		if msg.MessageContext != nil {
			channelContext = *msg.MessageContext
		}
		_, err = core.HandleConversationSessionRequest(ctx, agent, msg.UserID.String(), msg.AccountID.String(), conversation.SessionID, msg.Message,
			core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: msg.ConversationID, Valid: true}),
			core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: msg.ID, Valid: true}),
			// The atomic queue claim already reserved IN_PROGRESS for this message.
			core.ConversationSessionRequestWithQueuedDispatch(),
			core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConfig(msgConfig),
			core.ConversationSessionRequestWithImages(images), core.ConversationSessionRequestWithChannelContext(channelContext))
		if err != nil {
			fail(err)
		}
	})
	if err != nil {
		fail(err)
	}
	return err
}

func handleCompletionApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	groupV2 := r.Group("/v1/completions")

	core.SetQueuedMessageDispatcher(func(ctx *security.RequestContext, _ core.NBAgent, conversation core.Conversation, msg core.ConversationMessage) {
		if err := dispatchQueuedMessage(ctx, conversation, msg); err != nil {
			slog.Error("chains: queued message dispatch failed", "error", err, "message_id", msg.ID)
		}
	})

	groupV2.POST("/chat", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_chat")
		timer := newAcceptTimer()
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		if request.Query == "" && request.Resolution == "" {
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

			// A follow-up cancel (request.Resolution) or resume-by-agentId is an
			// in-flight commitment shouldSkipResumeForTerminalConversation always
			// lets through — must not be blocked here just because the parent
			// conversation was swept to KILLED in the meantime.
			isFollowupCompletion := request.Resolution != "" || request.AgentId != ""
			if conversation.Status != core.ConversationStatusInProgress && rejectIfConversationLocked(c, "/v1/completions/chat", conversation, isFollowupCompletion) {
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
		timer.mark("prepare")
		// A closure, so the log uses logger as it is when the handler returns.
		defer func() { timer.log(logger, "/v1/completions/chat") }()
		ctx := context.WithoutCancel(c.Request.Context())
		// Optimization: Add a thread-safe resolution cache to the context to prevent redundant DB calls for LLM config
		ctx = context.WithValue(ctx, core.ContextKeyLLMResolution, core.NewLLMResolutionCache())

		agentContext, err := buildContextFromPayload(ctx, c, &actionRequest, tracer, meter, logger)
		timer.mark("security_context")
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

		// Check if user has access to account
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!granted(agentContext.GetSecurityContext(), request.AccountId, moduleAiMisc, "Read", "Write", "Execute") {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		// Resolve a pending follow-up without answering it (dismiss/terminate).
		// Handled before budget checks — cancelling doesn't consume LLM budget.
		if request.Resolution != "" {
			body, status, err := handleFollowupCancel(agentContext, request)
			if err != nil {
				c.JSON(status, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
			c.JSON(status, buildApiResponse(body, nil))
			return
		}

		// Check budget limits for tenant and account
		module := budget.ModuleUserInvestigation
		if strings.HasPrefix(request.SessionId, events.SessionIdPrefixEvent) {
			module = budget.ModuleInvestigation
		}

		timer.mark("session_and_access")
		budgetExceeded := budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, module, logger)
		timer.mark("budget")
		if budgetExceeded {
			return
		}

		// Queue only after identity, account access, and budget validation. Reload
		// here so session-based requests follow the same path as explicit IDs.
		if request.ConversationId != "" && request.AgentId == "" && request.Resolution == "" {
			if conversation, err := core.GetConversationDao().GetConversation(request.ConversationId); err == nil {
				if conversation.AccountID.String() != request.AccountId {
					c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
					return
				}
				if config.Config.QueuedMessagesEnabled && queueAdmits(conversation, &request) {
					if tryQueueConversationMessage(c, conversation, &request, "") {
						return
					}
				}
				if rejectIfConversationLocked(c, "chat", conversation, false) {
					return
				}
			}
		}

		// Queued dispatch needs the authenticated author to reload permissions.
		// Apply legacy system attribution only to immediate execution.
		if agentContext.GetSecurityContext().IsSuperAdmin() {
			request.UserId = security.GetSystemUserId()
		}

		if rejectUnfulfilledQueueRequest(c, &request) {
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
			resolvedAgent, dto, walked, err := core.ResolveAgentByConversationAgentId(agentContext, agentId.UUID, request.AccountId)
			if err != nil {
				logger.Error("api: error resolving agent for followup resume", "error", err, "agent_id", request.AgentId)
				c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: unable to complete request, Please try again later",
					},
				}))
				return
			}
			if dto == nil {
				logger.Error("api: agent not found", "agent_id", request.AgentId)
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: agent not found",
					},
				}))
				return
			}
			if resolvedAgent == nil {
				logger.Error("api: agent not found", "agent_name", dto.AgentName, "agent_id", dto.ID.String())
				c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
					common.Error{
						Message: "api: agent not found",
					},
				}))
				return
			}
			if walked > 0 {
				// Sub-agent constructed on-the-fly by a tool wrapper (delegate_agent's
				// dynamicReActAgent today) — its agent_name isn't registered, so we
				// resumed via a registered ancestor. See ResolveAgentByConversationAgentId
				// for the full rationale.
				logger.Info("api: resumed unregistered dynamic sub-agent via registered ancestor",
					"original_agent_name", dto.AgentName,
					"original_agent_id", dto.ID.String(),
					"ancestor_agent_name", resolvedAgent.GetName(),
					"walked_levels", walked)
			}
			agent = resolvedAgent
		}

		source := core.ConversationSourceUserInvestigation
		if request.Source != "" {
			source = request.Source
		}
		timer.mark("queue_check")
		// Handle user conversation question to add context to the original question
		if agent == nil {
			agent, err = agents.InferAgentOrHelp(agentContext, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithIsNewConversation(isNewConversation), core.ConversationSessionRequestWithConfig(request.Config))
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

		// Audit follow-up answers only, and only for web sources. Emit strictly
		// when a follow-up is actually pending (the agent is 'waiting') — a normal
		// send that merely carries agent_id/message_id has no pending follow-up and
		// must not be logged as an answer. Gating on web source also lets non-web
		// follow-ups skip the DB lookup entirely.
		if !isNewConversation && (request.AgentId != "" || request.MessageId != "") && isWebChatSource(source) {
			if info, perr := core.GetPendingFollowupInfo(request.AgentId, request.AccountId, request.ConversationId); perr != nil {
				logger.Warn("chat audit: failed to load pending followup info", "error", perr, "agent_id", request.AgentId)
			} else if info != nil {
				// Consistent shape across follow-up events: query = originating user
				// question, response = the user's answer, followup_type = the kind of
				// follow-up (so credential/config selections stay filterable).
				chatState := map[string]any{
					"query":         truncateQueryForAudit(info.Query),
					"response":      truncateQueryForAudit(request.Query),
					"followup_type": info.FollowupType,
				}
				chatEventType := audit.EventTypeChatClarificationRespond
				if info.IsToolConfirmation {
					chatEventType = audit.EventTypeChatToolConfirmationRespond
					chatState["tool_name"] = info.ToolName
					chatState["command"] = truncateQueryForAudit(info.Command)
					chatState["decision"] = toolConfirmationDecision(request.Query)
				}
				emitChatActionAudit(agentContext, chatEventType, audit.EventActionUpdate, request.AccountId, request.UserId, request.ConversationId, request.SessionId, source, nil, chatState)
			}
		}

		timer.mark("routing")
		// Execute the agent
		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_chat", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, agent, request.UserId, request.AccountId, request.SessionId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConversationId(conversationId), core.ConversationSessionRequestWithMessageId(messageId), core.ConversationSessionRequestWithAgentId(agentId), core.ConversationSessionRequestWithConfig(request.Config), core.ConversationSessionRequestWithClientTools(request.ClientTools), core.ConversationSessionRequestWithCapabilities(request.Capabilities), core.ConversationSessionRequestWithIsNewConversation(isNewConversation), core.ConversationSessionRequestWithImages(request.Images), core.ConversationSessionRequestWithChannelContext(request.ChannelContext), core.ConversationSessionRequestWithChannelContextRefs(request.ChannelContextRefs), core.ConversationSessionRequestWithReplyRef(request.ReplyRef))
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
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
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
			earlyAgentContext, earlyErr := buildContextFromPayload(earlyCtx, c, &actionRequest, tracer, meter, slog.Default())
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

			// See the matching comment in /v1/completions/chat: agentId-resume of a
			// pending followup must stay allowed on a KILLED conversation.
			isFollowupCompletion := request.AgentId != ""
			if conversation.Status != core.ConversationStatusInProgress && rejectIfConversationLocked(c, "/v1/completions/chat/auto", conversation, isFollowupCompletion) {
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
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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

		// Queue only after identity, account access, and budget validation. Reload
		// here so session-based requests follow the same path as explicit IDs.
		if request.ConversationId != "" && request.AgentId == "" && request.Resolution == "" {
			if conversation, err := core.GetConversationDao().GetConversation(request.ConversationId); err == nil {
				if conversation.AccountID.String() != request.AccountId {
					c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
					return
				}
				if config.Config.QueuedMessagesEnabled && queueAdmits(conversation, &request) {
					if tryQueueConversationMessage(c, conversation, &request, "") {
						return
					}
				}
				if rejectIfConversationLocked(c, "chat", conversation, false) {
					return
				}
			}
		}

		if rejectUnfulfilledQueueRequest(c, &request) {
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
			agent, err = agents.InferAgentOrHelp(agentContext, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source), core.ConversationSessionRequestWithConfig(request.Config))
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
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

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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

		// Check if user has access to account — honor a dynamic-RBAC
		// ai_conversations:Read grant in addition to built-in account roles
		// (ai_list_conversation_suggestions classifies to ai_conversations).
		if !agentContext.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_conversations") {
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

		// Suggestions already stored on the message cost no LLM call, so serve them
		// before the budget check — a tenant-month spend aggregate (~0.8 s) that
		// every repeat request used to pay for nothing.
		if stored, ok := core.StoredConversationSuggestions(agentContext, request, message); ok {
			c.JSON(http.StatusOK, buildApiResponse(stored, nil))
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!grantedRun(agentContext.GetSecurityContext(), request.AccountId, moduleAiMisc) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		if request.ConversationId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: conversation_id is required to stop the investigation",
				},
			}))
			return
		}

		// TerminateConversation writes to a UUID column — a malformed id would
		// otherwise reach Postgres and surface as an unhandled 500 with a raw
		// `invalid input syntax for type uuid` message. Validate at the RPC
		// boundary so the caller gets a 400 with a clean reason, matching the
		// pattern used elsewhere in this file (e.g. /chat, /chat_suggestions).
		if _, err := uuid.Parse(request.ConversationId); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: invalid conversation_id",
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

	// metricsQueryHandler generates ONE provider-native metrics query from a natural
	// language question — Elasticsearch DSL, a Datadog query or PromQL, whichever the
	// account's metrics backend speaks. It does not execute it.
	metricsQueryHandler := func(c *gin.Context) {
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
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
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!grantedRun(agentContext.GetSecurityContext(), request.AccountId, moduleAiGeneration) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		source := core.ConversationSourcePrometheusQuery
		if request.Source != "" {
			source = request.Source
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))

		// Provider-aware query generation: the account's metrics backend decides the
		// query language, so an ES account gets ES DSL rather than the PromQL this
		// handler used to return unconditionally. Resolved before the budget check so
		// an unsupported provider costs the tenant nothing.
		metricsQueryChain, err := agents.NewMetricsQueryAgent(agentContext, request.AccountId, request.Index)
		if err != nil {
			logger.Error("api: unable to build metrics query agent", "error", err.Error())
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}

		// Check budget limits for tenant and account
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), request.AccountId, budget.ModuleUserInvestigation, logger) {
			return
		}

		if request.SessionId == "" {
			request.SessionId = request.ConversationId
		}

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_prometheus_query", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, metricsQueryChain, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source))
		}, core.NBAgentResponse{
			Response:       []string{},
			Query:          request.Query,
			ConversationId: request.ConversationId,
			SessionId:      request.SessionId,
			Status:         core.ConversationStatusInProgress,
		})
	}

	groupV2.POST("/metrics-query", metricsQueryHandler)

	// Deprecated alias for /metrics-query, kept because the app and llm-server deploy
	// independently: during a rollout an app still on the old build calls this path,
	// and removing it would 404 every AI-tab request until both services land. Delete
	// once no deployed app references it.
	groupV2.POST("/prometheus-query", metricsQueryHandler)

	groupV2.POST("/log-query", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_log_query")
		var request ConversationApiRequest
		var actionRequest ActionRequest
		err := c.ShouldBindJSON(&actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
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
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!grantedRun(agentContext.GetSecurityContext(), request.AccountId, moduleAiGeneration) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		logger.Info("api: processing request", "request", slog.AnyValue(request))

		source := core.ConversationSourceLogQuery
		if request.Source != "" {
			source = request.Source
		}

		var logQueryChain = agents.NewLogQueryAgent(request.AccountId, request.LogProvider, request.Index)
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

		handleRequestExecution(c, agentContext, request.Async, request.UserId, "chains_log_query", logger, func(ctx *security.RequestContext) (core.NBAgentResponse, error) {
			return core.HandleConversationSessionRequest(ctx, logQueryChain, request.UserId, request.AccountId, request.ConversationId, request.Query, core.ConversationSessionRequestWithSource(source))
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
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

			// No followup-completion exemption here: this endpoint's agentId feeds
			// ConversationSessionRequestWithAgentId, not a followup-resume lookup.
			if rejectIfConversationLocked(c, "/v1/completions/workflow-generate", conversation, false) {
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

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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
		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!grantedRun(agentContext.GetSecurityContext(), request.AccountId, moduleAiGeneration) {
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "conversation_id", request.ConversationId, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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

	// usage-metrics backs the AI Cost Analyser Overview + Models screens: KPI
	// totals plus one cost breakdown per requested group_by dimension
	// (model/provider/source/agent/status/user/account), all off one filter.
	// Pass several dimensions for the whole Overview in one call, or one for a
	// single cut, or none for the KPI cards only.
	groupV2.POST("/usage-metrics", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_usage_metrics")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.UsageMetricsRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		logger := slog.With("user_id", request.UserId, "group_by", request.GroupBy)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		data, err := core.HandleUsageMetricsApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// ai-account-report backs the Cost Analyser dashboard's Accounts tab: the
	// consolidated daily/MTD/prev-month cost report, one row per account. Same
	// computation (core.GetAiCostAccountReport) as the Slack daily digest and
	// the Nubi cost-report agent, so all three surfaces reconcile.
	groupV2.POST("/ai-account-report", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_ai_account_report")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.AiCostAccountReportRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		logger := slog.With("account_ids", request.AccountIds, "reference_date", request.ReferenceDate)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.ReferenceDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: reference_date is required"},
			}))
			return
		}

		data, err := core.HandleAiCostAccountReportApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// usage-filters returns the option-sets that populate the Cost Analyser
	// filter bar (distinct sources/models/providers/agents/statuses/users in
	// the window + the caller's accessible accounts).
	groupV2.POST("/usage-filters", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_usage_filters")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.UsageFiltersRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		data, err := core.HandleUsageFiltersApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// usage-conversations is the Cost Analyser explorer: a filtered, sorted,
	// paginated list of conversations with per-conversation cost/model/token
	// rollups plus filter-wide KPI totals for the header.
	groupV2.POST("/usage-conversations", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_usage_conversations")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.ListConversationCostsRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		logger := slog.With("user_id", request.UserId, "sort_by", request.SortBy)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		data, err := core.HandleListConversationCostsApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// conversation-tree is the Cost Analyser detailed drill-down: the full
	// recursive tree (messages -> agents -> tool calls + model/API calls) of one
	// conversation, with cost/tokens/latency on every node.
	groupV2.POST("/conversation-tree", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_conversation_tree")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.ConversationTreeRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.ConversationId == "" || request.AccountId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: conversation_id and account_id are required"},
			}))
			return
		}

		data, err := core.HandleConversationTreeApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// conversation-agent is the on-click drill-down for ONE agent in the Cost
	// Analyser tree: its execution content (query/thought/response), every tool
	// call (params + response + status) and every model call (tokens + cost
	// breakdown + error/success).
	groupV2.POST("/conversation-agent", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_conversation_agent")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.ConversationAgentDetailRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountId, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if request.ConversationId == "" || request.AccountId == "" || request.AgentId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: conversation_id, account_id and agent_id are required"},
			}))
			return
		}

		data, err := core.HandleConversationAgentDetailApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// event-analysis-digests backs the weekly digest tab: the account's digest
	// history (list) and one week's full briefing including per-class findings.
	groupV2.POST("/event-analysis-digests", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_event_analysis_digests_list")
		var request ListEventAnalysisDigestsRequest
		reqCtx, ok := bindDigestRequest(c, tracer, meter, &request)
		if !ok {
			return
		}
		data, err := HandleListEventAnalysisDigestsApi(reqCtx, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	groupV2.POST("/event-analysis-digest", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_event_analysis_digest_get")
		var request GetEventAnalysisDigestRequest
		reqCtx, ok := bindDigestRequest(c, tracer, meter, &request)
		if !ok {
			return
		}
		data, err := HandleGetEventAnalysisDigestApi(reqCtx, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	groupV2.POST("/event-analysis-digest-generate", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_event_analysis_digest_generate")
		var request GenerateEventAnalysisDigestRequest
		reqCtx, ok := bindDigestRequest(c, tracer, meter, &request)
		if !ok {
			return
		}
		data, err := HandleGenerateEventAnalysisDigestApi(reqCtx, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// usage-agents is the Cost Analyser "Agents" leaderboard: top agent invocations
	// across conversations ranked by cost / latency / errors, each linked to its
	// conversation for cross-navigation.
	groupV2.POST("/usage-agents", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_usage_agents")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.ListAgentCallsRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		// Reject empty dates with a clean 400 (parity with the sibling handlers);
		// otherwise time.Parse("") inside the handler surfaces as a 500.
		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountIds, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		data, err := core.HandleListAgentCostsApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// usage-tools is the Cost Analyser "Tools" leaderboard: a per-tool rollup
	// (calls / reliability / latency / reach) across conversations from
	// llm_conversation_tool_calls, with downstream LLM cost attributed to
	// sub-agent-spawn tools.
	groupV2.POST("/usage-tools", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_usage_tools")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.ListToolUsageRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		// Reject empty dates with a clean 400 (parity with the sibling handlers).
		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountIds, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		data, err := core.HandleListToolUsageApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// tool-calls is the Tools-tab drill-in: the recent invocations of one tool
	// (optionally restricted to failure / in-progress statuses), each linked to its
	// conversation for a deep dive.
	groupV2.POST("/tool-calls", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_tool_calls")
		requestMap := make(map[string]any)
		err := c.ShouldBindJSON(&requestMap)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.ListToolCallsRequest
		var actionRequest ActionRequest
		if err = common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err = common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}
		if request.ToolName == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: tool_name is required"},
			}))
			return
		}

		logger := slog.With("account_id", request.AccountIds, "user_id", request.UserId)

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		data, err := core.HandleListToolCallsApi(agentContext, request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	// Internal cross-tenant Critique Analytics routes. No account scoping —
	// gated by requireTenantReadAdmin. A tenant_admin sees every tenant's data.

	// Like requireTenantAdmin but also accepts tenant_admin_readonly. Kept
	// separate — requireTenantAdmin's other call sites gate writes.
	requireTenantReadAdmin := func(c *gin.Context, ctx *security.RequestContext) bool {
		sc := ctx.GetSecurityContext()
		if sc == nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{errors.New("critiques: missing security context")}))
			return false
		}
		if sc.IsSuperAdmin() || sc.IsSuperAdminReadonly() || sc.IsTenantAdmin() || sc.IsTenantReadAdmin() {
			return true
		}
		c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New("critiques: tenant admin role required")}))
		return false
	}
	groupV2.POST("/critiques-summary", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_critiques_summary")
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.CritiqueSummaryRequest
		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}
		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !requireTenantReadAdmin(c, agentContext) {
			return
		}

		data, err := core.HandleCritiqueSummaryApi(request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	groupV2.POST("/critiques-trend", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_critiques_trend")
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.CritiqueTrendRequest
		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}
		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !requireTenantReadAdmin(c, agentContext) {
			return
		}

		data, err := core.HandleCritiqueTrendApi(request)
		if err != nil {
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{
				common.Error{Message: err.Error()},
			}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(data, nil))
	})

	groupV2.POST("/critiques-list", func(c *gin.Context) {
		common.MetricsApiRequestsTotal("chains_critiques_list")
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		var request core.CritiqueListRequest
		var actionRequest ActionRequest
		if err := common.DecodeMapToStruct(requestMap, &actionRequest); err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionRequestPayload, &request); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: " + err.Error()},
			}))
			return
		}
		if request.StartDate == "" || request.EndDate == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{Message: "api: start_date and end_date are required"},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !requireTenantReadAdmin(c, agentContext) {
			return
		}

		data, err := core.HandleCritiqueListApi(request)
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

		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
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
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!granted(agentContext.GetSecurityContext(), request.AccountId, moduleAiConversations, "Read", "Write") {
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}
		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		logger := slog.Default()

		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}

		if !agentContext.GetSecurityContext().HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
			!granted(agentContext.GetSecurityContext(), request.AccountId, moduleAiConversations, "Read", "Write") {
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
		var actionRequest ActionRequest
		err = common.DecodeMapToStruct(requestMap, &actionRequest)
		if err != nil {
			slog.Error(errorBindingMessage, "error", err)
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{
				common.Error{
					Message: "api: " + err.Error(),
				},
			}))
			return
		}

		actionRequestPayload := actionRequest.Input
		if reqVal, ok := actionRequestPayload["request"].(map[string]any); ok {
			actionRequestPayload = reqVal
		}
		if actionRequestPayload == nil {
			actionRequestPayload = requestMap
		}
		err = common.DecodeMapToStruct(actionRequestPayload, &request)
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
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
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

	// Queued Message Management
	groupV2.POST("/queued_message_delete", func(c *gin.Context) {
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		var req QueuedMessageDeleteRequest
		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)
		actionPayload := actionRequest.Input
		if reqVal, ok := actionPayload["request"].(map[string]any); ok {
			actionPayload = reqVal
		}
		if actionPayload == nil {
			actionPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionPayload, &req); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		if req.MessageId == "" || req.AccountId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: account_id and message_id are required"}}))
			return
		}
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !agentContext.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) &&
			!grantedWrite(agentContext.GetSecurityContext(), req.AccountId, moduleAiConversations) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}
		isAdmin := isSecurityContextAdmin(agentContext.GetSecurityContext(), req.AccountId)
		userId := agentContext.GetSecurityContext().GetUserId()
		if err := core.GetConversationDao().DeleteQueuedMessage(req.MessageId, req.AccountId, userId, isAdmin); err != nil {
			if errors.Is(err, core.ErrQueuedMessageNotFoundOrLocked) {
				c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "deleted", "id": req.MessageId}, nil))
	})

	groupV2.DELETE("/queued_message/:id", func(c *gin.Context) {
		messageId := c.Param("id")
		accountId := c.Query("account_id")

		if messageId == "" || accountId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: account_id and id are required"}}))
			return
		}
		var actionRequest ActionRequest
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !agentContext.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) &&
			!grantedWrite(agentContext.GetSecurityContext(), accountId, moduleAiConversations) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}
		isAdmin := isSecurityContextAdmin(agentContext.GetSecurityContext(), accountId)
		userId := agentContext.GetSecurityContext().GetUserId()
		if err := core.GetConversationDao().DeleteQueuedMessage(messageId, accountId, userId, isAdmin); err != nil {
			if errors.Is(err, core.ErrQueuedMessageNotFoundOrLocked) {
				c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "deleted", "id": messageId}, nil))
	})

	groupV2.POST("/queued_message_update", func(c *gin.Context) {
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		var req QueuedMessageUpdateRequest
		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)
		actionPayload := actionRequest.Input
		if reqVal, ok := actionPayload["request"].(map[string]any); ok {
			actionPayload = reqVal
		}
		if actionPayload == nil {
			actionPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionPayload, &req); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		if req.MessageId == "" || req.AccountId == "" || strings.TrimSpace(req.Message) == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: account_id, message_id, and non-empty message are required"}}))
			return
		}
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !agentContext.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) &&
			!grantedWrite(agentContext.GetSecurityContext(), req.AccountId, moduleAiConversations) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}
		isAdmin := isSecurityContextAdmin(agentContext.GetSecurityContext(), req.AccountId)
		userId := agentContext.GetSecurityContext().GetUserId()
		if err := core.GetConversationDao().UpdateQueuedMessage(req.MessageId, req.AccountId, userId, strings.TrimSpace(req.Message), isAdmin); err != nil {
			if errors.Is(err, core.ErrQueuedMessageNotFoundOrLocked) {
				c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "updated", "id": req.MessageId, "message": strings.TrimSpace(req.Message)}, nil))
	})

	groupV2.PATCH("/queued_message/:id", func(c *gin.Context) {
		messageId := c.Param("id")
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		var req QueuedMessageUpdateRequest
		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)
		actionPayload := actionRequest.Input
		if reqVal, ok := actionPayload["request"].(map[string]any); ok {
			actionPayload = reqVal
		}
		if actionPayload == nil {
			actionPayload = requestMap
		}
		_ = common.DecodeMapToStruct(actionPayload, &req)
		if req.MessageId == "" {
			req.MessageId = messageId
		}
		if req.AccountId == "" {
			req.AccountId = c.Query("account_id")
		}
		if req.MessageId == "" || req.AccountId == "" || strings.TrimSpace(req.Message) == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: account_id, message_id, and non-empty message are required"}}))
			return
		}
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !agentContext.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) &&
			!grantedWrite(agentContext.GetSecurityContext(), req.AccountId, moduleAiConversations) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}
		isAdmin := isSecurityContextAdmin(agentContext.GetSecurityContext(), req.AccountId)
		userId := agentContext.GetSecurityContext().GetUserId()
		if err := core.GetConversationDao().UpdateQueuedMessage(req.MessageId, req.AccountId, userId, strings.TrimSpace(req.Message), isAdmin); err != nil {
			if errors.Is(err, core.ErrQueuedMessageNotFoundOrLocked) {
				c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
			c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
			return
		}
		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "updated", "id": req.MessageId, "message": strings.TrimSpace(req.Message)}, nil))
	})

	groupV2.POST("/queued_message_force", func(c *gin.Context) {
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		var req QueuedMessageForceRequest
		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)
		actionPayload := actionRequest.Input
		if reqVal, ok := actionPayload["request"].(map[string]any); ok {
			actionPayload = reqVal
		}
		if actionPayload == nil {
			actionPayload = requestMap
		}
		if err := common.DecodeMapToStruct(actionPayload, &req); err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: " + err.Error()}}))
			return
		}
		if req.MessageId == "" || req.AccountId == "" || req.ConversationId == "" {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{common.Error{Message: "api: account_id, conversation_id, and message_id are required"}}))
			return
		}
		agentContext, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		if err != nil {
			c.JSON(http.StatusUnauthorized, buildApiResponse(nil, []error{err}))
			return
		}
		if !agentContext.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) &&
			!grantedRun(agentContext.GetSecurityContext(), req.AccountId, moduleAiMisc) {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}

		conversation, err := core.GetConversationDao().GetConversation(req.ConversationId)
		if err != nil {
			c.JSON(http.StatusNotFound, buildApiResponse(nil, []error{common.Error{Message: "api: conversation not found"}}))
			return
		}

		if conversation.AccountID.String() != req.AccountId {
			c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
			return
		}
		if budget.CheckBudgetAndRespond(c, agentContext.GetSecurityContext().GetTenantId(), req.AccountId, budget.ModuleUserInvestigation, slog.Default()) {
			return
		}
		// Validation, interruption and claim are one transaction. An invalid or
		// already-dispatched target must not terminate the current investigation.
		claimedMsg, err := core.GetConversationDao().ClaimQueuedMessageById(agentContext, req.AccountId, req.ConversationId, req.MessageId,
			agentContext.GetSecurityContext().GetUserId(), isSecurityContextAdmin(agentContext.GetSecurityContext(), req.AccountId))
		if err != nil || claimedMsg == nil {
			c.JSON(http.StatusConflict, buildApiResponse(nil, []error{common.Error{Message: "api: queued message unavailable or permission denied"}}))
			return
		}
		if err := dispatchQueuedMessage(agentContext, conversation, *claimedMsg); err != nil {
			c.JSON(http.StatusServiceUnavailable, buildApiResponse(nil, []error{common.Error{Message: "api: unable to dispatch queued message"}}))
			return
		}

		c.JSON(http.StatusOK, buildApiResponse(map[string]any{"status": "executing", "message_id": req.MessageId}, nil))
	})
}
