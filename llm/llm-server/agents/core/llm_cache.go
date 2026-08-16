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
	toolcore "nudgebee/llm/tools/core"

	"github.com/tmc/langchaingo/llms"
	"golang.org/x/sync/singleflight"
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
	TenantId       string // Required for non-global scope so lifecycle rows roll up into tenant budgets
	AccountId      string
	ConversationId string
	AgentName      string // Agent type/name (not per-request ID) for stable cross-request cache keys
	Model          string
	Provider       string
	Messages       []llms.MessageContent
	ApiKey         string
	Endpoint       string // Optional AI Gateway base URL; empty = talk to Google directly. Must match the generation client's endpoint so cache create/reference resolve to the same Google key.
	Scope          CacheScope
	Capabilities   toolcore.AgentCapabilities // Optional; used to isolate cache slots when tool set varies per request
	PromptVariant  string                     // Optional; isolates the lean vs full orchestrator prompt into distinct cache slots (empty = full/default)
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

// googleAICacheCreateLockTTL bounds how long the cross-replica creation lock is
// held; if the holder dies mid-create the lock self-expires so peers can proceed.
// googleAICacheCreateLockWait is how long a non-holder waits for the holder to
// publish the cache entry before falling back to creating it itself.
const (
	googleAICacheCreateLockTTL  = 2 * time.Minute
	googleAICacheCreateLockWait = 30 * time.Second
)

// GoogleAICacheProvider implements caching for Google AI (pre-created cached content)
type GoogleAICacheProvider struct {
	namespace string
	// createGroup collapses concurrent cache-miss creations for the same cache
	// key into a single createCache call, so parallel conversations sharing an
	// account:agent:model key don't each create a distinct (duplicate) Google AI
	// CachedContent resource. Zero value is ready to use.
	createGroup singleflight.Group
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

// googleAICacheOpts builds the googleai client options shared by every cache
// helper. Passing the same apiKey + endpoint the generation client uses is
// mandatory: Gemini caches are isolated by credential/project, so a cache
// created under one key/endpoint and referenced under another would miss or 403.
func googleAICacheOpts(apiKey, endpoint string) []googleai.Option {
	opts := []googleai.Option{googleai.WithAPIKey(apiKey)}
	if endpoint != "" {
		opts = append(opts, googleai.WithBaseURL(endpoint))
	}
	return opts
}

func (p *GoogleAICacheProvider) ApplyCache(ctx context.Context, req *CacheRequest) *CacheResponse {
	// Stamp trace_id/span_id from the request context so cache decisions
	// correlate with the conversation's other log lines in Loki.
	logger := common.TraceLogger(ctx)
	// Append a capability fingerprint to agentName so that requests with different
	// allowed_tools sets get distinct Google AI CachedContent slots. Google AI uses
	// a single slot per cache key, so alternating tool scopes would otherwise thrash
	// each other. Anthropic uses inline cache_control (content-addressed) and is unaffected.
	agentName := req.AgentName
	if fp := capabilityFingerprint(req.Capabilities); fp != "" {
		agentName = req.AgentName + ":" + fp
	}
	// Isolate the lean vs full orchestrator prompt into distinct slots, for the
	// same reason as the capability fingerprint: the lean prompt is a different
	// cacheable prefix and must not overwrite the full-prompt slot. Empty
	// (full/default) appends nothing, leaving existing slots byte-identical.
	if req.PromptVariant != "" {
		agentName = agentName + ":" + req.PromptVariant
	}

	// Generate cache key based on scope, including a short hash of the api key
	// so two requests with the same (account, conv, agent, model) but different
	// Google projects (=different api keys) get distinct slots. Without this a
	// reused local-cache hit would reference a CachedContent owned by another
	// project and 403 at use time.
	cacheKey := generateCacheKey(req.Scope, req.AccountId, req.ConversationId, agentName, req.Model, credsFingerprint(req.ApiKey, "", "", "", "", "", ""))

	logger.Info("Google AI cache: Starting cache check",
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
		logger.Info("Google AI cache: Not using cache - No cacheable messages found",
			"conversationId", req.ConversationId,
			"reason", "no_cacheable_messages",
			"totalMessages", len(req.Messages))
		common.MetricsLLMCacheSkip(req.Provider, req.Model, "no_cacheable_messages", req.AccountId, req.AgentName, string(req.Scope))
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	logger.Debug("Google AI cache: Identified cacheable messages",
		"conversationId", req.ConversationId,
		"cacheableMessages", len(cacheableMessages),
		"nonCachedMessages", len(nonCacheableMessages))

	// For Global and Account scopes, apply deterministic padding so system prompts meet minimum threshold
	if req.Scope == CacheScopeGlobal || req.Scope == CacheScopeAccount {
		cacheableMessages = padMessagesIfRequired(cacheableMessages, req.Scope)
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
			logger.Warn("Google AI cache: Failed to unmarshal cache info, clearing bad entry", "error", err, "cacheKey", cacheKey)
			if delErr := common.CacheDelete(p.namespace, cacheKey); delErr != nil {
				logger.Warn("Google AI cache: Failed to delete corrupt entry", "error", delErr, "cacheKey", cacheKey)
			}
		}
	}

	now := time.Now()

	// Cache hit path (zero-latency: trust Redis TTL and let GenerateContent execute immediately)
	if exists && cacheInfo.ExpiresAt.After(now) && cacheInfo.ContentHash == contentHash {
		timeToExpiry := cacheInfo.ExpiresAt.Sub(now)
		logger.Info("Google AI cache: CACHE HIT - Using existing cache",
			"cacheName", cacheInfo.CacheName,
			"conversationId", req.ConversationId,
			"cacheAge", now.Sub(cacheInfo.CreatedAt).String(),
			"timeToExpiry", timeToExpiry.String(),
			"cachedMessages", len(cacheableMessages),
			"nonCachedMessages", len(nonCacheableMessages),
			"status", "hit")
		common.MetricsLLMCacheTotal(req.Provider, req.Model, "hit", req.AccountId, req.AgentName, string(req.Scope))

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
	}

	// Check if cacheable messages meet Google AI's minimum token requirement.
	// Minimum varies by model: 2.5 Pro = 4,096 tokens; 2.5 Flash = 1,024 tokens;
	// 1.5 Pro/Flash = 32,768 tokens. Returns 0 if the model does not support caching.
	minGoogleAITokens := GetLlmMinCacheTokens(req.Model)
	if minGoogleAITokens == 0 {
		logger.Info("Google AI cache: Not using cache - Model does not support context caching",
			"model", req.Model,
			"conversationId", req.ConversationId,
			"reason", "model_no_cache_support")
		common.MetricsLLMCacheSkip(req.Provider, req.Model, "model_no_cache_support", req.AccountId, req.AgentName, string(req.Scope))
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
			logger.Info("Google AI cache: Not using cache - Local token estimate too low",
				"localEstimate", localCount,
				"minRequired", minGoogleAITokens,
				"conversationId", req.ConversationId)
			common.MetricsLLMCacheSkip(req.Provider, req.Model, "insufficient_tokens", req.AccountId, req.AgentName, string(req.Scope))
			return &CacheResponse{
				Messages: req.Messages,
				CacheHit: false,
			}
		}
	}

	// Use Google AI's CountTokens API for accurate token counting
	cachingHelper, err := googleai.NewCachingHelper(ctx, googleAICacheOpts(req.ApiKey, req.Endpoint)...)
	if err != nil {
		logger.Warn("Google AI cache: Not using cache - Failed to create caching helper",
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
		logger.Warn("Google AI cache: Not using cache - Token counting failed",
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

	logger.Info("Google AI cache: Token count for cacheable messages",
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"minRequired", minGoogleAITokens,
		"meetsRequirement", int(tokenCount) >= minGoogleAITokens)

	if int(tokenCount) < minGoogleAITokens {
		logger.Info("Google AI cache: Not using cache - Token count below minimum",
			"tokenCount", tokenCount,
			"minRequired", minGoogleAITokens,
			"deficit", minGoogleAITokens-int(tokenCount),
			"conversationId", req.ConversationId,
			"reason", "insufficient_tokens")
		common.MetricsLLMCacheSkip(req.Provider, req.Model, "insufficient_tokens", req.AccountId, req.AgentName, string(req.Scope))
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	// Check if token count exceeds model's context window limit
	maxTokens := GetLlmMaxTokenLength(req.Model)
	if tokenCount > int32(maxTokens) {
		logger.Warn("Google AI cache: Not using cache - Token count exceeds model's context window limit",
			"tokenCount", tokenCount,
			"maxTokens", maxTokens,
			"model", req.Model,
			"conversationId", req.ConversationId,
			"reason", "exceeds_context_window")
		common.MetricsLLMCacheSkip(req.Provider, req.Model, "exceeds_context_window", req.AccountId, req.AgentName, string(req.Scope))
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	if exists {
		// Log why cache was not used and handle stale cache deletion
		var reason string
		if !cacheInfo.ExpiresAt.After(now) {
			reason = "cache_expired"
			logger.Info("Google AI cache: Not using cache - Cache expired",
				"conversationId", req.ConversationId,
				"expiredAt", cacheInfo.ExpiresAt,
				"reason", reason)
		} else if cacheInfo.ContentHash != contentHash {
			reason = "content_changed"
			logger.Info("Google AI cache: Not using cache - Content has changed, deleting old cache",
				"conversationId", req.ConversationId,
				"oldCacheName", cacheInfo.CacheName,
				"reason", reason)
			// Drop the stale shared pointer immediately.
			if delErr := common.CacheDelete(p.namespace, cacheKey); delErr != nil {
				logger.Warn("Google AI cache: failed to delete stale pointer on content_changed",
					"error", delErr, "cacheKey", cacheKey, "conversationId", req.ConversationId)
			}
			// Explicitly delete the old Google AI cache.
			if helper, helperErr := googleai.NewCachingHelper(ctx, googleAICacheOpts(req.ApiKey, req.Endpoint)...); helperErr == nil {
				if delErr := helper.DeleteCachedContent(ctx, cacheInfo.CacheName); delErr != nil {
					logger.Warn("Google AI cache: failed to delete orphaned content_changed cache",
						"error", delErr,
						"cacheName", cacheInfo.CacheName,
						"conversationId", req.ConversationId)
				} else {
					recordCacheLifecycleInvalidation(cacheInfo.CacheName)
					common.MetricsLLMCacheInvalidations(req.Provider, req.Model, invalidationScope(req.Scope), "content_changed")
				}
			} else {
				logger.Warn("Google AI cache: failed to init helper for orphan deletion",
					"error", helperErr,
					"conversationId", req.ConversationId)
			}
		}
	}

	// Cache miss - record miss metric
	logger.Info("Google AI cache: CACHE MISS",
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"cachedMessages", len(cacheableMessages),
		"nonCachedMessages", len(nonCacheableMessages),
		"status", "miss")
	common.MetricsLLMCacheTotal(req.Provider, req.Model, "miss", req.AccountId, req.AgentName, string(req.Scope))

	// Async Cache Creation:
	// On cache miss, don't block the caller for 1-5s. Send full un-cached messages immediately,
	// while creating the Google AI CachedContent resource in a background goroutine for subsequent calls.
	if config.Config.LlmServerAsyncCacheCreation {
		// Shallow copy req and deep copy cacheableMessages to prevent data races
		// with the caller modifying them concurrently in the main request thread.
		reqCopy := *req
		copiedMessages := make([]llms.MessageContent, len(cacheableMessages))
		for i, msg := range cacheableMessages {
			copiedMessages[i] = llms.MessageContent{
				Role:  msg.Role,
				Parts: make([]llms.ContentPart, len(msg.Parts)),
			}
			copy(copiedMessages[i].Parts, msg.Parts)
		}
		reqCopy.Messages = copiedMessages

		go func() {
			detachedCtx := context.WithoutCancel(ctx)
			detachedCtx, cancelCreate := context.WithTimeout(detachedCtx, 5*time.Minute)
			defer cancelCreate()

			var logger *slog.Logger
			if detachedCtx != nil {
				logger = common.TraceLogger(detachedCtx)
			}

			defer func() {
				if r := recover(); r != nil {
					log := logger
					if log == nil {
						log = slog.Default()
					}
					log.Error("Google AI cache: Panic recovered in async cache creation", "panic", r, "cacheKey", cacheKey)
				}
			}()

			_, err, _ := p.createGroup.Do(cacheKey, func() (interface{}, error) {
				if info := p.readSharedCacheInfo(cacheKey); cacheInfoUsable(info, contentHash) {
					return info, nil
				}
				token, acquired := common.CacheTryLock(detachedCtx, cacheKey, googleAICacheCreateLockTTL)
				if acquired {
					defer common.CacheUnlock(detachedCtx, cacheKey, token)
				} else {
					if info := p.waitForSharedCacheInfo(cacheKey, contentHash, googleAICacheCreateLockWait); info != nil {
						return info, nil
					}
				}
				return p.createCache(detachedCtx, &reqCopy, copiedMessages, contentHash, cacheKey, tokenCount)
			})
			if err != nil {
				log := logger
				if log == nil {
					log = slog.Default()
				}
				log.Error("Google AI cache: Async cache creation failed", "error", err, "cacheKey", cacheKey, "conversationId", reqCopy.ConversationId)
				common.MetricsLLMCacheTotal(reqCopy.Provider, reqCopy.Model, "error", reqCopy.AccountId, reqCopy.AgentName, string(reqCopy.Scope))
			}
		}()

		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
		}
	}

	// Synchronous blocking creation path (if async creation is explicitly disabled via config)
	detachedCtx := context.WithoutCancel(ctx)
	detachedCtx, cancelCreate := context.WithTimeout(detachedCtx, 5*time.Minute)
	defer cancelCreate()
	created, errCreate, _ := p.createGroup.Do(cacheKey, func() (interface{}, error) {
		if info := p.readSharedCacheInfo(cacheKey); cacheInfoUsable(info, contentHash) {
			return info, nil
		}
		token, acquired := common.CacheTryLock(detachedCtx, cacheKey, googleAICacheCreateLockTTL)
		if acquired {
			defer common.CacheUnlock(detachedCtx, cacheKey, token)
		} else {
			if info := p.waitForSharedCacheInfo(cacheKey, contentHash, googleAICacheCreateLockWait); info != nil {
				return info, nil
			}
		}
		return p.createCache(detachedCtx, req, cacheableMessages, contentHash, cacheKey, tokenCount)
	})
	var cacheInfoResult *CacheInfo
	if errCreate == nil {
		cacheInfoResult, _ = created.(*CacheInfo)
	}
	if errCreate != nil {
		logger.Error("Google AI cache: Failed to create cache",
			"error", errCreate,
			"conversationId", req.ConversationId,
			"tokenCount", tokenCount,
			"reason", "cache_creation_failed")
		common.MetricsLLMCacheTotal(req.Provider, req.Model, "error", req.AccountId, req.AgentName, string(req.Scope))
		return &CacheResponse{
			Messages: req.Messages,
			CacheHit: false,
			Error:    errCreate,
		}
	}

	logger.Info("Google AI cache: Successfully created new cache",
		"cacheName", cacheInfoResult.CacheName,
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"cachedMessages", len(cacheableMessages),
		"nonCachedMessages", len(nonCacheableMessages),
		"ttl", getCacheTTL(req.Scope, req.Model).String())

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

// cacheInfoUsable reports whether a shared-cache pointer can be safely reused
// for a request with the given contentHash. It must reference the SAME content
// (Google prepends the cached block verbatim, so a mismatch would send the wrong
// context) and still be within its TTL. Reusing a mismatched or expired pointer
// hands the caller a CachedContentName that no longer exists in Google → 403
// "CachedContent not found" at GenerateContent time. Both shared-pointer read
// sites (the singleflight early-return and waitForSharedCacheInfo) must gate on
// this: the expired / content_changed paths don't always delete the pointer, so
// a stale one can still be sitting in the shared cache.
func cacheInfoUsable(info *CacheInfo, contentHash string) bool {
	return info != nil && info.ContentHash == contentHash && info.ExpiresAt.After(time.Now())
}

// readSharedCacheInfo returns the published CacheInfo for cacheKey from the
// shared cache, or nil if absent/corrupt. Used to reuse a cache another
// goroutine or replica just created instead of creating a duplicate.
func (p *GoogleAICacheProvider) readSharedCacheInfo(cacheKey string) *CacheInfo {
	data, ok := common.CacheGet(p.namespace, cacheKey)
	if !ok {
		return nil
	}
	var info CacheInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil
	}
	return &info
}

// waitForSharedCacheInfo polls the shared cache for up to `within` for a usable
// entry published by the replica that holds the creation lock. A pre-existing
// stale pointer (e.g. left behind by the expired path, which doesn't delete it)
// is skipped, not returned — otherwise the waiter would immediately reuse an
// expired/mismatched CachedContentName and 403 at GenerateContent time. Keeps
// polling until the holder publishes a fresh one matching contentHash, or the
// timeout elapses (caller then falls through to create its own).
func (p *GoogleAICacheProvider) waitForSharedCacheInfo(cacheKey, contentHash string, within time.Duration) *CacheInfo {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(within)
	for {
		select {
		case <-timeout:
			return nil
		case <-ticker.C:
			if info := p.readSharedCacheInfo(cacheKey); cacheInfoUsable(info, contentHash) {
				return info
			}
		}
	}
}

func (p *GoogleAICacheProvider) createCache(ctx context.Context, req *CacheRequest, cacheableMessages []llms.MessageContent, contentHash, cacheKey string, tokenCount int32) (*CacheInfo, error) {
	logger := common.TraceLogger(ctx)
	cachingHelper, err := googleai.NewCachingHelper(ctx, googleAICacheOpts(req.ApiKey, req.Endpoint)...)
	if err != nil {
		return nil, err
	}

	ttl := getCacheTTL(req.Scope, req.Model)

	// Create a display name that fits within Google AI's 128 character limit
	// Use conversation ID (meaningful for debugging) + hash of full cache key
	displayName := fmt.Sprintf("conv_%s_%s", req.ConversationId, contentHash[:16])
	if len(displayName) > 128 {
		// Fallback: use just the hash if conversation ID is very long
		displayName = fmt.Sprintf("cache_%s", contentHash[:32])
	}

	logger.Debug("Google AI cache: Calling CreateCachedContent API",
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

	logger.Debug("Google AI cache: CreateCachedContent API returned",
		"cacheName", cachedContent.Name,
		"conversationId", req.ConversationId,
		"tokenCount", tokenCount,
		"ttl", ttl)

	// Store cache info
	// Use UTC for storage timestamps. llm_cache_lifecycle uses
	// `timestamp without time zone`, so the wall-clock value gets stored
	// as-is. time.Now() returns local time, which would shift the stored
	// wall-clock by the local TZ offset and break later (now() - created_at)
	// math in budget/usage queries.
	createdAt := time.Now().UTC()
	cacheInfo := &CacheInfo{
		CacheName:           cachedContent.Name,
		AccountId:           req.AccountId,
		ConversationId:      req.ConversationId,
		AgentName:           req.AgentName,
		Model:               req.Model,
		CreatedAt:           createdAt,
		ExpiresAt:           createdAt.Add(ttl),
		ContentHash:         contentHash,
		CacheCreationTokens: tokenCount,
	}

	if data, err := json.Marshal(cacheInfo); err == nil {
		if err := common.CacheSet(p.namespace, cacheKey, data, common.CacheSetWithExpiration(ttl)); err != nil {
			logger.Error("Google AI cache: Failed to store cache info", "error", err, "cacheKey", cacheKey)
		}
	} else {
		logger.Error("Google AI cache: Failed to marshal cache info", "error", err)
	}

	// Record this cache in llm_cache_lifecycle so storage cost can be billed
	// against tenant/account/conversation later. Best-effort — a failed insert
	// just means storage cost for this cache is undercounted, not that the LLM
	// call should fail.
	scopeOverride := string(req.Scope)
	if scopeOverride == "" {
		scopeOverride = string(CacheScopeConversation)
	}
	// TenantId required for non-global scope so /v1/budget/status rollup at
	// tenant level matches reality. Surface a loud Error if it's missing —
	// silently writing NULL would peg tenant cache-storage cost at $0.
	if scopeOverride != string(CacheScopeGlobal) && strings.TrimSpace(req.TenantId) == "" {
		logger.Error("cache lifecycle: tenant_id missing on non-global cache; tenant rollup will undercount",
			"scope", scopeOverride,
			"account_id", req.AccountId,
			"agent", req.AgentName,
			"model", req.Model,
			"cache_name", cachedContent.Name)
	}
	recordCacheLifecycle(&CacheLifecycleRecord{
		CacheName:      cachedContent.Name,
		LLMProvider:    "googleai",
		LLMModel:       req.Model,
		Scope:          scopeOverride,
		TenantID:       stringPtrIfNotEmpty(req.TenantId),
		AccountID:      stringPtrIfNotEmpty(req.AccountId),
		ConversationID: stringPtrIfNotEmpty(req.ConversationId),
		AgentName:      stringPtrIfNotEmpty(req.AgentName),
		CachedTokens:   int64(tokenCount),
		CreatedAt:      cacheInfo.CreatedAt,
		ExpiresAt:      cacheInfo.ExpiresAt,
	})

	return cacheInfo, nil
}

func (p *GoogleAICacheProvider) InvalidateCache(ctx context.Context, req *CacheRequest) error {
	logger := common.TraceLogger(ctx)
	agentName := req.AgentName
	if fp := capabilityFingerprint(req.Capabilities); fp != "" {
		agentName = req.AgentName + ":" + fp
	}
	// Isolate the lean vs full orchestrator prompt into distinct slots, for the
	// same reason as the capability fingerprint: the lean prompt is a different
	// cacheable prefix and must not overwrite the full-prompt slot. Empty
	// (full/default) appends nothing, leaving existing slots byte-identical.
	if req.PromptVariant != "" {
		agentName = agentName + ":" + req.PromptVariant
	}
	cacheKey := generateCacheKey(req.Scope, req.AccountId, req.ConversationId, agentName, req.Model, credsFingerprint(req.ApiKey, "", "", "", "", "", ""))

	var cacheInfo CacheInfo
	exists := false
	if data, ok := common.CacheGet(p.namespace, cacheKey); ok {
		// Always delete if it exists in shared cache, even if unmarshal fails (to clear corruption)
		if err := common.CacheDelete(p.namespace, cacheKey); err != nil {
			logger.Warn("Google AI cache: Failed to delete cache entry from shared storage", "error", err, "cacheKey", cacheKey)
		}
		if err := json.Unmarshal(data, &cacheInfo); err == nil {
			exists = true
		}
	}

	if !exists {
		return nil
	}

	// Delete from Google AI
	cachingHelper, err := googleai.NewCachingHelper(ctx, googleAICacheOpts(req.ApiKey, req.Endpoint)...)
	if err != nil {
		return err
	}

	if err := cachingHelper.DeleteCachedContent(ctx, cacheInfo.CacheName); err != nil {
		return err
	}

	// Mark the lifecycle row as invalidated so storage cost is billed only for
	// the actual time the cache was alive, not the planned TTL. Fire-and-forget;
	// the cache is already gone from the provider, our DB bookkeeping
	// shouldn't block the caller.
	recordCacheLifecycleInvalidation(cacheInfo.CacheName)
	common.MetricsLLMCacheInvalidations(req.Provider, req.Model, invalidationScope(req.Scope), "explicit")

	return nil
}

// invalidationScope normalizes an empty scope to the conversation default,
// mirroring createCache's scopeOverride logic, so the invalidation metric
// never emits an empty scope label.
func invalidationScope(scope CacheScope) string {
	if scope == "" {
		return string(CacheScopeConversation)
	}
	return string(scope)
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

// ApplyCache modifies the message list to include Anthropic's inline cache control directives.
//
// Anthropic supports up to 4 cache breakpoints. Placing cache_control at multiple strategic points
// (e.g. system instructions, tools block, and the last stable turn) allows partial cache hits
// even when conversation history evolves.
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

	// Constraints:
	//  1. Only TextContent/BinaryContent can be wrapped — ToolCall/ToolCallResponse crash
	//     the Anthropic handler with "unsupported cached content part type".
	//  2. Only Human and System message handlers support CachedContent. The AI message
	//     handler (handleAIMessage) does NOT handle CachedContent and would error with
	//     ErrInvalidContentType.
	//  3. Parts already wrapped in CachedContent are skipped to prevent double-wrapping
	//     (which causes "unsupported cached content part type: llms.CachedContent").
	const maxBreakpoints = 3
	type targetLocation struct {
		msgIdx  int
		partIdx int
	}
	var targets []targetLocation
	seenMsgs := make(map[int]bool)

	// Target 1: Last part of the last System message (so static prompt/tools are cached independently)
	for i := len(cacheableMessages) - 1; i >= 0; i-- {
		if cacheableMessages[i].Role == llms.ChatMessageTypeSystem {
			found := false
			for j := len(cacheableMessages[i].Parts) - 1; j >= 0; j-- {
				if isAnthropicCacheablePart(cacheableMessages[i].Parts[j]) {
					targets = append(targets, targetLocation{msgIdx: i, partIdx: j})
					seenMsgs[i] = true
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}

	// Target 2 & 3: Walk remaining cacheableMessages in reverse to find other eligible Human/System messages
	for i := len(cacheableMessages) - 1; i >= 0 && len(targets) < maxBreakpoints; i-- {
		if seenMsgs[i] {
			continue
		}
		role := cacheableMessages[i].Role
		if role != llms.ChatMessageTypeHuman && role != llms.ChatMessageTypeSystem {
			continue
		}
		for j := len(cacheableMessages[i].Parts) - 1; j >= 0; j-- {
			if isAnthropicCacheablePart(cacheableMessages[i].Parts[j]) {
				targets = append(targets, targetLocation{msgIdx: i, partIdx: j})
				seenMsgs[i] = true
				break
			}
		}
	}

	// Build lookup map for fast checking
	targetMap := make(map[int]map[int]bool)
	for _, t := range targets {
		if targetMap[t.msgIdx] == nil {
			targetMap[t.msgIdx] = make(map[int]bool)
		}
		targetMap[t.msgIdx][t.partIdx] = true
	}

	modifiedMessages := make([]llms.MessageContent, 0, len(req.Messages))

	for i, msg := range cacheableMessages {
		if partsMap, hasTarget := targetMap[i]; hasTarget {
			cachedParts := make([]llms.ContentPart, 0, len(msg.Parts))
			for j, part := range msg.Parts {
				if partsMap[j] {
					if _, isAlreadyCached := part.(llms.CachedContent); !isAlreadyCached {
						cachedParts = append(cachedParts, llms.WithCacheControl(part, &llms.CacheControl{
							Type: "ephemeral",
						}))
						slog.Debug("Added Anthropic cache control", "messageIndex", i, "partIndex", j, "conversationId", req.ConversationId)
						continue
					}
				}
				cachedParts = append(cachedParts, part)
			}
			modifiedMessages = append(modifiedMessages, llms.MessageContent{
				Role:  msg.Role,
				Parts: cachedParts,
			})
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

func isAnthropicCacheablePart(part llms.ContentPart) bool {
	switch p := part.(type) {
	case llms.TextContent:
		return strings.TrimSpace(p.Text) != ""
	case llms.BinaryContent:
		return len(p.Data) > 0
	default:
		return false
	}
}

func (p *AnthropicCacheProvider) InvalidateCache(ctx context.Context, req *CacheRequest) error {
	// Anthropic caching is ephemeral and managed by Anthropic, nothing to invalidate
	return nil
}

// ========== Helper Functions ==========

// generateCacheKey produces the namespace+conversation/account/global-scoped
// key under which a Google AI CachedContent slot is recorded. The credsFp
// suffix isolates slots by the api key (Google project) that owns them — two
// calls with the same (account, conv, agent, model) but different api keys
// would otherwise share a single local key, and the second call would attempt
// to reuse a slot owned by the first call's project and get 403 from the
// CachedContent API. credsFp should be the short hash of the api key (or
// empty when the provider has no slot-cache semantics).
func generateCacheKey(scope CacheScope, accountId, conversationId, agentName, model, credsFp string) string {
	switch scope {
	case CacheScopeGlobal:
		return fmt.Sprintf("global:%s:%s:%s", agentName, model, credsFp)
	case CacheScopeAccount:
		return fmt.Sprintf("account:%s:%s:%s:%s", accountId, agentName, model, credsFp)
	default:
		return fmt.Sprintf("conv:%s:%s:%s:%s:%s", accountId, conversationId, agentName, model, credsFp)
	}
}

// capabilityFingerprint returns an 8-hex-char suffix derived from the sorted
// AllowedTools list in capabilities, or an empty string when no allow-list is
// set. This suffix is appended to agentName inside GoogleAICacheProvider only
// so that different tool scopes get distinct Google AI CachedContent slots and
// don't thrash each other (Google AI uses a single slot per cache key).
//
// Anthropic inline cache_control is content-addressed and unaffected.
func capabilityFingerprint(capabilities toolcore.AgentCapabilities) string {
	tools := toolcore.NormalizeList(capabilities.AllowedTools)
	if len(tools) == 0 {
		return ""
	}
	for i, t := range tools {
		tools[i] = strings.ToLower(t)
	}
	sort.Strings(tools)
	h := sha256.Sum256([]byte(strings.Join(tools, ",")))
	return hex.EncodeToString(h[:4]) // 8 hex chars from first 4 bytes
}

func padMessagesIfRequired(messages []llms.MessageContent, scope CacheScope) []llms.MessageContent {
	if (scope != CacheScopeGlobal && scope != CacheScopeAccount) || len(messages) == 0 {
		return messages
	}
	padText := "\n\n--- CACHE STABILITY PADDING ---\n" +
		"The following is standard operating procedure text appended to ensure the system prompt meets minimum cache size requirements for the LLM provider. " +
		"You are Nubi, an AI assistant. Always be helpful, concise, and accurate. " +
		"Follow all instructions precisely. Do not hallucinate. Respect user privacy. " +
		"Maintain a professional tone. Respond in the requested format. " +
		"Analyze context carefully. Be reliable and efficient. "
	padText += strings.Repeat("Focus on the primary task. Ignore this padding text during your actual reasoning process. Ensure output is syntactically valid. Provide high-quality insights. ", 40)

	lastIdx := len(messages) - 1
	newParts := make([]llms.ContentPart, len(messages[lastIdx].Parts))
	copy(newParts, messages[lastIdx].Parts)
	newParts = append(newParts, llms.TextContent{Text: padText})

	newMessages := make([]llms.MessageContent, len(messages))
	copy(newMessages, messages)
	newMessages[lastIdx].Parts = newParts
	return newMessages
}

func hashContent(messages []llms.MessageContent) string {
	hasher := sha256.New()
	for _, msg := range messages {
		_, _ = fmt.Fprintf(hasher, "%v:%v", msg.Role, msg.Parts)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func getCacheTTL(scope CacheScope, model string) time.Duration {
	// For global and account scopes (static instructions), use a long TTL to maximize hit rates
	// across different user sessions throughout the day.
	if scope == CacheScopeGlobal || scope == CacheScopeAccount {
		return defaultStaticCacheTTL
	}

	// For conversation scope (dynamic history), optimize TTL based on model cost profile:
	// Flash models have low storage costs; longer TTL (default 30m) increases break-even hit probability.
	// Pro models have high storage costs; shorter TTL (default 10m) prevents unnecessary storage spend.
	m := strings.ToLower(model)
	if strings.Contains(m, "flash") && config.Config.LlmServerCacheFlashTTLMinutes > 0 {
		return time.Duration(config.Config.LlmServerCacheFlashTTLMinutes) * time.Minute
	}
	if strings.Contains(m, "pro") && config.Config.LlmServerCacheProTTLMinutes > 0 {
		return time.Duration(config.Config.LlmServerCacheProTTLMinutes) * time.Minute
	}

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
