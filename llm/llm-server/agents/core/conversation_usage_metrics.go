package core

import (
	"database/sql"
	"fmt"

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
	// ReasoningByToolCall maps a tool_call_id to the reasoning (LLM thinking) its
	// owning agent spent across the conversation, so the UI can show, in a tool's
	// details, how long that tool's agent reasoned. Keyed by tool_call_id because
	// UI tool-call tasks carry only that id (no agent_id/message_id).
	ReasoningByToolCall map[string]ToolReasoning `json:"reasoning_by_tool_call,omitempty"`
}

// ToolReasoning is an agent's reasoning total, attributed to each of its tool calls.
// Sourced from llm_conversation_token_usage (no new storage). The same agent total
// is repeated for each of that agent's tool calls — it is an agent-level figure
// shown per tool, not a per-tool-call split (token_usage has no tool_call_id).
type ToolReasoning struct {
	AgentName        string  `json:"agent_name"`
	ReasoningSeconds float64 `json:"reasoning_seconds"`
	ThinkingTokens   int     `json:"thinking_tokens"`
	Calls            int     `json:"calls"`
}

type ModelUsageStat struct {
	ModelProvider          string   `json:"model_provider"`
	ModelName              string   `json:"model_name"`
	Requests               int      `json:"requests"`
	InputTokens            int      `json:"input_tokens"`
	OutputTokens           int      `json:"output_tokens"`
	CachedInputTokens      int      `json:"cached_input_tokens"`
	CacheCreationTokens    int      `json:"cache_creation_tokens"`
	ThinkingTokens         int      `json:"thinking_tokens"`
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
	// Honor a dynamic-RBAC ai_conversations:Read grant, not just built-in account
	// roles — this per-conversation usage view is part of the conversation read
	// surface (classifier maps ai_get_conversation_usage_metrics → ai_conversations).
	if !ctx.GetSecurityContext().CanReadAccountData(request.AccountId, "ai_conversations") {
		return ConversationUsageMetricsResponse{}, fmt.Errorf("HandleConversationUsageMetricsApi: forbidden account_id")
	}

	// All metrics queries key off session_id, but callers may pass a conversation id
	// (e.g. workflow/autopilot conversations opened by conversation_id, whose session_id is
	// a wf__… id). Normalize once here so every downstream lookup resolves either way.
	request.ConversationId = GetConversationDao().ResolveSessionId(request.ConversationId)

	// Get aggregated token usage (for backward compatibility)
	agents, err := GetConversationDao().GetConversationTokenUsage(request.ConversationId, request.AccountId)
	if err != nil {
		return ConversationUsageMetricsResponse{}, err
	}

	// Get detailed token usage records for new metrics
	detailedRecords, err := GetConversationDao().GetConversationTokenUsageDetailed(request.ConversationId, request.AccountId)
	if err != nil {
		return ConversationUsageMetricsResponse{}, err
	}

	// Get tool calls statistics
	toolStats, err := GetConversationDao().GetConversationToolCallsStats(request.ConversationId, request.AccountId)
	if err != nil {
		// Log error but don't fail - tool stats are optional
		toolStats = ToolCallsStats{}
	}

	// Get time breakdown
	timeBreakdown, err := GetConversationDao().GetConversationTimeBreakdown(request.ConversationId, request.AccountId)
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

	// Time-split each agent's reasoning (LLM thinking) calls onto the specific tool
	// call they produced: in a ReAct loop the agent reasons, then calls a tool, so a
	// reasoning call is attributed to the first of that agent's tool calls that starts
	// at/after it. This makes a tool's details show only the reasoning that led to that
	// call (slices sum to the agent total) instead of repeating the agent total on each.
	reasoningByToolCall := make(map[string]ToolReasoning)
	if toolAgents, taErr := GetConversationDao().GetConversationToolCallAgents(request.ConversationId); taErr == nil {
		// Tool calls grouped by agent, each list already chronological (query ORDER BY).
		toolsByAgent := make(map[string][]ToolCallAgent)
		for _, ta := range toolAgents {
			toolsByAgent[ta.AgentID] = append(toolsByAgent[ta.AgentID], ta)
		}
		// detailedRecords are chronological too; a per-agent cursor walks that agent's
		// tool calls forward as we process its reasoning calls in time order.
		cursor := make(map[string]int)
		for _, r := range detailedRecords {
			if !r.AgentID.Valid {
				continue
			}
			tools := toolsByAgent[r.AgentID.String]
			i := cursor[r.AgentID.String]
			for i < len(tools) && tools[i].CreatedAt.Before(r.CreatedAt) {
				i++
			}
			cursor[r.AgentID.String] = i
			if i >= len(tools) {
				// Reasoning after the agent's last tool call — final answer synthesis,
				// not attributable to a tool. Skip.
				continue
			}
			tc := reasoningByToolCall[tools[i].ToolCallID]
			tc.AgentName = r.AgentName
			if r.LatencySeconds.Valid {
				tc.ReasoningSeconds += r.LatencySeconds.Float64
			}
			tc.ThinkingTokens += r.ThinkingTokens
			tc.Calls++
			reasoningByToolCall[tools[i].ToolCallID] = tc
		}
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
	modelUsage := calculateModelUsageStats(detailedRecords, pricingTenantFromContext(ctx))

	// Fetch costs for cache savings calculation
	modelNames := []string{}
	for _, record := range detailedRecords {
		modelNames = append(modelNames, record.LLMModel)
	}
	costs, _ := GetConversationDao().GetConversationCosts(modelNames, pricingTenantFromContext(ctx))

	// Add per-hour storage cost for conversation-scoped caches owned by this
	// conversation. Account/tenant/global-scoped caches are excluded — their
	// storage rolls up into account/tenant budgets, not conversation metrics.
	// tenant_id pulled from security context for defense-in-depth tenant isolation.
	tenantId := ctx.GetSecurityContext().GetTenantId()
	if storageCost, scErr := GetConversationDao().GetConversationLifecycleStorageCost(request.ConversationId, tenantId); scErr == nil {
		totalCost += storageCost
	}

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
		ReasoningByToolCall:         reasoningByToolCall,
	}

	return ConversationUsageMetricsResponse{Conversation: conversation}, nil
}

// calculateModelUsageStats aggregates statistics by model
func calculateModelUsageStats(records []TokenUsageDetailedRecord, tenantId string) []ModelUsageStat {
	if len(records) == 0 {
		return []ModelUsageStat{}
	}

	modelNames := []string{}
	seenModel := map[string]bool{}
	for _, record := range records {
		if !seenModel[record.LLMModel] {
			seenModel[record.LLMModel] = true
			modelNames = append(modelNames, record.LLMModel)
		}
	}

	// Fetch costs in batch. Tolerate a nil DAO (unit tests) — without pricing,
	// CostUsd stays zero and only the per-model token aggregates are populated.
	var costs map[string]modelPricing
	if dao := GetConversationDao(); dao != nil {
		costs, _ = dao.GetConversationCosts(modelNames, tenantId)
	}

	modelMap := make(map[string]*ModelUsageStat)
	for _, record := range records {
		key := record.LLMProvider + "|" + record.LLMModel

		if modelMap[key] == nil {
			modelMap[key] = &ModelUsageStat{
				ModelProvider: record.LLMProvider,
				ModelName:     record.LLMModel,
			}
		}

		stat := modelMap[key]
		stat.Requests++
		stat.InputTokens += record.InputTokens
		stat.OutputTokens += record.OutputTokens
		stat.CachedInputTokens += record.CachedInputTokens
		stat.CacheCreationTokens += record.CacheCreationTokens
		stat.ThinkingTokens += record.ThinkingTokens

		switch record.RequestStatus {
		case "success":
			stat.SuccessfulRequests++
		case "failure":
			stat.FailedRequests++
		}

		// Prefer the cost persisted at insert time — the conversation total
		// (GetConversationTokenUsage) and the Cost Analyser (perCallCostExpr) both
		// do, so recomputing here made this table contradict the very total it sits
		// under whenever llm_model_pricing changed after the call was billed.
		//
		// Legacy rows with no stored cost fall back to a live recompute. Cost is
		// tiered per call (see CalculateTotalCost) on that call's own prompt size,
		// so it must be computed per record and summed here — aggregating tokens
		// across the whole conversation first and pricing the total once would push
		// the combined volume over the long-ctx threshold even when no single call
		// did, wildly inflating cost.
		if record.CostUsd.Valid {
			stat.CostUsd += record.CostUsd.Float64
		} else if pricing, ok := costs[record.LLMProvider+":"+record.LLMModel]; ok {
			nonCachedTokens := record.InputTokens - record.CachedInputTokens
			if nonCachedTokens < 0 {
				nonCachedTokens = 0
			}
			stat.CostUsd += CalculateTotalCost(
				&pricing,
				nonCachedTokens,
				record.CachedInputTokens,
				record.CacheCreationTokens,
				record.OutputTokens,
				record.ThinkingTokens,
			)
		}
	}

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

		result = append(result, *stat)
	}

	return result
}

// calculateCacheSavings computes cache savings metrics using the redesigned
// formula. For each call we compute:
//
//	cost_without_cache = (input + cache_creation) × input_rate
//	                   + (output + thinking)      × output_rate
//
// Then sum and subtract the actual cost. The difference is what caching
// saved (or, if negative, cost) for the period — accounting for both the
// cached-read discount and any cache-write premium (e.g. Anthropic 1.25x).
func calculateCacheSavings(records []TokenUsageDetailedRecord, actualCost float64, costs map[string]modelPricing) CacheSavingsInfo {
	totalCachedTokens := 0
	totalInputTokens := 0
	estimatedCostWithoutCache := 0.0

	for _, record := range records {
		totalCachedTokens += record.CachedInputTokens
		totalInputTokens += record.InputTokens

		if pricing, ok := costs[record.LLMProvider+":"+record.LLMModel]; ok {
			// "Without cache" hypothetical: all input tokens billed at the
			// non-cached rate, no cache creation, same output and thinking.
			estimatedCostWithoutCache += CalculateTotalCost(
				&pricing,
				record.InputTokens, // all input as non-cached
				0,                  // no cached read
				0,                  // no cache creation
				record.OutputTokens,
				record.ThinkingTokens,
			)
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
