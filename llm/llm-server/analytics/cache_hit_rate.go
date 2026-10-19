package analytics

import "nudgebee/llm/agents/core"

// PromptTokens and CacheWritesOutsideInputSQL mirror core's so usage code reads
// the same in both packages. See core.PromptTokens for why Anthropic and Bedrock
// add cache writes to input.
func PromptTokens(provider string, inputTokens, cacheCreationTokens int) int {
	return core.PromptTokens(provider, inputTokens, cacheCreationTokens)
}

const CacheWritesOutsideInputSQL = core.CacheWritesOutsideInputSQL
