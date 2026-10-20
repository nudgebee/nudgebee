package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// reactTurnMessages is the react_4 shape: system prompt, the question, and one
// tool step.
func reactTurnMessages() []llms.MessageContent {
	return []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("s", 10_000)}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "why is checkout slow?"}}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.ToolCall{ID: "c1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "kubectl", Arguments: `{"cmd":"get pods"}`}}}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolCallResponse{ToolCallID: "c1", Name: "kubectl", Content: "pod list"}}},
	}
}

// seedLiveCache stores a live cache pointer for msgs under scope, so ApplyCache
// would hit it if it looks it up.
func seedLiveCache(t *testing.T, p *GoogleAICacheProvider, scope CacheScope, accountId, conversationId, agentName string, msgs []llms.MessageContent) {
	t.Helper()
	cacheable, _ := identifyCacheableMessages(msgs, scope)
	cacheable = padMessagesIfRequired(cacheable, scope)
	key := generateCacheKey(scope, accountId, conversationId, agentName, "gemini-2.5-flash", credsFingerprint("", "", "", "", "", "", ""))
	data, err := json.Marshal(&CacheInfo{
		CacheName:   "cachedContents/seeded",
		Model:       "gemini-2.5-flash",
		ContentHash: hashContent(cacheable, nil),
		CreatedAt:   time.Now().Add(-time.Minute),
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(p.namespace, key, data))
}

func cacheRequest(scope CacheScope, accountId, conversationId, agentName string, msgs []llms.MessageContent) *CacheRequest {
	return &CacheRequest{
		Provider:       "googleai",
		Model:          "gemini-2.5-flash",
		AccountId:      accountId,
		AgentName:      agentName,
		ConversationId: conversationId,
		Messages:       msgs,
		Scope:          scope,
	}
}

func TestGeminiConversationScope_LeftToImplicitCaching(t *testing.T) {
	p := NewGoogleAICacheProvider()
	msgs := reactTurnMessages()
	seedLiveCache(t, p, CacheScopeConversation, "acc-conv-default", "conv-a", "qa_agent", msgs)

	resp := p.ApplyCache(context.Background(), cacheRequest(CacheScopeConversation, "acc-conv-default", "conv-a", "qa_agent", msgs))

	assert.False(t, resp.CacheHit, "a live conversation cache must not be referenced")
	assert.Empty(t, resp.Options, "no CachedContent, so Gemini's implicit caching covers the tool history")
	assert.Equal(t, msgs, resp.Messages, "the full prompt is sent")
}

func TestGeminiConversationScope_AccountScopeUnchanged(t *testing.T) {
	p := NewGoogleAICacheProvider()
	msgs := reactTurnMessages()
	seedLiveCache(t, p, CacheScopeAccount, "acc-scope-account", "", "k8s_orchestrator", msgs)

	resp := p.ApplyCache(context.Background(), cacheRequest(CacheScopeAccount, "acc-scope-account", "conv-c", "k8s_orchestrator", msgs))

	assert.True(t, resp.CacheHit, "account and global scope keep the explicit cache")
	assert.Len(t, resp.Options, 1)
}

func TestGlobalScopeCacheKeyIgnoresPromptVariant(t *testing.T) {
	p := NewGoogleAICacheProvider()
	msgs := reactTurnMessages()[:2]
	seedLiveCache(t, p, CacheScopeGlobal, "", "", "memory_rerank", msgs)

	req := cacheRequest(CacheScopeGlobal, "acc-variant", "conv-d", "memory_rerank", msgs)
	req.PromptVariant = "lean"
	resp := p.ApplyCache(context.Background(), req)

	assert.True(t, resp.CacheHit, "a helper called from a lean orchestrator shares the global slot")
}

func TestCacheModeOf(t *testing.T) {
	explicit := &LLMCallMetadata{CacheInfo: &CacheResponse{Options: []llms.CallOption{func(*llms.CallOptions) {}}}}
	skipped := &LLMCallMetadata{CacheInfo: &CacheResponse{}}

	assert.Equal(t, "explicit", cacheModeOf(explicit))
	assert.Equal(t, "implicit", cacheModeOf(skipped))
	assert.Equal(t, "implicit", cacheModeOf(&LLMCallMetadata{}))
	assert.Equal(t, "implicit", cacheModeOf(nil))
}
