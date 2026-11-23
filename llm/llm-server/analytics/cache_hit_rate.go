package analytics

import (
	llmruntime "nudgebee/llm/llms/runtime"
)

// PromptTokens and CacheWritesOutsideInputSQL mirror llmruntime's so usage code reads
// the same in both packages. See llmruntime.PromptTokens for why Anthropic and Bedrock
// add cache writes to input.
func PromptTokens(provider string, inputTokens, cacheCreationTokens int) int {
	return llmruntime.PromptTokens(provider, inputTokens, cacheCreationTokens)
}

const CacheWritesOutsideInputSQL = llmruntime.CacheWritesOutsideInputSQL
