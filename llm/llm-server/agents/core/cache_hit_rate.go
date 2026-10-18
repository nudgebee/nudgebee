package core

import "strings"

// providersWithCacheWritesOutsideInput are the providers whose recorded
// input_tokens leave out the tokens written to the prompt cache. Anthropic
// (direct or through Bedrock) reports fresh input, cache reads and cache writes
// as three separate counts, and getDetailedTokenInfo adds only the reads back
// into input. Google AI and OpenAI count every prompt token in input already
// (Gemini's explicit-cache writes are inside PromptTokenCount), so adding their
// cache writes again would count them twice.
var providersWithCacheWritesOutsideInput = map[string]bool{
	"anthropic": true,
	"bedrock":   true,
}

// promptTokens is the whole prompt a call (or a sum of calls from one provider)
// sent: the denominator of a cache hit rate. Stored input_tokens is left as it
// is because cost formulas price input − cache reads and cache writes apart.
func promptTokens(provider string, inputTokens, cacheCreationTokens int) int {
	if providersWithCacheWritesOutsideInput[strings.ToLower(strings.TrimSpace(provider))] {
		return inputTokens + cacheCreationTokens
	}
	return inputTokens
}

// cacheWritesOutsideInputSQL sums, in SQL, the cache writes that promptTokens
// adds for the same providers. Kept next to the map so the two lists stay one.
const cacheWritesOutsideInputSQL = `COALESCE(SUM(CASE WHEN lower(t.llm_provider) IN ('anthropic', 'bedrock') THEN COALESCE(t.cache_creation_tokens, 0) ELSE 0 END), 0)`
