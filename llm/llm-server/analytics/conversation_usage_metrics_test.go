package analytics

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	llmruntime "nudgebee/llm/llms/runtime"

	"github.com/stretchr/testify/assert"
)

func TestModelUsageStatsCalculation(t *testing.T) {
	// Test the helper function directly
	records := []TokenUsageDetailedRecord{
		{
			ConversationID:      "test-conv",
			MessageID:           "msg-1",
			AgentID:             sql.NullString{String: "agent-1", Valid: true},
			AgentName:           "test-agent",
			LLMProvider:         "anthropic",
			LLMModel:            "claude-3-5-sonnet",
			InputTokens:         1000,
			OutputTokens:        500,
			CachedInputTokens:   200,
			CacheCreationTokens: 0,
			RequestStatus:       "success",
			LatencySeconds:      sql.NullFloat64{Float64: 1.5, Valid: true},
		},
		{
			ConversationID:      "test-conv",
			MessageID:           "msg-2",
			AgentID:             sql.NullString{String: "agent-2", Valid: true},
			AgentName:           "test-agent",
			LLMProvider:         "anthropic",
			LLMModel:            "claude-3-5-sonnet",
			InputTokens:         2000,
			OutputTokens:        800,
			CachedInputTokens:   500,
			CacheCreationTokens: 0,
			RequestStatus:       "success",
			LatencySeconds:      sql.NullFloat64{Float64: 2.0, Valid: true},
		},
		{
			ConversationID:      "test-conv",
			MessageID:           "msg-3",
			AgentID:             sql.NullString{String: "agent-3", Valid: true},
			AgentName:           "test-agent",
			LLMProvider:         "google",
			LLMModel:            "gemini-2.5-flash",
			InputTokens:         1500,
			OutputTokens:        600,
			CachedInputTokens:   300,
			CacheCreationTokens: 0,
			RequestStatus:       "failure",
			LatencySeconds:      sql.NullFloat64{Float64: 0.5, Valid: true},
		},
	}

	stats := calculateModelUsageStats(records, "")

	// Should have 2 models
	assert.Equal(t, 2, len(stats))

	// Find anthropic model
	var anthropicStat *ModelUsageStat
	var googleStat *ModelUsageStat
	for i := range stats {
		switch stats[i].ModelProvider {
		case "anthropic":
			anthropicStat = &stats[i]
		case "google":
			googleStat = &stats[i]
		}
	}

	assert.NotNil(t, anthropicStat)
	assert.NotNil(t, googleStat)

	// Validate anthropic stats
	assert.Equal(t, "claude-3-5-sonnet", anthropicStat.ModelName)
	assert.Equal(t, 2, anthropicStat.Requests)
	assert.Equal(t, 3000, anthropicStat.InputTokens)
	assert.Equal(t, 1300, anthropicStat.OutputTokens)
	assert.Equal(t, 700, anthropicStat.CachedInputTokens)
	assert.Equal(t, 2, anthropicStat.SuccessfulRequests)
	assert.Equal(t, 0, anthropicStat.FailedRequests)
	assert.NotNil(t, anthropicStat.SuccessRatePercentage)
	assert.Equal(t, 100.0, *anthropicStat.SuccessRatePercentage)

	// Validate google stats
	assert.Equal(t, "gemini-2.5-flash", googleStat.ModelName)
	assert.Equal(t, 1, googleStat.Requests)
	assert.Equal(t, 1500, googleStat.InputTokens)
	assert.Equal(t, 600, googleStat.OutputTokens)
	assert.Equal(t, 300, googleStat.CachedInputTokens)
	assert.Equal(t, 0, googleStat.SuccessfulRequests)
	assert.Equal(t, 1, googleStat.FailedRequests)
	assert.NotNil(t, googleStat.SuccessRatePercentage)
	assert.Equal(t, 0.0, *googleStat.SuccessRatePercentage)
}

// fixedPricingDao is a fake UsageStore that returns a canned pricing
// map, letting tests exercise CalculateTotalCost's long-ctx tiering without a
// database. Embedding the interface panics loudly on any other store call.
type fixedPricingDao struct {
	llmruntime.UsageStore
	pricing map[string]llmruntime.ModelPricing
	err     error
}

func (d *fixedPricingDao) GetConversationCosts(models []string, tenantId string) (map[string]llmruntime.ModelPricing, error) {
	return d.pricing, d.err
}

// TestModelUsageStatsCalculation_PerCallTiering locks in the fix for a cost
// bug where two individually-small calls, once summed across the whole
// conversation, tripped a model's long-ctx pricing tier that neither call
// would have hit on its own — inflating a real conversation's reported cost
// from $16 to $30. CalculateTotalCost tiers on a single call's own prompt
// size, so calculateModelUsageStats must price each record independently and
// sum the resulting dollars, not sum tokens first and price the aggregate.
func TestModelUsageStatsCalculation_PerCallTiering(t *testing.T) {
	original := llmruntime.SetUsageStore(nil)
	defer llmruntime.SetUsageStore(original)
	llmruntime.SetUsageStore(&fixedPricingDao{
		pricing: map[string]llmruntime.ModelPricing{
			"googleai:gemini-3.1-pro-preview": {
				CostPerMillionInput:         2,
				CostPerMillionOutput:        12,
				ContextThresholdTokens:      sql.NullInt64{Int64: 200_000, Valid: true},
				CostPerMillionInputLongCtx:  sql.NullFloat64{Float64: 4, Valid: true},
				CostPerMillionOutputLongCtx: sql.NullFloat64{Float64: 18, Valid: true},
			},
		},
	})

	// Two calls, each with a 150K-token prompt (under the 200K threshold
	// individually) but summing to 300K across the conversation.
	records := []TokenUsageDetailedRecord{
		{LLMProvider: "googleai", LLMModel: "gemini-3.1-pro-preview", InputTokens: 150_000, RequestStatus: "success"},
		{LLMProvider: "googleai", LLMModel: "gemini-3.1-pro-preview", InputTokens: 150_000, RequestStatus: "success"},
	}

	stats := calculateModelUsageStats(records, "")
	assert.Equal(t, 1, len(stats))

	// Correct (per-call): both calls priced at the standard $2/M rate.
	wantCost := (150_000.0 / 1_000_000.0 * 2) * 2
	assert.InDelta(t, wantCost, stats[0].CostUsd, 1e-9)

	// The bug's signature: pricing the 300K aggregate at the long-ctx $4/M
	// rate would double this. Guard against regressing back to that.
	buggyCost := (300_000.0 / 1_000_000.0) * 4
	assert.NotEqual(t, buggyCost, stats[0].CostUsd)
}

// TestModelUsageStatsCalculation_PrefersStoredCost pins the per-model table to
// the cost persisted when the call was billed. The conversation total
// (GetConversationTokenUsage) and the Cost Analyser (perCallCostExpr) both read
// llm_conversation_token_usage.cost_usd; recomputing here from current pricing
// made the analytics panel's per-model rows disagree with its own total — and
// with Optimize → LLM Analyser — for any call billed before a pricing change.
func TestModelUsageStatsCalculation_PrefersStoredCost(t *testing.T) {
	original := llmruntime.SetUsageStore(nil)
	// Cleanup, not defer: the global store is restored even if this test panics or
	// exits early, so a failure here can't leak the fake into the next test.
	t.Cleanup(func() { llmruntime.SetUsageStore(original) })
	// Live pricing is 10x what these calls were actually billed at.
	llmruntime.SetUsageStore(&fixedPricingDao{
		pricing: map[string]llmruntime.ModelPricing{
			"googleai:gemini-3-flash-preview": {CostPerMillionInput: 5, CostPerMillionOutput: 30},
		},
	})

	records := []TokenUsageDetailedRecord{
		// Billed at insert time — must be taken verbatim.
		{
			LLMProvider: "googleai", LLMModel: "gemini-3-flash-preview",
			InputTokens: 100_000, OutputTokens: 1_000, RequestStatus: "success",
			CostUsd: sql.NullFloat64{Float64: 0.0098, Valid: true},
		},
		// Legacy row with no stored cost — falls back to the live recompute.
		{
			LLMProvider: "googleai", LLMModel: "gemini-3-flash-preview",
			InputTokens: 1_000_000, OutputTokens: 0, RequestStatus: "success",
		},
	}

	stats := calculateModelUsageStats(records, "")
	assert.Equal(t, 1, len(stats))
	// 0.0098 stored + 1M input × $5/M recomputed. Pricing the first call live
	// would have charged it $0.53 instead of the $0.0098 it was billed.
	assert.InDelta(t, 0.0098+5.0, stats[0].CostUsd, 1e-9)
}

// A model with no pricing row must be told apart from one priced at zero: both
// cost $0, but only the first needs a Model Pricing entry.
func TestModelUsageStatsCalculation_CountsUnpricedCalls(t *testing.T) {
	original := llmruntime.SetUsageStore(nil)
	t.Cleanup(func() { llmruntime.SetUsageStore(original) })
	llmruntime.SetUsageStore(&fixedPricingDao{
		pricing: map[string]llmruntime.ModelPricing{
			"custom:free-model": {CostPerMillionInput: 0, CostPerMillionOutput: 0},
		},
	})

	records := []TokenUsageDetailedRecord{
		{LLMProvider: "custom", LLMModel: "free-model", InputTokens: 1_000, RequestStatus: "success"},
		{LLMProvider: "custom", LLMModel: "new-model", InputTokens: 1_000, RequestStatus: "success"},
		{LLMProvider: "custom", LLMModel: "new-model", InputTokens: 1_000, RequestStatus: "success"},
		// Spent no tokens, so there is nothing to price.
		{LLMProvider: "custom", LLMModel: "new-model", RequestStatus: "failure"},
		// Billed at insert time: priced then, even though the row is gone now.
		{
			LLMProvider: "custom", LLMModel: "new-model", InputTokens: 1_000, RequestStatus: "success",
			CostUsd: sql.NullFloat64{Float64: 0.01, Valid: true},
		},
	}

	byModel := map[string]ModelUsageStat{}
	for _, s := range calculateModelUsageStats(records, "") {
		byModel[s.ModelName] = s
	}
	assert.Equal(t, 0, byModel["free-model"].UnpricedRequests)
	assert.Equal(t, 0.0, byModel["free-model"].CostUsd)
	assert.Equal(t, 2, byModel["new-model"].UnpricedRequests)
}

// A failed pricing lookup must not send users to Model Pricing for a price that may exist.
func TestModelUsageStatsCalculation_PricingLookupFailedFlagsNothing(t *testing.T) {
	original := llmruntime.SetUsageStore(nil)
	t.Cleanup(func() { llmruntime.SetUsageStore(original) })
	llmruntime.SetUsageStore(&fixedPricingDao{err: errors.New("db unavailable")})

	records := []TokenUsageDetailedRecord{
		{LLMProvider: "custom", LLMModel: "new-model", InputTokens: 1_000, RequestStatus: "success"},
	}
	stats := calculateModelUsageStats(records, "")
	assert.Equal(t, 1, len(stats))
	assert.Equal(t, 0, stats[0].UnpricedRequests)
}

func TestCacheSavingsCalculation(t *testing.T) {
	// Test cache savings calculation
	records := []TokenUsageDetailedRecord{
		{
			LLMProvider:         "anthropic",
			LLMModel:            "claude-3-5-sonnet",
			InputTokens:         1000,
			OutputTokens:        500,
			CachedInputTokens:   200,
			CacheCreationTokens: 0,
		},
		{
			LLMProvider:         "anthropic",
			LLMModel:            "claude-3-5-sonnet",
			InputTokens:         2000,
			OutputTokens:        800,
			CachedInputTokens:   500,
			CacheCreationTokens: 0,
		},
	}

	// Mock costs for calculation
	costs := map[string]llmruntime.ModelPricing{
		"anthropic:claude-3-5-sonnet": {
			CostPerMillionInput:  3.0,
			CostPerMillionOutput: 15.0,
		},
	}

	actualCost := 0.01
	savings := calculateCacheSavings(records, actualCost, costs)

	assert.Equal(t, 700, savings.TotalCachedTokens)
	assert.Equal(t, 700, savings.TokensSaved)
	assert.Equal(t, actualCost, savings.ActualCostUsd)
	assert.GreaterOrEqual(t, savings.EstimatedCostWithoutCacheUsd, actualCost)
	assert.GreaterOrEqual(t, savings.CostSavingsUsd, 0.0)
	assert.NotNil(t, savings.CacheHitRatePercentage)

	// Validate cache hit rate
	expectedRate := (float64(700) / float64(3000)) * 100
	assert.InDelta(t, expectedRate, *savings.CacheHitRatePercentage, 0.01)
}

func TestSuccessRateCalculation(t *testing.T) {
	records := []TokenUsageDetailedRecord{
		{RequestStatus: "success"},
		{RequestStatus: "success"},
		{RequestStatus: "success"},
		{RequestStatus: "failure"},
	}

	successRate, total, successful, failed := calculateSuccessRate(records)

	assert.Equal(t, 4, total)
	assert.Equal(t, 3, successful)
	assert.Equal(t, 1, failed)
	assert.NotNil(t, successRate)
	assert.Equal(t, 75.0, *successRate)
}

func TestLatencyStatsCalculation(t *testing.T) {
	records := []TokenUsageDetailedRecord{
		{LatencySeconds: sql.NullFloat64{Float64: 1.0, Valid: true}},
		{LatencySeconds: sql.NullFloat64{Float64: 2.0, Valid: true}},
		{LatencySeconds: sql.NullFloat64{Float64: 3.0, Valid: true}},
		{LatencySeconds: sql.NullFloat64{Valid: false}}, // Missing latency
	}

	totalLatency, avgLatency := calculateLatencyStats(records)

	assert.NotNil(t, totalLatency)
	assert.NotNil(t, avgLatency)
	assert.Equal(t, 6.0, *totalLatency)
	assert.Equal(t, 2.0, *avgLatency)
}

// TestMergedApiTimeCalculation_ConcurrentSubAgents reproduces #38129: three
// sub-agents each spend 10s on an LLM call at the same time. A naive sum
// (calculateLatencyStats) reports 30s of "API time" against a ~10s wall-clock
// window, i.e. 300% API time. The merged calculation must report ~10s, since
// the calls overlap almost entirely.
func TestMergedApiTimeCalculation_ConcurrentSubAgents(t *testing.T) {
	end := time.Date(2026, 1, 1, 12, 0, 10, 0, time.UTC)
	records := []TokenUsageDetailedRecord{
		{CreatedAt: end, LatencySeconds: sql.NullFloat64{Float64: 10.0, Valid: true}},
		{CreatedAt: end, LatencySeconds: sql.NullFloat64{Float64: 10.0, Valid: true}},
		{CreatedAt: end, LatencySeconds: sql.NullFloat64{Float64: 10.0, Valid: true}},
	}

	merged := calculateMergedApiTimeSeconds(records)

	assert.NotNil(t, merged)
	assert.Equal(t, 10.0, *merged)
}

// TestMergedApiTimeCalculation_SequentialCalls checks that non-overlapping
// calls still sum normally, so the fix for #38129 doesn't undercount the
// common (non-parallel) case.
func TestMergedApiTimeCalculation_SequentialCalls(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	records := []TokenUsageDetailedRecord{
		{CreatedAt: base.Add(2 * time.Second), LatencySeconds: sql.NullFloat64{Float64: 2.0, Valid: true}},  // [0s,2s]
		{CreatedAt: base.Add(10 * time.Second), LatencySeconds: sql.NullFloat64{Float64: 3.0, Valid: true}}, // [7s,10s]
		{CreatedAt: base, LatencySeconds: sql.NullFloat64{Valid: false}},                                    // ignored
	}

	merged := calculateMergedApiTimeSeconds(records)

	assert.NotNil(t, merged)
	assert.Equal(t, 5.0, *merged) // 2s + 3s, no overlap
}

func TestMergedApiTimeCalculation_NoValidRecords(t *testing.T) {
	records := []TokenUsageDetailedRecord{
		{LatencySeconds: sql.NullFloat64{Valid: false}},
		{LatencySeconds: sql.NullFloat64{Float64: 0, Valid: true}},
	}

	merged := calculateMergedApiTimeSeconds(records)

	assert.Nil(t, merged)
}
