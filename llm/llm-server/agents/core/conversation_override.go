package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"nudgebee/llm/common"
	llmruntime "nudgebee/llm/llms/runtime"
)

type conversationOverrideEntry struct {
	provider      string
	model         string
	tierOverrides llmruntime.ConversationTierOverrides
	configSource  string
	ts            time.Time
}

var (
	conversationOverrideCache      = make(map[string]conversationOverrideEntry)
	conversationOverrideCacheMutex sync.RWMutex
	conversationOverrideCacheTTL   = 5 * time.Minute
)

// Returns the conversation's sticky LLM selections: blanket (provider, model),
// per-tier overrides, and the pinned config source. All zero ⇒ no override; fall
// through to lower layers. The config source is independent of the other three —
// a conversation can pin a slot without pinning a provider/model, and vice versa.
func GetConversationOverride(conversationId string) (string, string, llmruntime.ConversationTierOverrides, string, error) {
	conversationOverrideCacheMutex.RLock()
	entry, found := conversationOverrideCache[conversationId]
	conversationOverrideCacheMutex.RUnlock()

	if found && time.Since(entry.ts) < conversationOverrideCacheTTL {
		return entry.provider, entry.model, entry.tierOverrides, entry.configSource, nil
	}

	// Fail closed if the conversation DAO is unavailable (e.g. DB not reachable):
	// return an explicit error rather than silently reporting "no override" (which
	// could mask a conversation-level model/tier restriction) — and never deref a
	// nil DAO, which previously panicked the whole request. Callers already treat
	// a non-nil error as "skip the override and fall through".
	dao := GetConversationDao()
	if dao == nil {
		return "", "", llmruntime.ConversationTierOverrides{}, "", fmt.Errorf("conversation DAO is unavailable")
	}

	conv, err := dao.GetConversation(conversationId)
	if err != nil {
		return "", "", llmruntime.ConversationTierOverrides{}, "", err
	}

	provider := ""
	model := ""
	configSource := ""
	if conv.LlmProvider != nil {
		provider = *conv.LlmProvider
	}
	if conv.LlmModel != nil {
		model = *conv.LlmModel
	}
	if conv.LlmConfigSource != nil {
		configSource = *conv.LlmConfigSource
	}
	var tierOverrides llmruntime.ConversationTierOverrides
	if conv.LlmTierOverrides != nil {
		tierOverrides = *conv.LlmTierOverrides
	}

	conversationOverrideCacheMutex.Lock()
	conversationOverrideCache[conversationId] = conversationOverrideEntry{
		provider:      provider,
		model:         model,
		tierOverrides: tierOverrides,
		configSource:  configSource,
		ts:            time.Now(),
	}
	conversationOverrideCacheMutex.Unlock()

	return provider, model, tierOverrides, configSource, nil
}

// InvalidateConversationOverrideCache drops the conversation's pinned model on
// every replica, so the next turn uses the new choice wherever it lands.
func InvalidateConversationOverrideCache(conversationId string) {
	if conversationId == "" {
		return
	}
	common.ReplicaBroadcast(context.Background(), common.ChangeConversationOverride, conversationId)
}

func init() {
	common.OnReplicaBroadcast(common.ChangeConversationOverride, common.ReplicaHandler{
		OnKeys: func(conversationIds []string) {
			conversationOverrideCacheMutex.Lock()
			defer conversationOverrideCacheMutex.Unlock()
			for _, conversationId := range conversationIds {
				delete(conversationOverrideCache, conversationId)
			}
		},
		OnAll: func() {
			conversationOverrideCacheMutex.Lock()
			defer conversationOverrideCacheMutex.Unlock()
			clear(conversationOverrideCache)
		},
	})
}
