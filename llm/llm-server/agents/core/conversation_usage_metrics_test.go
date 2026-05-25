package core

import (
	"database/sql"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConversationUsageMetricsApi(t *testing.T) {
	// Skip if database is not available
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}

	// Skip if environment variables are not set
	if os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("Skipping test: TEST_TENANT, TEST_ACCOUNT, or TEST_USER environment variables not set")
	}

	// Test with real conversation data
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	request := ConversationUsageMetricsRequest{
		ConversationId: "ut-k8s-chain-43-rewoo",
		AccountId:      os.Getenv("TEST_ACCOUNT"),
		UserId:         os.Getenv("TEST_USER"),
	}

	response, err := HandleConversationUsageMetricsApi(sc, request)

	// Basic assertions
	assert.Nil(t, err)
	assert.NotNil(t, response)
	assert.NotNil(t, response.Conversation)

	conv := response.Conversation

	// Test that we have messages
	assert.NotNil(t, conv.Messages)

	// Test basic metrics
	assert.GreaterOrEqual(t, conv.TotalCostUsd, 0.0)
	assert.GreaterOrEqual(t, conv.TotalInputTokens, 0)
	assert.GreaterOrEqual(t, conv.TotalOutputTokens, 0)
	assert.GreaterOrEqual(t, conv.TotalCachedInputTokens, 0)

	// Test new model usage field
	assert.NotNil(t, conv.ModelUsage)
	t.Logf("Found %d models used", len(conv.ModelUsage))

	if len(conv.ModelUsage) > 0 {
		for _, modelStat := range conv.ModelUsage {
			// Validate model statistics
			assert.NotEmpty(t, modelStat.ModelProvider, "Model provider should not be empty")
			assert.NotEmpty(t, modelStat.ModelName, "Model name should not be empty")
			assert.GreaterOrEqual(t, modelStat.Requests, 0, "Requests should be >= 0")
			assert.GreaterOrEqual(t, modelStat.InputTokens, 0, "Input tokens should be >= 0")
			assert.GreaterOrEqual(t, modelStat.OutputTokens, 0, "Output tokens should be >= 0")
			assert.GreaterOrEqual(t, modelStat.CachedInputTokens, 0, "Cached input tokens should be >= 0")
			assert.GreaterOrEqual(t, modelStat.CostUsd, 0.0, "Cost should be >= 0")
			assert.GreaterOrEqual(t, modelStat.SuccessfulRequests, 0, "Successful requests should be >= 0")
			assert.GreaterOrEqual(t, modelStat.FailedRequests, 0, "Failed requests should be >= 0")

			// Validate that total requests = successful + failed
			assert.Equal(t, modelStat.Requests, modelStat.SuccessfulRequests+modelStat.FailedRequests,
				"Total requests should equal successful + failed requests")

			// Validate cache hit rate if present
			if modelStat.CacheHitRatePercentage != nil {
				assert.GreaterOrEqual(t, *modelStat.CacheHitRatePercentage, 0.0)
				assert.LessOrEqual(t, *modelStat.CacheHitRatePercentage, 100.0)
			}

			// Validate success rate if present
			if modelStat.SuccessRatePercentage != nil {
				assert.GreaterOrEqual(t, *modelStat.SuccessRatePercentage, 0.0)
				assert.LessOrEqual(t, *modelStat.SuccessRatePercentage, 100.0)
			}

			t.Logf("Model: %s/%s - Requests: %d, Input: %d, Output: %d, Cached: %d, Cost: $%.6f, Success: %d, Failed: %d",
				modelStat.ModelProvider,
				modelStat.ModelName,
				modelStat.Requests,
				modelStat.InputTokens,
				modelStat.OutputTokens,
				modelStat.CachedInputTokens,
				modelStat.CostUsd,
				modelStat.SuccessfulRequests,
				modelStat.FailedRequests,
			)
		}
	}

	// Test new cache savings field
	assert.NotNil(t, conv.CacheSavings)
	assert.GreaterOrEqual(t, conv.CacheSavings.TotalCachedTokens, 0)
	assert.GreaterOrEqual(t, conv.CacheSavings.EstimatedCostWithoutCacheUsd, 0.0)
	assert.GreaterOrEqual(t, conv.CacheSavings.ActualCostUsd, 0.0)
	assert.GreaterOrEqual(t, conv.CacheSavings.CostSavingsUsd, 0.0)
	assert.GreaterOrEqual(t, conv.CacheSavings.TokensSaved, 0)

	// Validate cache hit rate percentage
	if conv.CacheSavings.CacheHitRatePercentage != nil {
		assert.GreaterOrEqual(t, *conv.CacheSavings.CacheHitRatePercentage, 0.0)
		assert.LessOrEqual(t, *conv.CacheSavings.CacheHitRatePercentage, 100.0)
	}

	// Validate cost calculations
	assert.LessOrEqual(t, conv.CacheSavings.ActualCostUsd, conv.CacheSavings.EstimatedCostWithoutCacheUsd,
		"Actual cost should be less than or equal to estimated cost without cache")
	assert.Equal(t, conv.CacheSavings.EstimatedCostWithoutCacheUsd-conv.CacheSavings.ActualCostUsd, conv.CacheSavings.CostSavingsUsd,
		"Cost savings should equal estimated - actual")

	t.Logf("Cache Savings: Tokens Saved: %d (%.2f%%), Cost Savings: $%.6f (Estimated: $%.6f, Actual: $%.6f)",
		conv.CacheSavings.TokensSaved,
		getSafeFloat(conv.CacheSavings.CacheHitRatePercentage),
		conv.CacheSavings.CostSavingsUsd,
		conv.CacheSavings.EstimatedCostWithoutCacheUsd,
		conv.CacheSavings.ActualCostUsd,
	)

	// Test new success rate fields
	assert.GreaterOrEqual(t, conv.TotalRequests, 0)
	assert.GreaterOrEqual(t, conv.SuccessfulRequests, 0)
	assert.GreaterOrEqual(t, conv.FailedRequests, 0)

	// Validate that total = successful + failed
	assert.Equal(t, conv.TotalRequests, conv.SuccessfulRequests+conv.FailedRequests,
		"Total requests should equal successful + failed")

	// Validate success rate percentage
	if conv.SuccessRatePercentage != nil {
		assert.GreaterOrEqual(t, *conv.SuccessRatePercentage, 0.0)
		assert.LessOrEqual(t, *conv.SuccessRatePercentage, 100.0)

		// Verify success rate calculation
		if conv.TotalRequests > 0 {
			expectedRate := (float64(conv.SuccessfulRequests) / float64(conv.TotalRequests)) * 100
			assert.InDelta(t, expectedRate, *conv.SuccessRatePercentage, 0.01, "Success rate calculation should be correct")
		}
	}

	t.Logf("Success Rate: %.2f%% (%d successful, %d failed out of %d total)",
		getSafeFloat(conv.SuccessRatePercentage),
		conv.SuccessfulRequests,
		conv.FailedRequests,
		conv.TotalRequests,
	)

	// Test tool calls
	assert.GreaterOrEqual(t, conv.TotalToolCalls, 0)
	assert.GreaterOrEqual(t, conv.SuccessfulToolCalls, 0)
	assert.LessOrEqual(t, conv.SuccessfulToolCalls, conv.TotalToolCalls,
		"Successful tool calls should be <= total tool calls")

	t.Logf("Tool Calls: %d total, %d successful",
		conv.TotalToolCalls,
		conv.SuccessfulToolCalls,
	)

	// Test latency metrics
	if conv.TotalLatencySeconds != nil {
		assert.GreaterOrEqual(t, *conv.TotalLatencySeconds, 0.0)
		t.Logf("Total Latency: %.2f seconds", *conv.TotalLatencySeconds)
	}

	if conv.AverageLatencySeconds != nil {
		assert.GreaterOrEqual(t, *conv.AverageLatencySeconds, 0.0)
		t.Logf("Average Latency: %.2f seconds", *conv.AverageLatencySeconds)
	}

	// Test cache hit rate consistency
	if conv.TotalCacheHitRatePercentage != nil && conv.TotalInputTokens > 0 {
		expectedRate := (float64(conv.TotalCachedInputTokens) / float64(conv.TotalInputTokens)) * 100
		assert.InDelta(t, expectedRate, *conv.TotalCacheHitRatePercentage, 0.01, "Cache hit rate calculation should be correct")
	}

	// Print summary
	t.Logf("\n=== Conversation Usage Metrics Summary ===")
	t.Logf("Conversation ID: %s", conv.ConversationId)
	t.Logf("Total Cost: $%.6f", conv.TotalCostUsd)
	t.Logf("Total Input Tokens: %d", conv.TotalInputTokens)
	t.Logf("Total Output Tokens: %d", conv.TotalOutputTokens)
	t.Logf("Total Cached Tokens: %d", conv.TotalCachedInputTokens)
	if conv.TotalCacheHitRatePercentage != nil {
		t.Logf("Cache Hit Rate: %.2f%%", *conv.TotalCacheHitRatePercentage)
	}
	t.Logf("Models Used: %d", len(conv.ModelUsage))
	t.Logf("Messages: %d", len(conv.Messages))
}

func TestConversationUsageMetricsWithNoData(t *testing.T) {
	// Skip if database is not available
	if GetConversationDao() == nil {
		t.Skip("Skipping test: database not available")
	}

	// Skip if environment variables are not set
	if os.Getenv("TEST_TENANT") == "" || os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("Skipping test: TEST_TENANT, TEST_ACCOUNT, or TEST_USER environment variables not set")
	}

	// Test with non-existent conversation
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	request := ConversationUsageMetricsRequest{
		ConversationId: "00000000-0000-0000-0000-000000000000",
		AccountId:      os.Getenv("TEST_ACCOUNT"),
		UserId:         os.Getenv("TEST_USER"),
	}

	response, err := HandleConversationUsageMetricsApi(sc, request)

	// Should not error even with no data
	assert.Nil(t, err)
	assert.NotNil(t, response)

	conv := response.Conversation

	// Should have empty/zero values
	assert.Equal(t, "", conv.ConversationId)
	assert.Equal(t, 0.0, conv.TotalCostUsd)
	assert.Equal(t, 0, conv.TotalInputTokens)
	assert.Equal(t, 0, conv.TotalOutputTokens)
	assert.Equal(t, 0, len(conv.Messages))
	assert.Equal(t, 0, len(conv.ModelUsage))
	assert.Equal(t, 0, conv.TotalRequests)
	assert.Equal(t, 0, conv.TotalToolCalls)
}

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

	stats := calculateModelUsageStats(records)

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
	costs := map[string]modelPricing{
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

// Helper function to safely get float value
func getSafeFloat(f *float64) float64 {
	if f == nil {
		return 0.0
	}
	return *f
}
