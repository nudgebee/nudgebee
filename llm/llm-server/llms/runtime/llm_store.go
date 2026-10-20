package runtime

import (
	"context"
	"errors"

	"nudgebee/llm/security"
)

// Store is the slice of the conversation DAO that the LLM runtime reads and
// writes: token usage, cache lifecycle, live status, agent thoughts and the
// image-support catalog. The conversation DAO implements it; agents/core
// installs it through GetStoreFn and PeekStoreFn from init().
//
// Keep it to what the runtime already calls. A new method here is a new
// dependency of the runtime on conversation state, so it needs a reason.
type Store interface {
	GetAgentNameFromAgentId(agentId string) (string, error)
	UpdateConversationAgentThought(agentId, thought string) error
	SetMessageLiveStatus(messageId, key string, status *LiveStatus) error
	GetConversationCost(provider string, model string, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int, tenantId string) (float64, error)
	GetImageSupportCatalog(tenantId string) (map[string]bool, error)
	InsertTokenUsage(record *TokenUsageRecord) error
	InsertCacheLifecycle(ctx context.Context, record *CacheLifecycleRecord) error
	SetCacheLifecycleInvalidated(ctx context.Context, cacheName string) error
}

// Hooks agents/core fills from init(). Until then the runtime sees no store,
// no conversation override and no run budget.
var (
	// GetStoreFn returns the store, connecting to the database if needed.
	GetStoreFn func() Store
	// PeekStoreFn returns the store only if it is already connected.
	PeekStoreFn func() Store
	// ConversationOverrideFn returns a conversation's sticky LLM selections:
	// provider, model, per-tier overrides and pinned config source.
	ConversationOverrideFn func(conversationId string) (string, string, ConversationTierOverrides, string, error)
	// AddRunTokensFn counts an LLM call's tokens against the run's budget.
	AddRunTokensFn func(ctx *security.RequestContext, tokens int)
)

func getStore() Store {
	if GetStoreFn == nil {
		return nil
	}
	return GetStoreFn()
}

func peekStore() Store {
	if PeekStoreFn == nil {
		return nil
	}
	return PeekStoreFn()
}

var errNoConversationOverride = errors.New("llm: conversation override lookup is not installed")

func conversationOverride(conversationId string) (string, string, ConversationTierOverrides, string, error) {
	if ConversationOverrideFn == nil {
		return "", "", ConversationTierOverrides{}, "", errNoConversationOverride
	}
	return ConversationOverrideFn(conversationId)
}

func addRunTokens(ctx *security.RequestContext, tokens int) {
	if AddRunTokensFn != nil {
		AddRunTokensFn(ctx, tokens)
	}
}
