package core

import (
	"database/sql"
	"nudgebee/llm/security"
)

type ConversationUsageMetricsRequest struct {
	ConversationId string `json:"conversation_id" validate:"required"` // Note: This is actually the session_id
	AccountId      string `json:"account_id" mapstructure:"required" validate:"required"`
	UserId         string `json:"user_id" mapstructure:"required"`
}

type ConversationUsageMetricsResponse struct {
	Conversation ConversationMetrics `json:"conversation"`
}

type ConversationMetrics struct {
	ConversationId              string           `json:"conversation_id"`
	Messages                    []MessageMetrics `json:"messages"`
	TotalCostUsd                float64          `json:"total_cost_usd"`
	TotalInputTokens            int              `json:"total_input_tokens"`
	TotalOutputTokens           int              `json:"total_output_tokens"`
	TotalCachedInputTokens      int              `json:"total_cached_input_tokens"`
	TotalCacheHitRatePercentage *float64         `json:"total_cache_hit_rate_percentage,omitempty"`
	ModelUsage                  []ModelUsageStat `json:"model_usage"`
	CacheSavings                CacheSavingsInfo `json:"cache_savings"`
	SuccessRatePercentage       *float64         `json:"success_rate_percentage,omitempty"`
	TotalRequests               int              `json:"total_requests"`
	SuccessfulRequests          int              `json:"successful_requests"`
	FailedRequests              int              `json:"failed_requests"`
	TotalToolCalls              int              `json:"total_tool_calls"`
	SuccessfulToolCalls         int              `json:"successful_tool_calls"`
	TotalLatencySeconds         *float64         `json:"total_latency_seconds,omitempty"`
	AverageLatencySeconds       *float64         `json:"average_latency_seconds,omitempty"`
	WallTimeSeconds             *float64         `json:"wall_time_seconds,omitempty"`
	AgentActiveTimeSeconds      *float64         `json:"agent_active_time_seconds,omitempty"`
	ToolTimeSeconds             *float64         `json:"tool_time_seconds,omitempty"`
	ApiTimeSeconds              *float64         `json:"api_time_seconds,omitempty"`
	ApiTimePercentage           *float64         `json:"api_time_percentage,omitempty"`
	ToolTimePercentage          *float64         `json:"tool_time_percentage,omitempty"`
}

type ModelUsageStat struct {
	ModelProvider          string   `json:"model_provider"`
	ModelName              string   `json:"model_name"`
	Requests               int      `json:"requests"`
	InputTokens            int      `json:"input_tokens"`
	OutputTokens           int      `json:"output_tokens"`
	CachedInputTokens      int      `json:"cached_input_tokens"`
	CacheHitRatePercentage *float64 `json:"cache_hit_rate_percentage,omitempty"`
	CostUsd                float64  `json:"cost_usd"`
	SuccessRatePercentage  *float64 `json:"success_rate_percentage,omitempty"`
	SuccessfulRequests     int      `json:"successful_requests"`
	FailedRequests         int      `json:"failed_requests"`
}

type CacheSavingsInfo struct {
	TotalCachedTokens            int      `json:"total_cached_tokens"`
	CacheHitRatePercentage       *float64 `json:"cache_hit_rate_percentage,omitempty"`
	EstimatedCostWithoutCacheUsd float64  `json:"estimated_cost_without_cache_usd"`
	ActualCostUsd                float64  `json:"actual_cost_usd"`
	CostSavingsUsd               float64  `json:"cost_savings_usd"`
	TokensSaved                  int      `json:"tokens_saved"`
}

type MessageMetrics struct {
	MessageId                     string         `json:"message_id"`
	Agents                        []AgentMetrics `json:"agents"`
	MessageCostUsd                float64        `json:"message_cost_usd"`
	MessageInputTokens            int            `json:"message_input_tokens"`
	MessageOutputTokens           int            `json:"message_output_tokens"`
	MessageCachedInputTokens      int            `json:"message_cached_input_tokens"`
	MessageCacheHitRatePercentage *float64       `json:"message_cache_hit_rate_percentage,omitempty"`
}

type AgentMetrics struct {
	AgentName              string          `json:"agent_name"`
	InputTokens            int             `json:"input_tokens"`
	OutputTokens           int             `json:"output_tokens"`
	CachedInputTokens      int             `json:"cached_input_tokens"`
	CacheHitRatePercentage sql.NullFloat64 `json:"cache_hit_rate_percentage"`
	CostUsd                float64         `json:"cost_usd"`
	ModelProviderName      sql.NullString  `json:"model_provider_name"`
	ModelName              sql.NullString  `json:"model_name"`
}

func HandleConversationUsageMetricsApi(ctx *security.RequestContext, request ConversationUsageMetricsRequest) (ConversationUsageMetricsResponse, error) {

	// Get aggregated token usage (for backward compatibility)
	agents, err := GetConversationDao().GetConversationTokenUsage(request.ConversationId)
	if err != nil {
		return ConversationUsageMetricsResponse{}, err
	}

	// Get detailed token usage records for new metrics
	detailedRecords, err := GetConversationDao().GetConversationTokenUsageDetailed(request.ConversationId)
	if err != nil {
		return ConversationUsageMetricsResponse{}, err
	}

	// Get tool calls statistics
	toolStats, err := GetConversationDao().GetConversationToolCallsStats(request.ConversationId)
	if err != nil {
		// Log error but don't fail - tool stats are optional
		toolStats = ToolCallsStats{}
	}

	// Get time breakdown
	timeBreakdown, err := GetConversationDao().GetConversationTimeBreakdown(request.ConversationId)
	if err != nil {
		// Log error but don't fail - time breakdown is optional
		timeBreakdown = TimeBreakdown{}
	}

	// Organize data into hierarchical structure
	messageMap := make(map[string][]AgentMetrics)
	conversationId := ""
	totalCost := 0.0
	totalInputTokens := 0
	totalOutputTokens := 0
	totalCachedInputTokens := 0

	for _, agent := range agents {
		if conversationId == "" {
			conversationId = agent.ConversationId
		}

		agentMetric := AgentMetrics{
			AgentName:              agent.AgentName,
			InputTokens:            agent.InputTokens,
			OutputTokens:           agent.OutputTokens,
			CachedInputTokens:      agent.CachedInputTokens,
			CacheHitRatePercentage: agent.CacheHitRate,
			CostUsd:                agent.Cost,
			ModelProviderName:      agent.ModelProviderName,
			ModelName:              agent.ModelName,
		}

		messageMap[agent.MessageId] = append(messageMap[agent.MessageId], agentMetric)
		totalCost += agentMetric.CostUsd
		totalInputTokens += agent.InputTokens
		totalOutputTokens += agent.OutputTokens
		totalCachedInputTokens += agent.CachedInputTokens
	}

	// Calculate conversation-level cache hit rate
	var totalCacheHitRate *float64
	if totalInputTokens > 0 {
		rate := (float64(totalCachedInputTokens) / float64(totalInputTokens)) * 100
		totalCacheHitRate = &rate
	}

	// Build messages array
	messages := []MessageMetrics{}
	for messageId, agentsList := range messageMap {
		messageCost := 0.0
		messageInputTokens := 0
		messageOutputTokens := 0
		messageCachedInputTokens := 0

		for _, agent := range agentsList {
			messageCost += agent.CostUsd
			messageInputTokens += agent.InputTokens
			messageOutputTokens += agent.OutputTokens
			messageCachedInputTokens += agent.CachedInputTokens
		}

		// Calculate message-level cache hit rate
		var messageCacheHitRate *float64
		if messageInputTokens > 0 {
			rate := (float64(messageCachedInputTokens) / float64(messageInputTokens)) * 100
			messageCacheHitRate = &rate
		}

		messages = append(messages, MessageMetrics{
			MessageId:                     messageId,
			Agents:                        agentsList,
			MessageCostUsd:                messageCost,
			MessageInputTokens:            messageInputTokens,
			MessageOutputTokens:           messageOutputTokens,
			MessageCachedInputTokens:      messageCachedInputTokens,
			MessageCacheHitRatePercentage: messageCacheHitRate,
		})
	}

	// Calculate model usage statistics
	modelUsage := calculateModelUsageStats(detailedRecords)

	// Fetch costs for cache savings calculation
	modelNames := []string{}
	for _, record := range detailedRecords {
		modelNames = append(modelNames, record.LLMModel)
	}
	costs, _ := GetConversationDao().GetConversationCosts(modelNames)

	// Calculate cache savings
	cacheSavings := calculateCacheSavings(detailedRecords, totalCost, costs)

	// Calculate success rate and request stats
	successRate, totalRequests, successfulRequests, failedRequests := calculateSuccessRate(detailedRecords)

	// Calculate latency statistics (API time)
	totalLatency, avgLatency := calculateLatencyStats(detailedRecords)

	// Calculate time breakdown percentages
	var wallTimePtr *float64
	var agentActiveTimePtr *float64
	var toolTimePtr *float64
	var apiTimePtr *float64
	var apiTimePercentagePtr *float64
	var toolTimePercentagePtr *float64

	if timeBreakdown.WallTimeSeconds > 0 {
		wallTimePtr = &timeBreakdown.WallTimeSeconds
	}
	if timeBreakdown.AgentActiveTimeSeconds > 0 {
		agentActiveTimePtr = &timeBreakdown.AgentActiveTimeSeconds
	}
	if timeBreakdown.ToolTimeSeconds > 0 {
		toolTimePtr = &timeBreakdown.ToolTimeSeconds
	}
	if totalLatency != nil && *totalLatency > 0 {
		apiTimePtr = totalLatency
		// Calculate percentages if we have agent active time
		if timeBreakdown.AgentActiveTimeSeconds > 0 {
			apiPct := (*totalLatency / timeBreakdown.AgentActiveTimeSeconds) * 100
			apiTimePercentagePtr = &apiPct
			toolPct := (timeBreakdown.ToolTimeSeconds / timeBreakdown.AgentActiveTimeSeconds) * 100
			toolTimePercentagePtr = &toolPct
		}
	}

	conversation := ConversationMetrics{
		ConversationId:              conversationId,
		Messages:                    messages,
		TotalCostUsd:                totalCost,
		TotalInputTokens:            totalInputTokens,
		TotalOutputTokens:           totalOutputTokens,
		TotalCachedInputTokens:      totalCachedInputTokens,
		TotalCacheHitRatePercentage: totalCacheHitRate,
		ModelUsage:                  modelUsage,
		CacheSavings:                cacheSavings,
		SuccessRatePercentage:       successRate,
		TotalRequests:               totalRequests,
		SuccessfulRequests:          successfulRequests,
		FailedRequests:              failedRequests,
		TotalToolCalls:              toolStats.TotalToolCalls,
		SuccessfulToolCalls:         toolStats.SuccessfulToolCalls,
		TotalLatencySeconds:         totalLatency,
		AverageLatencySeconds:       avgLatency,
		WallTimeSeconds:             wallTimePtr,
		AgentActiveTimeSeconds:      agentActiveTimePtr,
		ToolTimeSeconds:             toolTimePtr,
		ApiTimeSeconds:              apiTimePtr,
		ApiTimePercentage:           apiTimePercentagePtr,
		ToolTimePercentage:          toolTimePercentagePtr,
	}

	return ConversationUsageMetricsResponse{Conversation: conversation}, nil
}

// calculateModelUsageStats aggregates statistics by model
func calculateModelUsageStats(records []TokenUsageDetailedRecord) []ModelUsageStat {
	modelMap := make(map[string]*ModelUsageStat)
	modelNames := []string{}

	for _, record := range records {
		key := record.LLMProvider + "|" + record.LLMModel

		if modelMap[key] == nil {
			modelMap[key] = &ModelUsageStat{
				ModelProvider: record.LLMProvider,
				ModelName:     record.LLMModel,
			}
			modelNames = append(modelNames, record.LLMModel)
		}

		stat := modelMap[key]
		stat.Requests++
		stat.InputTokens += record.InputTokens
		stat.OutputTokens += record.OutputTokens
		stat.CachedInputTokens += record.CachedInputTokens

		switch record.RequestStatus {
		case "success":
			stat.SuccessfulRequests++
		case "failure":
			stat.FailedRequests++
		}
	}

	// Fetch costs in batch
	costs, _ := GetConversationDao().GetConversationCosts(modelNames)

	// Calculate derived metrics for each model
	result := []ModelUsageStat{}
	for _, stat := range modelMap {
		// Calculate cache hit rate
		if stat.InputTokens > 0 {
			rate := (float64(stat.CachedInputTokens) / float64(stat.InputTokens)) * 100
			stat.CacheHitRatePercentage = &rate
		}

		// Calculate success rate
		if stat.Requests > 0 {
			rate := (float64(stat.SuccessfulRequests) / float64(stat.Requests)) * 100
			stat.SuccessRatePercentage = &rate
		}

		// Calculate cost
		if pricing, ok := costs[stat.ModelProvider+":"+stat.ModelName]; ok {
			nonCachedTokens := stat.InputTokens - stat.CachedInputTokens
			var cacheTTLPtr *int
			if len(records) > 0 && records[0].CacheTTLMinutes.Valid {
				ttl := int(records[0].CacheTTLMinutes.Int64)
				cacheTTLPtr = &ttl
			}

			stat.CostUsd = CalculateTotalCost(
				pricing.CostPerMillionInput,
				pricing.CostPerMillionOutput,
				pricing.CacheCostPerMillionTokensPerHour,
				nonCachedTokens,
				stat.CachedInputTokens,
				stat.OutputTokens,
				cacheTTLPtr,
			)
		}

		result = append(result, *stat)
	}

	return result
}

// calculateCacheSavings computes cache savings metrics
func calculateCacheSavings(records []TokenUsageDetailedRecord, actualCost float64, costs map[string]modelPricing) CacheSavingsInfo {
	totalCachedTokens := 0
	totalInputTokens := 0
	estimatedCostWithoutCache := 0.0

	for _, record := range records {
		totalCachedTokens += record.CachedInputTokens
		totalInputTokens += record.InputTokens

		if pricing, ok := costs[record.LLMProvider+":"+record.LLMModel]; ok {
			var cacheTTLPtr *int
			if record.CacheTTLMinutes.Valid {
				ttl := int(record.CacheTTLMinutes.Int64)
				cacheTTLPtr = &ttl
			}

			// Calculate what the cost would have been without caching
			// (all cached tokens would be non-cached)
			costWithoutCache := CalculateTotalCost(
				pricing.CostPerMillionInput,
				pricing.CostPerMillionOutput,
				pricing.CacheCostPerMillionTokensPerHour,
				record.InputTokens, // All input tokens as non-cached
				0,                  // No cached tokens
				record.OutputTokens,
				cacheTTLPtr,
			)
			estimatedCostWithoutCache += costWithoutCache
		}
	}

	var cacheHitRate *float64
	if totalInputTokens > 0 {
		rate := (float64(totalCachedTokens) / float64(totalInputTokens)) * 100
		cacheHitRate = &rate
	}

	costSavings := estimatedCostWithoutCache - actualCost
	if costSavings < 0 {
		costSavings = 0
	}

	return CacheSavingsInfo{
		TotalCachedTokens:            totalCachedTokens,
		CacheHitRatePercentage:       cacheHitRate,
		EstimatedCostWithoutCacheUsd: estimatedCostWithoutCache,
		ActualCostUsd:                actualCost,
		CostSavingsUsd:               costSavings,
		TokensSaved:                  totalCachedTokens,
	}
}

// calculateSuccessRate computes success rate and request counts
func calculateSuccessRate(records []TokenUsageDetailedRecord) (*float64, int, int, int) {
	totalRequests := len(records)
	successfulRequests := 0
	failedRequests := 0

	for _, record := range records {
		switch record.RequestStatus {
		case "success":
			successfulRequests++
		case "failure":
			failedRequests++
		}
	}

	var successRate *float64
	if totalRequests > 0 {
		rate := (float64(successfulRequests) / float64(totalRequests)) * 100
		successRate = &rate
	}

	return successRate, totalRequests, successfulRequests, failedRequests
}

// calculateLatencyStats computes total and average latency
func calculateLatencyStats(records []TokenUsageDetailedRecord) (*float64, *float64) {
	totalLatency := 0.0
	latencyCount := 0

	for _, record := range records {
		if record.LatencySeconds.Valid {
			totalLatency += record.LatencySeconds.Float64
			latencyCount++
		}
	}

	var totalLatencyPtr *float64
	var avgLatencyPtr *float64

	if latencyCount > 0 {
		totalLatencyPtr = &totalLatency
		avgLatency := totalLatency / float64(latencyCount)
		avgLatencyPtr = &avgLatency
	}

	return totalLatencyPtr, avgLatencyPtr
}
