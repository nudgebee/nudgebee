package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// UsageStore is the LLM runtime's own data: token-usage rows, model pricing,
// the image-support catalog and cache lifecycle rows.
type UsageStore interface {
	InsertTokenUsage(record *TokenUsageRecord) error
	GetConversationCost(provider string, model string, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int, tenantId string) (float64, error)
	GetConversationCosts(models []string, tenantId string) (map[string]ModelPricing, error)
	GetImageSupportCatalog(tenantId string) (map[string]bool, error)
	InsertCacheLifecycle(ctx context.Context, record *CacheLifecycleRecord) error
	SetCacheLifecycleInvalidated(ctx context.Context, cacheName string) error
}

type usageDAO struct {
	dbManager *common.DatabaseManager
}

// NewUsageStore returns a UsageStore on dbManager, for callers that already
// hold a database handle.
func NewUsageStore(dbManager *common.DatabaseManager) UsageStore {
	return &usageDAO{dbManager: dbManager}
}

var (
	usageStoreMu sync.Mutex
	usageStore   UsageStore
)

// GetUsageStore returns the shared store on the metastore database, connecting
// if needed. It returns nil when the database is unavailable.
func GetUsageStore() UsageStore {
	usageStoreMu.Lock()
	defer usageStoreMu.Unlock()
	if usageStore != nil {
		return usageStore
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("llm: unable to get database manager", "error", err)
		return nil
	}
	usageStore = &usageDAO{dbManager: dbManager}
	return usageStore
}

// PeekUsageStore returns the shared store only if the metastore connection
// already exists. It never connects.
func PeekUsageStore() UsageStore {
	usageStoreMu.Lock()
	defer usageStoreMu.Unlock()
	if usageStore != nil {
		return usageStore
	}
	if dbManager := common.PeekDatabaseManager(common.Metastore); dbManager != nil {
		usageStore = &usageDAO{dbManager: dbManager}
		return usageStore
	}
	return nil
}

// SetUsageStore replaces the shared store and returns the one it replaced.
// Tests use it to install fakes.
func SetUsageStore(store UsageStore) (previous UsageStore) {
	usageStoreMu.Lock()
	defer usageStoreMu.Unlock()
	previous, usageStore = usageStore, store
	return previous
}

// ConversationPort is what the runtime needs from the conversation it serves:
// the agent's name and thought, the message's live status, the conversation's
// pinned models, and the run's token budget. agents/core implements it and
// installs it with SetConversationPort from init().
//
// Keep it to what the runtime already calls. A new method here is a new
// dependency of the runtime on conversation state, so it needs a reason.
type ConversationPort interface {
	GetAgentNameFromAgentId(agentId string) (string, error)
	UpdateConversationAgentThought(agentId, thought string) error
	SetMessageLiveStatus(messageId, key string, status *LiveStatus) error
	// GetConversationOverride returns the conversation's sticky LLM selections:
	// provider, model, per-tier overrides and pinned config source.
	GetConversationOverride(conversationId string) (string, string, ConversationTierOverrides, string, error)
	// AddRunTokens counts an LLM call's tokens against the run's budget.
	AddRunTokens(ctx *security.RequestContext, tokens int)
	// PeekAgentNameFromAgentId is GetAgentNameFromAgentId that never opens a
	// database connection: it errors when none is open yet.
	PeekAgentNameFromAgentId(agentId string) (string, error)
}

var conversationPort ConversationPort

// SetConversationPort installs the conversation side of the runtime and
// returns the one it replaced.
func SetConversationPort(port ConversationPort) (previous ConversationPort) {
	previous, conversationPort = conversationPort, port
	return previous
}

var (
	errNoConversationOverride = errors.New("llm: conversation override lookup is not installed")
	errNoConversationPort     = errors.New("llm: conversation port is not installed")
)

func conversationOverride(conversationId string) (string, string, ConversationTierOverrides, string, error) {
	if conversationPort == nil {
		return "", "", ConversationTierOverrides{}, "", errNoConversationOverride
	}
	return conversationPort.GetConversationOverride(conversationId)
}

// AddRunTokens counts tokens against the run's budget, for LLM calls made
// outside the runtime's own call path (e.g. a decision model).
func AddRunTokens(ctx *security.RequestContext, tokens int) {
	if conversationPort != nil {
		conversationPort.AddRunTokens(ctx, tokens)
	}
}

// PeekAgentNameFromAgentId returns the agent's name without opening a database
// connection; it errors when none is open yet or no port is installed.
func PeekAgentNameFromAgentId(agentId string) (string, error) {
	if conversationPort == nil {
		return "", errNoConversationPort
	}
	return conversationPort.PeekAgentNameFromAgentId(agentId)
}
