package agents

import (
	"sync"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUsageDao embeds IConversationDao (nil) and overrides only the two
// methods recordCodeAnalysisTokenUsage touches. Pricing mirrors the real
// per-record long-context tier switch: totalPrompt > 200K → 2× rates.
type fakeUsageDao struct {
	core.IConversationDao
	mu           sync.Mutex
	inserted     []core.TokenUsageRecord
	nilAgentOnce bool // simulate the FK retry nulling record.AgentID on first insert
}

func (f *fakeUsageDao) GetConversationCost(provider, model string, nonCachedInputTokens, cachedInputTokens, cacheCreationTokens, outputTokens, thinkingTokens int, tenantId string) (float64, error) {
	inRate, cachedRate, outRate := 2.0, 0.2, 12.0
	if nonCachedInputTokens+cachedInputTokens+cacheCreationTokens > 200000 {
		inRate, cachedRate, outRate = 4.0, 0.4, 18.0
	}
	const m = 1_000_000.0
	return float64(nonCachedInputTokens)/m*inRate +
		float64(cachedInputTokens)/m*cachedRate +
		float64(outputTokens+thinkingTokens)/m*outRate, nil
}

func (f *fakeUsageDao) InsertTokenUsage(record *core.TokenUsageRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nilAgentOnce && len(f.inserted) == 0 {
		record.AgentID = nil // what the real FK-violation retry does in place
	}
	f.inserted = append(f.inserted, *record)
	return nil
}

func withFakeUsageDao(t *testing.T) *fakeUsageDao {
	t.Helper()
	fake := &fakeUsageDao{}
	prev := core.GetConversationDao()
	core.SetConversationDao(fake)
	t.Cleanup(func() { core.SetConversationDao(prev) })
	return fake
}

func usageQuery() core.NBAgentRequest {
	return core.NBAgentRequest{ConversationId: "conv-1", MessageId: "msg-1", AccountId: "acct-1"}
}

// Per-call records must become one row each, priced at the CALL's own tier:
// three 150K-prompt calls stay on standard rates, while the 450K aggregate
// would have been priced long-ctx — the ~2× reported-cost inflation this
// change removes.
func TestRecordCodeAnalysisTokenUsage_MultiRowPerCallTiering(t *testing.T) {
	fake := withFakeUsageDao(t)
	tu := &codeAnalysisTokenUsage{
		PromptTokens: 450000, CompletionTokens: 3000, TotalTokens: 453000,
		Model: "gemini-3.1-pro-preview", Provider: "googleai",
		Calls: []codeAnalysisTokenUsageCall{
			{PromptTokens: 150000, CompletionTokens: 1000, LatencySeconds: 2.5, Model: "gemini-3.1-pro-preview", Provider: "googleai"},
			{PromptTokens: 150000, CompletionTokens: 1000, Model: "gemini-3.1-pro-preview", Provider: "googleai"},
			{PromptTokens: 150000, CompletionTokens: 1000, Model: "gemini-3-flash-preview", Provider: "googleai"},
		},
	}

	recordCodeAnalysisTokenUsage(usageQuery(), tu, 900)

	require.Len(t, fake.inserted, 3)
	var total float64
	for _, r := range fake.inserted {
		require.NotNil(t, r.CostUsd)
		total += *r.CostUsd
	}
	// Standard rates: 3 × (0.15M×$2 + 0.001M×$12) = 3 × $0.312 = $0.936
	assert.InDelta(t, 0.936, total, 0.001)
	// The aggregate priced as one record would be long-ctx: 0.45M×$4 + 0.003M×$18 = $1.854
	aggCost, err := fake.GetConversationCost("googleai", "gemini-3.1-pro-preview", 450000, 0, 0, 3000, 0, "")
	require.NoError(t, err)
	assert.Greater(t, aggCost, 1.8)

	// Per-call latency only; the run wall time must never land on call rows.
	require.NotNil(t, fake.inserted[0].LatencySeconds)
	assert.InDelta(t, 2.5, *fake.inserted[0].LatencySeconds, 0.0001)
	assert.Nil(t, fake.inserted[1].LatencySeconds)
	// Per-call model attribution survives.
	assert.Equal(t, "gemini-3-flash-preview", fake.inserted[2].LLMModel)
}

// Without per-call records (older pod image) the single-aggregate-row
// behavior is preserved, including the run wall-time latency.
func TestRecordCodeAnalysisTokenUsage_FallbackSingleRow(t *testing.T) {
	fake := withFakeUsageDao(t)
	tu := &codeAnalysisTokenUsage{PromptTokens: 450000, CompletionTokens: 3000, TotalTokens: 453000, Model: "m", Provider: "p"}

	recordCodeAnalysisTokenUsage(usageQuery(), tu, 900)

	require.Len(t, fake.inserted, 1)
	assert.Equal(t, 450000, fake.inserted[0].InputTokens)
	require.NotNil(t, fake.inserted[0].LatencySeconds)
	assert.InDelta(t, 900, *fake.inserted[0].LatencySeconds, 0.0001)
}

// When the aggregate exceeds the recorded calls (cap drop / version skew) a
// residual row reconciles so SUM(rows) == aggregate for billing.
func TestRecordCodeAnalysisTokenUsage_ResidualRow(t *testing.T) {
	fake := withFakeUsageDao(t)
	tu := &codeAnalysisTokenUsage{
		PromptTokens: 200000, CompletionTokens: 2000, TotalTokens: 202000,
		Model: "m", Provider: "p", CallsDropped: 1,
		Calls: []codeAnalysisTokenUsageCall{
			{PromptTokens: 120000, CompletionTokens: 1500},
		},
	}

	recordCodeAnalysisTokenUsage(usageQuery(), tu, 100)

	require.Len(t, fake.inserted, 2)
	assert.Equal(t, 80000, fake.inserted[1].InputTokens)
	assert.Equal(t, 500, fake.inserted[1].OutputTokens)
	assert.Nil(t, fake.inserted[1].LatencySeconds)
	sum := fake.inserted[0].InputTokens + fake.inserted[1].InputTokens
	assert.Equal(t, tu.PromptTokens, sum)
}

// A cached-only residual (version-skew shape: prompt/completion sums match but
// cached tokens don't) must still produce a reconciliation row — cached tokens
// bill at their own rate and would otherwise silently vanish.
func TestRecordCodeAnalysisTokenUsage_ResidualRowCachedOnly(t *testing.T) {
	fake := withFakeUsageDao(t)
	tu := &codeAnalysisTokenUsage{
		PromptTokens: 100000, CompletionTokens: 1000, TotalTokens: 101000,
		CachedContentTokens: 60000, Model: "m", Provider: "p",
		Calls: []codeAnalysisTokenUsageCall{
			{PromptTokens: 100000, CompletionTokens: 1000, CachedContentTokens: 20000},
		},
	}

	recordCodeAnalysisTokenUsage(usageQuery(), tu, 100)

	require.Len(t, fake.inserted, 2)
	assert.Equal(t, 0, fake.inserted[1].InputTokens)
	assert.Equal(t, 40000, fake.inserted[1].CachedInputTokens)
}

// After the FK retry nils AgentID on the first insert, subsequent rows must
// arrive without an AgentID instead of repeating the failing exec.
func TestRecordCodeAnalysisTokenUsage_AgentIDPropagation(t *testing.T) {
	fake := withFakeUsageDao(t)
	fake.nilAgentOnce = true
	query := usageQuery()
	query.AgentId = "not-a-real-agent"
	tu := &codeAnalysisTokenUsage{
		PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220, Model: "m", Provider: "p",
		Calls: []codeAnalysisTokenUsageCall{
			{PromptTokens: 100, CompletionTokens: 10},
			{PromptTokens: 100, CompletionTokens: 10},
		},
	}

	recordCodeAnalysisTokenUsage(query, tu, 1)

	require.Len(t, fake.inserted, 2)
	assert.Nil(t, fake.inserted[0].AgentID)
	assert.Nil(t, fake.inserted[1].AgentID)
}

// Nil and empty usage must record nothing.
func TestRecordCodeAnalysisTokenUsage_EmptyGuard(t *testing.T) {
	fake := withFakeUsageDao(t)
	recordCodeAnalysisTokenUsage(usageQuery(), nil, 1)
	recordCodeAnalysisTokenUsage(usageQuery(), &codeAnalysisTokenUsage{}, 1)
	assert.Empty(t, fake.inserted)
}

// parseTokenUsageMap must pick up calls[] with the same dual thinking-token
// key acceptance as the aggregate.
func TestParseTokenUsageMap_Calls(t *testing.T) {
	tu := parseTokenUsageMap(map[string]any{
		"prompt_tokens": float64(300), "completion_tokens": float64(30), "total_tokens": float64(330),
		"model": "m", "provider": "p", "calls_dropped": float64(2),
		"calls": []any{
			map[string]any{"prompt_tokens": float64(100), "completion_tokens": float64(10), "latency_seconds": 1.25, "thinking_tokens": float64(7), "model": "m1", "provider": "p"},
			map[string]any{"prompt_tokens": float64(200), "completion_tokens": float64(20), "thoughts_token_count": float64(3)},
		},
	})

	require.NotNil(t, tu)
	require.Len(t, tu.Calls, 2)
	assert.Equal(t, 2, tu.CallsDropped)
	assert.Equal(t, 100, tu.Calls[0].PromptTokens)
	assert.InDelta(t, 1.25, tu.Calls[0].LatencySeconds, 0.0001)
	assert.Equal(t, 7, tu.Calls[0].ThinkingTokens)
	assert.Equal(t, "m1", tu.Calls[0].Model)
	assert.Equal(t, 3, tu.Calls[1].ThinkingTokens)
}
