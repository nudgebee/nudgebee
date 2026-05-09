package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/llms/googleai"

	"github.com/tmc/langchaingo/llms"
)

// CacheScope defines the stability level of the cache
type CacheScope string

const (
	CacheScopeGlobal       CacheScope = "global"       // Stable across all conversations (e.g., system worker instructions)
	CacheScopeAccount      CacheScope = "account"      // Stable within an account (e.g., company standards)
	CacheScopeConversation CacheScope = "conversation" // Specific to a conversation (default)
)

// defaultStaticCacheTTL is the TTL used for Global and Account cache scopes.
// 12 hours provides "workday" stability while ensuring caches eventually cycle
// to pick up any underlying code/prompt updates.
const defaultStaticCacheTTL = 12 * time.Hour

// CacheRequest contains all information needed for caching
type CacheRequest struct {
	AccountId      string
	ConversationId string
	AgentName      string // Agent type/name (not per-request ID) for stable cross-request cache keys
	Model          string
	Provider       string
	Messages       []llms.MessageContent
	ApiKey         string
	Scope          CacheScope
	Capabilities   map[string]any // Optional; used to isolate cache slots when tool set varies per request
}

// CacheResponse contains the result of cache operation
type CacheResponse struct {
	// Modified messages (with inline cache control if applicable)
	Messages []llms.MessageContent

	// Options to add to the LLM call (e.g., cached content name for Google AI)
	Options []llms.CallOption

	// Whether cache was hit (true) or miss (false)
	CacheHit bool

	// Error if any
	Error error

	// CacheInfo (optional, provider-specific)
	CacheInfo *CacheInfo
}

// CacheProvider is an interface for provider-specific caching implementations
type CacheProvider interface {
	// ApplyCache checks for existing cache or creates new one, returns modified messages and options
	ApplyCache(ctx context.Context, req *CacheRequest) *CacheResponse

	// InvalidateCache removes cache for the given request
	InvalidateCache(ctx context.Context, req *CacheRequest) error

	// GetProviderName returns the name of the provider this cache implementation supports
	GetProviderName() string
}

// CacheManager manages caching across different LLM providers
type CacheManager struct {
	providers map[string]CacheProvider
	mutex     sync.RWMutex
}

var (
	globalCacheManager *CacheManager
	cacheManagerOnce   sync.Once
)

// GetCacheManager returns the global cache manager instance (singleton)
func GetCacheManager() *CacheManager {
	cacheManagerOnce.Do(func() {
		globalCacheManager = &CacheManager{
			providers: make(map[string]CacheProvider),
		}

		// Register provider-specific cache implementations
		googleAIProvider := NewGoogleAICacheProvider()
		globalCacheManager.RegisterProvider(googleAIProvider)

		anthropicProvider := NewAnthropicCacheProvider()
		globalCacheManager.RegisterProvider(anthropicProvider)

		slog.Info("Cache manager initialized",
			"providers", []string{googleAIProvider.GetProviderName(), anthropicProvider.GetProviderName()})
	})
	return globalCacheManager
}

// RegisterProvider registers a cache provider implementation
func (cm *CacheManager) RegisterProvider(provider CacheProvider) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.providers[provider.GetProviderName()] = provider
}

// ApplyCache applies caching based on the provider
func (cm *CacheManager) ApplyCache(ctx context.Context, req *CacheRequest) *CacheResponse {
	cm.mutex.RLock()
	provider, exists := cm.providers[req.Provider]
	cm.mutex.RUnlock()

	if !exists {
		slog.Debug("No cache provider registered for provider", "provider", req.Provider)
		return &CacheResponse{
			Messages:  req.Messages,
			Options:   nil,
			CacheHit:  false,
			Error:     nil,
			CacheInfo: nil,
		}
	}

	return provider.ApplyCache(ctx, req)
}

// InvalidateCache invalidates cache for the given request
func (cm *CacheManager) InvalidateCache(ctx context.Context, req *CacheRequest) error {
	cm.mutex.RLock()
	provider, exists := cm.providers[req.Provider]
	cm.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("no cache provider registered for provider: %s", req.Provider)
	}

	return provider.InvalidateCache(ctx, req)
}

// Stop stops the cache manager
func (cm *CacheManager) Stop() {
	// Cleanup if needed
}

// ========== Google AI Cache Provider ==========

const GoogleAICacheNamespace = "llm_googleai_cache"

// GoogleAICacheProvider implements caching for Google AI (pre-created cached content)
type GoogleAICacheProvider struct {
	namespace string
}

type CacheInfo struct {
	CacheName           string    `json:"cache_name"`
	AccountId           string    `json:"account_id"`
	ConversationId      string    `json:"conversation_id"`
	AgentName           string    `json:"agent_name"`
	Model               string    `json:"model"`
	CreatedAt           time.Time `json:"created_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	ContentHash         string    `json:"content_hash"`
	CacheCreationTokens int32     `json:"cache_creation_tokens"`
}

func NewGoogleAICacheProvider() *GoogleAICacheProvider {
	// Register the namespace with the shared cache manager. The namespace-level TTL is set to
	// the longer static scope TTL so that Global/Account entries are not evicted prematurely.
	// Individual Conversation-scope entries use their own shorter TTL via CacheSetWithExpiration,
	// which overrides the namespace default on a per-entry basis.
	common.CacheCreateNamespace(GoogleAICacheNamespace,
		common.CacheNamespaceWithExpiration(defaultStaticCacheTTL),
		common.CacheNamespaceWithMaxEntries(config.Config.CacheInMemoryMaxEntries),
	)

	return &GoogleAICacheProvider{
		namespace: GoogleAICacheNamespace,
	}
}

func (p *GoogleAICacheProvider) GetProviderName() string {
	return "googleai"
}

func (p *GoogleAICacheProvider) ApplyCache(ctx context.Context, req *CacheRequest) *CacheResponse {
	// Append a capability fingerprint to agentName so that requests with different
	// allowed_tools sets get distinct Google AI CachedContent slots. Google AI uses
	// a single slot per cache key, so alternating tool scopes would otherwise thrash
	// each other. Anthropic uses inline cache_control (content-addressed) and is unaffected.
	agentName := req.AgentName
	if fp := capabilityFingerprint(req.Capabilities); fp != "" {
		agentName = req.AgentName + ":" + fp
	}

	// Generate cache key based on scope
	cacheKey := generateCacheKey(req.Scope, req.AccountId, req.ConversationId, agentName, req.Model)

	slog.Info("Google AI cache: Starting cache check",
		"conversationId", req.ConversationId,
		"agentName", agentName,
		"model", req.Model,
		"totalMessages", len(req.Messages))

	// NOTE on Caching Strategy: The Google AI caching API treats the cacheable history as a single, immutable block.
	// It only allows providing a single `CachedContentName` per API call and does not support "stitching" multiple
	// smaller caches together. Therefore, our strategy is to treat the entire conversation history before the
	// last human message as one cacheable unit. When this history changes (e.g., a new message pair is added),
	// we must create a brand new cache for the entire updated history and delete the old, stale one.

	// Identify cacheable messages based on the requested scope
	cacheableMessages, nonCacheableMessages := identifyCacheableMessages(req.Messages, req.Scope)
	if len(cacheableMessages) == 0 {
		slog.Info("Google AI cache: Not using cache - No cacheable messages found",
			"conversationId", req.ConversationId,
			"reason", "no_cacheable_messages",
			"totalMessages", len(req.Messages))
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	slog.Debug("Google AI cache: Identified cacheable messages",
		"conversationId", req.ConversationId,
		"cacheableMessages", len(cacheableMessages),
		"nonCacheableMessages", len(nonCacheableMessages))

	// Check if cacheable messages meet Google AI's minimum token requirement.
	// Minimum varies by model: 2.5 Pro = 4,096 tokens; 2.5 Flash = 1,024 tokens;
	// 1.5 Pro/Flash = 32,768 tokens. Returns 0 if the model does not support caching.
	minGoogleAITokens := GetLlmMinCacheTokens(req.Model)
	if minGoogleAITokens == 0 {
		slog.Info("Google AI cache: Not using cache - Model does not support context caching",
			"model", req.Model,
			"conversationId", req.ConversationId,
			"reason", "model_no_cache_support")
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	// Step 1: Local Estimation (Optimization)
	// Avoid expensive API calls if local estimate is clearly below threshold
	if err := InitTokenizers(); err == nil {
		localCount := 0
		for _, msg := range cacheableMessages {
			// Extract actual text content from parts to avoid fmt.Sprintf overhead/inaccuracy
			contentStr := ""
			for _, part := range msg.Parts {
				if textPart, ok := part.(llms.TextContent); ok {
					contentStr += textPart.Text
				}
			}

			// Estimate using fallback tokenizer (cl100k_base is a decent proxy for most LLMs)
			c, _ := CountTokens("openai", "gpt-4", contentStr)
			localCount += c
		}
		// The cl100k_base tokenizer (GPT-4) significantly underestimates Gemini token counts
		// — empirically by 5–10x for typical agent system prompts (code, JSON, markdown).
		// Only skip the CountTokens API call for clearly tiny inputs; threshold is
		// minGoogleAITokens/10 so even a 10x correction stays below minimum.
		if localCount < (minGoogleAITokens / 10) {
			slog.Info("Google AI cache: Not using cache - Local token estimate too low",
				"localEstimate", localCount,
				"minRequired", minGoogleAITokens,
				"conversationId", req.ConversationId)
			return &CacheResponse{
				Messages: req.Messages,
				CacheHit: false,
			}
		}
	}

	// Use Google AI's CountTokens API for accurate token counting
	cachingHelper, err := googleai.NewCachingHelper(ctx, googleai.WithAPIKey(req.ApiKey))
	if err != nil {
		slog.Warn("Google AI cache: Not using cache - Failed to create caching helper",
			"error", err,
			"conversationId", req.ConversationId,
			"reason", "caching_helper_init_failed")
		// Fallback to no caching if we can't count tokens
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
			Error:    err,
		}
	}

	tokenCount, err := cachingHelper.CountTokens(ctx, req.Model, cacheableMessages)
	if err != nil {
		slog.Warn("Google AI cache: Not using cache - Token counting failed",
			"error", err,
			"conversationId", req.ConversationId,
			"reason", "token_count_failed")
		// Fallback to no caching if token counting fails
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
			Error:    err,
		}
	}

	slog.Info("Google AI cache: Token count for cacheable messages",
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"minRequired", minGoogleAITokens,
		"meetsRequirement", int(tokenCount) >= minGoogleAITokens)

	if int(tokenCount) < minGoogleAITokens {
		// Auto-pad Global/Account scope system prompts if they fall short of the cache threshold.
		// Flash models require 1024 tokens. Small internal system prompts (e.g. title_generation)
		// are around 200 tokens. Without this padding, they are rejected by Google AI caching.
		if (req.Scope == CacheScopeGlobal || req.Scope == CacheScopeAccount) && len(cacheableMessages) > 0 {
			padText := "\n\n--- CACHE STABILITY PADDING ---\n" +
				"The following is standard operating procedure text appended to ensure the system prompt meets minimum cache size requirements for the LLM provider. " +
				"You are Nubi, an AI assistant. Always be helpful, concise, and accurate. " +
				"Follow all instructions precisely. Do not hallucinate. Respect user privacy. " +
				"Maintain a professional tone. Respond in the requested format. " +
				"Analyze context carefully. Be reliable and efficient. "
			// Repeat to ensure it exceeds ~1100 tokens (each repeat is ~20 words / ~25 tokens. We need ~1000 tokens = ~40 repeats)
			padText += strings.Repeat("Focus on the primary task. Ignore this padding text during your actual reasoning process. Ensure output is syntactically valid. Provide high-quality insights. ", 40)

			// Append to the last message in the cacheable block (which will be a System message)
			// CRITICAL: Deep copy the Parts slice to prevent mutating the original request (which could affect fallback models)
			lastIdx := len(cacheableMessages) - 1
			newParts := make([]llms.ContentPart, len(cacheableMessages[lastIdx].Parts))
			copy(newParts, cacheableMessages[lastIdx].Parts)
			newParts = append(newParts, llms.TextContent{Text: padText})

			// Create a new slice for cacheableMessages so we don't mutate the backing array of req.Messages
			newCacheable := make([]llms.MessageContent, len(cacheableMessages))
			copy(newCacheable, cacheableMessages)
			newCacheable[lastIdx].Parts = newParts
			cacheableMessages = newCacheable

			// Recalculate tokens after padding
			tokenCount, err = cachingHelper.CountTokens(ctx, req.Model, cacheableMessages)
			if err != nil {
				slog.Warn("Google AI cache: Token recount failed after padding", "error", err)
			}
		} else {
			slog.Info("Google AI cache: Not using cache - Token count below minimum",
				"tokenCount", tokenCount,
				"minRequired", minGoogleAITokens,
				"deficit", minGoogleAITokens-int(tokenCount),
				"conversationId", req.ConversationId,
				"reason", "insufficient_tokens")
			return &CacheResponse{
				Messages: req.Messages,
				CacheHit: false,
			}
		}
	}

	// Check if token count exceeds model's context window limit
	maxTokens := GetLlmMaxTokenLength(req.Model)
	if tokenCount > int32(maxTokens) {
		slog.Warn("Google AI cache: Not using cache - Token count exceeds model's context window limit",
			"tokenCount", tokenCount,
			"maxTokens", maxTokens,
			"model", req.Model,
			"conversationId", req.ConversationId,
			"reason", "exceeds_context_window")
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	// Calculate content hash
	contentHash := hashContent(cacheableMessages)

	// Check if cache exists and is valid (shared cache)
	var cacheInfo CacheInfo
	exists := false
	if data, ok := common.CacheGet(p.namespace, cacheKey); ok {
		if err := json.Unmarshal(data, &cacheInfo); err == nil {
			exists = true
		} else {
			slog.Warn("Google AI cache: Failed to unmarshal cache info, clearing bad entry", "error", err, "cacheKey", cacheKey)
			if delErr := common.CacheDelete(p.namespace, cacheKey); delErr != nil {
				slog.Warn("Google AI cache: Failed to delete corrupt entry", "error", delErr, "cacheKey", cacheKey)
			}
		}
	}

	now := time.Now()

	// Cache hit path
	if exists && cacheInfo.ExpiresAt.After(now) && cacheInfo.ContentHash == contentHash {
		// Verify the cache actually exists in Google AI
		if p.verifyCacheExists(ctx, cacheInfo.CacheName, req.ApiKey) {
			timeToExpiry := cacheInfo.ExpiresAt.Sub(now)
			slog.Info("Google AI cache: CACHE HIT - Using existing cache",
				"cacheName", cacheInfo.CacheName,
				"conversationId", req.ConversationId,
				"tokenCount", tokenCount,
				"cacheAge", now.Sub(cacheInfo.CreatedAt).String(),
				"timeToExpiry", timeToExpiry.String(),
				"cachedMessages", len(cacheableMessages),
				"nonCachedMessages", len(nonCacheableMessages),
				"status", "hit")
			common.MetricsLLMCacheTotal(req.Provider, req.Model, "hit", req.AccountId)

			// IMPORTANT: Return only non-cacheable messages
			// The cached content is automatically prepended by Google AI when using CachedContentName
			return &CacheResponse{
				Messages: nonCacheableMessages,
				Options: []llms.CallOption{
					func(o *llms.CallOptions) {
						if o.Metadata == nil {
							o.Metadata = make(map[string]any)
						}
						o.Metadata["CachedContentName"] = cacheInfo.CacheName
					},
				},
				CacheHit: true,
			}
		} else {
			slog.Warn("Google AI cache: Cache entry exists in storage but not in Google AI, will recreate",
				"cacheName", cacheInfo.CacheName,
				"conversationId", req.ConversationId,
				"reason", "cache_verification_failed")
			if err := common.CacheDelete(p.namespace, cacheKey); err != nil {
				slog.Error("Google AI cache: Failed to delete stale cache entry", "error", err, "cacheKey", cacheKey)
			}
		}
	} else if exists {
		// Log why cache was not used and handle stale cache deletion
		var reason string
		if !cacheInfo.ExpiresAt.After(now) {
			reason = "cache_expired"
			slog.Info("Google AI cache: Not using cache - Cache expired",
				"conversationId", req.ConversationId,
				"expiredAt", cacheInfo.ExpiresAt,
				"reason", reason)
		} else if cacheInfo.ContentHash != contentHash {
			reason = "content_changed"
			slog.Info("Google AI cache: Not using cache - Content has changed, deleting old cache",
				"conversationId", req.ConversationId,
				"oldCacheName", cacheInfo.CacheName,
				"reason", reason)
			// Explicitly delete the old Google AI cache. The Redis pointer is about
			// to be overwritten by createCache below, so this cacheName becomes
			// unreachable. Without this delete, it sits orphaned for the remainder
			// of its TTL paying full storage cost — historically the dominant cause
			// of Gemini cache spend on this service.
			if helper, helperErr := googleai.NewCachingHelper(ctx, googleai.WithAPIKey(req.ApiKey)); helperErr == nil {
				if delErr := helper.DeleteCachedContent(ctx, cacheInfo.CacheName); delErr != nil {
					slog.Warn("Google AI cache: failed to delete orphaned content_changed cache",
						"error", delErr,
						"cacheName", cacheInfo.CacheName,
						"conversationId", req.ConversationId)
				}
			} else {
				slog.Warn("Google AI cache: failed to init helper for orphan deletion",
					"error", helperErr,
					"conversationId", req.ConversationId)
			}
		}
	}

	// Cache miss - create new cache
	slog.Info("Google AI cache: CACHE MISS - Creating new cache",
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"cachedMessages", len(cacheableMessages),
		"nonCachedMessages", len(nonCacheableMessages),
		"status", "miss")
	common.MetricsLLMCacheTotal(req.Provider, req.Model, "miss", req.AccountId)

	cacheInfoResult, errCreate := p.createCache(ctx, req, cacheableMessages, contentHash, cacheKey, tokenCount)
	if errCreate != nil {
		slog.Error("Google AI cache: Failed to create cache",
			"error", errCreate,
			"conversationId", req.ConversationId,
			"tokenCount", tokenCount,
			"reason", "cache_creation_failed")
		common.MetricsLLMCacheTotal(req.Provider, req.Model, "error", req.AccountId)
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
			Error:    errCreate,
		}
	}

	slog.Info("Google AI cache: Successfully created new cache",
		"cacheName", cacheInfoResult.CacheName,
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"cachedMessages", len(cacheableMessages),
		"nonCachedMessages", len(nonCacheableMessages),
		"ttl", getCacheTTL(req.Scope).String())

	// IMPORTANT: Return only non-cacheable messages
	// The cached content is automatically prepended by Google AI when using CachedContentName
	return &CacheResponse{
		Messages: nonCacheableMessages,
		Options: []llms.CallOption{
			func(o *llms.CallOptions) {
				if o.Metadata == nil {
					o.Metadata = make(map[string]any)
				}
				o.Metadata["CachedContentName"] = cacheInfoResult.CacheName
			},
		},
		CacheHit:  false,
		CacheInfo: cacheInfoResult,
	}
}

func (p *GoogleAICacheProvider) createCache(ctx context.Context, req *CacheRequest, cacheableMessages []llms.MessageContent, contentHash, cacheKey string, tokenCount int32) (*CacheInfo, error) {
	cachingHelper, err := googleai.NewCachingHelper(ctx, googleai.WithAPIKey(req.ApiKey))
	if err != nil {
		return nil, err
	}

	ttl := getCacheTTL(req.Scope)

	// Create a display name that fits within Google AI's 128 character limit
	// Use conversation ID (meaningful for debugging) + hash of full cache key
	displayName := fmt.Sprintf("conv_%s_%s", req.ConversationId, contentHash[:16])
	if len(displayName) > 128 {
		// Fallback: use just the hash if conversation ID is very long
		displayName = fmt.Sprintf("cache_%s", contentHash[:32])
	}

	slog.Debug("Google AI cache: Calling CreateCachedContent API",
		"conversationId", req.ConversationId,
		"model", req.Model,
		"tokenCount", tokenCount,
		"ttl", ttl.String(),
		"cacheableMessages", len(cacheableMessages),
		"displayName", displayName)

	cachedContent, err := cachingHelper.CreateCachedContent(ctx, req.Model, cacheableMessages, ttl, displayName)
	if err != nil {
		return nil, err
	}

	slog.Debug("Google AI cache: CreateCachedContent API returned",
		"cacheName", cachedContent.Name,
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"ttl", ttl)

	// Store cache info
	cacheInfo := &CacheInfo{
		CacheName:           cachedContent.Name,
		AccountId:           req.AccountId,
		ConversationId:      req.ConversationId,
		AgentName:           req.AgentName,
		Model:               req.Model,
		CreatedAt:           time.Now(),
		ExpiresAt:           time.Now().Add(ttl),
		ContentHash:         contentHash,
		CacheCreationTokens: tokenCount,
	}

	if data, err := json.Marshal(cacheInfo); err == nil {
		if err := common.CacheSet(p.namespace, cacheKey, data, common.CacheSetWithExpiration(ttl)); err != nil {
			slog.Error("Google AI cache: Failed to store cache info", "error", err, "cacheKey", cacheKey)
		}
	} else {
		slog.Error("Google AI cache: Failed to marshal cache info", "error", err)
	}

	return cacheInfo, nil
}

func (p *GoogleAICacheProvider) verifyCacheExists(ctx context.Context, cacheName, apiKey string) bool {
	if cacheName == "" {
		return false
	}

	cachingHelper, err := googleai.NewCachingHelper(ctx, googleai.WithAPIKey(apiKey))
	if err != nil {
		slog.Warn("Failed to create caching helper for verification", "error", err)
		return false
	}

	_, err = cachingHelper.GetCachedContent(ctx, cacheName)
	return err == nil
}

func (p *GoogleAICacheProvider) InvalidateCache(ctx context.Context, req *CacheRequest) error {
	agentName := req.AgentName
	if fp := capabilityFingerprint(req.Capabilities); fp != "" {
		agentName = req.AgentName + ":" + fp
	}
	cacheKey := generateCacheKey(req.Scope, req.AccountId, req.ConversationId, agentName, req.Model)

	var cacheInfo CacheInfo
	exists := false
	if data, ok := common.CacheGet(p.namespace, cacheKey); ok {
		// Always delete if it exists in shared cache, even if unmarshal fails (to clear corruption)
		if err := common.CacheDelete(p.namespace, cacheKey); err != nil {
			slog.Warn("Google AI cache: Failed to delete cache entry from shared storage", "error", err, "cacheKey", cacheKey)
		}
		if err := json.Unmarshal(data, &cacheInfo); err == nil {
			exists = true
		}
	}

	if !exists {
		return nil
	}

	// Delete from Google AI
	cachingHelper, err := googleai.NewCachingHelper(ctx, googleai.WithAPIKey(req.ApiKey))
	if err != nil {
		return err
	}

	return cachingHelper.DeleteCachedContent(ctx, cacheInfo.CacheName)
}

// ========== Anthropic Cache Provider ==========

// AnthropicCacheProvider implements caching for Anthropic (inline cache control)
type AnthropicCacheProvider struct{}

func NewAnthropicCacheProvider() *AnthropicCacheProvider {
	return &AnthropicCacheProvider{}
}

func (p *AnthropicCacheProvider) GetProviderName() string {
	return "anthropic"
}

// ApplyCache modifies the message list to include Anthropic's inline cache control directive.
//
// Anthropic's caching mechanism works by adding a `CacheControl` directive to one of the messages.
// This directive acts as a marker, signaling to the API that all messages in the request *up to and including*
// the marked message should be considered for caching as a single, contiguous block.
//
// Therefore, this function identifies the cacheable portion of the conversation and attaches the
// `CacheControl` directive only to the very last message of that cacheable block. This is the
// correct and most efficient way to implement their caching strategy.
func (p *AnthropicCacheProvider) ApplyCache(ctx context.Context, req *CacheRequest) *CacheResponse {
	// Anthropic uses inline cache control - modify messages directly
	cacheableMessages, nonCacheableMessages := identifyCacheableMessages(req.Messages, req.Scope)

	if len(cacheableMessages) == 0 {
		slog.Debug("No cacheable messages for Anthropic", "conversationId", req.ConversationId)
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	// Find the last TextContent/BinaryContent part in a Human or System message.
	//
	// Constraints:
	//  1. Only TextContent/BinaryContent can be wrapped — ToolCall/ToolCallResponse crash
	//     the Anthropic handler with "unsupported cached content part type".
	//  2. Only Human and System message handlers support CachedContent. The AI message
	//     handler (handleAIMessage) does NOT handle CachedContent and would error with
	//     ErrInvalidContentType.
	//  3. Parts already wrapped in CachedContent are skipped to prevent double-wrapping
	//     (which causes "unsupported cached content part type: llms.CachedContent").
	//
	// The cache_control marker tells Anthropic to cache everything from the beginning
	// up to and including that block.
	targetMsg, targetPart := -1, -1
FindTarget:
	for i := len(cacheableMessages) - 1; i >= 0; i-- {
		role := cacheableMessages[i].Role
		if role != llms.ChatMessageTypeHuman && role != llms.ChatMessageTypeSystem {
			continue
		}
		for j := len(cacheableMessages[i].Parts) - 1; j >= 0; j-- {
			switch cacheableMessages[i].Parts[j].(type) {
			case llms.TextContent, llms.BinaryContent:
				targetMsg, targetPart = i, j
				break FindTarget
			}
		}
	}

	modifiedMessages := make([]llms.MessageContent, 0, len(req.Messages))

	for i, msg := range cacheableMessages {
		if i == targetMsg {
			// This message contains the cache boundary — wrap only the target text/binary part
			cachedParts := make([]llms.ContentPart, 0, len(msg.Parts))
			for j, part := range msg.Parts {
				if j == targetPart {
					cachedParts = append(cachedParts, llms.WithCacheControl(part, &llms.CacheControl{
						Type: "ephemeral",
					}))
				} else {
					cachedParts = append(cachedParts, part)
				}
			}
			modifiedMessages = append(modifiedMessages, llms.MessageContent{
				Role:  msg.Role,
				Parts: cachedParts,
			})
			slog.Debug("Added Anthropic cache control", "messageIndex", i, "partIndex", targetPart, "conversationId", req.ConversationId)
		} else {
			modifiedMessages = append(modifiedMessages, msg)
		}
	}

	// Add non-cacheable messages
	modifiedMessages = append(modifiedMessages, nonCacheableMessages...)

	return &CacheResponse{
		Messages:  modifiedMessages,
		Options:   nil,
		CacheHit:  false, // Anthropic doesn't tell us about cache hits upfront
		CacheInfo: nil,
	}
}

func (p *AnthropicCacheProvider) InvalidateCache(ctx context.Context, req *CacheRequest) error {
	// Anthropic caching is ephemeral and managed by Anthropic, nothing to invalidate
	return nil
}

// ========== Helper Functions ==========

func generateCacheKey(scope CacheScope, accountId, conversationId, agentName, model string) string {
	switch scope {
	case CacheScopeGlobal:
		return fmt.Sprintf("global:%s:%s", agentName, model)
	case CacheScopeAccount:
		return fmt.Sprintf("account:%s:%s:%s", accountId, agentName, model)
	default:
		return fmt.Sprintf("conv:%s:%s:%s:%s", accountId, conversationId, agentName, model)
	}
}

// capabilityFingerprint returns an 8-hex-char suffix derived from the sorted
// allowed_tools list in capabilities, or an empty string when no allow-list is
// set. This suffix is appended to agentName inside GoogleAICacheProvider only
// so that different tool scopes get distinct Google AI CachedContent slots and
// don't thrash each other (Google AI uses a single slot per cache key).
//
// Anthropic inline cache_control is content-addressed and unaffected.
func capabilityFingerprint(capabilities map[string]any) string {
	if len(capabilities) == 0 {
		return ""
	}
	raw, ok := capabilities["allowed_tools"]
	if !ok {
		return ""
	}
	var tools []string
	switch v := raw.(type) {
	case []string:
		tools = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				tools = append(tools, s)
			}
		}
	}
	if len(tools) == 0 {
		return ""
	}
	sorted := make([]string, len(tools))
	copy(sorted, tools)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(h[:4]) // 8 hex chars from first 4 bytes
}

func hashContent(messages []llms.MessageContent) string {
	hasher := sha256.New()
	for _, msg := range messages {
		_, _ = fmt.Fprintf(hasher, "%v:%v", msg.Role, msg.Parts)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func getCacheTTL(scope CacheScope) time.Duration {
	// For global and account scopes (static instructions), use a long TTL to maximize hit rates
	// across different user sessions throughout the day.
	if scope == CacheScopeGlobal || scope == CacheScopeAccount {
		return defaultStaticCacheTTL
	}

	// For conversation scope (dynamic history), use the shorter configured TTL
	if config.Config.LlmCacheTTLMinutes > 0 {
		return time.Duration(config.Config.LlmCacheTTLMinutes) * time.Minute
	}
	return 10 * time.Minute // Fallback (viper default of 10 min normally takes effect first)
}

// identifyCacheableMessages separates messages into two groups: cacheable and non-cacheable.
// The logic is to cache the stable, historical context of a conversation while leaving the most recent user query
// and subsequent messages as non-cacheable. This ensures that the LLM processes the new query to generate a fresh response.
//
// The split point is the last message from a human user:
// - Everything *before* the last human message is considered stable context and is returned as `cacheable`.
// - The last human message and everything after it is considered the active prompt and is returned as `nonCacheable`.
//
// If no human messages are found, only system messages are considered cacheable.
func identifyCacheableMessages(messages []llms.MessageContent, scope CacheScope) (cacheable, nonCacheable []llms.MessageContent) {
	if len(messages) == 0 {
		return nil, nil
	}

	// For Global and Account scopes, the "stable" part is strictly the system instructions.
	// We do not want to cache user queries or conversation history under these scopes
	// because they are highly dynamic and would result in 0% cache hits across different sessions.
	if scope == CacheScopeGlobal || scope == CacheScopeAccount {
		firstNonSystemIdx := -1
		for i, msg := range messages {
			if msg.Role != llms.ChatMessageTypeSystem {
				firstNonSystemIdx = i
				break
			}
		}
		if firstNonSystemIdx == -1 {
			// All messages are System messages
			return messages, nil
		}
		return messages[:firstNonSystemIdx], messages[firstNonSystemIdx:]
	}

	// For Conversation scope (default), we want to cache the stable historical context.
	// Find the last human message index.
	lastHumanIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llms.ChatMessageTypeHuman {
			lastHumanIdx = i
			break
		}
	}

	// If no human messages are found, we should only cache system messages.
	// Other messages (like AI responses without a human prompt) are not suitable for caching.
	if lastHumanIdx == -1 {
		for _, msg := range messages {
			if msg.Role == llms.ChatMessageTypeSystem {
				cacheable = append(cacheable, msg)
			} else {
				nonCacheable = append(nonCacheable, msg)
			}
		}
		return cacheable, nonCacheable
	}

	// Cache everything before the last human message.
	// This includes any system messages and previous human/AI message pairs.
	cacheable = messages[:lastHumanIdx]
	nonCacheable = messages[lastHumanIdx:]

	return cacheable, nonCacheable
}
