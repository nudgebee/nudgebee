package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nudgebee/llm/attachments"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/knowledge"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/prompts"
	"nudgebee/llm/security"
	"nudgebee/llm/security/egressfilter"

	toolcore "nudgebee/llm/tools/core"

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

// QueuedMessageDispatcherFunc defines the function signature for dispatching queued messages
// to an asynchronous execution pool.
type QueuedMessageDispatcherFunc func(ctx *security.RequestContext, agent NBAgent, conversation Conversation, msg ConversationMessage)

var queuedMessageDispatcher QueuedMessageDispatcherFunc

// SetQueuedMessageDispatcher configures the dispatcher used to execute queued conversation messages.
func SetQueuedMessageDispatcher(d QueuedMessageDispatcherFunc) {
	queuedMessageDispatcher = d
}

// QueueAcceptsStatus reports whether a new message on a conversation in this
// status waits in the queue instead of starting a turn. WAITING counts: a turn
// resumed from a followup answer runs with the conversation still WAITING
// (resume_v2 keeps it so unanswered sibling followups stay visible), and a new
// turn is refused while a followup is pending, so the message must queue.
func QueueAcceptsStatus(status ConversationStatus) bool {
	switch status {
	case ConversationStatusInProgress, ConversationStatusWaiting, ConversationStatusWaitingForClientTool:
		return true
	}
	return false
}

// dispatchNextQueuedMessage starts the oldest queued message once the turn has
// ended. The claim is a no-op unless the conversation is COMPLETED or FAILED and
// no message is still running or waiting.
func dispatchNextQueuedMessage(ctx *security.RequestContext, agent NBAgent, conversation Conversation) {
	if queuedMessageDispatcher == nil {
		return
	}
	conversationId := conversation.ID.String()
	claimCtx, cancelClaim := context.WithTimeout(context.WithoutCancel(ctx.GetContext()), 10*time.Second)
	nextMsg, claimErr := GetConversationDao().ClaimNextQueuedMessage(claimCtx, conversationId)
	cancelClaim()
	if claimErr == nil && nextMsg != nil {
		ctx.GetLogger().Info("conversation: auto-dispatching next queued message", "conversation_id", conversationId, "message_id", nextMsg.ID.String())
		queuedMessageDispatcher(ctx, agent, conversation, *nextMsg)
	} else if claimErr != nil {
		ctx.GetLogger().Warn("conversation: error checking for queued messages", "error", claimErr, "conversation_id", conversationId)
	}
}

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
		context.WithValue(context.WithValue(ctx.GetContext(), llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary), llmruntime.ContextKeyCacheScope, llmruntime.CacheScopeGlobal),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := llmruntime.GenerateAndTrackLLMContent(titleCtx, userId, accountId, conversationId, messageId, "summary_agent", false, messageContent, true, llmruntime.WithThinkingLevel(llmruntime.ThinkingLevelFastTask))
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
		// A short query can still be a JSON alert payload — unwrap it to a readable
		// field rather than using the raw `{"description":...` envelope as the title.
		if derived, ok := common.DeriveTitleFromJSONQuery(query); ok {
			title = derived
		} else {
			title = query
			if len(title) > 100 {
				// Byte-boundary safe: never split a multi-byte rune (raw title[:97] could).
				title = common.TruncateHead(title, 97) + "..."
			}
		}
	}

	if agentName != "" && agentName == llmruntime.ToolLlm {
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
			bgCtx.GetLogger().Warn("conversation: unable to generate title via LLM in background task", "conversation_id", conversationId, "error", err)
			bgCtx.GetLogger().Info("conversation: title generation task completed with error")
			return
		}
		bgCtx.GetLogger().Info("conversation: generated title via LLM in background task", "conversation_id", conversationId, "title", title)
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

	// Reformatting a finished answer for Slack — no tools, no reasoning — so it
	// resolves on the summary tier instead of the conversation's main model.
	compactCtx := security.NewRequestContext(
		context.WithValue(ctx.GetContext(), llmruntime.ContextKeyModelTier, llmruntime.ModelTierSummary),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	completion, err := llmruntime.GenerateAndTrackLLMContent(compactCtx, userId, accountId, conversationId, messageId, "compact_response_agent", true, messageContent, false)
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
	isQueuedDispatch      bool
	runProfile            *RunProfile
	images                []attachments.ImageAttachment
	channelContext        string
	channelContextRefs    map[string]any
	replyRef              string
	skipHistory           bool
	title                 string
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

type sessionRequestWithQueuedDispatch struct{}

func (sessionRequestWithQueuedDispatch) apply(c *additionalConversationSessionRequestConfig) {
	c.isResume = true
	c.isQueuedDispatch = true
}

// ConversationSessionRequestWithQueuedDispatch is only for an atomically claimed
// queue row. It bypasses the competing-turn guard without recovery cleanup.
func ConversationSessionRequestWithQueuedDispatch() ConversationSessionRequestConfig {
	return sessionRequestWithQueuedDispatch{}
}

type sessionRequestWithRunProfile struct{ profile *RunProfile }

func (h sessionRequestWithRunProfile) apply(c *additionalConversationSessionRequestConfig) {
	c.runProfile = h.profile
}

// ConversationSessionRequestWithRunProfile makes the request create the
// conversation of the governed run profile (from a validated run claim). It
// is used only when the conversation is created; an existing conversation
// keeps the profile it has.
func ConversationSessionRequestWithRunProfile(profile *RunProfile) ConversationSessionRequestConfig {
	return sessionRequestWithRunProfile{profile: profile}
}

type sessionRequestWithImages struct {
	images []attachments.ImageAttachment
}

func (h sessionRequestWithImages) apply(c *additionalConversationSessionRequestConfig) {
	c.images = h.images
}

func ConversationSessionRequestWithImages(images []attachments.ImageAttachment) ConversationSessionRequestConfig {
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

type sessionRequestWithReplyRef struct {
	replyRef string
}

func (h sessionRequestWithReplyRef) apply(c *additionalConversationSessionRequestConfig) {
	c.replyRef = h.replyRef
}

// ConversationSessionRequestWithReplyRef carries an opaque per-question
// correlator through to NBAgentRequest.ReplyRef — see its doc comment on
// ConversationApiRequest.ReplyRef for why SessionId alone isn't enough.
func ConversationSessionRequestWithReplyRef(replyRef string) ConversationSessionRequestConfig {
	return sessionRequestWithReplyRef{
		replyRef: replyRef,
	}
}

type sessionRequestWithTitle struct {
	title string
}

func (h sessionRequestWithTitle) apply(c *additionalConversationSessionRequestConfig) {
	c.title = h.title
}

// ConversationSessionRequestWithTitle names the conversation this request
// creates, skipping title generation. It has no effect when the conversation
// already exists. Event investigations use it to keep the event id in the
// title, which the Investigations tab and the investigation counts match on.
func ConversationSessionRequestWithTitle(title string) ConversationSessionRequestConfig {
	return sessionRequestWithTitle{
		title: title,
	}
}

type sessionRequestWithoutConversationHistory struct{}

func (h sessionRequestWithoutConversationHistory) apply(c *additionalConversationSessionRequestConfig) {
	c.skipHistory = true
}

// ConversationSessionRequestWithoutConversationHistory runs the turn without the
// conversation's earlier messages or distilled conversation memory, while still
// saving the turn into that conversation. For automated pipelines that reuse one
// conversation across independent runs (event analysis per fingerprint), where
// prior answers must not stand in for fresh investigation.
func ConversationSessionRequestWithoutConversationHistory() ConversationSessionRequestConfig {
	return sessionRequestWithoutConversationHistory{}
}

// resolveSessionRequestConfigs applies the options, then folds the fields that
// live inside NBQueryConfig into it. The fold runs after every option so
// WithConfig, which replaces config wholesale, cannot drop them whatever the
// option order.
func resolveSessionRequestConfigs(configs ...ConversationSessionRequestConfig) additionalConversationSessionRequestConfig {
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
	if defaultConfig.skipHistory {
		defaultConfig.config.SkipConversationHistory = true
	}
	// Which follow-up a person answered, and who they were, is written by this server when it
	// records the answer (refineAgentQuestionAndHandleFollowups) and read back from the message's
	// stored config. A caller's config never supplies it: an approved write is attributed to, and
	// used once under, that approval (ApprovedWriteRecorder).
	defaultConfig.config.ToolConfirmationRefs = nil
	return defaultConfig
}

// isChatOrAlertSource reports whether a turn is a person's chat (app, CLI, or a
// Slack/Teams thread) or an alert investigation. Only these save memory and
// session notes and get an answer-confidence score; automations, PR jobs,
// builders and any source added later skip that background work.
func isChatOrAlertSource(source ConversationSource) bool {
	switch source {
	case "", ConversationSourceUserInvestigation, ConversationSourceUserInvestigationCLI,
		ConversationSourceInvestigation, ConversationSourceInstantNotification:
		return true
	}
	return false
}

// withMemoryWriteOff returns a copy of m with writing switched off, leaving reading and reranking as they were.
func withMemoryWriteOff(m *toolcore.MemoryOverrides) *toolcore.MemoryOverrides {
	out := m.Clone()
	if out == nil {
		out = &toolcore.MemoryOverrides{}
	}
	out.Write = lo.ToPtr(false)
	return out
}

func HandleConversationSessionRequest(ctx *security.RequestContext, agent NBAgent, userId string, accountId string, sessionId string, query string, configs ...ConversationSessionRequestConfig) (NBAgentResponse, error) {

	defaultConfig := resolveSessionRequestConfigs(configs...)

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

	// Keep the stable account-wide GlobalContext separate from request-specific
	// additional guidance. ReAct planners cache AccountContext in their system
	// prefix while AccountPrompt stays in the dynamic human turn. The GC loader
	// is soft-failing — DB issues never block a chat turn.
	gcPrompt := knowledge.LoadActiveGlobalContext(ctx, accountId)

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
		AccountContext:        gcPrompt,
		AccountPrompt:         strings.TrimSpace(defaultConfig.systemPrompt),
		SessionId:             sessionId,
		ConversationSource:    defaultConfig.source,
		EnableCritique:        *defaultConfig.enableCritique,
		ClientTools:           defaultConfig.clientTools,
		Capabilities:          defaultConfig.capabilities,
		PreviousState:         defaultConfig.previousState,
		IsResume:              defaultConfig.isResume,
		IsQueuedDispatch:      defaultConfig.isQueuedDispatch,
		RunProfile:            defaultConfig.runProfile,
		Images:                defaultConfig.images,
		ChannelContext:        defaultConfig.channelContext,
		ChannelContextRefs:    defaultConfig.channelContextRefs,
		ReplyRef:              defaultConfig.replyRef,
		Title:                 defaultConfig.title,
	}

	response, err := handleConversationRequest(ctx, agentRequest, agent, sessionId, defaultConfig.source)

	if defaultConfig.source == ConversationSourceInstantNotification || defaultConfig.source == ConversationSourceInvestigation {
		sendReplyToNotificationServer(ctx, agentRequest, response, err)
	}

	return response, err
}

func shouldSkipQueuedDispatch(conversationStatus, messageStatus ConversationStatus) bool {
	return conversationStatus != ConversationStatusInProgress || messageStatus != ConversationStatusInProgress
}

func shouldCleanupExistingMessage(queuedDispatch bool, status ConversationStatus, messageType MessageType) bool {
	// A claimed queue row is fresh. Recovery cleanup would reset its status and
	// could resurrect it if cancellation committed after the worker read it.
	return !queuedDispatch && status != ConversationStatusWaiting && status != ConversationStatusWaitingForClientTool && messageType != MessageTypeFollowup
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
// conversationModelUnchanged reports whether a turn's blanket model pick is
// what the conversation already stores, so writing it again would change
// nothing. The chat UI sends the picked model with every message; without this
// each message rewrote the row and told every replica to drop its cached copy.
// Anything else (a reset, tier picks, a new config source, stored tier picks
// the blanket write would clear) still goes through the write.
func conversationModelUnchanged(conv Conversation, llmProvider, llmModel string, llmTierOverrides llmruntime.ConversationTierOverrides, llmConfigSource string, llmConfigReset bool) bool {
	if llmConfigReset || llmTierOverrides.HasAny() || llmProvider == "" || llmModel == "" {
		return false
	}
	if conv.LlmTierOverrides.HasAny() {
		return false
	}
	if llmConfigSource != "" && lo.FromPtr(conv.LlmConfigSource) != llmConfigSource {
		return false
	}
	return lo.FromPtr(conv.LlmProvider) == llmProvider && lo.FromPtr(conv.LlmModel) == llmModel
}

func applyConversationModelConfig(ctx *security.RequestContext, dao IConversationDao, conversationId, llmProvider, llmModel string, llmTierOverrides llmruntime.ConversationTierOverrides, llmConfigSource string, llmConfigReset bool) {
	// One override-cache broadcast per call, however many columns changed.
	changed := false
	defer func() {
		if changed {
			InvalidateConversationOverrideCache(conversationId)
		}
	}()

	// A reset drops everything and takes no other write: the picker sends it
	// only when nothing is selected, so there is nothing to persist after it.
	if llmConfigReset {
		if uerr := dao.ClearConversationModelConfig(conversationId); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to clear llm model configuration", "error", uerr)
			return
		}
		changed = true
		ctx.GetLogger().Info("conversation: cleared sticky llm model configuration")
		return
	}

	if llmConfigSource != "" {
		if uerr := dao.UpdateConversationConfigSource(conversationId, llmConfigSource); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to update pinned llm config source", "error", uerr)
		} else {
			changed = true
			ctx.GetLogger().Info("conversation: updated sticky llm config source", "config_source", llmConfigSource)
		}
	}

	switch {
	case llmTierOverrides.HasAny():
		if uerr := dao.UpdateConversationTierOverrides(conversationId, llmTierOverrides); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to update per-tier model configuration", "error", uerr)
			return
		}
		changed = true
		ctx.GetLogger().Info("conversation: updated sticky per-tier model configuration", "tier_count", len(llmTierOverrides.Picks))
	case llmProvider != "" && llmModel != "":
		if uerr := dao.UpdateConversationModelBlanket(conversationId, llmProvider, llmModel); uerr != nil {
			ctx.GetLogger().Warn("conversation: failed to update sticky model configuration", "error", uerr)
			return
		}
		changed = true
		ctx.GetLogger().Info("conversation: updated sticky blanket model configuration", "provider", llmProvider, "model", llmModel)
	}
}

// droppedTierPick records a per-tier pick that could not be used, so the caller
// can log it. Silent discards are what let a benchmark run report the filters a
// user chose while executing something else entirely.
type droppedTierPick struct {
	Tier     string
	Provider string
	Model    string
}

// tierOverridesFromPicks converts the wire shape into the resolver's, keeping a
// pick that names a config source OR a complete provider+model pair.
//
// A source alone is sufficient: resolveFromPinnedSource reads provider, model,
// endpoint and key off the slot, and tierPinFor tolerates an empty model. That
// is exactly how "use this config's own model" is encoded — requiring
// provider AND model discarded it, so those tiers silently fell back to account
// defaults. Guessing a provider from the config's first model is not a fix: on a
// mixed config that row can belong to a different provider than the base slot.
func tierOverridesFromPicks(picks map[string]toolcore.TierModelPick) (llmruntime.ConversationTierOverrides, []droppedTierPick) {
	var overrides llmruntime.ConversationTierOverrides
	var dropped []droppedTierPick
	for tier, p := range picks {
		if p.ConfigSource == "" && (p.Provider == "" || p.Model == "") {
			dropped = append(dropped, droppedTierPick{Tier: tier, Provider: p.Provider, Model: p.Model})
			continue
		}
		if overrides.Picks == nil {
			overrides.Picks = make(map[string]llmruntime.TierModelPick)
		}
		overrides.Picks[tier] = llmruntime.TierModelPick{Provider: p.Provider, Model: p.Model, ConfigSource: p.ConfigSource}
	}
	return overrides, dropped
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
	// Raw values live only in the accumulator's in-memory map for the
	// duration of this turn. The consolidated event persists a per-value
	// breakdown (category, source, tool, KB origin, length, character-class
	// shape) but NOT the values themselves — unless the testing-only
	// EGRESSFILTER_REVEAL_VALUES feature flag is enabled for the tenant, which writes them
	// verbatim and must never be enabled in production.
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

	llmTierOverrides, droppedTiers := tierOverridesFromPicks(request.QueryConfig.LlmTierModels)
	for _, d := range droppedTiers {
		ctx.GetLogger().Warn("conversation: ignoring incomplete per-tier LLM pick — needs a config source, or both provider and model",
			"tier", d.Tier, "provider", d.Provider, "model", d.Model)
	}
	if llmTierOverrides.HasAny() {
		ctx.GetLogger().Info("conversation: overriding per-tier models from request config", "tier_count", len(llmTierOverrides.Picks))
	}

	newConversation := false
	turnPolicyApplied := false
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

			// Before anything is written for this turn, the follow-up resume
			// below included.
			// The run's profile comes with the row: every later step of this
			// turn, and every agent it starts, is governed by its owner. An
			// agent bound to another extension is refused before anything is
			// written for the turn.
			if err := CheckAgentInRun(agent, conversation.RunProfile); err != nil {
				return NBAgentResponse{}, err
			}
			ctx = WithRunProfile(ctx, conversation.RunProfile)
			ctx, err = policyFor(ctx).ConversationTurn(ctx, agent, conversation, ActingUser(ctx, request.UserId))
			if err != nil {
				return NBAgentResponse{}, err
			}
			turnPolicyApplied = true

			if request.IsQueuedDispatch {
				// Reject a superseded claim before writing sticky model settings.
				claimed, loadErr := GetConversationDao().GetConversationMessage(request.MessageId, request.AccountId, request.ConversationId)
				if loadErr != nil {
					return NBAgentResponse{}, loadErr
				}
				if shouldSkipQueuedDispatch(conversation.Status, claimed.Status) {
					return NBAgentResponse{Status: claimed.Status, ConversationId: request.ConversationId, MessageId: request.MessageId}, nil
				}
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
			if !conversationModelUnchanged(conversation, llmProvider, llmModel, llmTierOverrides, request.QueryConfig.LlmConfigSource, request.QueryConfig.LlmConfigReset) {
				applyConversationModelConfig(ctx, GetConversationDao(), request.ConversationId, llmProvider, llmModel, llmTierOverrides, request.QueryConfig.LlmConfigSource, request.QueryConfig.LlmConfigReset)
			}

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
			// Refused before the conversation is written: an agent bound to an
			// extension starts only that extension's runs, from every entry
			// point (chat, auto chat, queued dispatch, event analysis).
			if err := CheckAgentInRun(agent, request.RunProfile); err != nil {
				return NBAgentResponse{}, err
			}
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
			if request.Title != "" {
				title = request.Title
			} else if derived, ok := common.DeriveTitleFromJSONQuery(initialTitleSource); ok {
				// Webhook/alert triggers send a JSON payload as the query; unwrap it to
				// a readable field so the placeholder isn't the raw `{"description":...`
				// envelope. Matters most for single-shot classifiers (e.g.
				// webhook_subject_name_extractor), which skip async title generation and
				// keep this placeholder as their final, user-facing title.
				title = derived
			} else if initialTitleSource != "" {
				words := strings.Fields(initialTitleSource)
				if len(words) > 5 {
					title = strings.Join(words[:5], " ") + "..."
				} else {
					title = initialTitleSource
				}
			} else {
				title = DefaultConversationTitle
			}
			var saveTierOverrides *llmruntime.ConversationTierOverrides
			if llmTierOverrides.HasAny() {
				to := llmTierOverrides
				saveTierOverrides = &to
				llmProvider = ""
				llmModel = ""
			}
			id, err := GetConversationDao().SaveConversation(request.ConversationId, sessionId, tenantId, request.AccountId, request.UserId, "", title, ConversationStatusInProgress, source, llmProvider, llmModel, saveTierOverrides, request.QueryConfig.LlmConfigSource, request.RunProfile)
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

		if !turnPolicyApplied {
			if err := CheckAgentInRun(agent, conversation.RunProfile); err != nil {
				return NBAgentResponse{}, err
			}
			ctx = WithRunProfile(ctx, conversation.RunProfile)
			ctx, err = policyFor(ctx).ConversationTurn(ctx, agent, conversation, ActingUser(ctx, request.UserId))
			if err != nil {
				return NBAgentResponse{}, err
			}
		}

		// Handle existing conversation found by session ID - update model if needed
		if !newConversation && llmProvider != "" && llmModel != "" &&
			!conversationModelUnchanged(conversation, llmProvider, llmModel, llmruntime.ConversationTierOverrides{}, "", false) {
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
		parentContext = context.WithValue(parentContext, llmruntime.ContextKeyLlmProviderOverride, request.QueryConfig.LlmProvider)
	}
	if request.QueryConfig.LlmModelName != "" {
		parentContext = context.WithValue(parentContext, llmruntime.ContextKeyLlmModelOverride, request.QueryConfig.LlmModelName)
	}
	if llmTierOverrides.HasAny() {
		parentContext = context.WithValue(parentContext, llmruntime.ContextKeyLlmTierModelOverrides, llmTierOverrides)
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
		parentContext = context.WithValue(parentContext, llmruntime.ContextKeyLlmConfigSourceOverride, effectiveConfigSource)
	}

	ctx = security.NewRequestContext(
		parentContext,
		ctx.GetSecurityContext(),
		ctx.GetLogger().With("account_id", request.AccountId, "conversation_id", request.ConversationId),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	// Establish reporting attribution before saving/activating a message, so a
	// history-read failure cannot strand newly accepted work in IN_PROGRESS.
	// This also precedes contextualization and agent execution.
	// The executor's context copies and child agents inherit this same scope.
	// Include stored context because refinement can resend it independently of
	// the recent-message window. Security still scans every outbound payload.
	reportBaseline := ""
	if !newConversation {
		reportHistory, err := getExistingHistory(request, ctx)
		if err != nil {
			return NBAgentResponse{}, fmt.Errorf("conversation: load history for egress reporting baseline: %w", err)
		}
		reportBaseline = messageFormatterToString(reportHistory)
	}
	if len(conversation.Context) > 0 && !request.QueryConfig.SkipConversationHistory {
		contextJSON, err := common.MarshalJson(conversation.Context)
		if err != nil {
			return NBAgentResponse{}, fmt.Errorf("conversation: encode egress reporting baseline: %w", err)
		}
		reportBaseline += "\n" + string(contextJSON)
		// Match the executor's human-readable memory too: JSON escapes
		// multiline values that appear literally in the outbound prompt.
		var unified LlmUnifiedExtraction
		if err := common.UnmarshalJson(contextJSON, &unified); err == nil {
			reportBaseline += "\n" + unified.String()
		}
	}
	ctx.SetContext(egressfilter.WithReportBaseline(ctx.GetContext(), reportBaseline, request.Query))

	historyType := MessageTypeGeneration
	status := ConversationStatusCompleted
	if agent.GetName() == RouterAgentName {
		historyType = MessageTypeRoute
	}

	messageStatus := ConversationStatusCompleted
	messageType := MessageTypeGeneration

	// Saved with the message so a recovered run stays read-only.
	if IsReadOnlyRun(ctx) {
		request.QueryConfig.ReadOnlyRun = true
	}
	// Before the message's stored config is restored below: only the caller's own config is dropped.
	dropCallerApprovalsInAlertChat(ctx, request.AccountId, conversation.SessionID, &request.QueryConfig)

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
					if v.Status == ConversationStatusQueued || v.MessageType != string(MessageTypeGeneration) {
						continue
					}
					if v.MessageConfig != nil && *v.MessageConfig != "" {
						prevConfig := toolcore.NBQueryConfig{}
						err := common.UnmarshalJson([]byte(*v.MessageConfig), &prevConfig)
						if err != nil {
							ctx.GetLogger().Error("conversation: unable to unmarshal message config from previous message", "error", err, "data", *v.MessageConfig, "id", v.ID)
						}
						request.QueryConfig.MergeFrom(inheritableMessageConfig(prevConfig))
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
			if res, err := llmruntime.ResolveLLMConfig(ctx, request.AccountId, agent.GetName(), request.ConversationId); err == nil {
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

		// Saved with the message so an approval resume, which rebuilds the request from it, saves nothing either.
		if !isChatOrAlertSource(request.ConversationSource) {
			request.QueryConfig.Memory = withMemoryWriteOff(request.QueryConfig.Memory)
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
			dao := attachments.GetAttachmentDAO()
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

		if request.IsQueuedDispatch && shouldSkipQueuedDispatch(conversation.Status, message.Status) {
			// Stop/force may have superseded this claim while the worker waited.
			return NBAgentResponse{Status: message.Status, ConversationId: request.ConversationId, MessageId: request.MessageId}, nil
		}

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
		if shouldCleanupExistingMessage(request.IsQueuedDispatch, message.Status, messageType) {
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
	// summary_agent title generation for them. A request that set its own title
	// keeps it.
	if request.Query != "" && newConversation && request.Title == "" && !isSingleShotClassifier(agent) {
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
	// Carried in the config so delegated sub-agents, which do not inherit the source, save nothing either.
	if !isChatOrAlertSource(request.ConversationSource) {
		request.QueryConfig.Memory = withMemoryWriteOff(request.QueryConfig.Memory)
	}

	// Generate acknowledgment for new messages (not for retries or existing messages)
	// TO Do move this to async task
	// Single-shot internal classifiers have no user watching for an acknowledgment,
	// so skip the acknowledgment_agent call for them.
	if messageStatus != ConversationStatusWaiting && messageType != MessageTypeFollowup && agent.GetName() != RouterAgentName && agent.GetName() != llmruntime.ToolLlm && request.Query != "" && !isSingleShotClassifier(agent) {
		processAcknowledgmentAsync(ctx, request.AccountId, request.Query, agent.GetName(), conversation.ID.String(), request.MessageId, request.UserId, request.ConversationSource)
	}

	// Add context-aware query processing before executing the agent
	// When ConversationContextEnabled is true, skip query rewriting — the agent LLM resolves
	// references naturally from the increased history window + summary context.
	// Also skip if we are resuming an agent (request.AgentId is set) to prevent feedback turns from being rewritten.
	if !config.Config.ConversationContextEnabled && (conversation.Context != nil) && !request.QueryConfig.SkipConversationHistory && request.EnableQueryRefinement && messageStatus != ConversationStatusWaiting && request.Query != "" && messageType != MessageTypeFollowup && agent.GetName() != RouterAgentName && agent.GetPlannerType() != AgentPlannerTypeCustom && request.AgentId == "" {
		contextualizedQuery, err := GetContextManager().ContextualizeQuery(ctx, request, conversation.Context)
		if err == nil && contextualizedQuery != "" && contextualizedQuery != request.Query {
			ctx.GetLogger().Info("conversation: query contextualized", "original", request.Query, "contextualized", contextualizedQuery)
			request.Query = contextualizedQuery
		}
	}

	// Set the conversation context in the request
	if len(conversation.Context) > 0 && !request.QueryConfig.SkipConversationHistory {
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

	// "/skill-name question" puts that skill in front of the question.
	var commandSkills []knowledge.SkillCommand
	request, commandSkills = evalSkillCommand(ctx, request)
	saveSkillCommandReferences(ctx, request, conversation.ID.String(), commandSkills)

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
		// Before anything reads or saves the answer, so the saved, returned and
		// notified copies all carry the charts.
		resolveAnswerCharts(request.ConversationId, agentResponse.Response)
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
		// This string becomes the assistant's persisted reply, so it is the last
		// place a raw internal error can reach the user. executor.go sanitizes the
		// planner-construction path, but an error raised once the planner is
		// running (config resolved lazily at the first LLM call, provider refusals)
		// arrives here instead and was previously stored verbatim — account UUIDs
		// and internal agent names included.
		agentResponseContent = sanitizeErrorForUser(executeErr)
		status = ConversationStatusFailed
	}

	// After getting the agent response, update the context asynchronously.
	// Single-shot internal classifiers use a throwaway conversation per call, so
	// there is nothing to carry forward — skip context_memories_extractions.
	// Headless automated pipelines (alert investigations, PR lifecycle bots, workflow
	// automations, and recommendation generators) also never reuse conversational context.
	effectiveSource := source
	if conversation.Source != nil && *conversation.Source != "" {
		effectiveSource = *conversation.Source
	} else if effectiveSource == "" {
		effectiveSource = request.ConversationSource
	}
	// Before the answer is saved, returned or sent, so every copy carries the
	// same record of which automations ran. A paused turn's answer is a question
	// for the user; its record goes on the answer that finishes the turn.
	runReceipt := ""
	if executeErr == nil && status != ConversationStatusWaiting && status != ConversationStatusWaitingForClientTool {
		runReceipt = applyRunReceipt(ctx, request.AccountId, request.MessageId, effectiveSource, agentResponse.Response)
		if runReceipt != "" {
			agentResponseContent = agentResponse.Response[0]
		}
	}
	if len(agentResponse.Response) > 0 &&
		agentResponse.AgentName != RouterAgentName &&
		agentResponse.AgentName != llmruntime.ToolLlm &&
		!isSingleShotClassifier(agent) &&
		isContextExtractionApplicable(effectiveSource) {
		updateConversationContextAsync(ctx, request, toolcore.StripChartData(agentResponseContent), conversation.ID.String(), conversation.Context, agentResponse.AgentName)
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
		if config.Config.QueuedMessagesEnabled {
			var applied bool
			applied, err = GetConversationDao().FinalizeConversationMessage(request.ConversationId, request.MessageId, agentResponseContent, status)
			if err == nil && !applied {
				return NBAgentResponse{ConversationId: request.ConversationId, MessageId: request.MessageId, Status: ConversationStatusTerminated}, nil
			}
		} else {
			err = GetConversationDao().UpdateConversationMessage(request.MessageId, agentResponseContent, status)
		}
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

		if !config.Config.QueuedMessagesEnabled {
			err = GetConversationDao().UpdateConversationStatus(request.ConversationId, status)
		}
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

		if status == ConversationStatusCompleted && agentResponse.AgentName != llmruntime.ToolLlm {
			computeProductivityMetricsAsync(ctx, request.UserId, request.AccountId, request.ConversationId, request.MessageId)
			// Post-hoc answer grading. Runs only after the answer row is
			// committed above, so the grader reads the same text the user sees,
			// and is fire-and-forget — the turn is already complete and its
			// status does not depend on the outcome. Self-gates on the feature
			// flag and on the turn being an investigation.
			ScoreAnswerConfidenceAsync(ctx, request, toolcore.StripChartData(agentResponseContent))
		}

		if status == ConversationStatusCompleted || status == ConversationStatusFailed {
			dispatchNextQueuedMessage(ctx, agent, conversation)
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
	if agentResponse.FollowupRequest.Question == "" && agentResponse.Status != ConversationStatusWaiting && agentResponse.Status != ConversationStatusWaitingForClientTool && agent.GetName() != RouterAgentName && agentResponse.AgentName != llmruntime.ToolLlm && !isSingleShotClassifier(agent) {
		tools := agent.GetSupportedTools(ctx)
		evaluateAgentResponseAsync(ctx, agentResponse, request.AccountId, tools, request.UserId)
	}

	// Post-process response if the agent supports it
	if handler, ok := agent.(NBAgentResponseHandler); ok {
		agentResponse = handler.PostProcessResponse(ctx, request, agentResponse)
	}

	// Generate compact response for InstantNotification source (Slack) and Investigation
	if config.Config.SlackCompactResponse && (effectiveSource == ConversationSourceInstantNotification || effectiveSource == ConversationSourceInvestigation) && status == ConversationStatusCompleted && len(agentResponse.Response) > 0 && agentResponse.Response[0] != "" && agentResponse.AgentName != llmruntime.ToolLlm {
		// The rewrite model sees chart placeholders, not chart data, and the charts
		// are put back after: a model must never retype chart numbers.
		// Nor does it see the run record, which goes back on unchanged: a
		// rewrite must not reword what ran.
		compactResponse, compactErr := generateCompactResponse(ctx, request.AccountId, request.ConversationId, request.MessageId, request.UserId, request.Query, toolcore.StripChartData(withoutRunReceipt(runReceipt, agentResponse.Response[0])))
		if compactErr == nil {
			compactResponse = toolcore.RestoreCharts(compactResponse, agentResponse.Response[0])
			if runReceipt != "" {
				compactResponse = joinRunReceipt(runReceipt, compactResponse)
			}
		}

		if compactErr != nil {
			ctx.GetLogger().Warn("conversation: failed to generate compact response, using original", "error", compactErr)
		} else {
			// For Investigation source, preserve the original structured response (used by event_analyzer
			// for structured field parsing like git_diff, pr_list, etc.) and only save compact to DB.
			// For Slack/InstantNotification, replace the response with compact version for display.
			if effectiveSource != ConversationSourceInvestigation {
				agentResponse.Response = []string{compactResponse}
			}
			// Save compact response to DB asynchronously
			ctx.GetLogger().Info("conversation: saving compact response asynchronously")
			saveCompactResponseAsync(ctx, request.ConversationId, request.MessageId, compactResponse)
		}
	}

	if executeErr != nil {
		// Set only on the returned value, after the context update, so the failure
		// text never becomes the conversation's last answer.
		agentResponse.Response = []string{agentResponseContent}
	}
	return agentResponse, executeErr
}

// resolveAnswerCharts swaps the chart_refs in an answer for the charts the metrics
// tools saved with their tool calls in this conversation (#40909), in place.
func resolveAnswerCharts(conversationID string, response []string) {
	if len(response) == 0 {
		return
	}
	response[0] = toolcore.ResolveMetricCharts(response[0], func(ref string) ([]byte, bool) {
		chart, err := GetConversationDao().GetToolCallChart(conversationID, ref)
		if err != nil {
			slog.Warn("conversation: unable to load chart", "error", err, "conversation_id", conversationID, "chart_ref", ref)
		}
		return chart, len(chart) > 0
	})
}

var getSkillsForCommandFn = knowledge.GetSkillsForCommand

// maxSkillMentions caps how many "/name" words in one message are looked up.
const maxSkillMentions = 3

// skillMention is one "/name" or "/call name" in a message: text[start:end].
type skillMention struct {
	start, end int
	name       string
}

func isMentionSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// readSkillWord reads a skill-name word at text[i:] (a letter, then letters,
// digits, "_", "-" or ":"), dropping a trailing ":". Returns "" when none.
func readSkillWord(text string, i int) (string, int) {
	j := i
	for j < len(text) {
		c := text[j]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if isLetter || (j > i && ((c >= '0' && c <= '9') || c == '_' || c == '-' || c == ':')) {
			j++
			continue
		}
		break
	}
	for j > i && text[j-1] == ':' {
		j--
	}
	return text[i:j], j
}

// endsMention reports whether a mention may end at text[end]: the end of the
// text, a space, or punctuation that is itself followed by a space or the end
// (so "/pod-triage." counts but "/pod-triage.md" does not).
func endsMention(text string, end int) bool {
	if end >= len(text) || isMentionSpace(text[end]) {
		return true
	}
	if !strings.ContainsRune(",;:.!?)", rune(text[end])) {
		return false
	}
	return end+1 >= len(text) || isMentionSpace(text[end+1]) || strings.ContainsRune(",;:.!?)", rune(text[end+1]))
}

// findSkillMentions returns the first maxSkillMentions "/name" words of text
// that start a word and end at a space, punctuation or the end of the text,
// so a path such as /var/log never counts. "/call name" counts as "name".
func findSkillMentions(text string) []skillMention {
	var out []skillMention
	for i := 0; i < len(text) && len(out) < maxSkillMentions; i++ {
		if text[i] != '/' || (i > 0 && !isMentionSpace(text[i-1])) {
			continue
		}
		name, end := readSkillWord(text, i+1)
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "call") {
			j := end
			for j < len(text) && isMentionSpace(text[j]) {
				j++
			}
			if next, nextEnd := readSkillWord(text, j); next != "" && j > end {
				name, end = next, nextEnd
			}
		}
		if !endsMention(text, end) {
			i = end
			continue
		}
		out = append(out, skillMention{start: i, end: end, name: name})
		i = end - 1
	}
	return out
}

// evalSkillCommand handles "/skill-name" (or "/call skill-name") words in a
// message: each one that names a skill the account can see puts that skill's
// text ahead of the message. A mention that opens the message is dropped; one
// further in is replaced by the skill's name so the sentence still reads.
// Words that name no skill (a path, say) are left as typed. A leading
// "@agent" mention is kept in front. Returns the skills it loaded.
func evalSkillCommand(ctx *security.RequestContext, request NBAgentRequest) (NBAgentRequest, []knowledge.SkillCommand) {
	query := strings.TrimSpace(request.Query)
	_, rest := common.ParseAgentMention(query)
	prefix := query[:len(query)-len(rest)]

	mentions := findSkillMentions(rest)
	if len(mentions) == 0 {
		return request, nil
	}
	names := make([]string, len(mentions))
	for i, m := range mentions {
		names[i] = m.name
	}
	skills, err := getSkillsForCommandFn(ctx, request.AccountId, names)
	if err != nil {
		ctx.GetLogger().Warn("conversation: unable to look up skill commands", "skills", names, "error", err)
		return request, nil
	}

	// Skills named back to back at the very start are dropped, not replaced.
	leading := make([]bool, len(mentions))
	from := 0
	for i, m := range mentions {
		_, ok := skills[strings.ToLower(m.name)]
		if !ok || strings.TrimSpace(rest[from:m.start]) != "" {
			break
		}
		leading[i], from = true, m.end
	}

	// One block per skill, in the order the message first names them.
	var blocks []string
	var used []knowledge.SkillCommand
	loaded := map[string]bool{}
	for _, m := range mentions {
		skill, ok := skills[strings.ToLower(m.name)]
		if !ok || loaded[skill.ID] {
			continue
		}
		loaded[skill.ID] = true
		used = append(used, skill)
		body := strings.TrimSpace(skill.Data)
		if utf8.RuneCountInString(body) > knowledge.SkillCommandMaxChars {
			body = body[:runeByteOffset(body, knowledge.SkillCommandMaxChars)] +
				fmt.Sprintf("\n[Cut at %d characters. Load the skill %q to read the rest.]", knowledge.SkillCommandMaxChars, skill.Name)
		}
		blocks = append(blocks, fmt.Sprintf("<skill name=%q>\n%s\n</skill>", skill.Name, body))
		ctx.GetLogger().Info("conversation: skill command", "skill", skill.Name, "kb_id", skill.ID)
	}

	// Edit the message from the end, so earlier offsets stay valid.
	question := rest
	for i := len(mentions) - 1; i >= 0; i-- {
		m := mentions[i]
		skill, ok := skills[strings.ToLower(m.name)]
		if !ok {
			continue
		}
		replacement := ""
		if !leading[i] {
			replacement = fmt.Sprintf("%q", skill.Name)
		}
		question = question[:m.start] + replacement + question[m.end:]
	}
	if len(blocks) == 0 {
		return request, nil
	}
	text := strings.Join(blocks, "\n\n")
	// A skill dropped from the start can leave its punctuation behind ("/x, then ...").
	if question = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(question), ",;:")); question != "" {
		text += "\n\n" + question
	}
	request.Query = prefix + text
	return request, used
}

// saveSkillCommandReferences records the skills a "/skill-name" message loaded
// on that message, like a load_skills call does, so they are listed with the
// answer's contexts. Best-effort: a failed save never fails the turn.
func saveSkillCommandReferences(ctx *security.RequestContext, request NBAgentRequest, conversationId string, skills []knowledge.SkillCommand) {
	if len(skills) == 0 || request.MessageId == "" || conversationId == "" {
		return
	}
	refs := make([]AgentReference, 0, len(skills))
	for _, skill := range skills {
		refs = append(refs, AgentReference{
			Type:        AgentReferenceTypeKB,
			ReferenceID: skill.ID,
			Metadata:    map[string]any{"kind": AgentReferenceKindSkill, "via": "skill_command", "name": skill.Name},
		})
	}
	if err := saveSkillCommandReferencesFn(request.AccountId, conversationId, request.MessageId, uuid.Nil.String(), refs); err != nil {
		ctx.GetLogger().Error("conversation: failed to save skill command references", "error", err, "message_id", request.MessageId)
	}
}

var saveSkillCommandReferencesFn = func(accountId, conversationId, messageId, agentId string, refs []AgentReference) error {
	return GetConversationDao().SaveAgentReferences(accountId, conversationId, messageId, agentId, refs)
}

func HandleConversationMessageRequest(ctx *security.RequestContext, accountId, conversationId, messageId string, resumeAgentId ...string) (NBAgentResponse, error) {
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

	// A recovered alert investigation still has no person to approve changes.
	if messageRanReadOnly(conversationMessage) {
		ctx = WithReadOnlyRun(ctx)
	}

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

// dropCallerApprovalsInAlertChat drops the approvals a caller's config carries (tool_confirmations) in an alert's
// conversation with the remediation loop on, or with the flag unread: there a "yes" decides a change on the alert,
// and it counts only as the answer to its follow-up, which this server records (refineAgentQuestionAndHandleFollowups)
// after checking who may answer it. Any other conversation is as before.
func dropCallerApprovalsInAlertChat(ctx *security.RequestContext, accountId, sessionId string, cfg *toolcore.NBQueryConfig) {
	if len(cfg.ToolConfirmations) == 0 {
		return
	}
	if _, alert := events.AlertFingerprintOfSession(sessionId); !alert {
		return
	}
	on, err := events.RemediationLoopRead(accountId)
	if err == nil && !on {
		return
	}
	ctx.GetLogger().Warn("conversation: approvals sent with the request dropped in an alert's conversation", "session_id", sessionId, "count", len(cfg.ToolConfirmations), "flag_error", err)
	cfg.ToolConfirmations = nil
}

// inheritableMessageConfig is the part of the previous message's config a new
// message carries over. Skipping history and read-only belong to the turn that
// asked for them: a user's follow-up after an automated re-analysis needs the
// history and may ask for a change. Approvals belong to the request they were
// given in: a "yes" to one change must not run a different change on the same
// cluster in a later message.
func inheritableMessageConfig(prev toolcore.NBQueryConfig) toolcore.NBQueryConfig {
	prev.SkipConversationHistory = false
	prev.ReadOnlyRun = false
	prev.StrictReadOnly = false
	prev.RemediationPlanTurn = ""
	prev.InvestigationRunId = ""
	prev.ToolConfirmations = nil
	prev.ToolConfirmationRefs = nil
	return prev
}
