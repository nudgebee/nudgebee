package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func setGeminiCacheStrategy(t *testing.T, strategy string, minCoverage float64) {
	t.Helper()
	prevStrategy := config.Config.LlmServerGeminiCacheStrategy
	prevCoverage := config.Config.LlmServerGeminiCacheHybridMinCoverage
	config.Config.LlmServerGeminiCacheStrategy = strategy
	config.Config.LlmServerGeminiCacheHybridMinCoverage = minCoverage
	t.Cleanup(func() {
		config.Config.LlmServerGeminiCacheStrategy = prevStrategy
		config.Config.LlmServerGeminiCacheHybridMinCoverage = prevCoverage
	})
}

// orchestratorLoop is the react_4 shape after n tool steps whose results are
// obsChars characters each.
func orchestratorLoop(n, obsChars int) []llms.MessageContent {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("s", 10_000)}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "why is checkout slow?"}}},
	}
	for i := 0; i < n; i++ {
		id := "call-" + string(rune('a'+i))
		msgs = append(msgs,
			llms.MessageContent{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.ToolCall{ID: id, Type: "function", FunctionCall: &llms.FunctionCall{Name: "kubectl", Arguments: `{"cmd":"get pods"}`}}}},
			llms.MessageContent{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{llms.ToolCallResponse{ToolCallID: id, Name: "kubectl", Content: strings.Repeat("o", obsChars)}}},
		)
	}
	return msgs
}

// seedAccountCacheOfSize stores a live account-scope cache pointer for msgs
// whose recorded size is cachedTokens.
func seedAccountCacheOfSize(t *testing.T, p *GoogleAICacheProvider, accountId string, msgs []llms.MessageContent, cachedTokens int32) {
	t.Helper()
	cacheable, _ := identifyCacheableMessages(msgs, CacheScopeAccount)
	cacheable = padMessagesIfRequired(cacheable, CacheScopeAccount)
	key := generateCacheKey(CacheScopeAccount, accountId, "", "k8s_orchestrator", "gemini-2.5-flash", credsFingerprint("", "", "", "", "", "", ""))
	data, err := json.Marshal(&CacheInfo{
		CacheName:           "cachedContents/hybrid-test",
		Model:               "gemini-2.5-flash",
		ContentHash:         hashContent(cacheable, nil),
		CreatedAt:           time.Now().Add(-time.Minute),
		ExpiresAt:           time.Now().Add(time.Hour),
		CacheCreationTokens: cachedTokens,
	})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(p.namespace, key, data))
}

func orchestratorRequest(accountId string, msgs []llms.MessageContent) *CacheRequest {
	return &CacheRequest{
		Provider:  "googleai",
		Model:     "gemini-2.5-flash",
		AccountId: accountId,
		AgentName: "k8s_orchestrator",
		Messages:  msgs,
		Scope:     CacheScopeAccount,
	}
}

func TestGeminiHybrid_ExplicitStrategyAlwaysReferencesCache(t *testing.T) {
	setGeminiCacheStrategy(t, "explicit", 0.65)
	p := NewGoogleAICacheProvider()
	msgs := orchestratorLoop(10, 30_000) // ~100k tokens of tool history
	seedAccountCacheOfSize(t, p, "acc-hybrid-explicit", msgs, 12_000)

	resp := p.ApplyCache(context.Background(), orchestratorRequest("acc-hybrid-explicit", msgs))

	assert.True(t, resp.CacheHit, "default behavior is unchanged")
	assert.Len(t, resp.Options, 1)
}

func TestGeminiHybrid_SkipsCacheOnceToolHistoryDominates(t *testing.T) {
	setGeminiCacheStrategy(t, "hybrid", 0.65)
	p := NewGoogleAICacheProvider()
	msgs := orchestratorLoop(10, 30_000) // cache ~11% of the prompt
	seedAccountCacheOfSize(t, p, "acc-hybrid-long", msgs, 12_000)

	resp := p.ApplyCache(context.Background(), orchestratorRequest("acc-hybrid-long", msgs))

	assert.False(t, resp.CacheHit)
	assert.Empty(t, resp.Options, "no CachedContent, so implicit caching covers the tool history")
	assert.Equal(t, msgs, resp.Messages, "the full prompt is sent")
}

func TestGeminiHybrid_KeepsCacheOnFirstCall(t *testing.T) {
	setGeminiCacheStrategy(t, "hybrid", 0.65)
	p := NewGoogleAICacheProvider()
	msgs := orchestratorLoop(0, 0) // system prompt + question only
	seedAccountCacheOfSize(t, p, "acc-hybrid-first", msgs, 12_000)

	resp := p.ApplyCache(context.Background(), orchestratorRequest("acc-hybrid-first", msgs))

	assert.True(t, resp.CacheHit, "a cold first call keeps the guaranteed hit")
	assert.Len(t, resp.Options, 1)
}

func TestGeminiHybridSkip(t *testing.T) {
	short := orchestratorLoop(1, 3_000)[2:] // ~1k tokens after the cached part
	long := orchestratorLoop(10, 30_000)[2:]
	// A helper call: one human message carrying a large per-call payload.
	oneShot := []llms.MessageContent{{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("d", 60_000)}}}}

	tests := []struct {
		name         string
		strategy     string
		cachedTokens int32
		rest         []llms.MessageContent
		want         bool
	}{
		{"explicit never skips", "explicit", 12_000, long, false},
		{"unknown strategy behaves as explicit", "bogus", 12_000, long, false},
		{"hybrid keeps a cache covering most of the prompt", "hybrid", 12_000, short, false},
		{"hybrid skips once the tool history dominates", "hybrid", 12_000, long, true},
		{"an entry with no recorded size is never skipped", "hybrid", 0, long, false},
		{"a one-shot call with a large input keeps the cache", "hybrid", 2_000, oneShot, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setGeminiCacheStrategy(t, tt.strategy, 0.65)
			_, skip := geminiHybridSkip(tt.cachedTokens, tt.rest)
			assert.Equal(t, tt.want, skip)
		})
	}
}
