package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"
	"slices"
	"strings"
	"sync"
	"time"

	toolcore "nudgebee/llm/tools/core"

	"github.com/google/shlex"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/tmc/langchaingo/llms"
)

var emptyQueryClarifications = []string{
	"Hey! Looks like your message came through empty. What would you like help with?",
	"I'm all ears, but I'll need a bit more to go on. What's on your mind?",
	"Hmm, I didn't catch a question there. Could you share a few more details?",
	"Ready when you are! Send over what you'd like me to dig into.",
	"Looks like your question got lost in transit. Mind trying again with some details?",
	"I'd love to help, but I need a bit more context. What's going on?",
	"You rang? Just let me know what you'd like me to look into.",
}

// finalizeOnPanicDBBudget caps how long the panic-recovery defer in
// handleConversationRequest waits for DB cleanup (message + tool-calls + conversation
// status flips to FAILED). Beyond this, the defer propagates the panic without
// waiting so a hung DB can't turn a panic into a permanent worker-goroutine zombie.
// 5s is generous for three point updates on indexed columns; if we ever see this
// budget tripping in production, the DB is the real problem to fix.
const finalizeOnPanicDBBudget = 5 * time.Second

var conversationAsyncTaskWorkerPool *common.WorkerPool

func init() {
	conversationAsyncTaskWorkerPool = common.NewWorkerPool("conversation_async_tasks", config.Config.ConversationTaskWorkerCount, 100)
}

// generateConversationTitle uses an LLM to generate a concise title for a conversation based on the initial query.
func generateConversationTitle(ctx *security.RequestContext, accountId string, conversationId string, messageId string, query string, userId string) (string, error) {
	titlePrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptTitleGeneration, accountId)
	if err != nil {
		return "", fmt.Errorf("generateConversationTitle: loading prompt: %w", err)
	}
	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, titlePrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, query),
	}

	// Use Lite model for title generation
	titleCtx := security.NewRequestContext(
		context.WithValue(context.WithValue(ctx.GetContext(), ContextKeyModelTier, ModelTierSummary), ContextKeyCacheScope, CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := GenerateAndTrackLLMContent(titleCtx, userId, accountId, conversationId, messageId, "summary_agent", false, messageContent, true, WithThinkingLevel(ThinkingLevelFastTask))
	if err != nil {
		return "", fmt.Errorf("llm call for title generation failed: %w", err)
	}

	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return "", errors.New("llm generated an empty or invalid title")
	}

	generatedTitle := strings.Trim(completion.Choices[0].Content, "\"")
	return generatedTitle, nil
}

// generateConversationTitleAsync generates a title for a conversation using the worker pool
func generateConversationTitleAsync(ctx *security.RequestContext, conversationId, messageId, accountId, agentName, query, userId string) {
	// Strip leading "@agent_name" so it doesn't end up in the title (short-query
	// fast path) or confuse the title-generation LLM. The executor strips this
	// later for the actual agent run, but title generation runs before that.
	query = common.StripLeadingAgentMention(query)

	// Optimization: If query is short, use it directly as title and skip LLM
	wordCount := common.GetWordCount(query)
	title := ""
	if wordCount > 0 && wordCount <= common.ShortQueryWordCountThreshold {
		title = query
		if len(title) > 100 {
			title = title[:97] + "..."
		}
	}

	if agentName != "" && agentName == ToolLlm {
		title = "Processing LLM Request"
	}

	if title != "" {
		err := GetConversationDao().UpdateConversationTitle(conversationId, title)
		if err == nil {
			ctx.GetLogger().Info("conversation: using query as title (short query optimization)", "conversation_id", conversationId, "title", title)
			return
		}
	}

	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		title, err := generateConversationTitle(bgCtx, accountId, conversationId, messageId, query, userId)
		if err != nil {
			bgCtx.GetLogger().Warn("conversation: unable to generate title via LLM in background task", "conversation_id", conversationId, "query", query, "error", err)
			bgCtx.GetLogger().Info("conversation: title generation task completed with error")
			return
		}
		bgCtx.GetLogger().Info("conversation: generated title via LLM in background task", "conversation_id", conversationId, "query", query, "title", title)
		err = GetConversationDao().UpdateConversationTitle(conversationId, title)
		if err != nil {
			bgCtx.GetLogger().Error("conversation: unable to update conversation title in background task", "conversation_id", conversationId, "error", err)
			bgCtx.GetLogger().Info("conversation: title update task completed with error")
			return
		}
		bgCtx.GetLogger().Info("conversation: title update task completed successfully")
	})
	if err != nil {
		bgCtx.GetLogger().Error("conversation: failed to submit title generation task", "error", err)
	}
}

// processAcknowledgmentAsync handles creating and saving an acknowledgment using the worker pool
func processAcknowledgmentAsync(ctx *security.RequestContext, accountId, query, agentName, conversationId, messageId, userId string, source ConversationSource) {
	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		ackResp := CreateAcknowledgmentResponse(
			bgCtx,
			userId,
			accountId,
			query,
			agentName,
			conversationId,
			messageId,
			source,
		)
		err := GetConversationDao().UpdateMessageAcknowledgement(messageId, accountId, ackResp.Acknowledgment)
		if err != nil {
			bgCtx.GetLogger().Error("conversation: unable to save acknowledgment to DB asynchronously", "conversation_id", conversationId, "error", err)
			bgCtx.GetLogger().Info("conversation: acknowledgment task completed with error")
			return
		}
		bgCtx.GetLogger().Info("conversation: acknowledgment task completed successfully", "conversation_id", conversationId)
	})
	if err != nil {
		bgCtx.GetLogger().Error("conversation: failed to submit acknowledgment task", "error", err)
	}
}

// updateConversationContextAsync extracts and updates conversation context using the worker pool
func updateConversationContextAsync(ctx *security.RequestContext, request NBAgentRequest, agentResponseContent string, conversationId string, existingContext map[string]any, agentName string) {
	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()
	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		updatedContext, err := GetContextManager().ExtractContextFromExchange(bgCtx, request, agentResponseContent, existingContext, agentName)
		if err != nil {
			bgCtx.GetLogger().Error("conversation: failed to extract context asynchronously", "error", err)
			bgCtx.GetLogger().Info("conversation: context extraction task completed with error")
			return
		}

		err = GetConversationDao().UpdateConversationContext(conversationId, updatedContext)
		if err != nil {
			bgCtx.GetLogger().Error("conversation: failed to update context asynchronously", "error", err)

			return
		}

		bgCtx.GetLogger().Info("conversation: context update task completed successfully")
	})
	if err != nil {
		bgCtx.GetLogger().Error("conversation: failed to submit context update task", "error", err)
	}
}

// computeProductivityMetricsAsync computes and saves productivity metrics for a specific message using the worker pool
func computeProductivityMetricsAsync(ctx *security.RequestContext, userId, accountId, conversationId, messageId string) {
	if !config.Config.ProductivityMetricsEnabled {
		return
	}

	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()

	convId, err := uuid.Parse(conversationId)
	if err != nil {
		bgCtx.GetLogger().Error("productivity: invalid conversation id", "error", err, "conversation_id", conversationId)
		return
	}

	msgId, err := uuid.Parse(messageId)
	if err != nil {
		bgCtx.GetLogger().Error("productivity: invalid message id", "error", err, "message_id", messageId)
		return
	}

	err = conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		err := ComputeAndSaveMessageProductivityMetrics(bgCtx, userId, accountId, convId, msgId)
		if err != nil {
			bgCtx.GetLogger().Error("productivity: failed to compute message metrics asynchronously", "message_id", messageId, "error", err)
			return
		}
		bgCtx.GetLogger().Info("productivity: message metrics task completed successfully", "message_id", messageId)
	})
	if err != nil {
		bgCtx.GetLogger().Error("productivity: failed to submit message metrics task", "error", err)
	}
}

// generateCompactResponse generates a compact version of the response optimized for Slack using an LLM.
func generateCompactResponse(ctx *security.RequestContext, accountId, conversationId, messageId, userId, query, response string) (string, error) {
	systemPrompt, err := prompts.GetPromptStrict(ctx.GetContext(), prompts.PromptResponseFormatterSlack, accountId)
	if err != nil {
		return "", fmt.Errorf("generateCompactResponse: loading slack formatter prompt: %w", err)
	}

	userPrompt := fmt.Sprintf(`
**Question** = %s

**Answer** = %s
`,
		query,
		response,
	)

	messageContent := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, userPrompt),
	}

	completion, err := GenerateAndTrackLLMContent(ctx, userId, accountId, conversationId, messageId, "compact_response_agent", true, messageContent, false)
	if err != nil {
		return "", fmt.Errorf("llm call for compact response generation failed: %w", err)
	}

	if completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "" {
		return "", errors.New("llm generated an empty or invalid compact response")
	}

	return completion.Choices[0].Content, nil
}

// saveCompactResponseAsync saves the compact response to the database asynchronously
func saveCompactResponseAsync(ctx *security.RequestContext, conversationId, messageId, compactResponse string) {
	bgCtx := security.NewRequestContext(
		context.Background(),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
	submissionCtx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Config.AsyncOperationTimeoutSeconds)*time.Second)
	defer cancel()

	err := conversationAsyncTaskWorkerPool.Submit(submissionCtx, func() {
		if err := GetConversationDao().UpdateMessageCompactResponse(bgCtx, messageId, compactResponse); err != nil {
			bgCtx.GetLogger().Error("conversation: unable to save compact response to DB", "conversation_id", conversationId, "message_id", messageId, "error", err)
			return
		}
		bgCtx.GetLogger().Info("conversation: compact response saved to DB", "conversation_id", conversationId, "message_id", messageId)
	})
	if err != nil {
		bgCtx.GetLogger().Error("conversation: failed to submit save compact response task", "error", err)
	}
}

const (
	RouterAgentName          = "router"
	DefaultConversationTitle = "New Conversation"
)

type additionalConversationSessionRequestConfig struct {
	source                ConversationSource
	config                toolcore.NBQueryConfig
	queryContext          string
	conversationId        uuid.NullUUID
	messageId             uuid.NullUUID
	agentId               uuid.NullUUID
	enableQueryRefinement *bool
	systemPrompt          string
	enableCritique        *bool
	clientTools           []toolcore.NBToolCommand
	capabilities          toolcore.AgentCapabilities
	previousState         string
	isNewConversation     bool
	isResume              bool
	images                []ImageAttachment
	channelContext        string
	channelContextRefs    map[string]any
}

type ConversationSessionRequestConfig interface {
	apply(config *additionalConversationSessionRequestConfig)
}

type sessionRequestWithIsNewConversation struct {
	isNewConversation bool
}

func (h sessionRequestWithIsNewConversation) apply(c *additionalConversationSessionRequestConfig) {
	c.isNewConversation = h.isNewConversation
}

func ConversationSessionRequestWithIsNewConversation(isNew bool) ConversationSessionRequestConfig {
	return sessionRequestWithIsNewConversation{
		isNewConversation: isNew,
	}
}

func IsNewConversationRequest(configs ...ConversationSessionRequestConfig) bool {
	cfg := additionalConversationSessionRequestConfig{}
	for _, c := range configs {
		c.apply(&cfg)
	}
	return cfg.isNewConversation
}

// QueryConfigFromRequests extracts the NBQueryConfig from a list of
// ConversationSessionRequestConfig options. Mirrors IsNewConversationRequest —
// used by callers (e.g. the router) that need to see per-request signals
// (k8s_orchestrator_mode, tool_configs, capabilities…) before the agent is
// picked, not only after HandleConversationSessionRequest carries the config
// to the executor.
func QueryConfigFromRequests(configs ...ConversationSessionRequestConfig) toolcore.NBQueryConfig {
	cfg := additionalConversationSessionRequestConfig{}
	for _, c := range configs {
		c.apply(&cfg)
	}
	return cfg.config
}

type sessionRequestWithClientTools struct {
	clientTools []toolcore.NBToolCommand
}

func (h sessionRequestWithClientTools) apply(c *additionalConversationSessionRequestConfig) {
	c.clientTools = h.clientTools
}

func ConversationSessionRequestWithClientTools(clientTools []toolcore.NBToolCommand) ConversationSessionRequestConfig {
	return sessionRequestWithClientTools{
		clientTools: clientTools,
	}
}

type sessionRequestWithCapabilities struct {
	capabilities toolcore.AgentCapabilities
}

func (h sessionRequestWithCapabilities) apply(c *additionalConversationSessionRequestConfig) {
	c.capabilities = h.capabilities
}

func ConversationSessionRequestWithCapabilities(capabilities toolcore.AgentCapabilities) ConversationSessionRequestConfig {
	return sessionRequestWithCapabilities{
		capabilities: capabilities,
	}
}

type sessionRequestWithSource struct {
	source ConversationSource
}

func (h sessionRequestWithSource) apply(c *additionalConversationSessionRequestConfig) {
	c.source = h.source
}
func ConversationSessionRequestWithSource(source ConversationSource) ConversationSessionRequestConfig {
	return sessionRequestWithSource{
		source: source,
	}
}

type sessionRequestWithQueryContext struct {
	queryContext string
}

func (h sessionRequestWithQueryContext) apply(c *additionalConversationSessionRequestConfig) {
	c.queryContext = h.queryContext
}
func ConversationSessionRequestWithQueryContext(queryContext string) ConversationSessionRequestConfig {
	return sessionRequestWithQueryContext{
		queryContext: queryContext,
	}
}

type sessionRequestWithConfig struct {
	config toolcore.NBQueryConfig
}

func (h sessionRequestWithConfig) apply(c *additionalConversationSessionRequestConfig) {
	c.config = h.config
}
func ConversationSessionRequestWithConfig(config toolcore.NBQueryConfig) ConversationSessionRequestConfig {
	return sessionRequestWithConfig{
		config: config,
	}
}

type sessionRequestWithAdditionalSystemPrompt struct {
	prompt string
}

func (h sessionRequestWithAdditionalSystemPrompt) apply(c *additionalConversationSessionRequestConfig) {
	c.systemPrompt = h.prompt
}
func ConversationSessionRequestWitAdditionalSystemPrompt(config string) ConversationSessionRequestConfig {
	return sessionRequestWithAdditionalSystemPrompt{
		prompt: config,
	}
}

type sessionRequestWithConversationId struct {
	conversationId uuid.NullUUID
}

func (h sessionRequestWithConversationId) apply(c *additionalConversationSessionRequestConfig) {
	c.conversationId = h.conversationId
}
func ConversationSessionRequestWithConversationId(conversationId uuid.NullUUID) ConversationSessionRequestConfig {
	if conversationId.UUID == uuid.Nil {
		return sessionRequestWithConversationId{}
	}
	return sessionRequestWithConversationId{
		conversationId: conversationId,
	}
}

type sessionRequestWithPreviousState struct {
	previousState string
}

func (h sessionRequestWithPreviousState) apply(c *additionalConversationSessionRequestConfig) {
	c.previousState = h.previousState
}

func ConversationSessionRequestWithPreviousState(state string) ConversationSessionRequestConfig {
	return sessionRequestWithPreviousState{
		previousState: state,
	}
}

type sessionRequestWithMessageId struct {
	messageId uuid.NullUUID
}

func (h sessionRequestWithMessageId) apply(c *additionalConversationSessionRequestConfig) {
	c.messageId = h.messageId
}
func ConversationSessionRequestWithMessageId(messageId uuid.NullUUID) ConversationSessionRequestConfig {
	if messageId.UUID == uuid.Nil {
		return sessionRequestWithMessageId{}
	}
	return sessionRequestWithMessageId{
		messageId: messageId,
	}
}

type sessionRequestWithAgentId struct {
	agentId uuid.NullUUID
}

func (h sessionRequestWithAgentId) apply(c *additionalConversationSessionRequestConfig) {
	c.agentId = h.agentId
}
func ConversationSessionRequestWithAgentId(agentId uuid.NullUUID) ConversationSessionRequestConfig {
	if agentId.UUID == uuid.Nil {
		return sessionRequestWithAgentId{}
	}
	return sessionRequestWithAgentId{
		agentId: agentId,
	}
}

type sessionRequestWithCritique struct {
	enableCritique bool
}

func (h sessionRequestWithCritique) apply(c *additionalConversationSessionRequestConfig) {
	c.enableCritique = &h.enableCritique
}
func ConversationSessionRequestWithEnableCritique(enableCritique bool) ConversationSessionRequestConfig {
	return sessionRequestWithCritique{
		enableCritique: enableCritique,
	}
}

type sessionRequestWithEnableQueryRefinement struct {
	enableQueryRefinement bool
}

func (h sessionRequestWithEnableQueryRefinement) apply(c *additionalConversationSessionRequestConfig) {
	c.enableQueryRefinement = &h.enableQueryRefinement
}
func ConversationSessionRequestWithEnableQueryRefinement(enableQueryRefinement bool) ConversationSessionRequestConfig {
	return sessionRequestWithEnableQueryRefinement{
		enableQueryRefinement: enableQueryRefinement,
	}
}

type sessionRequestWithIsResume struct {
	isResume bool
}

func (h sessionRequestWithIsResume) apply(c *additionalConversationSessionRequestConfig) {
	c.isResume = h.isResume
}

// ConversationSessionRequestWithIsResume marks the request as a resume of an
// already-active conversation (client-tool-result, dead-worker recovery).
// Causes handleConversationRequest to skip its IN_PROGRESS guard since the
// resume worker has legitimately taken over an active conversation.
func ConversationSessionRequestWithIsResume(isResume bool) ConversationSessionRequestConfig {
	return sessionRequestWithIsResume{
		isResume: isResume,
	}
}

type sessionRequestWithImages struct {
	images []ImageAttachment
}

func (h sessionRequestWithImages) apply(c *additionalConversationSessionRequestConfig) {
	c.images = h.images
}

func ConversationSessionRequestWithImages(images []ImageAttachment) ConversationSessionRequestConfig {
	return sessionRequestWithImages{
		images: images,
	}
}

type sessionRequestWithChannelContext struct {
	channelContext string
}

func (h sessionRequestWithChannelContext) apply(c *additionalConversationSessionRequestConfig) {
	c.channelContext = h.channelContext
}

// ConversationSessionRequestWithChannelContext carries conversation observed in
// a watched messaging channel. Deliberately separate from the query so the
// agent can distinguish what the user asked from what it merely overheard.
func ConversationSessionRequestWithChannelContext(channelContext string) ConversationSessionRequestConfig {
	return sessionRequestWithChannelContext{
		channelContext: channelContext,
	}
}

type sessionRequestWithChannelContextRefs struct {
	channelContextRefs map[string]any
}

func (h sessionRequestWithChannelContextRefs) apply(c *additionalConversationSessionRequestConfig) {
	c.channelContextRefs = h.channelContextRefs
}

// ConversationSessionRequestWithChannelContextRefs carries the provenance of
// the channel context block — destined for a conversation reference row the
// UI cites, never for the prompt.
func ConversationSessionRequestWithChannelContextRefs(refs map[string]any) ConversationSessionRequestConfig {
	return sessionRequestWithChannelContextRefs{
		channelContextRefs: refs,
	}
}

func HandleConversationSessionRequest(ctx *security.RequestContext, agent NBAgent, userId string, accountId string, sessionId string, query string, configs ...ConversationSessionRequestConfig) (NBAgentResponse, error) {

	defaultConfig := additionalConversationSessionRequestConfig{
		source: ConversationSourceUserInvestigation,
	}

	for _, c := range configs {
		c.apply(&defaultConfig)
	}
	if len(defaultConfig.clientTools) > 0 {
		defaultConfig.config.ClientTools = defaultConfig.clientTools
	}
	if !defaultConfig.capabilities.IsEmpty() {
		defaultConfig.config.Capabilities = defaultConfig.capabilities
	}

	if defaultConfig.enableQueryRefinement == nil {
		defaultConfig.enableQueryRefinement = lo.ToPtr(true)
	}

	if defaultConfig.enableQueryRefinement == nil {
		defaultConfig.enableQueryRefinement = lo.ToPtr(true)
	}

	if userId == "" {
		userId = ctx.GetSecurityContext().GetUserId()
	}

	if userId == "" && defaultConfig.source == ConversationSourceUserInvestigation {
		return NBAgentResponse{}, errors.New("userId not found")
	}

	if defaultConfig.enableCritique == nil {
		defaultConfig.enableCritique = lo.ToPtr(false)
	}

	// Compose AccountPrompt from per-request additional system prompt (e.g.
	// event-analysis additional_instructions, memory bridge writes) and the
	// account-wide GlobalContext. Both surfaces feed the same <global_preferences>
	// block in the planner human-message; see renderGlobalPreferencesBlock.
	// GC loader is soft-failing — DB issues never block a chat turn.
	gcPrompt := toolcore.LoadActiveGlobalContext(ctx, accountId)
	composedAccountPrompt := mergeAccountPrompts(defaultConfig.systemPrompt, gcPrompt)

	agentRequest := NBAgentRequest{
		Query:                 query,
		AccountId:             accountId,
		UserId:                userId,
		QueryConfig:           defaultConfig.config,
		ConversationId:        lo.Ternary(defaultConfig.conversationId.Valid, defaultConfig.conversationId.UUID.String(), ""),
		MessageId:             lo.Ternary(defaultConfig.messageId.Valid, defaultConfig.messageId.UUID.String(), ""),
		AgentId:               lo.Ternary(defaultConfig.agentId.Valid, defaultConfig.agentId.UUID.String(), ""),
		QueryContext:          defaultConfig.queryContext,
		EnableQueryRefinement: *defaultConfig.enableQueryRefinement,
		AccountPrompt:         composedAccountPrompt,
		SessionId:             sessionId,
		ConversationSource:    defaultConfig.source,
		EnableCritique:        *defaultConfig.enableCritique,
		ClientTools:           defaultConfig.clientTools,
		Capabilities:          defaultConfig.capabilities,
		PreviousState:         defaultConfig.previousState,
		IsResume:              defaultConfig.isResume,
		Images:                defaultConfig.images,
		ChannelContext:        defaultConfig.channelContext,
		ChannelContextRefs:    defaultConfig.channelContextRefs,
	}

	response, err := handleConversationRequest(ctx, agentRequest, agent, sessionId, defaultConfig.source)

	if defaultConfig.source == ConversationSourceInstantNotification || defaultConfig.source == ConversationSourceInvestigation {
		sendReplyToNotificationServer(ctx, agentRequest, response, err)
	}

	return response, err
}

// shouldSkipResumeForTerminalConversation returns true when a recovery / resume
// path should refuse to re-process a message because its conversation already
// reached a terminal state (COMPLETED / FAILED / KILLED / TERMINATED) and the
// message itself is not in a legitimately-resumable wait state.
//
// WAITING / WAITING_FOR_CLIENT_TOOL messages and Followup-typed messages are
// always resumable and bypass this guard — they represent in-flight UX
// commitments the user is still completing.
//
// Centralized so handleConversationRequest (new turn / mid-flight resume) and
// HandleConversationMessageRequest (dead-worker recovery / client-tool result)
// stay in lockstep on what counts as "do not touch".
func shouldSkipResumeForTerminalConversation(convStatus ConversationStatus, msgStatus ConversationStatus, msgType MessageType) bool {
	if !IsTerminalConversationStatus(convStatus) {
		return false
	}
	if msgStatus == ConversationStatusWaiting || msgStatus == ConversationStatusWaitingForClientTool {
		return false
	}
	if msgType == MessageTypeFollowup {
		return false
	}
	return true
}

// shouldSkipSaveBack decides whether handleConversationRequest should skip
// persisting this turn's outcome (AI response + conversation status) at the
// end of processing.
//
// Conversation-level KILLED is system-initiated (budget exhausted, hard
// shutdown) and always wins — never overwrite.
//
// For TERMINATED the question is per-message, not per-conversation. After
// #30137, a brand-new turn on a previously-terminated conversation flips the
// row back to IN_PROGRESS via markConversationActive, and the agent's
// response for that fresh MessageId must be persisted. The race the original
// guard was protecting against — Q1's late agent return racing back after
// Stop — is identifiable on the *message*: TerminateConversation marks the
// in-flight message row as TERMINATED, so Q1's late return sees msgStatus ==
// TERMINATED and skips, preserving "Conversation terminated by user". A
// fresh Q2 message starts as IN_PROGRESS and saves normally even when its
// concurrent flip-back-to-IN_PROGRESS has already happened.
func shouldSkipSaveBack(convStatus, msgStatus ConversationStatus) (skip bool, reason string) {
	if convStatus == ConversationStatusKilled {
		return true, "conversation_killed"
	}
	if msgStatus == ConversationStatusTerminated {
		return true, "message_terminated"
	}
	return false, ""
}

// markConversationActive flips the conversation row to IN_PROGRESS once we are
// committed to processing real work. KILLED stays sticky (system-initiated:
// budget, hard shutdown). TERMINATED stays sticky for resume / recovery /
// followup turns — a callback for the just-stopped message must not revive
// its own row — but `allowReviveTerminated` lets the new-message branch flip
// it back, because a brand-new MessageId can't be the racing callback.
// IN_PROGRESS is a no-op. `reason` is included in the error log so the call
// sites (new turn vs. resumed/followup vs. recovery) stay distinguishable in
// observability.
func markConversationActive(ctx *security.RequestContext, conversationId string, currentStatus ConversationStatus, reason string, allowReviveTerminated bool) {
	if currentStatus == ConversationStatusKilled || currentStatus == ConversationStatusInProgress {
		return
	}
	if currentStatus == ConversationStatusTerminated && !allowReviveTerminated {
		return
	}
	if err := GetConversationDao().UpdateConversationStatus(conversationId, ConversationStatusInProgress); err != nil {
		ctx.GetLogger().Error("conversation: failed to mark conversation in-progress", "reason", reason, "error", err)
	}
}

// Tier wins when both are supplied; DAO failures are logged, not propagated
// (sticky-config write is best-effort and must not abort the chat turn).
//
// llmConfigSource is handled outside the switch: pinning a config slot is
// independent of picking a model, so it persists whichever of the two branches
// below applies — or neither.
func applyConversationModelConfig(ctx *security.RequestContext, dao IConversationDao, conversationId, llmProvider, llmModel string, llmTierOverrides ConversationTierOverrides, llmConfigSource string, llmConfigReset bool) {
	// A reset drops everything and takes no other write: the picker sends it
	// only when nothing is selected, so there is nothing to persist after it.
	if llmConfigReset {
		if uerr := dao.ClearConversationModelConfig(conversationId); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to clear llm model configuration", "error", uerr)
			return
		}
		InvalidateConversationOverrideCache(conversationId)
		ctx.GetLogger().Info("conversation: cleared sticky llm model configuration")
		return
	}

	if llmConfigSource != "" {
		if uerr := dao.UpdateConversationConfigSource(conversationId, llmConfigSource); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to update pinned llm config source", "error", uerr)
		} else {
			InvalidateConversationOverrideCache(conversationId)
			ctx.GetLogger().Info("conversation: updated sticky llm config source", "config_source", llmConfigSource)
		}
	}

	switch {
	case llmTierOverrides.HasAny():
		if uerr := dao.UpdateConversationTierOverrides(conversationId, llmTierOverrides); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to update per-tier model configuration", "error", uerr)
			return
		}
		InvalidateConversationOverrideCache(conversationId)
		ctx.GetLogger().Info("conversation: updated sticky per-tier model configuration", "tier_count", len(llmTierOverrides.Picks))
	case llmProvider != "" && llmModel != "":
		if uerr := dao.UpdateConversationModelBlanket(conversationId, llmProvider, llmModel); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to update sticky model configuration", "error", uerr)
			return
		}
		InvalidateConversationOverrideCache(conversationId)
		ctx.GetLogger().Info("conversation: updated sticky blanket model configuration", "provider", llmProvider, "model", llmModel)
	}
}

func handleConversationRequest(ctx *security.RequestContext, request NBAgentRequest, agent NBAgent, sessionId string, source ConversationSource) (NBAgentResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		ctx.GetLogger().Info("conversation: validation failed", "error", err.Error(), "agent", agent.GetName())
		return NBAgentResponse{}, common.ErrorBadRequest("conversation: unable to complete request, Please try again later")
	}

	// Finalize-on-panic: without this, a panic inside executeAgent (or any
	// downstream sub-agent) unwinds the worker goroutine without reaching the
	// save-back path at ~line 1268, leaving the message row stuck IN_PROGRESS
	// until MarkInProgressConversationAsKilled runs (>=1h). Panic-only on purpose
	// — normal errors reach the executeErr branch at ~line 1237 which flips
	// status FAILED via the existing save-back; adding a general non-panic branch
	// here would risk racing legitimate WAITING transitions on abnormal exits.
	// Idempotent under a race with save-back: UpdateConversationMessage overwrites
	// unconditionally, and FinalizeInFlightToolCalls is UPDATE ... WHERE status = 'in_progress'.
	// Cost on happy path is a single defer registration (~30ns); the closure body
	// only runs on panic. Re-throws so the worker goroutine still logs the panic.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Resolve logger defensively. The whole point of this defer is to make
		// the process survive an unexpected panic in the executor chain — the
		// worst outcome would be a nested nil-panic inside our recover block
		// (masks the original panic, may crash the worker without any log).
		// ctx is non-nil in production (constructed by handleRequestExecution in
		// api/chains.go), but a test caller could pass nil, and ctx.GetLogger()
		// could theoretically return nil for a partially-initialised request
		// context. Falling back to slog.Default() (guaranteed non-nil) removes
		// both risks for the cost of one nil check.
		logger := slog.Default()
		if ctx != nil {
			if l := ctx.GetLogger(); l != nil {
				logger = l
			}
		}
		// Resolve agent name defensively — `agent` is an interface parameter and
		// the panic could originate from anywhere in the call chain, including a
		// path where a caller passed a nil agent (rare — most callers construct
		// the agent before invoking us — but a nil-deref inside a recover block
		// masks the original panic and is much harder to debug).
		agentName := ""
		if agent != nil {
			agentName = agent.GetName()
		}
		messageId := request.MessageId
		conversationId := request.ConversationId
		logger.Error("conversation: panic in handleConversationRequest — finalizing message as FAILED before re-throw",
			"panic", r, "agent", agentName, "message_id", messageId, "conversation_id", conversationId)

		// DB cleanup uses a ctx-cancellable path (MarkMessageFailedOnPanic uses
		// ExecContext internally) with a hard budget, so a hung DB connection
		// unblocks the SQL driver at the deadline — no leaked goroutine holding
		// a pool slot indefinitely, no zombie worker. dao is checked for nil to
		// avoid a nested panic in early-startup / test contexts where the
		// singleton isn't set.
		if dao := GetConversationDao(); dao != nil {
			finalizeCtx, cancel := context.WithTimeout(context.Background(), finalizeOnPanicDBBudget)
			if err := dao.MarkMessageFailedOnPanic(finalizeCtx, messageId, conversationId,
				fmt.Sprintf("execution panicked: %v", r)); err != nil {
				logger.Error("conversation: finalize-on-panic DB update failed",
					"error", err, "message_id", messageId, "conversation_id", conversationId)
			}
			cancel()
		} else {
			logger.Error("conversation: finalize-on-panic — GetConversationDao returned nil, cannot finalize DB state")
		}
		panic(r)
	}()

	// Attach the egressfilter event reporter for the duration of this turn.
	// The wrapper invokes the callback once per outbound LLM call that
	// produces hits; we accumulate them under egressFilterEvents and write
	// the slice to the message's metadata column at the finalize step.
	//
	// A single message can produce multiple LLM calls (planner thinks, tool
	// runs, planner thinks again, ...) so events is a slice, not a single
	// value. Mutated under egressFilterEventsMu because the wrapper may be
	// called from multiple goroutines under parallel plan execution.
	var (
		egressFilterEvents   []egressfilter.FilterEvent
		egressFilterEventsMu sync.Mutex
	)
	ctx.SetContext(egressfilter.WithFilterEventReporter(ctx.GetContext(), func(e egressfilter.FilterEvent) {
		egressFilterEventsMu.Lock()
		egressFilterEvents = append(egressFilterEvents, e)
		egressFilterEventsMu.Unlock()
	}))

	// Sibling accumulator for the EE PII scrubber (ee/scrubbing). Unlike the
	// egressfilter reporter above, PII uses a per-turn DISTINCT-value
	// accumulator (2026-07-31) so the chip count reflects unique values
	// rather than sum-of-hit_counts across every wrapper call. A react loop
	// of many calls that reference the same email would otherwise show
	// e.g. "95 PII scrubbed" for what is really 1-few distinct values.
	//
	// Values live only in the accumulator's in-memory map for the duration
	// of this turn — never serialized or persisted. Same risk profile as
	// the per-call mapping the wrapper already holds for rehydration.
	// At message-end we call Consolidated() to build ONE PIIScrubEvent
	// with the true-distinct hit_count + union of categories + agent list.
	piiAccumulator := egressfilter.NewPIIValueAccumulator()
	ctx.SetContext(egressfilter.WithPIIValueAccumulator(ctx.GetContext(), piiAccumulator))

	var agentResponse NBAgentResponse
	var executeErr error
	ctx.GetLogger().Info("conversation: processing request", "query", lo.Substring(request.Query, 0, 50), "agent_id", request.AgentId, "message_id", request.MessageId, "query_config", request.QueryConfig, "conversation_context", request.ConversationContext)
	t0 := time.Now()

	llmProvider := ""
	llmModel := ""
	if request.QueryConfig.LlmProvider != "" {
		ctx.GetLogger().Info("conversation: overriding llm provider from request config", "provider", request.QueryConfig.LlmProvider)
		llmProvider = request.QueryConfig.LlmProvider
	}
	if request.QueryConfig.LlmModelName != "" {
		ctx.GetLogger().Info("conversation: overriding llm model from request config", "model", request.QueryConfig.LlmModelName)
		llmModel = request.QueryConfig.LlmModelName
	}

	// Drop half-set entries; convert wire shape → DAO/resolver struct.
	var llmTierOverrides ConversationTierOverrides
	for tier, p := range request.QueryConfig.LlmTierModels {
		if p.Provider == "" || p.Model == "" {
			continue
		}
		if llmTierOverrides.Picks == nil {
			llmTierOverrides.Picks = make(map[string]TierModelPick)
		}
		llmTierOverrides.Picks[tier] = TierModelPick{Provider: p.Provider, Model: p.Model, ConfigSource: p.ConfigSource}
	}
	if llmTierOverrides.HasAny() {
		ctx.GetLogger().Info("conversation: overriding per-tier models from request config", "tier_count", len(llmTierOverrides.Picks))
	}

	newConversation := false
	var conversation Conversation
	if request.ConversationId != "" {
		conversation, err = GetConversationDao().GetConversation(request.ConversationId)
		if err != nil && !errors.Is(err, ErrConversationNotFound) {
			ctx.GetLogger().Error("conversation: unable to load conversation", "error", err.Error())
			return NBAgentResponse{}, err
		}

		if conversation.ID != uuid.Nil {
			if conversation.AccountID.String() != request.AccountId {
				return NBAgentResponse{}, errors.New("conversation: user does not have access to this conversation")
			}

			if sessionId != "" && sessionId != conversation.SessionID {
				return NBAgentResponse{}, errors.New("conversation: sessionId doesnt match with existing conversation")
			}

			// Followup resume v2: when the feature flag is on and the request is a
			// followup response (has agent_id + query), delegate to the clean single-
			// entry-point handler. It serializes via conv-level lock, resolves the
			// correct message_id from the agent record, and handles parent bubble-up.
			// Applied for both WAITING and IN_PROGRESS conversations (#28141).
			isFollowupResume := config.Config.FollowupResumeV2Enabled && request.AgentId != "" && request.Query != "" &&
				(conversation.Status == ConversationStatusWaiting || conversation.Status == ConversationStatusInProgress)

			if !isFollowupResume {
				// IN_PROGRESS guard: reject new turns submitted while another turn is
				// already running. Resume requests (client-tool-result, dead-worker
				// recovery) bypass this guard — they're the workers that just took
				// over the active conversation, not a competing new submission.
				// Without IsResume, HandleConversationMessageRequest's call to
				// markConversationActive would flip the conversation to IN_PROGRESS
				// and then this guard would immediately reject the very work that
				// just authorized itself (regression introduced by #29973).
				if conversation.Status == ConversationStatusInProgress && !request.IsResume {
					return NBAgentResponse{}, ErrConversationInProgress
				}

				// A net-new generation (no MessageId) on a conversation whose latest
				// turn is still WAITING on a followup is rejected. The followup-answer
				// path above (FollowupResumeV2) already handles the legitimate case
				// where AgentId is set; reaching here means the user is asking a fresh
				// question while a prior turn still expects an answer — accepting
				// would leave that prior turn permanently orphaned.
				if request.MessageId == "" &&
					(conversation.Status == ConversationStatusWaiting ||
						conversation.Status == ConversationStatusWaitingForClientTool) {
					return NBAgentResponse{}, ErrConversationPendingFollowup
				}
			}

			// Only once the turn is going to run. The write is destructive —
			// blanket and per-tier picks are mutually exclusive, so persisting a
			// blanket pick clears the stored tier picks — and a rejected turn
			// must not cost the user their model selection.
			applyConversationModelConfig(ctx, GetConversationDao(), request.ConversationId, llmProvider, llmModel, llmTierOverrides, request.QueryConfig.LlmConfigSource, request.QueryConfig.LlmConfigReset)

			if isFollowupResume {
				request.ConversationId = conversation.ID.String()
				resp, rErr := HandleFollowupAndResumeV2(ctx, request)
				// Resume returns early here, skipping the SessionId/ConversationId
				// stamping the normal path does below; without it the response
				// carries an empty SessionId that the notification server 404s.
				resp.ConversationId = conversation.ID.String()
				resp.SessionId = conversation.SessionID
				if rErr != nil {
					ctx.GetLogger().Error("conversation: followup resume v2 failed", "error", rErr)
					return resp, rErr
				}
				return resp, nil
			}
		}
	}

	if conversation.ID == uuid.Nil {
		if sessionId != "" {
			conversation, err = GetConversationDao().GetConversationBySession(request.AccountId, sessionId)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to load conversation", "error", err)
				return NBAgentResponse{}, err
			}
		}

		// create new conversation
		if conversation.ID == uuid.Nil {
			newConversation = true
			tenantId, err := security.GetTenantIdFromAccountId(request.AccountId)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to get tenant id", "error", err)
				return NBAgentResponse{}, err
			}
			if sessionId == "" {
				sessionId = uuid.NewString()
			}
			// Create initial title from query (stripped of any leading @agent mention
			// so it doesn't leak into the placeholder title the UI shows until the
			// async title-generation task finishes).
			var title string
			initialTitleSource := common.StripLeadingAgentMention(request.Query)
			if initialTitleSource != "" {
				words := strings.Fields(initialTitleSource)
				if len(words) > 5 {
					title = strings.Join(words[:5], " ") + "..."
				} else {
					title = initialTitleSource
				}
			} else {
				title = DefaultConversationTitle
			}
			var saveTierOverrides *ConversationTierOverrides
			if llmTierOverrides.HasAny() {
				to := llmTierOverrides
				saveTierOverrides = &to
				llmProvider = ""
				llmModel = ""
			}
			id, err := GetConversationDao().SaveConversation(request.ConversationId, sessionId, tenantId, request.AccountId, request.UserId, "", title, ConversationStatusInProgress, source, llmProvider, llmModel, saveTierOverrides, request.QueryConfig.LlmConfigSource)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to save conversation to DB", "error", err)
				return NBAgentResponse{}, err
			}
			conversation, err = GetConversationDao().GetConversation(id.String())
			if err != nil {
				ctx.GetLogger().Error("conversation: Failed to load conversation from DB", "error", err)
				return NBAgentResponse{}, err
			}
		} else if conversation.Status == ConversationStatusInProgress && !request.IsResume {
			// Mirror of the IN_PROGRESS guard at the conversation-id gate above
			// — same rationale: resume workers (client-tool-result, dead-worker
			// recovery) legitimately re-enter an active conversation.
			return NBAgentResponse{}, ErrConversationInProgress
		} else if request.MessageId == "" &&
			(conversation.Status == ConversationStatusWaiting ||
				conversation.Status == ConversationStatusWaitingForClientTool) {
			// Mirror of the conversation-id gate above: don't accept a fresh
			// generation while the prior turn is still waiting on a followup.
			return NBAgentResponse{}, ErrConversationPendingFollowup
		}

		// Handle existing conversation found by session ID - update model if needed
		if !newConversation && llmProvider != "" && llmModel != "" {
			err = GetConversationDao().UpdateConversationModel(conversation.ID.String(), llmProvider, llmModel)
			if err != nil {
				ctx.GetLogger().Warn("conversation: failed to update sticky model configuration", "error", err)
			} else {
				InvalidateConversationOverrideCache(conversation.ID.String())
			}
		}
	}

	if conversation.ID == uuid.Nil {
		ctx.GetLogger().Error("conversation: unable to load conversation from DB", "error", err)
		return NBAgentResponse{}, errors.New("conversation: unable to load conversation from DB")
	}

	request.ConversationId = conversation.ID.String()

	// Conversation status is flipped to IN_PROGRESS later (via
	// markConversationActive), only after we know we are committed to processing
	// actual work — i.e. inside the new-message branch (after the message row is
	// saved) or inside the existing-message branch (after the terminal-state
	// defense has let us through).
	//
	// Flipping eagerly here would clobber terminal rows (COMPLETED / FAILED) on
	// duplicate triggers, because the defense below reads the in-memory copy of
	// conversation.Status loaded earlier and would bail out before we could
	// restore the terminal row — leaving the DB stuck at IN_PROGRESS forever.

	// Stamp per-request overrides so ResolveLLMConfig's context layers pick
	// them up on every downstream LLM call.
	parentContext := ctx.GetContext()
	if request.QueryConfig.LlmProvider != "" {
		parentContext = context.WithValue(parentContext, ContextKeyLlmProviderOverride, request.QueryConfig.LlmProvider)
	}
	if request.QueryConfig.LlmModelName != "" {
		parentContext = context.WithValue(parentContext, ContextKeyLlmModelOverride, request.QueryConfig.LlmModelName)
	}
	if llmTierOverrides.HasAny() {
		parentContext = context.WithValue(parentContext, ContextKeyLlmTierModelOverrides, llmTierOverrides)
	}
	// The pin falls back to the conversation row when the request didn't carry
	// one. MergeFrom normally inherits it from the previous message, but that
	// merge is skipped whenever the request supplies its own ToolConfigs — the
	// column is what keeps a pinned conversation pinned in that case.
	// On a reset the row was just cleared, but `conversation` was read before
	// that write — falling back to it would re-pin the turn we are unpinning.
	effectiveConfigSource := request.QueryConfig.LlmConfigSource
	if effectiveConfigSource == "" && !request.QueryConfig.LlmConfigReset && conversation.LlmConfigSource != nil {
		effectiveConfigSource = *conversation.LlmConfigSource
	}
	if effectiveConfigSource != "" {
		parentContext = context.WithValue(parentContext, ContextKeyLlmConfigSourceOverride, effectiveConfigSource)
	}

	ctx = security.NewRequestContext(
		parentContext,
		ctx.GetSecurityContext(),
		ctx.GetLogger().With("account_id", request.AccountId, "conversation_id", request.ConversationId),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	historyType := MessageTypeGeneration
	status := ConversationStatusCompleted
	if agent.GetName() == RouterAgentName {
		historyType = MessageTypeRoute
	}

	messageStatus := ConversationStatusCompleted
	messageType := MessageTypeGeneration

	if request.MessageId == "" {
		parentAgentId := uuid.Nil
		if request.ParentAgentId != "" {
			parentAgentId, err = uuid.Parse(request.ParentAgentId)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to parse parent agent id", "error", err)
				return NBAgentResponse{}, err
			}
		}

		// get previous message && if there is any tool config, then add it to current message as propagation
		if request.QueryConfig.ToolConfigs == nil {
			previousConversations, err := GetConversationDao().ListConversationMessages("", "", conversation.ID.String(), false)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to lookup previous conversations", "error", err)
			}
			if len(previousConversations) > 0 {
				for _, v := range slices.Backward(previousConversations) {
					if v.MessageType != string(MessageTypeGeneration) {
						continue
					}
					if v.MessageConfig != nil && *v.MessageConfig != "" {
						prevConfig := toolcore.NBQueryConfig{}
						err := common.UnmarshalJson([]byte(*v.MessageConfig), &prevConfig)
						if err != nil {
							ctx.GetLogger().Error("conversation: unable to unmarshal message config from previous message", "error", err, "data", *v.MessageConfig, "id", v.ID)
						}
						request.QueryConfig.MergeFrom(prevConfig)
						break
					}
				}
			}
		}

		// Determine effective LLM config for this message
		effectiveProvider := llmProvider
		effectiveModel := llmModel

		if effectiveProvider == "" && conversation.LlmProvider != nil {
			effectiveProvider = *conversation.LlmProvider
		}
		if effectiveModel == "" && conversation.LlmModel != nil {
			effectiveModel = *conversation.LlmModel
		}

		// Fallback to resolved config if still empty
		if effectiveProvider == "" || effectiveModel == "" {
			if res, err := ResolveLLMConfig(ctx, request.AccountId, agent.GetName(), request.ConversationId); err == nil {
				if effectiveProvider == "" {
					effectiveProvider = res.Provider
				}
				if effectiveModel == "" {
					effectiveModel = res.Model
				}
			} else {
				ctx.GetLogger().Warn("conversation: failed to resolve default LLM config for message", "error", err)
			}
		}

		messageId, err := GetConversationDao().SaveConversationMessage("", conversation.ID.String(), request.AccountId, request.UserId, MessageRoleHuman, historyType, request.Query, "", agent.GetName(), parentAgentId, request.QueryConfig, request.ConversationContext, effectiveProvider, effectiveModel, ConversationStatusInProgress)
		if err != nil {
			ctx.GetLogger().Error("conversation: unable to save user query to DB", "error", err)
			return NBAgentResponse{}, err
		}
		request.MessageId = messageId.String()

		// Best-effort: the citation must never fail the turn. Attached to the
		// turn's message row, where the channel context was injected.
		if len(request.ChannelContextRefs) > 0 {
			refId, _ := request.ChannelContextRefs["channel_id"].(string)
			if refId == "" {
				refId = string(AgentReferenceTypeChannelContext)
			}
			if err := GetConversationDao().SaveAgentReferences(request.AccountId, conversation.ID.String(), request.MessageId, uuid.Nil.String(), []AgentReference{{
				Type:        AgentReferenceTypeChannelContext,
				ReferenceID: refId,
				Metadata:    request.ChannelContextRefs,
			}}); err != nil {
				ctx.GetLogger().Error("conversation: failed to save channel context reference", "error", err, "message_id", request.MessageId)
			}
		}

		markConversationActive(ctx, conversation.ID.String(), conversation.Status, "new turn", true)

		// Save image attachments (non-fatal: message is saved even if attachment storage fails)
		if len(request.Images) > 0 {
			dao := GetAttachmentDAO()
			if dao != nil {
				_, attachErr := dao.SaveAttachments(request.MessageId, conversation.ID.String(), request.AccountId, request.Images)
				if attachErr != nil {
					ctx.GetLogger().Error("conversation: failed to save image attachments", "error", attachErr, "message_id", request.MessageId)
				}
			}
		}
	} else {
		message, err := GetConversationDao().GetConversationMessage(request.MessageId, request.AccountId, request.ConversationId)
		if err != nil || message.ID == uuid.Nil {
			ctx.GetLogger().Error("conversation: unable to get existing message", "error", err)
			return NBAgentResponse{}, err
		}
		messageStatus = message.Status
		messageType = MessageType(message.MessageType)

		// Defense-in-depth: don't cleanup or re-process messages for conversations
		// that already reached a terminal state. This prevents dead worker recovery
		// from destroying completed execution data.
		if shouldSkipResumeForTerminalConversation(conversation.Status, message.Status, messageType) {
			ctx.GetLogger().Warn("conversation: skipping re-processing for terminal conversation",
				"conversation_status", conversation.Status, "message_id", request.MessageId)
			if message.Status == ConversationStatusInProgress {
				_ = GetConversationDao().UpdateConversationMessage(request.MessageId, message.Response, conversation.Status)
			}
			return NBAgentResponse{
				Status:         conversation.Status,
				ConversationId: request.ConversationId,
				MessageId:      request.MessageId,
			}, nil
		}

		// We are committed to processing this message (followup, waiting resume,
		// or in-flight recovery). Mark the conversation active so UI/dashboards
		// reflect it.
		markConversationActive(ctx, conversation.ID.String(), conversation.Status, "resumed/followup turn", false)

		// Only cleanup if it's not a waiting message or a followup response
		if message.Status != ConversationStatusWaiting && message.Status != ConversationStatusWaitingForClientTool && messageType != MessageTypeFollowup {
			err = GetConversationDao().CleanupConversationMessage(request.MessageId, request.AccountId)
			if err != nil {
				if errors.Is(err, ErrCleanupRefusedActiveFollowup) {
					// An active followup still references one of the agents
					// the cleanup would have removed. Don't re-execute — the
					// followup IS the user's resume point. Flip the human
					// message to WAITING so future dead-worker scans skip it,
					// and return the existing followup-pending state to the
					// caller so it stops cleanly.
					ctx.GetLogger().Warn("conversation: skipping re-execution; active followup still references this turn's agents",
						"message_id", request.MessageId)
					if updateErr := GetConversationDao().UpdateConversationMessage(request.MessageId, message.Response, ConversationStatusWaiting); updateErr != nil {
						ctx.GetLogger().Error("conversation: failed to mark message WAITING after cleanup refusal", "error", updateErr)
					}
					return NBAgentResponse{
						Status:         ConversationStatusWaiting,
						ConversationId: request.ConversationId,
						MessageId:      request.MessageId,
					}, nil
				}
				ctx.GetLogger().Error("conversation: unable to cleanup existing message", "error", err)
				return NBAgentResponse{}, err
			}
		}

		// populate with existing toolConfigs, clientTools and capabilities so that same can be reused
		if message.MessageConfig != nil && *message.MessageConfig != "" {
			ctx.GetLogger().Debug("conversation: restoring message config", "config_len", len(*message.MessageConfig))
			restoredConfig := toolcore.NBQueryConfig{}
			if err := common.UnmarshalJson([]byte(*message.MessageConfig), &restoredConfig); err != nil {
				ctx.GetLogger().Error("conversation: unable to unmarshal message config", "error", err)
			}
			if request.QueryConfig.IsEmpty() {
				request.QueryConfig = restoredConfig
			} else {
				// Prioritize request values, but preserve existing ones if not provided
				request.QueryConfig.MergeFrom(restoredConfig)
			}
		}

	}

	// Single-shot internal classifiers (e.g. webhook_subject_name_extractor) get a
	// throwaway conversation per call with no user-facing title — skip the
	// summary_agent title generation for them.
	if request.Query != "" && newConversation && !isSingleShotClassifier(agent) {
		generateConversationTitleAsync(ctx, request.ConversationId, request.MessageId, request.AccountId, agent.GetName(), request.Query, request.UserId)
	}

	// Sync capabilities and client tools from QueryConfig if they are present there but missing at top level
	if request.Capabilities.IsEmpty() && !request.QueryConfig.Capabilities.IsEmpty() {
		request.Capabilities = request.QueryConfig.Capabilities
	}
	if len(request.ClientTools) == 0 && len(request.QueryConfig.ClientTools) > 0 {
		request.ClientTools = request.QueryConfig.ClientTools
		ctx.GetLogger().Debug("conversation: restored client tools from query config", "count", len(request.ClientTools))
	}

	// Generate acknowledgment for new messages (not for retries or existing messages)
	// TO Do move this to async task
	// Single-shot internal classifiers have no user watching for an acknowledgment,
	// so skip the acknowledgment_agent call for them.
	if messageStatus != ConversationStatusWaiting && messageType != MessageTypeFollowup && agent.GetName() != RouterAgentName && agent.GetName() != ToolLlm && request.Query != "" && !isSingleShotClassifier(agent) {
		processAcknowledgmentAsync(ctx, request.AccountId, request.Query, agent.GetName(), conversation.ID.String(), request.MessageId, request.UserId, request.ConversationSource)
	}

	// Add context-aware query processing before executing the agent
	// When ConversationContextEnabled is true, skip query rewriting — the agent LLM resolves
	// references naturally from the increased history window + summary context.
	// Also skip if we are resuming an agent (request.AgentId is set) to prevent feedback turns from being rewritten.
	if !config.Config.ConversationContextEnabled && (conversation.Context != nil) && request.EnableQueryRefinement && messageStatus != ConversationStatusWaiting && request.Query != "" && messageType != MessageTypeFollowup && agent.GetName() != RouterAgentName && agent.GetPlannerType() != AgentPlannerTypeCustom && request.AgentId == "" {
		contextualizedQuery, err := GetContextManager().ContextualizeQuery(ctx, request, conversation.Context)
		if err == nil && contextualizedQuery != "" && contextualizedQuery != request.Query {
			ctx.GetLogger().Info("conversation: query contextualized", "original", request.Query, "contextualized", contextualizedQuery)
			request.Query = contextualizedQuery
		}
	}

	// Set the conversation context in the request
	if len(conversation.Context) > 0 {
		contextJSON, err := common.MarshalJson(conversation.Context)
		if err != nil {
			ctx.GetLogger().Warn("conversation: unable to marshal conversation context", "error", err)
		}
		request.ConversationContext = string(contextJSON)
		// Update message conversation context if it exists. Skip for followup
		// messages — their message_context stores the original NBAgentRequest
		// JSON (followup.go:287), which refineAgentQuestionAndHandleFollowups
		// reads to recover `previousQuery` on resume. Overwriting it with the
		// conversation context blob leaves request.Query empty, so the agent
		// then errors out with "query is required" (e.g. agent_code2.go:995).
		if request.MessageId != "" && messageType != MessageTypeFollowup {
			err = GetConversationDao().UpdateConversationMessageContext(request.MessageId, conversation.Context)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to update message context", "error", err)
			}
		}
	}

	// before passing the request to agent eval functions
	request, err = evalLLMFunction(ctx, request)
	if err != nil {
		ctx.GetLogger().Error("conversation: unable to evaluate functions", "error", err)
		return NBAgentResponse{}, err
	}

	convSetupDur := time.Since(t0)
	ctx.GetLogger().Info("conversation: executing agent", "agent", agent.GetName(), "setup_duration", convSetupDur.String())

	// Short-circuit prefix-only queries (e.g. "@k8s_debug" with no instruction)
	// with a graceful clarification instead of invoking the agent. The user's
	// message and this response are still saved via the normal update path
	// below, so the exchange appears in chat history.
	// Parse rather than strip "@"+agent.GetName(): the typed name may be an ALIAS
	// (e.g. "@k8s_debug" -> k8s_orchestrator) that a name-based strip would miss.
	if _, rest := common.ParseAgentMention(request.Query); rest == "" {
		ctx.GetLogger().Info("conversation: empty query after stripping agent mention, returning clarification", "agent", agent.GetName())
		agentResponse = NBAgentResponse{
			Response:  []string{emptyQueryClarifications[rand.IntN(len(emptyQueryClarifications))]},
			AgentName: agent.GetName(),
			Status:    ConversationStatusCompleted,
		}
	} else {
		agentResponse, executeErr = executeAgent(ctx, agent, request)
	}
	agentResponseContent := ""
	if len(agentResponse.Response) > 0 {
		agentResponseContent = agentResponse.Response[0]
		switch agentResponse.Status {
		case ConversationStatusWaiting, ConversationStatusWaitingForClientTool:
			status = agentResponse.Status
		case ConversationStatusFailed:
			// The agent itself reported failure (e.g. executeAgentPlanner packaged an
			// error into Response) even when executeErr comes back nil. Without this,
			// a failed turn's raw error text gets persisted as if it were a genuine
			// COMPLETED answer.
			status = ConversationStatusFailed
		}
	} else {
		status = ConversationStatusFailed
	}
	if executeErr != nil {
		agentResponseContent = executeErr.Error()
		status = ConversationStatusFailed
	}

	// After getting the agent response, update the context asynchronously.
	// Single-shot internal classifiers use a throwaway conversation per call, so
	// there is nothing to carry forward — skip context_memories_extractions.
	if len(agentResponse.Response) > 0 && agentResponse.AgentName != RouterAgentName && agentResponse.AgentName != ToolLlm && !isSingleShotClassifier(agent) {
		updateConversationContextAsync(ctx, request, agentResponseContent, conversation.ID.String(), conversation.Context, agentResponse.AgentName)
	}
	conversation, err = GetConversationDao().GetConversation(conversation.ID.String())
	if err != nil {
		ctx.GetLogger().Error("conversation: unable to reload conversation after agent execution", "error", err)
		return NBAgentResponse{}, err
	}
	// Decide whether to persist this turn's outcome. Skip the message read for
	// the KILLED short-circuit (it always wins regardless of message state);
	// otherwise fetch the message so the guard works even when a concurrent
	// fresh turn has already flipped the conversation row back to IN_PROGRESS.
	var msgStatus ConversationStatus
	if conversation.Status != ConversationStatusKilled {
		msg, msgErr := GetConversationDao().GetConversationMessage(request.MessageId, request.AccountId, request.ConversationId)
		if msgErr == nil {
			msgStatus = msg.Status
		}
	}
	skipSaveBack, skipReason := shouldSkipSaveBack(conversation.Status, msgStatus)

	if !skipSaveBack {
		// Saving AI Response to DB
		err = GetConversationDao().UpdateConversationMessage(request.MessageId, agentResponseContent, status)
		if err != nil {
			ctx.GetLogger().Error("conversation: unable to save AI response to DB at router", "error", err)
			return NBAgentResponse{}, err
		}

		// Snapshot the egressfilter events captured during this turn under a
		// lock so we don't race with the reporter (the wrapper may still be
		// invoked from in-flight planner goroutines until they unwind).
		egressFilterEventsMu.Lock()
		eventsSnapshot := append([]egressfilter.FilterEvent(nil), egressFilterEvents...)
		egressFilterEventsMu.Unlock()

		// Consolidate PII into ONE event per message from the accumulator
		// (2026-07-31, see WithPIIValueAccumulator). Nil when nothing was
		// scrubbed this turn (clean turn or PII disabled for tenant).
		piiConsolidated := piiAccumulator.Consolidated()

		// Secrets (egressfilter) and PII (scrubber) are sibling detectors in
		// the "outbound payload inspection" family — they share the same JSONB
		// slot under `metadata.egressfilter[]`, and consumers switch on each
		// event's `detector` field ("secrets" vs "pii"). One array, N detector
		// types — capped at one key even if we add more detectors later
		// (prompt-injection, jailbreak, ...).
		piiCount := 0
		if piiConsolidated != nil {
			piiCount = 1
		}
		if total := len(eventsSnapshot) + piiCount; total > 0 {
			combined := make([]any, 0, total)
			for _, e := range eventsSnapshot {
				combined = append(combined, e)
			}
			if piiConsolidated != nil {
				combined = append(combined, *piiConsolidated)
			}
			metadata := map[string]any{"egressfilter": combined}
			if mdErr := GetConversationDao().UpdateConversationMessageMetadata(request.MessageId, metadata); mdErr != nil {
				ctx.GetLogger().Warn("conversation: unable to persist message metadata",
					"error", mdErr, "message_id", request.MessageId,
					"secrets_events", len(eventsSnapshot), "pii_events", piiCount)
			}
		}

		err = GetConversationDao().UpdateConversationStatus(request.ConversationId, status)
		if err != nil {
			ctx.GetLogger().Error("conversation: unable to save conversation to DB", "error", err)
			return NBAgentResponse{}, err
		}

		// Update message config if it was modified during execution (e.g., by auto-resolution)
		// This ensures configs selected via keyword matching or LLM persist across conversation turns
		if agentResponse.QueryConfig != nil && !agentResponse.QueryConfig.IsEmpty() {
			err = GetConversationDao().UpdateConversationMessageConfig(request.MessageId, *agentResponse.QueryConfig)
			if err != nil {
				ctx.GetLogger().Error("conversation: unable to update message config after agent execution", "error", err)
				// Non-fatal - don't return error, just log it
			} else {
				ctx.GetLogger().Debug("conversation: updated message config after agent execution", "message_id", request.MessageId)
			}
		}

		if status == ConversationStatusCompleted && agentResponse.AgentName != ToolLlm {
			computeProductivityMetricsAsync(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId)
		}
	} else {
		ctx.GetLogger().Info("conversation: skipping save-back", "reason", skipReason, "message_id", request.MessageId)
	}

	ctx.GetLogger().Info("conversation: time to process request", "time", time.Since(t0))

	agentResponse.ConversationId = conversation.ID.String()
	agentResponse.SessionId = conversation.SessionID
	agentResponse.AgentName = agent.GetName()
	if agentResponse.Query == "" {
		agentResponse.Query = request.Query
	}
	if agentResponse.MessageId == "" {
		agentResponse.MessageId = request.MessageId
	}
	if agentResponse.FollowupRequest.Question == "" && agentResponse.Status != ConversationStatusWaiting && agentResponse.Status != ConversationStatusWaitingForClientTool && agent.GetName() != RouterAgentName && agentResponse.AgentName != ToolLlm && !isSingleShotClassifier(agent) {
		tools := agent.GetSupportedTools(ctx)
		evaluateAgentResponseAsync(ctx, agentResponse, request.AccountId, tools, request.UserId)
	}

	// Post-process response if the agent supports it
	if handler, ok := agent.(NBAgentResponseHandler); ok {
		agentResponse = handler.PostProcessResponse(ctx, request, agentResponse)
	}

	// Generate compact response for InstantNotification source (Slack) and Investigation
	if config.Config.SlackCompactResponse && (source == ConversationSourceInstantNotification || source == ConversationSourceInvestigation) && status == ConversationStatusCompleted && len(agentResponse.Response) > 0 && agentResponse.Response[0] != "" && agentResponse.AgentName != ToolLlm {
		compactResponse, compactErr := generateCompactResponse(ctx, request.AccountId, request.ConversationId, request.MessageId, request.UserId, request.Query, agentResponse.Response[0])

		if compactErr != nil {
			ctx.GetLogger().Warn("conversation: failed to generate compact response, using original", "error", compactErr)
		} else {
			// For Investigation source, preserve the original structured response (used by event_analyzer
			// for structured field parsing like git_diff, pr_list, etc.) and only save compact to DB.
			// For Slack/InstantNotification, replace the response with compact version for display.
			if source != ConversationSourceInvestigation {
				agentResponse.Response = []string{compactResponse}
			}
			// Save compact response to DB asynchronously
			ctx.GetLogger().Info("conversation: saving compact response asynchronously")
			saveCompactResponseAsync(ctx, request.ConversationId, request.MessageId, compactResponse)
		}
	}

	return agentResponse, executeErr
}

func evalLLMFunction(ctx *security.RequestContext, request NBAgentRequest) (NBAgentRequest, error) {
	message := strings.TrimSpace(request.Query)

	if !strings.HasPrefix(strings.ToLower(message), "/") {
		request.Query = message
		return request, nil
	}

	parts, err := shlex.Split(message)
	// extract function name and arguments /call functionName arg1 arg2 ...
	if err != nil {
		ctx.GetLogger().Warn("conversation: invalid function call syntax", "message", message, "error", err)
		request.Query = message
		return request, nil
	}

	functionName := ""
	functionArgs := []string{}

	if parts[0] == "/call" {
		if len(parts) < 2 {
			ctx.GetLogger().Info("conversation: invalid function call syntax, missing function name", "message", message)
			request.Query = message
			return request, errors.New("invalid function call syntax, missing function name")
		}

		functionName = parts[1]
		if len(parts) > 2 {
			functionArgs = parts[2:]
		}
	} else {
		functionName = strings.TrimPrefix(parts[0], "/")
		if len(parts) > 1 {
			functionArgs = parts[1:]
		}
	}

	function, err := GetLLMFunctionByName(ctx, request.AccountId, functionName)
	if err != nil {
		ctx.GetLogger().Info("conversation: function not found", "function_name", functionName, "error", err)
		return request, fmt.Errorf("function '%s' not found", functionName)
	}

	// requires some more work to validate arguments against function variables and types
	request.Query = function.Prompt
	if len(functionArgs) > 0 && len(function.Variables) == 0 {
		request.Query = function.Prompt + "\n\n Question:" + strings.Join(functionArgs, " ")
	} else if len(functionArgs) > 0 && len(function.Variables) > 0 {
		request.Query = function.Prompt + "\n\n Inputs:" + strings.Join(functionArgs, " ")
	}
	return request, nil
}

func HandleConversationMessageRequest(accountId, conversationId, messageId string, resumeAgentId ...string) (NBAgentResponse, error) {
	history := GetConversationDao()
	conversationMessage, err := history.GetConversationMessage(messageId, accountId, conversationId)
	if err != nil {
		slog.Error("conversation: unable to get message", "error", err, "message", messageId)
		return NBAgentResponse{}, err
	}

	conversation, err := history.GetConversation(conversationMessage.ConversationID.String())
	if err != nil {
		slog.Error("conversation: unable to get conversation", "error", err, "message", conversationMessage.ID.String())
		return NBAgentResponse{}, err
	}

	ctx := security.NewRequestContextForTenantAccountAdmin(conversation.TenantID.String(), conversationMessage.UserID.String(), []string{conversation.AccountID.String()})

	// Defense-in-depth: don't reprocess a message whose conversation already
	// reached a terminal state. Mirrors the same guard in
	// handleConversationRequest (see ~line 867). WAITING /
	// WAITING_FOR_CLIENT_TOOL messages and Followup types remain legitimately
	// resumable and skip this guard.
	//
	// The previous implementation here unconditionally flipped IN_PROGRESS to
	// FAILED before any recovery work — which is the opposite of what every
	// caller wants. Both call sites of HandleConversationMessageRequest
	// (dead-worker recovery in api/conversation_sync.go and client-tool-result
	// resume in api/chains.go) pass an IN_PROGRESS conversation by design.
	// Eagerly flipping it terminated a healthy run before the resumed agent
	// could update it.
	messageType := MessageType(conversationMessage.MessageType)
	if shouldSkipResumeForTerminalConversation(conversation.Status, conversationMessage.Status, messageType) {
		ctx.GetLogger().Warn("conversation: skipping re-processing for terminal conversation",
			"conversation_status", conversation.Status, "message_id", messageId)
		if conversationMessage.Status == ConversationStatusInProgress {
			_ = history.UpdateConversationMessage(messageId, conversationMessage.Response, conversation.Status)
		}
		return NBAgentResponse{
			Status:         conversation.Status,
			ConversationId: conversation.ID.String(),
			MessageId:      messageId,
		}, nil
	}

	// We are committed to processing this message (recovery or client-tool
	// resume). Keep the conversation row marked active. markConversationActive
	// is a no-op for IN_PROGRESS / KILLED / TERMINATED, so it never clobbers
	// an operator-initiated stop and never double-flips an already-active row.
	markConversationActive(ctx, conversation.ID.String(), conversation.Status, "recovery/resume", false)

	existingAgents, err := history.ListConversationAgents(conversationMessage.ID.String(), "")
	if err != nil {
		slog.Error("conversation: unable to get agents", "error", err, "message", conversationMessage.ID.String())
		return NBAgentResponse{}, err
	}

	var agentName string
	targetResumeAgentId := ""
	if len(resumeAgentId) > 0 && resumeAgentId[0] != "" {
		targetResumeAgentId = resumeAgentId[0]
		// Get name if agentId is provided
		if name, err := history.GetAgentNameFromAgentId(targetResumeAgentId); err == nil {
			agentName = name
		}
	}

	// First try to get agent name from router response
	if agentName == "" {
		for _, a := range existingAgents {
			if a.AgentName == RouterAgentName && a.Response != nil && *a.Response != "" {
				// Verify the routed agent exists before using it
				if _, ok := GetNBAgent(ctx, *a.Response, conversation.AccountID.String(), AgentStatusEnabled); ok {
					agentName = *a.Response
					break
				}
			}
		}
	}

	// If no valid routed agent found, use the original agent
	if agentName == "" {
		for _, a := range existingAgents {
			if a.AgentName != RouterAgentName {
				agentName = a.AgentName
				break
			}
		}
	}
	agent, ok := GetNBAgent(ctx, agentName, conversation.AccountID.String(), AgentStatusEnabled)
	if !ok {
		slog.Error("conversation: unable to get agent, marking conversation as killed", "conversation_id", conversationId, "message", conversationMessage.ID.String(), "agentName", agentName)
		err = history.UpdateConversationMessage(conversationMessage.ID.String(), "error: unable to identify agent", ConversationStatusFailed)
		if err != nil {
			slog.Error("conversation: unable to update message status", "error", err, "message", conversationMessage.ID.String())
		}
		err = history.UpdateConversationStatus(conversation.ID.String(), ConversationStatusFailed)
		if err != nil {
			slog.Error("conversation: unable to update conversation status", "error", err, "message", conversationMessage.ID.String())
		}
		return NBAgentResponse{
			AgentName:      agentName,
			ConversationId: conversation.ID.String(),
			MessageId:      conversationMessage.ID.String(),
			Response:       []string{"Looks like the agent is not available, please try again later."},
			Status:         ConversationStatusFailed,
		}, err
	}

	var waitingState string
	var waitingMessageId uuid.UUID
	for _, a := range existingAgents {
		// If a specific agent was requested, prioritize it
		if targetResumeAgentId != "" && a.ID.String() == targetResumeAgentId {
			if a.State != nil {
				waitingState = *a.State
			}
			waitingMessageId = a.MessageID
			break
		}

		if strings.EqualFold(a.AgentName, agentName) && (strings.EqualFold(string(a.Status), "waiting") || strings.EqualFold(string(a.Status), "waiting_for_client_tool")) {
			if a.State != nil {
				waitingState = *a.State
			}
			waitingMessageId = a.MessageID
			break
		}
	}

	//TODO add support for config, context at message level
	conversationSource := ConversationSourceUserInvestigation
	if conversation.Source != nil {
		conversationSource = *conversation.Source
	}

	// Important: We must use the original AI message ID (waitingMessageId) for resumption
	// This ensures that the tool result (associated with Turn 1 AI message) is correctly found in DB.
	targetMessageId := conversationMessage.ID
	if waitingMessageId != uuid.Nil {
		targetMessageId = waitingMessageId
	}

	resumeAgentUUID := uuid.Nil
	resumeAgentValid := false
	if targetResumeAgentId != "" {
		parsedUUID, parseErr := uuid.Parse(targetResumeAgentId)
		if parseErr != nil {
			slog.Error("conversation: failed to parse resume agent ID, defaulting to nil", "error", parseErr, "id", targetResumeAgentId)
		} else {
			resumeAgentUUID = parsedUUID
			resumeAgentValid = true
		}
	}

	response, err := HandleConversationSessionRequest(ctx, agent, conversation.UserID.String(), conversation.AccountID.String(), conversation.SessionID, conversationMessage.Message, ConversationSessionRequestWithMessageId(uuid.NullUUID{
		UUID:  targetMessageId,
		Valid: true,
	}), ConversationSessionRequestWithSource(conversationSource), ConversationSessionRequestWithConversationId(uuid.NullUUID{
		UUID:  conversation.ID,
		Valid: true,
	}), ConversationSessionRequestWithPreviousState(waitingState), ConversationSessionRequestWithAgentId(uuid.NullUUID{
		UUID:  resumeAgentUUID,
		Valid: resumeAgentValid,
	}), ConversationSessionRequestWithIsResume(true))

	if err != nil {
		ctx.GetLogger().Error("conversation: unable to reprocess conversation message", "error", err, "message", conversationMessage.ID.String())
		return response, err
	}
	return response, nil
}
