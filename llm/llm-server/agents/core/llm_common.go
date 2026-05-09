package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tmc/langchaingo/llms"
)

// Wire LLM provider integration config invalidation into the shared tool
// cache invalidation chain so it runs whenever an integration mutation
// notifies llm-server. Without this registration the helper below was
// defined but never called — admins rotating LLM provider keys would see
// stale credentials for up to llmIntegrationConfigCacheTTL.
func init() {
	toolcore.RegisterToolCacheInvalidator(InvalidateLLMIntegrationConfigCache)
}

const agentScratchpad = "agent_scratchpad"
const ToolLlm = "LLM"

// ThinkingLevelFastTask is used for lightweight LLM calls (title generation,
// summarization, formatting, classification) where deep reasoning is not needed.
const ThinkingLevelFastTask = "low"

var codeBlockPrefixRegex = regexp.MustCompile("^```[a-zA-Z]*\n")
var (
	llmIntegrationConfigCache = make(map[string]struct {
		config map[string]string
		ts     time.Time
	})
	llmIntegrationConfigCacheMutex sync.RWMutex
	llmIntegrationConfigCacheTTL   = 30 * time.Minute

	// Separate cache for tenant-level LLM configs (keyed by tenantId).
	// Avoids duplicating the same tenant config for every account under the tenant.
	llmTenantConfigCache = make(map[string]struct {
		config map[string]string
		ts     time.Time
	})
	llmTenantConfigCacheMutex sync.RWMutex

	modelSemaphores sync.Map // map[string]chan struct{}
)

func getLlmSemaphore(accountId, provider, model string) chan struct{} {
	// Key pattern: accountId:provider:model.
	// This ensures accounts with independent API keys have separate concurrency pools.
	key := fmt.Sprintf("%s:%s:%s", accountId, provider, model)
	if sem, ok := modelSemaphores.Load(key); ok {
		return sem.(chan struct{})
	}

	// Use the hierarchical configuration resolution for concurrency limits, passing the accountId
	limit := GetLLMModelIntConfig(accountId, provider, model, "llm_server_max_concurrent_llm_calls", 20)

	// Atomic creation to prevent race conditions during initialization
	sem, _ := modelSemaphores.LoadOrStore(key, make(chan struct{}, limit))
	return sem.(chan struct{})
}

type LlmConcurrencyStats struct {
	MaxConcurrent int `json:"max_concurrent"`
	CurrentInUse  int `json:"current_in_use"`
}

func GetLlmConcurrencyStats() map[string]LlmConcurrencyStats {
	stats := make(map[string]LlmConcurrencyStats)
	modelSemaphores.Range(func(key, value any) bool {
		sem := value.(chan struct{})
		stats[key.(string)] = LlmConcurrencyStats{
			MaxConcurrent: cap(sem),
			CurrentInUse:  len(sem),
		}
		return true
	})
	return stats
}

func InvalidateLLMIntegrationConfigCache(accountId string) {
	slog.Info("Invalidating LLM integration config cache for account", "accountId", accountId)
	llmIntegrationConfigCacheMutex.Lock()
	delete(llmIntegrationConfigCache, accountId)
	llmIntegrationConfigCacheMutex.Unlock()

	// Also invalidate tenant-level cache and all sibling accounts that may be
	// using the tenant-level fallback config.
	if tenantId, err := security.GetTenantIdFromAccountId(accountId); err == nil && tenantId != "" {
		llmTenantConfigCacheMutex.Lock()
		delete(llmTenantConfigCache, tenantId)
		llmTenantConfigCacheMutex.Unlock()

		// Clear account-level cache entries for all accounts under this tenant
		// so they re-resolve on next call (they may have cached the old tenant config).
		if siblingAccounts, err := security.GetAccountIdsForTenant(tenantId); err == nil {
			llmIntegrationConfigCacheMutex.Lock()
			for _, siblingId := range siblingAccounts {
				if siblingId != accountId {
					delete(llmIntegrationConfigCache, siblingId)
				}
			}
			llmIntegrationConfigCacheMutex.Unlock()

			// Invalidate LLM client cache for all sibling accounts
			for _, siblingId := range siblingAccounts {
				InvalidateLLMClientCache(siblingId)
			}
		}
	}

	// Also invalidate the client cache for this account
	InvalidateLLMClientCache(accountId)
}

type LLMContextKey string

const (
	ContextKeyLlmProviderOverride LLMContextKey = "llm_provider_override"
	ContextKeyLlmModelOverride    LLMContextKey = "llm_model_override"
	ContextKeyUseLiteModel        LLMContextKey = "use_lite_model"
	// ContextKeyDisableCaching disables provider-level prompt caching for the current
	// request subtree. Set to true for AgentPlannerTypeCustom agents whose LLM calls
	// embed dynamic content (query text, log data) directly in the system message,
	// making caching wasteful. Individual custom agents that know a specific call has
	// a stable system prompt can create a sub-context that overrides this to false.
	ContextKeyDisableCaching LLMContextKey = "disable_caching"
	ContextKeyCacheScope     LLMContextKey = "cache_scope"
	// ContextKeyCapabilities carries the request's Capabilities map into the LLM call
	// stack so the cache layer can fingerprint capability-constrained requests and give
	// them distinct Google AI CachedContent slots.
	ContextKeyCapabilities LLMContextKey = "capabilities"
)

// TokenInfo contains detailed token breakdown including cache information
type TokenInfo struct {
	InputTokens         int // Total input tokens sent to the model
	OutputTokens        int // Total output tokens received from the model
	CacheReadTokens     int // Input tokens that were served from a cache
	CacheCreationTokens int // Input tokens used to create a new cache entry
	TotalTokens         int // Total tokens used (input + output)
}

// LLMCallMetadata contains metadata about the LLM call for token tracking
type LLMCallMetadata struct {
	LatencySeconds    float64
	RetryAttempt      int
	FallbackFromModel *string  // Immediate previous model
	FallbackChain     []string // Full chain of fallback models
	RequestStatus     string   // 'success' or 'failure'
	ErrorMessage      *string
	CacheInfo         *CacheResponse // Cache information from last successful call
}

type retryContext struct {
	ctx                      *security.RequestContext
	llm                      llms.Model
	promptMessages           []llms.MessageContent
	options                  []llms.CallOption
	agentId                  string
	agentName                string
	currentModel             string
	currentProvider          string
	triedModels              map[string]bool
	lastErr                  error
	errorHistory             []string // Track all errors encountered
	accountId                string
	conversationId           string
	messageId                string   // Track messageId for token tracking
	lastLatency              float64  // Track latency of last LLM call
	attemptCount             int      // Track number of attempts made
	fallbackFromModel        *string  // Track immediate previous model we fell back from
	fallbackChain            []string // Track full chain of models tried (for multiple fallbacks)
	userId                   string
	lastCacheInfo            *CacheResponse // Track cache info from last LLM call
	cacheScope               CacheScope
	capabilities             map[string]any // Forwarded to CacheRequest for capability fingerprinting
	totalStart               time.Time      // Track when the entire retry loop started
	maxTotalDuration         time.Duration  // Maximum allowed time for the entire retry loop
	enableCaching            bool           // Whether provider-level prompt caching is enabled for this call
	hasMalformedFunctionCall bool           // Sticky flag: set when Gemini returns MALFORMED_FUNCTION_CALL, never cleared during retries
}

func GetLLMNumTokensFromStringMessages(context *security.RequestContext, messages []string, provider string, model string) (numTokens int) {
	var tokensPerMessage int
	for _, message := range messages {
		numTokens += tokensPerMessage
		count, err := CountTokens(provider, model, message)
		if err != nil {
			context.GetLogger().Warn("Failed to count tokens for message", "warning", err)
		}
		numTokens += count
	}
	numTokens += 3
	return numTokens
}

func GetLLMNumTokensFromMessages(context *security.RequestContext, messages []llms.MessageContent, provider string, model string) (numTokens int) {
	var tokensPerMessage int
	for _, message := range messages {
		numTokens += tokensPerMessage
		roleCount, err := CountTokens(provider, model, string(message.Role))
		if err != nil {
			context.GetLogger().Warn("Failed to count tokens for message role", "warning", err)
		}
		numTokens += roleCount
		contentCount, err := CountTokens(provider, model, fmt.Sprintf("%v", message.Parts[0]))
		if err != nil {
			context.GetLogger().Warn("Failed to count tokens for message content", "warning", err)
		}
		numTokens += contentCount
	}
	numTokens += 3
	return numTokens
}

// GenerateAndTrackLLMContent generates content using an LLM and tracks token usage
func GenerateAndTrackLLMContent(ctx *security.RequestContext, userId string, accountId string, conversationId string, messageId string, agentId string, trackContent bool, promptMessages []llms.MessageContent, cleanupMarkdown bool, options ...llms.CallOption) (*llms.ContentResponse, error) {
	t0 := time.Now()
	// Validate userid, if it's empty or nil, set to system user
	if userId == "" {
		userId = security.GetSystemUserId()
	}

	// Add streaming option to handle large responses without hitting token limits
	options = append(options, createStreamingOption())

	// Get agent name if agentId is valid UUID
	agentName := ""
	if agentUID, err := uuid.Parse(agentId); err == nil {
		agentName = getAgentNameFromAgentId(agentUID.String())
	} else if agentId != "" {
		agentName = agentId
	}

	// Check for context overrides
	overrideProvider := ""
	overrideModel := ""
	useLiteModel := false

	if val, ok := ctx.GetContext().Value(ContextKeyLlmProviderOverride).(string); ok {
		overrideProvider = val
	}
	if val, ok := ctx.GetContext().Value(ContextKeyLlmModelOverride).(string); ok {
		overrideModel = val
	}
	if val, ok := ctx.GetContext().Value(ContextKeyUseLiteModel).(bool); ok {
		useLiteModel = val
	}

	// Step 1: Resolve configuration ONCE
	res, err := ResolveLLMConfig(ctx, accountId, agentName, conversationId)
	if err != nil {
		ctx.GetLogger().Error("Failed to resolve LLM config", "error", err, "agentName", agentName)
		return nil, ErrLlmUnableToGenerate(err)
	}

	provider := res.Provider
	model := res.Model

	if overrideProvider != "" && overrideModel != "" {
		ctx.GetLogger().Info("Using overridden LLM model", "provider", overrideProvider, "model", overrideModel)
		provider = overrideProvider
		model = overrideModel
	} else if useLiteModel {
		ctx.GetLogger().Info("Using Lite LLM model for task", "agentName", agentName)
		if fastRes, err := ResolveFastLLMConfig(ctx, accountId); err == nil {
			provider = fastRes.Provider
			model = fastRes.Model
		}
	}

	// Step 2: Initialize LLM using the resolution context
	llm, err := GetLLMModel(provider, model, agentName, agentName != "", accountId, res)
	if err != nil {
		ctx.GetLogger().Error("Failed to initialize LLM model", "error", err, "agentName", agentName, "provider", provider, "model", model)
		return nil, ErrLlmUnableToGenerate(err)
	}

	// Optimize chunk size to reduce continuation attempts
	maxOutputTokens := GetLlmMaxOutputTokens(model)
	if maxOutputTokens <= 0 {
		maxOutputTokens = 4096
	}
	options = append(options, llms.WithMaxTokens(maxOutputTokens))

	// Auto-apply thinking level for models that support thinking tokens (Gemini 2.5+, Gemini 3),
	// unless the caller has already set an explicit ThinkingLevel in the options metadata.
	if !hasThinkingLevelOption(options) {
		defaultLevel := GetLlmDefaultThinkingLevel(model)
		if defaultLevel != "" {
			// Config override takes precedence if set.
			if config.Config.LlmProviderThinkingLevel != "" {
				options = append(options, WithThinkingLevel(ClampThinkingLevelForModel(model, config.Config.LlmProviderThinkingLevel)))
			} else {
				options = append(options, WithThinkingLevel(defaultLevel))
			}
		}
	} else {
		// Caller explicitly set a thinking level — still clamp it for model compatibility.
		// e.g. ThinkingLevelFastTask="minimal" fails on gemini-3.1-pro-preview which needs >= "low".
		combined := llms.CallOptions{}
		for _, opt := range options {
			opt(&combined)
		}
		if lvl, ok := combined.Metadata["ThinkingLevel"].(string); ok {
			clamped := ClampThinkingLevelForModel(model, lvl)
			if clamped != lvl {
				options = append(options, WithThinkingLevel(clamped))
			}
		}
	}

	ctx.GetLogger().Info("LLM call configuration",
		"provider", provider,
		"model", model,
		"maxOutputTokens", maxOutputTokens,
		"agent", agentName)

	// Step 3: Pre-flight per-message size guard.
	// Truncate any individual message that exceeds the configurable byte cap before the first
	// LLM call. This prevents token-limit errors caused by upstream agents injecting massive
	// context (e.g. large event payloads, code files) without going through the scratchpad.
	// The summarization-loop fallback (Strategy 1) is still the safety net for edge cases.
	promptMessages = applyPreflightMessageSizeCap(ctx, promptMessages, agentName)

	// Step 4: Generate content with retry logic
	completion, callMetadata, err := generateLLMContentWithRetry(ctx, llm, promptMessages, options, agentName, agentId, accountId, conversationId, messageId, false, userId, res)

	if err != nil {
		ctx.GetLogger().Error("unable to generate content", "error", err, "agentName", agentName, "agentId", agentId)
		return nil, err
	}

	// Add safety check for nil completion (should not happen after retry logic)
	if completion == nil {
		ctx.GetLogger().Error("unable to generate content as data is nil", "error", err, "agentName", agentName, "agentId", agentId)
		return nil, fmt.Errorf("LLM returned nil completion response after all retries exhausted")
	}

	// Get detailed token info for the initial response. This is now the source of truth.
	totalTokenInfo := getDetailedTokenInfo(completion, callMetadata.CacheInfo)
	ctx.GetLogger().Debug("Token Usage Info", "details", fmt.Sprintf("%+v", totalTokenInfo))

	// Track the final prompt messages used for tracing (may be updated by continuation loop)
	traceMessages := promptMessages

	// Handle response truncation by attempting to continue generation
	if len(completion.Choices) > 0 && isMaxTokensStop(completion.Choices[0].StopReason) {
		ctx.GetLogger().Info("Response truncated (Chunk 1), starting continuation loop", "agentId", agentId, "agentName", agentName)

		var fullContentBuilder strings.Builder
		fullContentBuilder.WriteString(completion.Choices[0].Content)

		// Create a retry context for continuation calls to ensure model info is tracked
		disableCachingCont, _ := ctx.GetContext().Value(ContextKeyDisableCaching).(bool)
		rc := &retryContext{
			ctx:              ctx,
			llm:              llm,
			promptMessages:   nil, // Will be set in loop
			options:          options,
			agentId:          agentId,
			agentName:        agentName,
			currentModel:     model,
			currentProvider:  provider,
			triedModels:      make(map[string]bool),
			errorHistory:     make([]string, 0),
			accountId:        accountId,
			conversationId:   conversationId,
			userId:           userId,
			totalStart:       t0, // Budget tracks from initial start
			maxTotalDuration: time.Duration(config.Config.LlmServerGlobalRetryBudgetMinutes) * time.Minute,
			enableCaching:    config.Config.LlmEnableCaching && !disableCachingCont,
		}

		const maxContinuations = 3
		for i := range maxContinuations {
			chunkIdx := i + 2
			ctx.GetLogger().Info("Attempting continuation", "chunk", chunkIdx, "max", maxContinuations+1, "agentId", agentId)

			// Accumulate all content generated so far into a single AI message
			// to maintain coherence and leverage prefix caching.
			iterationMessages := make([]llms.MessageContent, len(promptMessages))
			copy(iterationMessages, promptMessages)

			// Append the accumulated assistant content
			iterationMessages = append(iterationMessages, llms.TextParts(llms.ChatMessageTypeAI, fullContentBuilder.String()))

			// Add the instruction to continue
			iterationMessages = append(iterationMessages, llms.TextParts(llms.ChatMessageTypeHuman, "Please continue generating the response from exactly where you left off. Do not repeat anything you've already said or add any introductory phrases. Just continue the text directly."))

			// Re-apply size cap to the accumulated messages (prevents context bleed)
			rc.promptMessages = applyPreflightMessageSizeCap(ctx, iterationMessages, agentName)

			// Update trace messages to capture the continuation prompt
			if config.Config.LlmTraceEnabled {
				traceMessages = rc.promptMessages
			}

			// Use tryWithModel to ensure model info is properly tracked via defer
			nextCompletion, continuationErr := tryWithModel(rc)

			if continuationErr != nil {
				ctx.GetLogger().Warn("Error during continuation attempt, stopping.", "error", continuationErr, "chunk", chunkIdx, "agentId", agentId)
				fullContentBuilder.WriteString("\n\n**Note**: The response was cut off and could not be fully continued.")
				break
			}

			if nextCompletion == nil || len(nextCompletion.Choices) == 0 {
				ctx.GetLogger().Warn("Empty response during continuation attempt, stopping.", "chunk", chunkIdx, "agentId", agentId)
				break
			}

			if nextCompletion.Choices[0].Content == "" {
				ctx.GetLogger().Warn("Empty content received during continuation attempt despite MaxTokens stop reason, stopping.", "chunk", chunkIdx, "agentId", agentId)
				break
			}

			ctx.GetLogger().Info("Successfully received continuation chunk", "chunk", chunkIdx, "agentId", agentId, "stopReason", nextCompletion.Choices[0].StopReason)

			continuationTokenInfo := getDetailedTokenInfo(nextCompletion, rc.lastCacheInfo)

			// Add the tokens from this continuation call to the totals
			totalTokenInfo.InputTokens += continuationTokenInfo.InputTokens
			totalTokenInfo.OutputTokens += continuationTokenInfo.OutputTokens
			totalTokenInfo.CacheReadTokens += continuationTokenInfo.CacheReadTokens
			totalTokenInfo.CacheCreationTokens += continuationTokenInfo.CacheCreationTokens
			totalTokenInfo.TotalTokens += continuationTokenInfo.TotalTokens

			chunkContent := nextCompletion.Choices[0].Content
			fullContentBuilder.WriteString(chunkContent)

			completion = nextCompletion

			if !isMaxTokensStop(completion.Choices[0].StopReason) {
				ctx.GetLogger().Info("Continuation loop finished naturally", "totalChunks", chunkIdx, "agentId", agentId)
				break
			}

			if i == maxContinuations-1 {
				ctx.GetLogger().Warn("Max continuations reached, response may still be incomplete.", "agentId", agentId)
				fullContentBuilder.WriteString("\n\n**Note**: The response is very long and may still be incomplete.")
			}
		}
		// Overwrite the content of the original completion object with the full, concatenated content.
		completion.Choices[0].Content = fullContentBuilder.String()
	}

	ctx.GetLogger().Info("Successfully generated complete content", "model", model, "provider", provider, "agent", agentName, "agentId", agentId, "totalTokens", totalTokenInfo.TotalTokens)

	// Token metrics
	if totalTokenInfo.InputTokens > 0 {
		common.MetricsLLMTokensTotal(provider, model, "input", accountId, int64(totalTokenInfo.InputTokens))
	}
	if totalTokenInfo.OutputTokens > 0 {
		common.MetricsLLMTokensTotal(provider, model, "output", accountId, int64(totalTokenInfo.OutputTokens))
	}

	// Track cache-specific metrics for cost calculation
	if totalTokenInfo.CacheReadTokens > 0 {
		// Track cached tokens separately - these are billed at a different rate
		common.MetricsLLMCachedTokensTotal(provider, model, accountId, int64(totalTokenInfo.CacheReadTokens))
		ctx.GetLogger().Info("LLM cache tokens used",
			"provider", provider,
			"model", model,
			"conversationId", conversationId,
			"cacheReadTokens", totalTokenInfo.CacheReadTokens,
			"cacheCreationTokens", totalTokenInfo.CacheCreationTokens,
			"nonCachedTokens", totalTokenInfo.InputTokens-totalTokenInfo.CacheReadTokens,
			"outputTokens", totalTokenInfo.OutputTokens,
			"totalInputTokens", totalTokenInfo.InputTokens,
			"cacheHitRate", fmt.Sprintf("%.1f%%", float64(totalTokenInfo.CacheReadTokens)/float64(totalTokenInfo.InputTokens)*100))
	}

	// Extract content from completion
	content := ""
	if len(completion.Choices) > 0 {
		content = completion.Choices[0].Content
	}

	// Extract stop reason
	var stopReason *string
	if len(completion.Choices) > 0 && completion.Choices[0].StopReason != "" {
		stopReason = &completion.Choices[0].StopReason
	}

	// Prepare trace data when LLM tracing is enabled
	// Uses traceMessages which reflects the last prompt sent (including continuations if any)
	var tracePrompt *string
	var traceResponse *string
	if config.Config.LlmTraceEnabled {
		serialized := serializePromptMessages(traceMessages)
		tracePrompt = &serialized
		if content != "" {
			traceResponse = &content
		}
	}

	// Track token usage for the agent including cache breakdown
	// Use metadata from the LLM call for accurate tracking
	// RUN ASYNCHRONOUSLY to prevent DB latency from blocking the response
	bgCtx := security.NewRequestContext(context.Background(), ctx.GetSecurityContext(), ctx.GetLogger(), ctx.GetTracer(), ctx.GetMeter())
	trackFn := func() {
		trackTokenUsage(
			bgCtx,
			conversationId,
			messageId,
			agentId,
			agentName,
			provider,
			model,
			accountId,
			userId,
			totalTokenInfo,
			callMetadata.LatencySeconds,
			callMetadata.RetryAttempt,
			callMetadata.FallbackFromModel,
			callMetadata.FallbackChain,
			trackContent,
			content,
			stopReason,
			tracePrompt,
			traceResponse,
		)
	}

	if metricsPool := GetMetricsWorkerPool(); metricsPool != nil {
		err = metricsPool.Submit(context.Background(), trackFn)
		if err != nil {
			ctx.GetLogger().Error("Failed to submit token tracking task to pool, falling back to goroutine", "error", err)
			go trackFn()
		}
	} else {
		// Fallback for early initialization or tests
		go trackFn()
	}

	ctx.GetLogger().Info("GenerateAndTrackLLMContent complete", "total_duration", time.Since(t0).String(), "agent", agentName, "model", model)

	// Clean up markdown content if needed
	if cleanupMarkdown {
		cleanupMarkdownInResponse(completion)
	}

	return completion, nil
}

func IsSLMEnabled(accountId string, agentName string) bool {
	if accountId == "" {
		return false
	}
	if agentName == "" {
		return false
	}
	sllmModel := ""
	modelKey := fmt.Sprintf(llmModelFormat, agentName)
	sllmModel = config.Config.GetString(modelKey, sllmModel)
	return sllmModel != ""
}

func GetModelNameForAgent(agentName string) string {
	modelKey := fmt.Sprintf(llmModelFormat, agentName)
	return config.Config.GetString(modelKey, "")
}

func IsAgentToolSupported(agentName string) bool {
	toolKey := fmt.Sprintf("agent_tool_support_%s", agentName)
	isAgentToolSupported := config.Config.GetString(toolKey, "")
	if isAgentToolSupported != "" && strings.ToLower(isAgentToolSupported) == "true" {
		return true
	}
	return false
}

func IsAgentsFollowupEnabled() bool {
	followupKey := "agents_followup_enabled"
	isFollowupEnabled := config.Config.GetString(followupKey, "")
	if isFollowupEnabled != "" && strings.ToLower(isFollowupEnabled) == "true" {
		return true
	}
	return false
}

func SanatizeMarkdownCodeBlock(content string) string {
	content = strings.TrimSpace(content)
	if codeBlockPrefixRegex.MatchString(content) {
		content = codeBlockPrefixRegex.ReplaceAllString(content, "")
		// Also remove the trailing ``` if present
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	//only clean when there is prefix.. else may result in inconsistent markdown
	if strings.HasPrefix(content, "`") && strings.HasSuffix(content, "`") {
		content = strings.TrimPrefix(content, "`")
		content = strings.TrimSuffix(content, "`")
	}
	return content
}

// getDetailedTokenInfo extracts comprehensive token information including cache details from the LLM response.
// It handles different structures from providers like Google AI and Anthropic.
func getDetailedTokenInfo(response *llms.ContentResponse, cacheResp *CacheResponse) *TokenInfo {
	info := &TokenInfo{}

	// Safety check for nil response or empty choices
	if response == nil || len(response.Choices) == 0 {
		return info
	}

	generateInfo := response.Choices[0].GenerationInfo
	if generateInfo == nil {
		return info
	}

	// Standard token fields (e.g., "input_tokens", "output_tokens")
	// Note: For Anthropic, "InputTokens" in the response refers to the non-cached tokens.
	if val, ok := generateInfo["InputTokens"]; ok {
		info.InputTokens = extractInt(val)
	} else if val, ok := generateInfo["input_tokens"]; ok {
		info.InputTokens = extractInt(val)
	}

	if val, ok := generateInfo["OutputTokens"]; ok {
		info.OutputTokens = extractInt(val)
	} else if val, ok := generateInfo["output_tokens"]; ok {
		info.OutputTokens = extractInt(val)
	}

	// Anthropic-specific cache fields
	if val, ok := generateInfo["CacheReadInputTokens"]; ok {
		info.CacheReadTokens = extractInt(val)
	}
	if val, ok := generateInfo["CacheCreationInputTokens"]; ok {
		info.CacheCreationTokens = extractInt(val)
	}

	// Google AI cache fields (for compatibility)
	if val, ok := generateInfo["CachedTokens"]; ok {
		// If we have Anthropic's more specific field, prefer it. Otherwise, use Google's.
		if info.CacheReadTokens == 0 {
			info.CacheReadTokens = extractInt(val)
		}
	}

	// The total input to the model is the sum of tokens read from cache and the new (non-cached) tokens.
	// Anthropic provides "InputTokens" as the non-cached part when caching is active.
	info.InputTokens += info.CacheReadTokens

	// Calculate total tokens
	if val, ok := generateInfo["total_tokens"]; ok {
		info.TotalTokens = extractInt(val)
	} else {
		info.TotalTokens = info.InputTokens + info.OutputTokens
	}

	// If cacheResp is provided, add cache creation tokens to CacheCreationTokens
	if cacheResp != nil && cacheResp.CacheInfo != nil {
		info.CacheCreationTokens += int(cacheResp.CacheInfo.CacheCreationTokens)
	}

	return info
}

// extractInt is a helper to extract int from various types
func extractInt(val any) int {
	switch v := val.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case *int32:
		if v != nil {
			return int(*v)
		}
	case *int64:
		if v != nil {
			return int(*v)
		}
	}
	return 0
}

// createStreamingOption returns a streaming function option for LLM content generation
// It just logs the chunks received from the LLM and enables streaming
func createStreamingOption() llms.CallOption {
	return llms.WithStreamingFunc(func(ctx context.Context, chunk []byte) error {
		// Handle nil chunks gracefully
		if chunk == nil {
			slog.Debug("Received nil chunk from LLM, continuing...")
			return nil
		}
		// Process non-empty chunks
		if len(chunk) > 0 {
			slog.Debug("Received LLM chunk", "size", len(chunk))
		}
		return nil
	})
}

// WithThinkingLevel returns a CallOption that sets the thinking level for
// models that support chain-of-thought reasoning (e.g. Gemini 2.5+, Gemini 3).
// Valid levels: "minimal", "low", "medium", "high".
// Use "none" to explicitly clear a previously set thinking level (e.g. for
// non-thinking models where a caller already set a level that must be suppressed).
// This uses the same metadata key as llms/googleai.WithThinkingLevel.
func WithThinkingLevel(level string) llms.CallOption {
	return func(o *llms.CallOptions) {
		if level == "" {
			return
		}
		if o.Metadata == nil {
			o.Metadata = make(map[string]any)
		}
		if strings.EqualFold(level, "none") {
			delete(o.Metadata, "ThinkingLevel")
			return
		}
		o.Metadata["ThinkingLevel"] = level
	}
}

// isMaxTokensStop returns true when the stop reason indicates the response was
// truncated due to reaching the max output-token limit.
// Comparison is case-insensitive to handle provider variations:
//
//	Google AI (new genai SDK) : "MAX_TOKENS"
//	Google AI (old SDK) / Vertex: "FinishReasonMaxTokens"
//	Bedrock Anthropic          : "max_tokens"
//	Azure / OpenAI             : "length"
func isMaxTokensStop(stopReason string) bool {
	r := strings.ToLower(stopReason)
	return r == "max_tokens" || r == "finishreasonmaxtokens" || r == "length"
}

// hasThinkingLevelOption returns true if any of the provided options already
// contain an explicit ThinkingLevel entry in their metadata. This prevents
// the auto-apply logic from overwriting a caller-supplied value.
func hasThinkingLevelOption(options []llms.CallOption) bool {
	combined := &llms.CallOptions{}
	for _, o := range options {
		o(combined)
	}
	if combined.Metadata == nil {
		return false
	}
	_, ok := combined.Metadata["ThinkingLevel"]
	return ok
}

func getAgentNameFromAgentId(agentId string) string {
	agentName, err := GetConversationDao().GetAgentNameFromAgentId(agentId)
	if err != nil {
		return ""
	}
	return agentName
}

func tryWithModel(rc *retryContext) (*llms.ContentResponse, error) {
	if rc == nil {
		return nil, fmt.Errorf("retryContext is nil")
	}
	if rc.ctx == nil {
		return nil, fmt.Errorf("request context is nil")
	}
	if rc.llm == nil {
		rc.lastErr = fmt.Errorf("llm model is nil")
		errorMsg := fmt.Sprintf("[%s] %s", rc.currentModel, safeError(rc.lastErr))
		rc.errorHistory = append(rc.errorHistory, errorMsg)
		rc.ctx.GetLogger().Error("LLM model is nil", "model", rc.currentModel)
		return nil, rc.lastErr
	}

	start := time.Now()
	// Use context with timeout (10 minutes default for thinking models, capped by parent)
	previousContext := rc.ctx.GetContext()
	if previousContext == nil {
		previousContext = context.Background()
	}

	// Calculate remaining budget
	elapsed := time.Since(rc.totalStart)
	remaining := rc.maxTotalDuration - elapsed
	if remaining <= 0 {
		return nil, fmt.Errorf("LLM global retry budget exhausted (%v elapsed)", elapsed)
	}

	// Dynamic timeout: max individual call timeout OR remaining budget
	callTimeout := time.Duration(config.Config.LlmServerMaxIndividualCallTimeoutMinutes) * time.Minute
	if remaining < callTimeout {
		callTimeout = remaining
	}

	ctx, cancel := context.WithTimeout(previousContext, callTimeout)
	defer cancel()

	// Apply cache for current model (if enabled)
	// This ensures cache is always correct for the current model, including fallback models
	messagesToSend := rc.promptMessages
	optionsToSend := rc.options
	rc.lastCacheInfo = nil // Reset cache info

	if rc.conversationId != "" && rc.enableCaching {
		rc.ctx.GetLogger().Debug("Applying cache for current model",
			"model", rc.currentModel,
			"provider", rc.currentProvider,
			"conversationId", rc.conversationId,
			"agentId", rc.agentId)

		cacheManager := GetCacheManager()
		appendAgentName := rc.agentName != ""
		apiKey := getLLMApiKey(rc.accountId, rc.currentProvider, rc.agentName, appendAgentName)

		cacheReq := &CacheRequest{
			AccountId:      rc.accountId,
			ConversationId: rc.conversationId,
			AgentName:      rc.agentName,
			Model:          rc.currentModel, // Uses CURRENT model (important for fallbacks)
			Provider:       rc.currentProvider,
			Messages:       rc.promptMessages,
			ApiKey:         apiKey,
			Scope:          rc.cacheScope,
			Capabilities:   rc.capabilities,
		}

		cacheResp := cacheManager.ApplyCache(ctx, cacheReq)
		if cacheResp.Error != nil {
			rc.ctx.GetLogger().Warn("Cache operation failed, continuing without cache",
				"error", cacheResp.Error,
				"model", rc.currentModel,
				"provider", rc.currentProvider,
				"conversationId", rc.conversationId)
		} else {
			// Store cache info for token tracking
			rc.lastCacheInfo = cacheResp

			// Use modified messages (reduced for Google AI, marked for Anthropic)
			messagesToSend = cacheResp.Messages

			// Add cache-specific options (e.g., CachedContentName for Google AI)
			if len(cacheResp.Options) > 0 {
				// Guard: CacheInfo.Model records which model this cached content belongs to.
				// If it doesn't match rc.currentModel the provider will reject the request
				// (Google AI 400: "model used by GenerateContent and CachedContent must be same").
				// Drop cache options and restore full messages so the call still succeeds.
				if cacheResp.CacheInfo != nil && cacheResp.CacheInfo.Model != "" &&
					cacheResp.CacheInfo.Model != rc.currentModel {
					rc.ctx.GetLogger().Error("Cache model mismatch: skipping cached content to prevent API error",
						"cacheModel", cacheResp.CacheInfo.Model,
						"currentModel", rc.currentModel,
						"cacheName", cacheResp.CacheInfo.CacheName,
						"conversationId", rc.conversationId)
					messagesToSend = rc.promptMessages // restore full messages
				} else {
					optionsToSend = append(optionsToSend, cacheResp.Options...)
					rc.ctx.GetLogger().Info("Cache applied successfully",
						"model", rc.currentModel,
						"provider", rc.currentProvider,
						"cacheHit", cacheResp.CacheHit,
						"conversationId", rc.conversationId,
						"originalMessages", len(rc.promptMessages),
						"messagesInRequest", len(messagesToSend),
						"messagesSavedByCache", len(rc.promptMessages)-len(messagesToSend),
						"optionsAdded", len(cacheResp.Options))
				}
			} else {
				rc.ctx.GetLogger().Debug("Cache check completed, no cache options applied",
					"model", rc.currentModel,
					"cacheHit", cacheResp.CacheHit,
					"conversationId", rc.conversationId)
			}
		}
	}

	// Generate content with potentially cached messages/options
	sem := getLlmSemaphore(rc.accountId, rc.currentProvider, rc.currentModel)
	semStart := time.Now()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
		waitDuration := time.Since(semStart)
		if waitDuration > 100*time.Millisecond {
			rc.ctx.GetLogger().Info("LLM concurrency semaphore wait", "duration", waitDuration.String(), "model", rc.currentModel, "provider", rc.currentProvider)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if rc.hasMalformedFunctionCall || (rc.lastErr != nil && strings.Contains(rc.lastErr.Error(), "llm returned empty content")) {
		if rc.hasMalformedFunctionCall {
			messagesToSend = append(messagesToSend, llms.TextParts(llms.ChatMessageTypeHuman,
				"IMPORTANT: Do NOT use native function calls. Your previous response was rejected because you used native function call format. "+
					"You MUST respond in plain text using the XML tag format shown in your instructions. "+
					"You MUST call a tool first before giving a final answer — do NOT output <final_answer> yet. "+
					"Use <thought_action><thought>...</thought><action><tool_name>TOOL</tool_name><tool_input>INPUT</tool_input></action></thought_action> to call a tool."))
		} else {
			messagesToSend = append(messagesToSend, llms.TextParts(llms.ChatMessageTypeHuman, "Think STEP BY STEP very carefully to decide next steps to solve the problem. If <scratchpad> is available, use it to get more context on what is done and what is required to be done. If <scratchpad> is not available, use the instructions to get more context on what is done and what is required to be done."))
		}
	}

	completion, err := rc.llm.GenerateContent(ctx, messagesToSend, optionsToSend...)
	if err == nil && (completion == nil || len(completion.Choices) == 0 || completion.Choices[0].Content == "") {
		stopReason := ""
		if completion != nil && len(completion.Choices) > 0 {
			stopReason = completion.Choices[0].StopReason
		}
		rc.ctx.GetLogger().Warn("LLM returned empty content", "model", rc.currentModel, "stopReason", stopReason)
		if strings.EqualFold(stopReason, "MALFORMED_FUNCTION_CALL") {
			err = errors.New("llm returned empty content: MALFORMED_FUNCTION_CALL")
			rc.hasMalformedFunctionCall = true // sticky: preserved across all retries
		} else {
			err = errors.New("llm returned empty content")
		}
		rc.lastErr = err
		rc.errorHistory = append(rc.errorHistory, fmt.Sprintf("[%s] %s", rc.currentModel, err.Error()))
		return nil, err
	}
	generationDuration := time.Since(start)
	latency := generationDuration.Seconds()
	rc.ctx.GetLogger().Info("LLM GenerateContent call complete", "duration", generationDuration.String(), "model", rc.currentModel, "provider", rc.currentProvider)
	rc.lastLatency = latency // Store latency in retry context
	provider := rc.currentProvider
	model := rc.currentModel
	status := "success"
	if err != nil {
		status = "fail"
	}
	common.MetricsLLMRequestsTotal(provider, model, status, rc.accountId)
	common.MetricsLLMLatencySeconds(provider, model, rc.accountId, latency)
	if err != nil {
		rc.lastErr = err
		errorMsg := fmt.Sprintf("[%s] %s", rc.currentModel, safeError(err))
		rc.errorHistory = append(rc.errorHistory, errorMsg)

		rc.ctx.GetLogger().Warn("Failed to generate content", "error", err, "model", rc.currentModel, "agent", rc.agentName, "agentId", rc.agentId)
		return nil, err
	}
	if completion == nil || completion.Choices == nil {
		err = fmt.Errorf("LLM returned invalid completion response")
		rc.lastErr = err
		errorMsg := fmt.Sprintf("[%s] %s", rc.currentModel, safeError(err))
		rc.errorHistory = append(rc.errorHistory, errorMsg)
		rc.ctx.GetLogger().Warn("LLM returned nil completion, will retry", "model", rc.currentModel, "agent", rc.agentName, "agentId", rc.agentId)
		return nil, err
	}
	if len(completion.Choices) == 0 {
		err = fmt.Errorf("LLM returned empty choices in completion response")
		rc.lastErr = err
		errorMsg := fmt.Sprintf("[%s] %s", rc.currentModel, safeError(err))
		rc.errorHistory = append(rc.errorHistory, errorMsg)
		rc.ctx.GetLogger().Warn("LLM returned empty choices, will retry", "model", rc.currentModel, "agent", rc.agentName, "agentId", rc.agentId)
		return nil, err
	}

	// Record success to close circuit breaker (if it was half-open, this confirms recovery)
	RecordModelSuccess(rc.currentProvider, rc.currentModel)
	rc.ctx.GetLogger().Info("Received LLM chunk", "model", rc.currentModel, "provider", rc.currentProvider, "agent", rc.agentName, "agentId", rc.agentId, "stopReason", completion.Choices[0].StopReason)
	return completion, nil
}

func tryFallbackModel(rc *retryContext, nextModel string, attempt int) (*llms.ContentResponse, error) {
	if rc == nil || rc.ctx == nil {
		return nil, errors.New("invalid retry context")
	}
	if nextModel == "" || rc.triedModels[nextModel] {
		return nil, errors.New("invalid or already tried model")
	}

	// Save current state before mutation
	previousModel := rc.currentModel
	previousLLM := rc.llm
	previousProvider := rc.currentProvider

	rc.triedModels[nextModel] = true
	rc.ctx.GetLogger().Info("Trying fallback model",
		"previousError", safeError(rc.lastErr),
		"previousModel", previousModel,
		"nextModel", nextModel,
		"attempt", attempt+1)

	provider := GetLLMProvider(rc.ctx, rc.accountId, rc.agentName, false, rc.conversationId)
	newLLM, err := GetLLMModel(provider, nextModel, rc.agentName, false, rc.accountId)
	if err != nil {
		rc.ctx.GetLogger().Warn("Failed to create fallback model",
			"model", nextModel,
			"error", safeError(err))
		errorMsg := fmt.Sprintf("[%s] Failed to initialize: %s", nextModel, safeError(err))
		rc.errorHistory = append(rc.errorHistory, errorMsg)
		return nil, err
	}

	// Update state and track fallback
	rc.currentModel = nextModel
	rc.currentProvider = provider
	rc.llm = newLLM
	rc.fallbackFromModel = &previousModel                      // Track immediate previous model
	rc.fallbackChain = append(rc.fallbackChain, previousModel) // Add to chain

	completion, err := tryWithModel(rc)
	if err != nil {
		// Revert state on failure
		rc.currentModel = previousModel
		rc.currentProvider = previousProvider
		rc.llm = previousLLM
		rc.fallbackFromModel = nil                                    // Clear immediate fallback
		rc.fallbackChain = rc.fallbackChain[:len(rc.fallbackChain)-1] // Remove last from chain
		return nil, err
	}

	return completion, nil
}

func logAttemptError(rc *retryContext, attempt, maxAttempts int) {
	if rc == nil || rc.ctx == nil {
		return
	}
	if !isRetryableError(rc.lastErr) {
		rc.ctx.GetLogger().Error("Encountered non-retryable error, giving up",
			"error", safeError(rc.lastErr),
			"attempt", attempt+1,
			"errorHistory", rc.errorHistory)
	} else {
		rc.ctx.GetLogger().Error("Maximum retry attempts reached",
			"error", safeError(rc.lastErr),
			"attempt", attempt+1,
			"maxAttempts", maxAttempts,
			"errorHistory", rc.errorHistory)
	}
}

// serializePromptMessages converts prompt messages to a JSON string for trace logging.
func serializePromptMessages(messages []llms.MessageContent) string {
	type tracePart struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		Name    string `json:"name,omitempty"`
		Content string `json:"content,omitempty"`
	}
	type traceMessage struct {
		Role  string      `json:"role"`
		Parts []tracePart `json:"parts"`
	}

	out := make([]traceMessage, 0, len(messages))
	for _, msg := range messages {
		tm := traceMessage{Role: string(msg.Role)}
		for _, part := range msg.Parts {
			switch p := part.(type) {
			case llms.TextContent:
				tm.Parts = append(tm.Parts, tracePart{Type: "text", Text: p.Text})
			case llms.ToolCall:
				tm.Parts = append(tm.Parts, tracePart{Type: "tool_call", Name: p.FunctionCall.Name, Content: p.FunctionCall.Arguments})
			case llms.ToolCallResponse:
				tm.Parts = append(tm.Parts, tracePart{Type: "tool_result", Name: p.Name, Content: p.Content})
			default:
				tm.Parts = append(tm.Parts, tracePart{Type: fmt.Sprintf("%T", part), Text: fmt.Sprintf("%+v", part)})
			}
		}
		out = append(out, tm)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf("[serialization error: %v]", err)
	}
	return string(b)
}

// sanitizeMessageContent sanitizes UTF-8 in message content
func sanitizeMessageContent(messages []llms.MessageContent) []llms.MessageContent {
	sanitized := make([]llms.MessageContent, len(messages))
	for i, msg := range messages {
		newParts := make([]llms.ContentPart, len(msg.Parts))
		copy(newParts, msg.Parts)

		// Sanitize text parts
		for j, part := range newParts {
			if textPart, ok := part.(llms.TextContent); ok {
				newParts[j] = llms.TextContent{Text: sanitizeUTF8Input(textPart.Text)}
			}
		}

		sanitized[i] = llms.MessageContent{
			Role:  msg.Role,
			Parts: newParts,
		}
	}
	return sanitized
}

// sanitizeUTF8Input removes invalid UTF-8 sequences from input strings
func sanitizeUTF8Input(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	// Replace invalid UTF-8 with an empty string. This is more robust and available in Go 1.21+.
	return strings.ToValidUTF8(s, "")
}

// buildCallMetadata creates metadata from retry context for token tracking
func buildCallMetadata(rc *retryContext, success bool) *LLMCallMetadata {
	metadata := &LLMCallMetadata{
		LatencySeconds:    rc.lastLatency,
		RetryAttempt:      rc.attemptCount,
		FallbackFromModel: rc.fallbackFromModel,
		FallbackChain:     rc.fallbackChain, // Copy the full fallback chain
		RequestStatus:     "success",
		ErrorMessage:      nil,
		CacheInfo:         rc.lastCacheInfo, // Include cache info from last call
	}

	if !success && rc.lastErr != nil {
		metadata.RequestStatus = "failure"
		errMsg := safeError(rc.lastErr)
		metadata.ErrorMessage = &errMsg
	}

	return metadata
}

// ============================================================================
// STRATEGY HANDLERS: Three specialized retry strategies for different error types
// ============================================================================

// handleTokenLimitError handles token limit errors using intelligent chunking with context preservation
// STRATEGY 1: Split messages into chunks, process sequentially with cumulative summaries
func handleTokenLimitError(rc *retryContext) (*llms.ContentResponse, *LLMCallMetadata, error) {
	ctx := rc.ctx

	ctx.GetLogger().Info("STRATEGY 1: Token limit error detected, applying chunking with context preservation",
		"agentId", rc.agentId,
		"model", rc.currentModel,
		"provider", rc.currentProvider)

	maxTokens := GetLlmMaxTokenLength(rc.currentModel)

	// Calculate initial token distribution before summarization
	totalTokens, err := CalculateTotalTokens(ctx, rc.promptMessages, rc.currentProvider, rc.currentModel)
	if err != nil {
		ctx.GetLogger().Warn("Failed to calculate initial tokens, using estimation", "error", err)
		totalTokens = 0
		for _, msg := range rc.promptMessages {
			totalTokens += estimateMessageTokens(msg)
		}
	}

	// Calculate how many chunks would be needed
	chunkSize := int(float64(maxTokens) * 0.7) // 70% of max tokens per chunk
	chunksNeeded := int(math.Ceil(float64(totalTokens) / float64(chunkSize)))

	ctx.GetLogger().Info("Token limit error analysis",
		"totalTokens", totalTokens,
		"maxTokens", maxTokens,
		"exceedsBy", totalTokens-maxTokens,
		"messageCount", len(rc.promptMessages),
		"chunkSize", chunkSize,
		"chunksNeeded", chunksNeeded)

	// Log individual message token counts
	for i, msg := range rc.promptMessages {
		msgTokens, err := calculateMessageTokens(ctx, msg, rc.currentProvider, rc.currentModel)
		if err != nil {
			msgTokens = estimateMessageTokens(msg)
		}
		ctx.GetLogger().Info("Message token analysis",
			"messageIndex", i,
			"messageRole", msg.Role,
			"messageTokens", msgTokens,
			"percentOfMax", fmt.Sprintf("%.1f%%", float64(msgTokens)/float64(maxTokens)*100))
	}

	// Step 1: Loop until total tokens are within limit
	// Summarize all large messages, check total, repeat if needed
	ctx.GetLogger().Info("Step 1: Starting recursive summarization loop",
		"maxTokens", maxTokens)

	// Safe limit per message = chunk size (70% of max) - overhead
	safeMessageLimit := int(float64(maxTokens) * 0.7 * 0.7) // 49% of max tokens per message
	maxIterations := 10                                     // Safety limit to prevent infinite loops
	iteration := 0

	for iteration < maxIterations {
		iteration++
		summarizedAnyMessage := false

		ctx.GetLogger().Info("Summarization iteration starting",
			"iteration", iteration,
			"maxIterations", maxIterations)

		// Summarize all messages that exceed safe limit
		for i, msg := range rc.promptMessages {
			msgTokens, err := calculateMessageTokens(ctx, msg, rc.currentProvider, rc.currentModel)
			if err != nil {
				// If can't count, estimate
				msgTokens = estimateMessageTokens(msg)
			}

			if msgTokens > safeMessageLimit {
				ctx.GetLogger().Warn("Individual message exceeds safe limit, attempting to downsample or summarize",
					"messageIndex", i,
					"iteration", iteration,
					"messageTokens", msgTokens,
					"safeLimit", safeMessageLimit)
				if textContent, isText := msg.Parts[0].(llms.TextContent); isText {
					var summarizedText string
					// Use feature flag to switch between summarization strategies
					if config.Config.LlmSummarizationParallelEnabled {
						summarizedText = SummarizeLargeMessageChunkedParallel(
							ctx, rc.llm, textContent.Text,
							rc.currentProvider, rc.currentModel, maxTokens,
							rc.accountId, rc.agentId,
							rc.conversationId, rc.messageId, rc.userId,
						)
					} else {
						// Original sequential summarization
						summarizedText = SummarizeLargeMessageChunked(
							ctx, rc.llm, textContent.Text,
							rc.currentProvider, rc.currentModel, maxTokens,
							rc.accountId, rc.agentId,
							rc.conversationId, rc.messageId, rc.userId,
						)
					}
					// Guard: if summarization failed and returned empty string,
					// hard-truncate as a fallback. Keeping the original would leave tokens
					// unreduced (loop breaks with no progress), and sending an empty string
					// causes 400: required oneof field 'data' must have one initialized field.
					// Hard-truncating ensures forward progress while preserving leading content.
					if summarizedText == "" {
						// Cons~4 chars/token is a reasonable estimate for mixed technical content.
						const bytesPerToken = 4
						safeTruncBytes := safeMessageLimit * bytesPerToken
						truncated := TruncateHead(textContent.Text, safeTruncBytes)
						if truncated != textContent.Text {
							ctx.GetLogger().Warn("Summarization returned empty string, falling back to hard-truncation",
								"messageIndex", i, "originalTokens", msgTokens,
								"safeMessageLimit", safeMessageLimit, "safeTruncBytes", safeTruncBytes)
							rc.promptMessages[i] = llms.MessageContent{
								Role: msg.Role,
								Parts: []llms.ContentPart{
									llms.TextContent{Text: truncated + "\n[message hard-truncated: content exceeded summarization capacity]"},
								},
							}
							summarizedAnyMessage = true
						} else {
							ctx.GetLogger().Warn("Summarization returned empty string and message already at safe byte limit, keeping original",
								"messageIndex", i, "originalTokens", msgTokens)
						}
					} else {
						rc.promptMessages[i] = llms.MessageContent{
							Role: msg.Role,
							Parts: []llms.ContentPart{
								llms.TextContent{Text: summarizedText},
							},
						}
						summarizedAnyMessage = true
					}
				}
			}
		}

		// Calculate total tokens after summarization
		totalTokens, err := CalculateTotalTokens(
			ctx,
			rc.promptMessages,
			rc.currentProvider,
			rc.currentModel,
		)

		if err != nil {
			ctx.GetLogger().Error("Failed to calculate total tokens",
				"error", err,
				"iteration", iteration)
			return nil, buildCallMetadata(rc, false), fmt.Errorf("token calculation failed: %w", err)
		}

		ctx.GetLogger().Info("Token analysis after summarization",
			"iteration", iteration,
			"totalTokens", totalTokens,
			"maxTokens", maxTokens,
			"summarizedAnyMessage", summarizedAnyMessage)

		// Check if total is within limit
		if totalTokens <= maxTokens {
			ctx.GetLogger().Info("Total tokens now within limit, trying LLM call",
				"iteration", iteration,
				"totalTokens", totalTokens,
				"maxTokens", maxTokens)

			// Try LLM call
			completion, err := tryWithModel(rc)
			if err == nil {
				ctx.GetLogger().Info("Successfully handled token limit after summarization",
					"iterations", iteration)
				return completion, buildCallMetadata(rc, true), nil
			}

			ctx.GetLogger().Warn("LLM call failed despite tokens being within limit",
				"iteration", iteration,
				"error", err)
			// Continue to next iteration to further summarize
		}

		// If no messages were summarized this iteration, we can't reduce further
		if !summarizedAnyMessage {
			ctx.GetLogger().Warn("No messages to summarize but total still exceeds limit",
				"iteration", iteration,
				"totalTokens", totalTokens,
				"maxTokens", maxTokens)
			break
		}

		ctx.GetLogger().Info("Total still exceeds limit, continuing to next iteration",
			"iteration", iteration,
			"totalTokens", totalTokens,
			"maxTokens", maxTokens)
	}

	if iteration >= maxIterations {
		ctx.GetLogger().Error("Failed to reduce tokens to limit after max iterations",
			"maxIterations", maxIterations)
	}

	return nil, buildCallMetadata(rc, false),
		fmt.Errorf("failed to handle token limit error after %d iterations", iteration)
}

// handleQuotaError handles quota/rate limit errors by trying fallback models
// STRATEGY 2: Try each configured fallback model until one succeeds
func handleQuotaError(rc *retryContext, fallbackModels []string, recordPrimaryHit bool) (*llms.ContentResponse, *LLMCallMetadata, error) {
	ctx := rc.ctx

	// Record rate limit hit to open circuit breaker for the primary model.
	// Skipped when called from the transient-error exhaustion path or when the
	// circuit is already open — in those cases the model had timeouts rather than
	// quota issues, and tripping the circuit would give it an inaccurate label.
	if recordPrimaryHit {
		RecordModelRateLimitHit(rc.currentProvider, rc.currentModel)
		common.MetricsLLMRateLimitHitsTotal(rc.currentProvider, rc.currentModel, rc.accountId)
	}

	ctx.GetLogger().Info("STRATEGY 2: Quota/rate limit error detected, trying fallback models",
		"agentId", rc.agentId,
		"primaryModel", rc.currentModel,
		"fallbackCount", len(fallbackModels))

	// Validate fallback models exist
	if len(fallbackModels) == 0 {
		ctx.GetLogger().Warn("No fallback models configured for quota handling")
		return nil, buildCallMetadata(rc, false),
			fmt.Errorf("quota exceeded on model %s and no fallback models available", rc.currentModel)
	}

	// Try each fallback model
	for i, model := range fallbackModels {
		ctx.GetLogger().Info("Attempting fallback model",
			"fallbackModel", model,
			"attemptNumber", i+1,
			"totalFallbacks", len(fallbackModels),
			"primaryModel", rc.currentModel)

		// Skip if already tried
		if rc.triedModels[model] {
			ctx.GetLogger().Debug("Skipping already tried model",
				"model", model)
			continue
		}

		// Try this fallback model
		completion, err := tryFallbackModel(rc, model, i)

		if err == nil {
			// Success!
			ctx.GetLogger().Info("STRATEGY 2 SUCCESS: Fallback model succeeded",
				"fallbackModel", model,
				"attemptNumber", i+1,
				"originalModel", rc.currentModel)
			return completion, buildCallMetadata(rc, true), nil
		}

		// Analyze failure reason
		if isQuotaError(err) {
			// Record rate limit hit for the fallback model too
			fallbackProvider := GetLLMProvider(rc.ctx, rc.accountId, rc.agentName, false, rc.conversationId)
			RecordModelRateLimitHit(fallbackProvider, model)
			common.MetricsLLMRateLimitHitsTotal(fallbackProvider, model, rc.accountId)
			ctx.GetLogger().Warn("Fallback model also has quota issues, trying next",
				"model", model,
				"error", safeError(err))
			// Brief delay before next fallback to avoid hitting shared quota pools
			time.Sleep(time.Second + time.Duration(rand.Int64N(int64(time.Second))))
			continue
		}

		if isTokenLimitError(err) {
			ctx.GetLogger().Warn("Fallback model has token limit issue (different context window?)",
				"model", model,
				"error", safeError(err))
			// Could try chunking with this model, but for now just try next fallback
			continue
		}

		if isTransientError(err) {
			// continue to next fallback — deliberately NOT delegating back to
			// handleTransientError, which would re-enter generateLLMContentWithRetry's
			// strategy router and create an unbounded recursion. Skipping the fallback
			// and trying the next one is the correct termination-safe behaviour here.
			ctx.GetLogger().Warn("Fallback model has transient error, trying next",
				"model", model,
				"error", safeError(err))
			continue
		}

		// Other error type
		ctx.GetLogger().Warn("Fallback model failed with non-retryable error, trying next",
			"model", model,
			"error", safeError(err))
	}

	// All fallbacks exhausted
	ctx.GetLogger().Error("STRATEGY 2 FAILED: All fallback models exhausted",
		"triedModels", len(rc.triedModels),
		"fallbackCount", len(fallbackModels),
		"primaryModel", rc.currentModel)

	return nil, buildCallMetadata(rc, false),
		fmt.Errorf("quota exceeded on all available models (tried %d models)", len(rc.triedModels))
}

// handleTransientError handles temporary errors (timeouts, 500s) with exponential backoff retry
// STRATEGY 3: Retry original model with exponential backoff for temporary issues
func handleTransientError(rc *retryContext, maxAttempts int) (*llms.ContentResponse, *LLMCallMetadata, error) {
	ctx := rc.ctx

	ctx.GetLogger().Info("STRATEGY 3: Transient error detected, retrying with exponential backoff",
		"agentId", rc.agentId,
		"model", rc.currentModel,
		"error", safeError(rc.lastErr),
		"maxAttempts", maxAttempts)

	// Context deadline exceeded means the model accepted the connection but hung for the
	// full timeout period without sending data. Retrying the same model will almost certainly
	// hit the same timeout again, wasting the entire retry budget. Skip straight to fallbacks.
	if isDeadlineExceededError(rc.lastErr) {
		ctx.GetLogger().Warn("STRATEGY 3: Deadline exceeded — skipping same-model retries, trying fallback models directly",
			"model", rc.currentModel,
			"agentId", rc.agentId)
		goto tryFallbacks
	}

	// Retry with exponential backoff
	for attempt := 1; attempt < maxAttempts; attempt++ {
		rc.attemptCount = attempt

		// Calculate exponential backoff: 2^(attempt-1) * initialBackoff + 0-50% jitter.
		// Guard base > 0 to avoid rand.Int64N(0) panic when initialBackoffSeconds = 0.
		base := time.Duration(math.Pow(2, float64(attempt-1))) *
			time.Duration(config.Config.LlmServerLlmInitialBackoffSeconds) * time.Second
		jitter := time.Duration(0)
		if base > 0 {
			jitter = time.Duration(rand.Int64N(int64(base) / 2))
		}
		backoffDuration := base + jitter

		// Abort retries early if the parent context is already expired — retrying
		// would fail immediately and waste backoff time.
		if rc.ctx.GetContext() != nil && rc.ctx.GetContext().Err() != nil {
			ctx.GetLogger().Info("Parent context already expired, aborting transient error retry",
				"attempt", attempt, "ctxErr", rc.ctx.GetContext().Err())
			break
		}

		ctx.GetLogger().Info("Applying exponential backoff before retry",
			"duration", backoffDuration,
			"attempt", attempt,
			"maxAttempts", maxAttempts,
			"previousError", safeError(rc.lastErr))

		time.Sleep(backoffDuration)

		// Retry with original model
		completion, err := tryWithModel(rc)

		if err == nil {
			// Success!
			ctx.GetLogger().Info("STRATEGY 3 SUCCESS: Transient error resolved after retry",
				"attempt", attempt,
				"model", rc.currentModel)
			return completion, buildCallMetadata(rc, true), nil
		}

		// Check if error type changed
		if isTokenLimitError(err) {
			ctx.GetLogger().Info("Error type changed to token limit during retry, switching to Strategy 1",
				"attempt", attempt)
			return handleTokenLimitError(rc)
		}

		if isQuotaError(err) {
			common.MetricsLLMRateLimitHitsTotal(rc.currentProvider, rc.currentModel, rc.accountId)
			ctx.GetLogger().Info("Error type changed to quota error during retry, switching to Strategy 2",
				"attempt", attempt)

			// Check if conversation has explicit model configuration
			conversationHasExplicitModel := false
			if rc.conversationId != "" {
				if p, m, err := GetConversationOverride(rc.conversationId); err == nil && p != "" && m != "" {
					conversationHasExplicitModel = true
					ctx.GetLogger().Info("Conversation has explicit model, skipping fallbacks in retry",
						"provider", p,
						"model", m)
				}
			}

			// Get fallback models (skip if conversation has explicit model)
			fallbackModelsRaw := getLLMFallbackModelName(rc.accountId, rc.agentName, true)
			var fallbackModels []string
			if fallbackModelsRaw != "" && !conversationHasExplicitModel {
				for model := range strings.SplitSeq(fallbackModelsRaw, ",") {
					trimmed := strings.TrimSpace(model)
					if trimmed != "" && trimmed != rc.currentModel {
						fallbackModels = append(fallbackModels, trimmed)
					}
				}
			}
			return handleQuotaError(rc, fallbackModels, true) // actual quota error mid-retry
		}

		if !isRetryableError(err) {
			ctx.GetLogger().Error("Error became non-retryable during retry",
				"attempt", attempt,
				"error", safeError(err))
			return nil, buildCallMetadata(rc, false),
				fmt.Errorf("error became non-retryable: %w", err)
		}

		// Still transient, continue retrying
		ctx.GetLogger().Warn("Retry attempt failed, will try again",
			"attempt", attempt,
			"maxAttempts", maxAttempts,
			"error", safeError(err))
	}

tryFallbacks:
	// Max attempts reached (or deadline exceeded skip) — try fallback models before giving up
	ctx.GetLogger().Warn("STRATEGY 3: Trying fallback models",
		"maxAttempts", maxAttempts,
		"model", rc.currentModel)

	fallbackModelsRaw := getLLMFallbackModelName(rc.accountId, rc.agentName, true)
	if fallbackModelsRaw != "" {
		var fallbackModels []string
		for model := range strings.SplitSeq(fallbackModelsRaw, ",") {
			trimmed := strings.TrimSpace(model)
			if trimmed != "" && !rc.triedModels[trimmed] {
				fallbackModels = append(fallbackModels, trimmed)
			}
		}
		if len(fallbackModels) > 0 {
			// Delegate to handleQuotaError to try fallback models.
			// Termination is guaranteed:
			//   1. fallbackModels is a finite, pre-filtered list (triedModels excludes already-attempted models).
			//   2. handleQuotaError iterates the list once; errors from each fallback (quota, transient,
			//      token-limit, non-retryable) are handled with a `continue` or an early return — it never
			//      re-enters generateLLMContentWithRetry's strategy-routing loop.
			//   3. When all fallbacks are exhausted, handleQuotaError returns an error directly.
			//
			// Note: handleQuotaError calls RecordModelRateLimitHit for the primary model.
			// When invoked from this transient-error path that label is semantically imprecise
			// (timeouts ≠ rate limits), but the circuit-breaker backpressure it introduces is
			// still a useful safety net while the model is in a degraded state.
			return handleQuotaError(rc, fallbackModels, false) // transient exhaustion — do not trip circuit
		}
	}

	// No fallbacks available
	ctx.GetLogger().Error("STRATEGY 3 FAILED: Max retry attempts reached and no fallback models available",
		"maxAttempts", maxAttempts,
		"lastError", safeError(rc.lastErr),
		"model", rc.currentModel)

	return nil, buildCallMetadata(rc, false),
		fmt.Errorf("transient error persisted after %d attempts: %w", maxAttempts, rc.lastErr)
}

// ============================================================================
// MAIN RETRY FUNCTION: Routes to appropriate strategy based on error type
// ============================================================================

// generateLLMContentWithRetry attempts to generate content with retries and backoff
// Returns the completion and metadata about the call for token tracking
func generateLLMContentWithRetry(ctx *security.RequestContext, llm llms.Model, promptMessages []llms.MessageContent, options []llms.CallOption, agentName string, agentId string, accountId string, conversationId string, messageId string, disableRetry bool, userId string, resolution ...*LLMConfigResolution) (*llms.ContentResponse, *LLMCallMetadata, error) {
	// Optimization: Use provided resolution context if available
	var res *LLMConfigResolution
	if len(resolution) > 0 {
		res = resolution[0]
	}

	// Validate inputs
	if ctx == nil {
		return nil, nil, fmt.Errorf("request context is nil")
	}

	// Ensure maxAttempts has a sensible default
	maxAttempts := config.Config.LlmProviderMaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	// Sanitize input messages to prevent UTF-8 errors
	promptMessages = sanitizeMessageContent(promptMessages)

	// Initialize retry context with deep copy of messages
	originalMessages := make([]llms.MessageContent, len(promptMessages))
	copy(originalMessages, promptMessages)

	// Global budget for the entire retry sequence (including initial attempt)
	totalStart := time.Now()
	maxTotalDuration := time.Duration(config.Config.LlmServerGlobalRetryBudgetMinutes) * time.Minute
	// If it's a thinking model or a complex task, we might want to allow more time,
	// but this is a solid "safety ceiling" for a single agent step.

	disableCaching, _ := ctx.GetContext().Value(ContextKeyDisableCaching).(bool)
	cacheScope, _ := ctx.GetContext().Value(ContextKeyCacheScope).(CacheScope)
	if cacheScope == "" {
		cacheScope = CacheScopeConversation
	}
	capabilities, _ := ctx.GetContext().Value(ContextKeyCapabilities).(map[string]any)

	rc := &retryContext{
		ctx:              ctx,
		llm:              llm,
		promptMessages:   promptMessages,
		options:          options,
		agentId:          agentId,
		agentName:        agentName,
		triedModels:      make(map[string]bool),
		errorHistory:     make([]string, 0),
		accountId:        accountId,
		conversationId:   conversationId,
		messageId:        messageId,
		userId:           userId,
		totalStart:       totalStart,
		maxTotalDuration: maxTotalDuration,
		enableCaching:    config.Config.LlmEnableCaching && !disableCaching,
		cacheScope:       cacheScope,
		capabilities:     capabilities,
	}

	// Use the provided resolution or resolve it now
	if res == nil {
		res, _ = ResolveLLMConfig(ctx, accountId, agentName, conversationId)
	}

	rc.currentProvider = GetLLMProvider(ctx, accountId, rc.agentName, true, conversationId, res)
	rc.currentModel = GetLLMModelName(ctx, accountId, rc.currentProvider, rc.agentName, true, conversationId, res)

	// Sync rc.currentModel/rc.currentProvider with any runtime overrides applied by the caller
	// when creating rc.llm. If these diverge, the cache key will reference a different model
	// than the one actually used in GenerateContent, causing Google AI 400 errors.
	rc.currentProvider, rc.currentModel = syncModelWithContextOverrides(ctx, rc.currentProvider, rc.currentModel, accountId)

	rc.triedModels[rc.currentModel] = true

	// Check if conversation has explicit model configuration
	conversationHasExplicitModel := res != nil && res.IsOverridden

	// Parse and validate fallback models
	fallbackModelsRaw := getLLMFallbackModelName(accountId, rc.agentName, true, res)
	var fallbackModels []string
	if fallbackModelsRaw != "" && !conversationHasExplicitModel {
		for model := range strings.SplitSeq(fallbackModelsRaw, ",") {
			trimmed := strings.TrimSpace(model)
			if trimmed != "" && trimmed != rc.currentModel {
				fallbackModels = append(fallbackModels, trimmed)
			}
		}
	} else if conversationHasExplicitModel {
		ctx.GetLogger().Info("Skipping fallback models because conversation has explicit model configuration")
		disableRetry = true
	}

	// ============================================================================
	// DISABLE RETRY PATH: Single attempt only (used during summarization to prevent recursion)
	// ============================================================================
	if disableRetry {
		ctx.GetLogger().Info("Retry disabled, single attempt only", "agentId", agentId)
		rc.attemptCount = 0
		if completion, err := tryWithModel(rc); err == nil {
			return completion, buildCallMetadata(rc, true), nil
		}
		logAttemptError(rc, 0, 1)
		return nil, buildCallMetadata(rc, false), ErrLlmUnableToGenerate(rc.lastErr)
	}

	// ============================================================================
	// INITIAL ATTEMPT: Try primary model first
	// ============================================================================

	// Check circuit breaker before trying primary model
	if IsModelCircuitOpen(rc.currentProvider, rc.currentModel) {
		ctx.GetLogger().Warn("Circuit breaker open for primary model, skipping to fallbacks",
			"provider", rc.currentProvider, "model", rc.currentModel)

		if len(fallbackModels) > 0 {
			return handleQuotaError(rc, fallbackModels, false) // circuit already open — do not extend cooldown
		}
		// No fallbacks available, try anyway (circuit may have recovered)
		ctx.GetLogger().Warn("No fallback models available despite open circuit breaker, trying primary model anyway",
			"provider", rc.currentProvider, "model", rc.currentModel)
	}

	ctx.GetLogger().Info("Starting LLM content generation",
		"agentId", agentId,
		"model", rc.currentModel,
		"provider", rc.currentProvider,
		"messageCount", len(promptMessages))

	rc.attemptCount = 0
	completion, err := tryWithModel(rc)

	if err == nil {
		// Success on first try!
		ctx.GetLogger().Info("Primary model call successful",
			"model", rc.currentModel,
			"agentId", agentId)
		return completion, buildCallMetadata(rc, true), nil
	}

	rc.lastErr = err

	// ============================================================================
	// SLOW MODEL DETECTION: If first call was unusually slow and failed, skip retries and use fallbacks
	// Threshold: 60% of max individual call timeout (e.g. 3 mins if timeout is 5 mins), min 3 mins.
	// ============================================================================
	slowModelThresholdSeconds := math.Max(180.0, float64(config.Config.LlmServerMaxIndividualCallTimeoutMinutes)*60.0*0.6)
	if rc.lastLatency > slowModelThresholdSeconds {
		ctx.GetLogger().Warn("Slow model call detected, bypassing primary model retries",
			"latency", rc.lastLatency,
			"threshold", slowModelThresholdSeconds,
			"model", rc.currentModel,
			"error", safeError(err))

		if len(fallbackModels) > 0 {
			ctx.GetLogger().Info("Routing to STRATEGY 2 (Fallback) due to slow primary model")
			return handleQuotaError(rc, fallbackModels, false)
		}
	}

	// ============================================================================
	// ERROR ROUTING: Classify error and route to appropriate strategy
	// ============================================================================
	ctx.GetLogger().Warn("Primary model failed, analyzing error type",
		"error", safeError(err),
		"model", rc.currentModel,
		"agentId", agentId)

	// Check if we're already in summarization mode (prevent infinite recursion)
	isSummarization, _ := rc.ctx.GetContext().Value(summarizationCtxKey).(bool)

	// Route based on error type
	if isTokenLimitError(rc.lastErr) {
		// STRATEGY 1: Token Limit → Chunking with Context Preservation
		if isSummarization {
			ctx.GetLogger().Error("Token limit error during summarization (recursive), cannot handle",
				"error", safeError(rc.lastErr))
			return nil, buildCallMetadata(rc, false),
				fmt.Errorf("token limit error occurred during summarization itself: %w", rc.lastErr)
		}
		ctx.GetLogger().Info("Routing to STRATEGY 1: Token Limit Handling")
		return handleTokenLimitError(rc)

	} else if isQuotaError(rc.lastErr) {
		// STRATEGY 2: Quota/Rate Limit → Fallback Models
		ctx.GetLogger().Info("Routing to STRATEGY 2: Quota/Rate Limit Handling")
		return handleQuotaError(rc, fallbackModels, true) // genuine quota error — trip circuit

	} else if isEmptyResponseError(rc.lastErr) {
		// STRATEGY 3a: Empty LLM Response → immediate retry first, then bounded backoff.
		// Empty responses are a model behaviour issue (e.g. gemini-3-flash-preview instability),
		// NOT a network issue. Most cases resolve on the first immediate retry (DB data shows
		// retry_attempt=1 succeeds in the majority of cases). We avoid adding unnecessary latency
		// by retrying immediately on attempt 1, then applying a small backoff on attempt 2.
		// Total retries kept at 2 to avoid long waits in multi-step agent plans.
		ctx.GetLogger().Warn("Routing to STRATEGY 3a: Empty LLM Response — immediate + bounded backoff retry", "model", rc.currentModel, "agentId", agentId)
		const (
			emptyResponseMaxRetries = 2
			emptyResponseMaxBackoff = 5 * time.Second
		)
		for attempt := 1; attempt <= emptyResponseMaxRetries; attempt++ {
			rc.attemptCount = attempt

			// Attempt 1: retry immediately (most empty responses are transient model flukes)
			// Attempt 2+: small backoff (2s base, capped at 5s)
			if attempt > 1 {
				const baseBackoff = 2 * time.Second
				base := time.Duration(math.Pow(2, float64(attempt-2))) * baseBackoff
				jitter := time.Duration(0)
				if base > 0 {
					jitter = time.Duration(rand.Int64N(int64(base) / 2))
				}
				backoff := min(base+jitter, emptyResponseMaxBackoff)
				ctx.GetLogger().Warn("Empty response backoff before retry",
					"attempt", attempt, "backoff", backoff, "model", rc.currentModel)
				time.Sleep(backoff)
			} else {
				ctx.GetLogger().Warn("Empty response immediate retry",
					"attempt", attempt, "model", rc.currentModel)
			}

			completion, retryErr := tryWithModel(rc)
			if retryErr == nil {
				return completion, buildCallMetadata(rc, true), nil
			}
			ctx.GetLogger().Warn("Empty response retry failed", "attempt", attempt, "error", safeError(retryErr))
			rc.lastErr = retryErr
		}
		ctx.GetLogger().Error("Empty response persisted after fast retries, giving up", "model", rc.currentModel, "agentId", agentId)
		return nil, buildCallMetadata(rc, false), ErrLlmUnableToGenerate(rc.lastErr)

	} else if isTransientError(rc.lastErr) {
		// STRATEGY 3: Transient Error → Retry with Exponential Backoff
		ctx.GetLogger().Info("Routing to STRATEGY 3: Transient Error Handling")
		return handleTransientError(rc, maxAttempts)

	} else if isCacheError(rc.lastErr) {
		// Cache Error → Return to caller to retry without cache
		ctx.GetLogger().Info("Cache error detected, returning to caller for non-cached retry",
			"error", safeError(rc.lastErr))
		return nil, buildCallMetadata(rc, false), rc.lastErr

	} else if isProgramError(rc.lastErr) {
		// Program error (nil pointer, etc.) — retry once in case of race condition
		ctx.GetLogger().Error("Program error detected, retrying once",
			"error", safeError(rc.lastErr), "model", rc.currentModel, "agentId", agentId)
		rc.attemptCount = 1
		time.Sleep(time.Duration(config.Config.LlmServerLlmInitialBackoffSeconds) * time.Second)
		completion, retryErr := tryWithModel(rc)
		if retryErr == nil {
			return completion, buildCallMetadata(rc, true), nil
		}
		ctx.GetLogger().Error("Program error persisted after retry",
			"error", safeError(retryErr), "model", rc.currentModel)
		return nil, buildCallMetadata(rc, false), ErrLlmUnableToGenerate(retryErr)

	} else {
		// NON-RETRYABLE ERROR: Give up immediately
		ctx.GetLogger().Error("Non-retryable error encountered",
			"error", safeError(rc.lastErr),
			"model", rc.currentModel,
			"agentId", agentId)
		logAttemptError(rc, 0, 1)
		return nil, buildCallMetadata(rc, false), ErrLlmUnableToGenerate(rc.lastErr)
	}
}

// isTokenLimitError checks if the error is specifically related to token limits or message size
// This indicates the prompt is too long for the model's context window
func isTokenLimitError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(safeError(err))
	return strings.Contains(errMsg, "token limit") ||
		strings.Contains(errMsg, "context length") ||
		strings.Contains(errMsg, "context window") ||
		strings.Contains(errMsg, "maximum tokens") ||
		strings.Contains(errMsg, "too many tokens") ||
		strings.Contains(errMsg, "maximum context") ||
		strings.Contains(errMsg, "prompt is too long") ||
		strings.Contains(errMsg, "too large") ||
		strings.Contains(errMsg, "exceeds maximum") ||
		(strings.Contains(errMsg, "400") && (strings.Contains(errMsg, "token") || strings.Contains(errMsg, "large")))
}

// isQuotaError checks if the error is related to API quota or rate limits
// This indicates we should try fallback models with different quota pools
func isQuotaError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(safeError(err))
	return strings.Contains(errMsg, "quota exceeded") ||
		strings.Contains(errMsg, "quota_exceeded") ||
		strings.Contains(errMsg, "rate limit") ||
		strings.Contains(errMsg, "rate_limit") ||
		strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "too many requests") ||
		strings.Contains(errMsg, "billing limit") ||
		strings.Contains(errMsg, "insufficient quota") ||
		strings.Contains(errMsg, "resource exhausted")
}

// isTransientError checks if the error is temporary and might succeed on retry
// This includes network issues, timeouts, and temporary server errors
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(safeError(err))
	return strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "timed out") ||
		strings.Contains(errMsg, "408") ||
		strings.Contains(errMsg, "500") ||
		strings.Contains(errMsg, "502") ||
		strings.Contains(errMsg, "503") ||
		strings.Contains(errMsg, "504") ||
		strings.Contains(errMsg, "gateway") ||
		strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "network") ||
		strings.Contains(errMsg, "streaming error") ||
		strings.Contains(errMsg, "model has timed out") ||
		strings.Contains(errMsg, "deadline exceeded")
}

// isDeadlineExceededError checks if the error is specifically a context deadline exceeded.
// This is a subset of transient errors that indicates the model hung for the full timeout
// period without responding — retrying the same model is futile, so we skip straight to
// fallback models instead of burning the retry budget on exponential backoff.
func isDeadlineExceededError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(safeError(err))
	return strings.Contains(errMsg, "deadline exceeded")
}

// isEmptyResponseError checks if the LLM returned a structurally valid but empty response.
// This is distinct from transient errors (network, timeouts) — empty responses are a model
// behaviour issue and must NOT be retried with exponential backoff.
func isEmptyResponseError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(safeError(err)), "llm returned empty content")
}

// isCacheError checks if the error is specifically related to Google AI or Anthropic caching
func isCacheError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(safeError(err))
	return strings.Contains(errMsg, "cachedcontent not found") ||
		strings.Contains(errMsg, "cache not found") ||
		strings.Contains(errMsg, "cached_content_not_found") ||
		(strings.Contains(errMsg, "403") && strings.Contains(errMsg, "cachedcontent")) ||
		(strings.Contains(errMsg, "404") && strings.Contains(errMsg, "cachedcontent")) ||
		// Model mismatch between cached content and inference request (Google AI 400)
		(strings.Contains(errMsg, "cachedcontent") && strings.Contains(errMsg, "has to be the same"))
}

// isProgramError checks if the error is a programming bug (nil pointer, panic recovery)
// These get 1 retry max since they could be caused by a race condition
func isProgramError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(safeError(err))
	return strings.Contains(errMsg, "invalid memory address") ||
		strings.Contains(errMsg, "nil pointer dereference")
}

// isRetryableError checks if an error is retryable (any type that we can handle)
// Returns true for token limits, quota errors, transient errors, cache errors, and program errors
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// All our specific error types are retryable
	return isTokenLimitError(err) || isQuotaError(err) || isTransientError(err) || isEmptyResponseError(err) || isCacheError(err) || isProgramError(err)
}

// syncModelWithContextOverrides returns the effective provider and model for an LLM call,
// accounting for runtime context keys that may have changed the llm client from what the
// config-based resolution returned. It must be called after the config-based resolution so
// that rc.currentModel/currentProvider stay in sync with the model actually used in rc.llm.
//
// Priority (highest wins):
//  1. ContextKeyLlmModelOverride — explicit runtime override from QueryConfig / conversation
//  2. ContextKeyUseLiteModel     — caller swapped to lite/flash model via GetLlmModelLite
//  3. baseProvider / baseModel   — unchanged (config resolution already correct)
//
// When rc.currentModel diverges from the model used in rc.llm, the cache key references
// the wrong model, causing a Google AI 400:
// "Model used by GenerateContent request and CachedContent has to be the same."
func syncModelWithContextOverrides(ctx *security.RequestContext, baseProvider, baseModel, accountId string) (provider, model string) {
	provider = baseProvider
	model = baseModel

	if om, ok := ctx.GetContext().Value(ContextKeyLlmModelOverride).(string); ok && om != "" {
		model = om
		if op, ok := ctx.GetContext().Value(ContextKeyLlmProviderOverride).(string); ok && op != "" {
			provider = op
		}
		return
	}

	if val, ok := ctx.GetContext().Value(ContextKeyUseLiteModel).(bool); ok && val {
		if res, err := ResolveFastLLMConfig(ctx, accountId); err == nil && res.Model != "" {
			model = res.Model
		}
	}
	return
}

// trackTokenUsage tracks token usage for an agent including cache breakdown
func trackTokenUsage(
	ctx *security.RequestContext,
	conversationId string,
	messageId string,
	agentId string,
	agentName string,
	provider string,
	model string,
	accountId string,
	userId string,
	tokenInfo *TokenInfo,
	latency float64,
	retryAttempt int,
	fallbackFromModel *string,
	fallbackChain []string,
	trackContent bool,
	content string,
	stopReason *string,
	promptMessagesJSON *string,
	responseContent *string,
) {
	if tokenInfo == nil {
		return
	}

	if agentId == "" {
		callerName := agentName
		if callerName == "" {
			callerName = "unknown"
		}
		ctx.GetLogger().Warn("llm: tracking token usage without agent ID",
			"caller", callerName, "conversationId", conversationId,
			"accountId", accountId, "provider", provider, "model", model,
			"inputTokens", tokenInfo.InputTokens, "outputTokens", tokenInfo.OutputTokens)
		common.MetricsAgentOperationsTotal(callerName, "no_agent_id", accountId)
	}

	// Log cache breakdown for debugging and cost tracking
	if tokenInfo.CacheReadTokens > 0 {
		ctx.GetLogger().Debug("Token usage breakdown",
			"agentId", agentId,
			"totalInput", tokenInfo.InputTokens,
			"cacheReadTokens", tokenInfo.CacheReadTokens,
			"cacheCreationTokens", tokenInfo.CacheCreationTokens,
			"nonCachedTokens", tokenInfo.InputTokens-tokenInfo.CacheReadTokens,
			"outputTokens", tokenInfo.OutputTokens,
			"cacheSavings", fmt.Sprintf("%.1f%%", float64(tokenInfo.CacheReadTokens)/float64(tokenInfo.InputTokens)*100))
	}

	// Parse agentId to check if it's a valid UUID (skip nil UUID which indicates a failed agent record creation)
	var agentUUID *string
	if parsedUUID, err := uuid.Parse(agentId); err == nil && parsedUUID != uuid.Nil {
		agentUUID = &agentId
	}

	// Calculate cache hit rate
	var cacheHitRate *float64
	if tokenInfo.InputTokens > 0 && tokenInfo.CacheReadTokens > 0 {
		rate := (float64(tokenInfo.CacheReadTokens) / float64(tokenInfo.InputTokens)) * 100
		cacheHitRate = &rate
	}

	// Calculate content length
	var contentLength *int
	if trackContent && content != "" {
		length := len(content)
		contentLength = &length
	}

	latencyPtr := &latency

	// Convert fallback chain to JSON string
	var fallbackChainJSON *string
	if len(fallbackChain) > 0 {
		chainBytes, err := common.MarshalJson(fallbackChain)
		if err == nil {
			chainStr := string(chainBytes)
			fallbackChainJSON = &chainStr
		} else {
			ctx.GetLogger().Warn("trackTokenUsage: failed to marshal fallback chain", "error", err)
		}
	}

	// Create token usage record for new table
	// Capture cache TTL at insert time for accurate cost calculation
	cacheTTL := config.Config.LlmCacheTTLMinutes
	record := &TokenUsageRecord{
		ConversationID:      conversationId,
		MessageID:           messageId,
		AgentID:             agentUUID,
		AgentName:           agentName,
		AccountID:           accountId,
		UserID:              userId,
		LLMProvider:         provider,
		LLMModel:            model,
		InputTokens:         tokenInfo.InputTokens,
		OutputTokens:        tokenInfo.OutputTokens,
		CachedInputTokens:   tokenInfo.CacheReadTokens,
		CacheCreationTokens: tokenInfo.CacheCreationTokens,
		IsCacheHit:          tokenInfo.CacheReadTokens > 0,
		CacheHitRate:        cacheHitRate,
		RetryAttempt:        retryAttempt,
		FallbackFromModel:   fallbackFromModel,
		FallbackChain:       fallbackChainJSON,
		LatencySeconds:      latencyPtr,
		RequestStatus:       "success",
		ErrorMessage:        nil,
		ContentLength:       contentLength,
		StopReason:          stopReason,
		CacheTTLMinutes:     &cacheTTL,
		PromptMessages:      promptMessagesJSON,
		ResponseContent:     responseContent,
	}

	// Insert into new token usage table
	err := GetConversationDao().InsertTokenUsage(record)
	if err != nil {
		ctx.GetLogger().Error("llm: unable to insert token usage", "error", err)
	}

	// Update thought content on the agent record (if applicable)
	if agentUUID != nil && trackContent && content != "" {
		if err := GetConversationDao().UpdateConversationAgentThought(*agentUUID, content); err != nil {
			ctx.GetLogger().Error("llm: unable to update agent thought", "error", err)
		}
	}
}

// cleanupMarkdownInResponse cleans up markdown content in the response
func cleanupMarkdownInResponse(completion *llms.ContentResponse) {
	if completion == nil || len(completion.Choices) == 0 {
		return
	}

	content := completion.Choices[0].Content
	completion.Choices[0].Content = SanatizeMarkdownCodeBlock(content)
}

func getLLMIntegrationConfig(ctx *security.RequestContext, accountId string, overrides ...map[string]string) (map[string]string, error) {
	if len(overrides) > 0 && overrides[0] != nil {
		return overrides[0], nil
	}
	if accountId == "" {
		slog.Debug("getLLMIntegrationConfig: empty accountId, returning nil")
		return nil, nil
	}

	slog.Debug("Getting LLM integration config", "accountId", accountId)

	llmIntegrationConfigCacheMutex.RLock()
	cacheEntry, found := llmIntegrationConfigCache[accountId]
	llmIntegrationConfigCacheMutex.RUnlock()
	if found && time.Since(cacheEntry.ts) < llmIntegrationConfigCacheTTL {
		slog.Debug("LLM integration config found in cache", "accountId", accountId, "configKeys", len(cacheEntry.config))
		return cacheEntry.config, nil
	}
	slog.Debug("LLM integration config not in cache or expired, fetching from DB", "accountId", accountId)

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("Failed to get database manager for LLM integration config", "error", err, "accountId", accountId)
		return nil, err
	}

	// Try account-level config first
	configMap, err := fetchLLMIntegrationConfigByAccount(ctx, dbManager, accountId)
	if err != nil {
		return nil, err
	}

	if configMap != nil {
		slog.Debug("LLM integration config loaded (account-level)", "accountId", accountId, "configKeys", len(configMap))
	} else {
		// Fallback to tenant-level config if no account-level config found
		tenantId, tenantErr := security.GetTenantIdFromAccountId(accountId)
		if tenantErr != nil {
			return nil, tenantErr
		}
		if tenantId != "" {
			// Check tenant cache first
			llmTenantConfigCacheMutex.RLock()
			tenantEntry, tenantFound := llmTenantConfigCache[tenantId]
			llmTenantConfigCacheMutex.RUnlock()
			if tenantFound && time.Since(tenantEntry.ts) < llmIntegrationConfigCacheTTL {
				slog.Debug("LLM integration config found in tenant cache", "tenantId", tenantId, "configKeys", len(tenantEntry.config))
				configMap = tenantEntry.config
			} else {
				slog.Debug("No account-level LLM config, trying tenant-level", "accountId", accountId, "tenantId", tenantId)
				configMap, err = fetchLLMIntegrationConfigByTenant(ctx, dbManager, tenantId)
				if err != nil {
					return nil, err
				}

				// Cache tenant-level result (even nil — avoids repeated DB queries for tenants without config)
				llmTenantConfigCacheMutex.Lock()
				llmTenantConfigCache[tenantId] = struct {
					config map[string]string
					ts     time.Time
				}{config: configMap, ts: time.Now()}
				llmTenantConfigCacheMutex.Unlock()

				if configMap != nil {
					slog.Info("Using tenant-level LLM integration config", "accountId", accountId, "tenantId", tenantId, "configKeys", len(configMap))
				}
			}
		}
	}

	// Always cache the resolved result (account-level, tenant-level, or nil) under the account key.
	// This avoids repeated DB queries for accounts without their own config. Maps are reference types
	// so caching the tenant's configMap here only copies a pointer.
	llmIntegrationConfigCacheMutex.Lock()
	llmIntegrationConfigCache[accountId] = struct {
		config map[string]string
		ts     time.Time
	}{config: configMap, ts: time.Now()}
	llmIntegrationConfigCacheMutex.Unlock()

	if configMap == nil {
		slog.Debug("No LLM integration config found (account or tenant)", "accountId", accountId)
	}
	return configMap, nil
}

// fetchLLMIntegrationConfigByAccount queries LLM integration config linked to a specific cloud account.
func fetchLLMIntegrationConfigByAccount(ctx *security.RequestContext, dbManager *common.DatabaseManager, accountId string) (map[string]string, error) {
	query := `SELECT i.id, icv.name, icv.value, icv.is_encrypted FROM integrations i JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
			  JOIN integration_config_values icv ON i.id = icv.integration_id
			  WHERE i."type" = 'llm' AND i.status = 'enabled' AND ia.cloud_account_id = :ac_id`
	return execLLMIntegrationConfigQuery(ctx, dbManager, query, map[string]any{"ac_id": accountId}, accountId)
}

// fetchLLMIntegrationConfigByTenant queries LLM integration config at the tenant level —
// integrations that belong to the tenant but are NOT linked to any specific cloud account.
func fetchLLMIntegrationConfigByTenant(ctx *security.RequestContext, dbManager *common.DatabaseManager, tenantId string) (map[string]string, error) {
	query := `SELECT i.id, icv.name, icv.value, icv.is_encrypted FROM integrations i
			  JOIN integration_config_values icv ON i.id = icv.integration_id
			  WHERE i."type" = 'llm' AND i.status = 'enabled' AND i.tenant_id = :tenant_id
			  AND NOT EXISTS (SELECT 1 FROM integrations_cloud_accounts ia WHERE ia.integration_id = i.id)`
	return execLLMIntegrationConfigQuery(ctx, dbManager, query, map[string]any{"tenant_id": tenantId}, tenantId)
}

// execLLMIntegrationConfigQuery runs a named query and scans results into a config map.
// Values flagged with is_encrypted are decrypted before being returned so callers
// always see plaintext regardless of storage format. A decrypt failure is logged
// and the row is skipped — we'd rather fall back to the default credential chain
// or ENV-scoped value than hand the SDK an unusable ciphertext string.
func execLLMIntegrationConfigQuery(ctx *security.RequestContext, dbManager *common.DatabaseManager, query string, params map[string]any, logId string) (map[string]string, error) {
	var rows *sqlx.Rows
	var err error
	if ctx != nil {
		rows, err = dbManager.Db.NamedQueryContext(ctx.GetContext(), query, params)
	} else {
		rows, err = dbManager.Db.NamedQuery(query, params)
	}
	if err != nil {
		slog.Error("Failed to query LLM integration config from DB", "error", err, "id", logId)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Warn("LLM Integration: unable to close rows", "error", err)
		}
	}()
	var configMap map[string]string
	var foundRow bool
	for rows.Next() {
		var id string
		// name, value, is_encrypted are NULL-able in integration_config_values; scan
		// into Null* and treat NULL is_encrypted as false (conservative default).
		var name, value sql.NullString
		var isEncrypted sql.NullBool
		if err := rows.Scan(&id, &name, &value, &isEncrypted); err != nil {
			slog.Error("Failed to scan LLM integration config row", "error", err, "id", logId)
			return nil, err
		}
		if !name.Valid || name.String == "" {
			slog.Warn("LLM integration config: row has empty name; skipping", "id", logId, "integrationId", id)
			continue
		}
		plain := value.String
		if isEncrypted.Valid && isEncrypted.Bool && plain != "" {
			decrypted, decErr := common.Decrypt(plain)
			if decErr != nil {
				slog.Warn("LLM integration config: failed to decrypt encrypted field; skipping row",
					"error", decErr, "id", logId, "integrationId", id, "key", name.String)
				continue
			}
			plain = decrypted
		}
		if !foundRow {
			configMap = make(map[string]string)
			foundRow = true
		}
		configMap[name.String] = plain
		slog.Debug("Found LLM integration config value", "id", logId, "integrationId", id, "key", name.String, "hasValue", plain != "", "isEncrypted", isEncrypted.Valid && isEncrypted.Bool)
	}
	if err := rows.Err(); err != nil {
		slog.Error("Error iterating LLM integration config rows", "error", err, "id", logId)
		return nil, err
	}
	if !foundRow {
		return nil, nil
	}
	return configMap, nil
}

// normalizeModel removes vendor prefixes, timestamps and common suffixes
// so we can match a wide range of vendor/platform model-id variants.
func normalizeModel(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))

	// remove vendor prefix like "anthropic.", "amazon.", "meta.", "google.", "vertex.", "openai."
	if idx := strings.Index(m, "."); idx != -1 {
		pfx := m[:idx]
		if pfx == "anthropic" || pfx == "amazon" || pfx == "meta" ||
			pfx == "google" || pfx == "vertex" || pfx == "openai" {
			m = m[idx+1:]
		}
	}

	// unify some separators and remove common platform suffixes
	m = strings.ReplaceAll(m, "@", "-")
	m = strings.ReplaceAll(m, ":", "-")

	// remove timestamps like -20250805 or -20250522
	reDates := regexp.MustCompile(`-\d{6,8}`)
	m = reDates.ReplaceAllString(m, "")

	// remove -vN variants e.g., -v1, -v2
	reV := regexp.MustCompile(`-v\d+`)
	m = reV.ReplaceAllString(m, "")

	// collapse duplicate hyphens and trim
	for strings.Contains(m, "--") {
		m = strings.ReplaceAll(m, "--", "-")
	}
	m = strings.Trim(m, "-")

	return m
}

func safeError(err error) string {
	if err == nil {
		return "unknown error (nil)"
	}
	return err.Error()
}
