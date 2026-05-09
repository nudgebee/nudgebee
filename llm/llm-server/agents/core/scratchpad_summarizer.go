package core

import (
	"context"
	"fmt"
	"time"

	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// getScratchpadSummaryMaxLen returns the configured max length for LLM-generated summaries.
func getScratchpadSummaryMaxLen() int {
	if config.Config.LlmServerScratchpadSummaryMaxLen > 0 {
		return config.Config.LlmServerScratchpadSummaryMaxLen
	}
	return 500
}

// getScratchpadSummaryMinBytes returns the minimum observation size that triggers LLM
// summarization. Smaller observations use byte truncation instead.
func getScratchpadSummaryMinBytes() int {
	if config.Config.LlmServerScratchpadSummaryMinBytes > 0 {
		return config.Config.LlmServerScratchpadSummaryMinBytes
	}
	return 1024
}

// getScratchpadSummaryTimeout returns the configured timeout for a single summarization call.
func getScratchpadSummaryTimeout() time.Duration {
	ms := config.Config.LlmServerScratchpadSummaryTimeoutMs
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 10 * time.Second
}

// SummarizeObservation compresses a tool observation for inclusion in an older scratchpad slot.
//
// The caller passes the already-processed observation (post response-handler) as `obs`;
// `step` is used only for metadata (tool name, tool input) and for caching the resulting
// summary in `step.CompressedObservation`.
//
// Compression strategy (tiered by size):
//  1. obs <= compressedObservationPreview (100 bytes): returned verbatim
//  2. feature flag off, or obs < min bytes, or ctx nil: byte truncation via compressObservation
//  3. obs >= min bytes and flag on: LLM summary (falls back to byte truncation on failure)
//
// The cached result is returned on subsequent calls to avoid redundant LLM invocations.
func SummarizeObservation(
	ctx *security.RequestContext,
	step *NBAgentPlannerToolActionStep,
	request NBAgentRequest,
	obs string,
) string {
	// Tier 1: very short observations need no compression.
	if len(obs) <= compressedObservationPreview {
		return obs
	}

	// Tier 2a: feature flag off → byte truncation (current behavior).
	if !config.Config.LlmServerScratchpadSummarizationEnabled {
		return compressObservation(obs)
	}

	// Return cached summary if available.
	if step.CompressedObservation != "" {
		return step.CompressedObservation
	}

	// Tier 2b: too small for LLM summarization to be worth the cost/latency.
	if len(obs) < getScratchpadSummaryMinBytes() {
		result := compressObservation(obs)
		step.CompressedObservation = result
		return result
	}

	// Tier 2c: ctx required for LLM calls. Fall back if not available.
	if ctx == nil {
		result := compressObservation(obs)
		step.CompressedObservation = result
		return result
	}

	// Tier 2d: parent context already cancelled (e.g. circuit breaker tripped in
	// prewarmSummaries). Fall back immediately instead of wasting time on the LLM call.
	if ctx.GetContext() != nil && ctx.GetContext().Err() != nil {
		result := compressObservation(obs)
		step.CompressedObservation = result
		return result
	}

	// Tier 3: LLM summarization.
	return llmSummarizeObservation(ctx, step, request, obs)
}

// llmSummarizeObservation performs the actual LLM call for summarization. All guards
// should be done by the caller — this function assumes the caller has decided an LLM
// call is appropriate.
func llmSummarizeObservation(
	ctx *security.RequestContext,
	step *NBAgentPlannerToolActionStep,
	request NBAgentRequest,
	obs string,
) string {
	toolName := step.Action.Tool
	toolInput := step.Action.ToolInput
	originalLen := len(obs)
	maxLen := getScratchpadSummaryMaxLen()

	// Cap the observation sent to the summarizer. Huge payloads are middle-truncated
	// before being sent — the summarizer doesn't need more than maxObservationChars.
	summarizeInput := obs
	if len(summarizeInput) > getMaxObservationChars() {
		summarizeInput = TruncateMiddle(summarizeInput, 2048, getMaxObservationChars()-2048)
	}

	prompt := prompts_repo.GetPrompt(prompts_repo.PromptScratchpadSummarizer, toolName, toolInput, originalLen, summarizeInput)

	// Build a lite-model context with a timeout. WithoutCancel decouples this call from
	// parent cancellation — the internal timeout ensures we don't hang indefinitely.
	timeoutCtx, cancel := context.WithTimeout(
		context.WithValue(context.WithoutCancel(ctx.GetContext()), ContextKeyUseLiteModel, true),
		getScratchpadSummaryTimeout(),
	)
	defer cancel()

	summaryCtx := security.NewRequestContext(
		timeoutCtx,
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)

	t0 := time.Now()
	completion, err := GenerateAndTrackLLMContent(
		summaryCtx,
		request.UserId,
		request.AccountId,
		request.ConversationId,
		request.MessageId,
		request.AgentId,
		false,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, prompt),
		},
		false,
		llms.WithTemperature(0.0),
		WithThinkingLevel(ThinkingLevelFastTask),
	)
	latency := time.Since(t0).Seconds()

	if err != nil {
		ctx.GetLogger().Warn("scratchpad: observation summarization failed, falling back to truncation",
			"error", err,
			"tool", toolName,
			"observation_len", originalLen,
			"latency_s", latency,
		)
		MetricsScratchpadSummarization(toolName, "error", latency)
		MetricsScratchpadSummarizationFallback(toolName, "llm_error")
		result := compressObservation(obs)
		step.CompressedObservation = result
		return result
	}

	summary := ""
	if len(completion.Choices) > 0 {
		summary = completion.Choices[0].Content
	}

	if summary == "" {
		ctx.GetLogger().Warn("scratchpad: observation summarization returned empty, falling back to truncation",
			"tool", toolName,
			"observation_len", originalLen,
			"latency_s", latency,
		)
		MetricsScratchpadSummarization(toolName, "empty", latency)
		MetricsScratchpadSummarizationFallback(toolName, "empty_response")
		result := compressObservation(obs)
		step.CompressedObservation = result
		return result
	}

	// Enforce max length on the summary itself.
	if len(summary) > maxLen {
		summary = TruncateHead(summary, maxLen)
	}

	result := fmt.Sprintf("[summarized from %d chars] %s", originalLen, summary)

	// Cache for reuse across subsequent scratchpad builds.
	step.CompressedObservation = result

	ctx.GetLogger().Debug("scratchpad: observation summarized",
		"tool", toolName,
		"original_len", originalLen,
		"summary_len", len(result),
		"latency_s", latency,
	)
	MetricsScratchpadSummarization(toolName, "success", latency)

	return result
}
