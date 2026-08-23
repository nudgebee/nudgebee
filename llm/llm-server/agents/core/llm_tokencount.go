package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"                        // OpenAI/GPT + fallback
	tiktoken_loader "github.com/pkoukk/tiktoken-go-loader" // embedded BPE vocab (offline)
	anthropic "github.com/qhenkart/anthropic-tokenizer-go" // Claude
)

// oSeriesRE matches an OpenAI o-series reasoning model (o1 / o3 / o4 / a future oN)
// as a whole SEGMENT of the id.
//
// Neither a plain prefix nor a substring test works here. normalizeModel strips at
// most ONE leading vendor segment and only from a fixed list, so ids that arrive
// with a region or platform qualifier keep it — "azure.openai.o1-mini",
// "us.openai.o3" and "azure.o3-mini" all survive normalization intact, and a
// HasPrefix check silently drops them onto the 4096 floor. A plain Contains test
// has the opposite failure, capturing unrelated ids like "some-o1-lookalike".
//
// Anchoring on a segment boundary ("." "/" or start) and requiring the version to
// end the segment satisfies both. `o\d+` rather than a fixed o1/o3/o4 set, and `+`
// rather than a single digit, for the same reason Claude is keyed on generation:
// a new release — including a two-digit one — must not fall through the hole this
// table exists to close.
var oSeriesRE = regexp.MustCompile(`(^|[./])o\d+(-|$)`)

var anthropicTokenizer *anthropic.Tokenizer
var modelEncodingMap = map[string]*tiktoken.Tiktoken{}
var modelEncodingMutex = &sync.RWMutex{}

func init() {
	// Use the offline BPE loader so tiktoken resolves encodings (e.g.
	// cl100k_base) from vocab embedded in the binary instead of downloading it
	// from a remote blob store on first use. This removes a runtime network
	// dependency on cold start and makes token counting deterministic in tests
	// and air-gapped environments.
	tiktoken.SetBpeLoader(tiktoken_loader.NewOfflineLoader())
}

// InitTokenizers initializes expensive tokenizers once at startup
func InitTokenizers() error {
	var err error
	anthropicTokenizer, err = anthropic.New()
	if err != nil {
		return fmt.Errorf("failed to initialize anthropic tokenizer: %w", err)
	}
	return nil
}

func CountTokens(provider, model, text string) (int, error) {
	switch provider {
	case "openai", "azure":
		return countOpenAITokens(model, text)
	case "anthropic":
		return countAnthropicTokens(text)
	default:
		return countFallbackTokens(text)
	}
}

func countOpenAITokens(model, text string) (int, error) {
	modelEncodingMutex.RLock()
	enc, ok := modelEncodingMap[model]
	modelEncodingMutex.RUnlock()
	if ok {
		return len(enc.Encode(text, nil, nil)), nil
	}

	// Not in map, so create it. This is slow, so do it outside the lock.
	newEnc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// If we can't get a specific encoder, fall back.
		// The fallback also uses the map, so no lock should be held.
		return countFallbackTokens(text)
	}

	// Got a new encoder, now get a write lock to add it to the map.
	modelEncodingMutex.Lock()
	defer modelEncodingMutex.Unlock()

	// Double-check in case another goroutine created it while we were creating ours.
	// The one in the map should be preferred to avoid replacing it unnecessarily.
	if enc, ok := modelEncodingMap[model]; ok {
		return len(enc.Encode(text, nil, nil)), nil
	}

	// It's still not there, so add the one we created.
	modelEncodingMap[model] = newEnc
	return len(newEnc.Encode(text, nil, nil)), nil
}

func countAnthropicTokens(text string) (int, error) {
	if anthropicTokenizer == nil {
		return 0, fmt.Errorf("anthropic tokenizer not initialized, call InitTokenizers first")
	}
	// Tokens() already returns int
	return anthropicTokenizer.Tokens(text), nil
}

func countFallbackTokens(text string) (int, error) {
	// o200k_base (GPT-4o / o-series vocab) is a closer approximation than the
	// older cl100k_base for modern non-OpenAI models (Gemini, Qwen, Llama-3+),
	// which keeps the token-budget gates that drive summarization recovery from
	// undercounting. The vocab ships offline in tiktoken-go-loader's assets.
	defaultEncodingName := "o200k_base"

	modelEncodingMutex.RLock()
	enc, ok := modelEncodingMap[defaultEncodingName]
	modelEncodingMutex.RUnlock()
	if ok {
		return len(enc.Encode(text, nil, nil)), nil
	}

	// Not in map, create it.
	newEnc, err := tiktoken.GetEncoding(defaultEncodingName)
	if err != nil {
		return 0, err // Can't do much if the default fails.
	}

	// Got the encoder, now lock and add it.
	modelEncodingMutex.Lock()
	defer modelEncodingMutex.Unlock()

	// Double-check
	if enc, ok := modelEncodingMap[defaultEncodingName]; ok {
		return len(enc.Encode(text, nil, nil)), nil
	}

	modelEncodingMap[defaultEncodingName] = newEnc
	return len(newEnc.Encode(text, nil, nil)), nil
}

// GetLlmMaxTokenLength returns a safe max token length for common/famous models.
// Add new models in the obvious places or extend the substring checks.
func GetLlmMaxTokenLength(model string) int {
	n := normalizeModel(model)

	// small / exact-known legacy OpenAI models
	switch n {
	case "gpt-3.5-turbo-0301", "gpt-3.5-turbo-0613":
		return 4_096
	case "gpt-3.5-turbo-16k-0613", "gpt-3.5-turbo-1106":
		return 16_384
	case "gpt-4-0314", "gpt-4-0613":
		return 8_192
	case "gpt-4-32k-0314", "gpt-4-32k-0613":
		return 32_768
	}

	// substring / family-based fallbacks (covers platform variants)
	switch {
	// OpenAI newer families
	case strings.Contains(n, "gpt-4.1"):
		// GPT-4.1 / GPT-4.1-mini / GPT-4.1-nano → up to ~1,000,000 tokens
		return 1_000_000
	case strings.Contains(n, "o3") || strings.Contains(n, "o4-mini"):
		// OpenAI reasoning models (o3 / o4-mini) → ~200,000 tokens
		return 200_000

	// Anthropic Claude family (Opus / Sonnet long-context)
	case strings.Contains(n, "claude-opus-4-1") || strings.Contains(n, "claude-opus-4") || strings.Contains(n, "claude-sonnet-4"):
		return 200_000
	case strings.Contains(n, "claude"):
		return 100_000

	// Amazon Titan (Bedrock)
	case strings.Contains(n, "titan-text-premier") || strings.Contains(n, "amazon-titan-text-premier"):
		return 32_768
	case strings.Contains(n, "titan-text-express"):
		return 8_192

	// Meta LLaMA 4 family
	case strings.Contains(n, "llama-4-scout") || strings.Contains(n, "scout"):
		// Llama 4 Scout → very large context (up to ~10,000,000 tokens per Meta announcements)
		return 10_000_000
	case strings.Contains(n, "llama-4-maverick") || strings.Contains(n, "maverick"):
		// Llama 4 Maverick → up to ~1,000,000 tokens (common published value)
		return 1_000_000

	// LLaMA 3 family (legacy values)
	case strings.Contains(n, "llama3-1-70b") || strings.Contains(n, "llama3-1-70b-instruct"):
		return 131_072
	case strings.Contains(n, "llama3-70b") || strings.Contains(n, "llama3-8b"):
		return 8_192

	// Google Gemini / Gemma
	case strings.Contains(n, "gemini-3-pro"):
		return 2_000_000 // Gemini 3 Pro → 2M tokens

	case strings.Contains(n, "gemini-3-flash"):
		return 1_000_000 // Gemini 3 Flash → 1M tokens

	case strings.Contains(n, "gemini-3"):
		// Generic gemini-3 fallback
		return 1_000_000

	case strings.Contains(n, "gemini-1.5-pro"):
		return 2_000_000 // Gemini 1.5 Pro → 2M tokens

	case strings.Contains(n, "gemini-1.5-flash"):
		return 1_000_000 // Gemini 1.5 Flash → 1M tokens

	case strings.Contains(n, "gemini-2.0-pro"):
		return 2_000_000 // Gemini 2.0 Pro → 2M tokens

	case strings.Contains(n, "gemini-2.0-flash"):
		return 1_048_576 // Gemini 2.0 Flash → 1,048,576 tokens (2^20)

	case strings.Contains(n, "gemini-2.5-pro") || strings.Contains(n, "gemini-2-5-pro"):
		return 1_000_000 // Gemini 2.5 Pro → 1M tokens (will be 2M soon)

	case strings.Contains(n, "gemini-2.5-flash") || strings.Contains(n, "gemini-2-5-flash"):
		return 1_000_000 // Gemini 2.5 Flash → 1M tokens

	// Gemma open models
	case strings.Contains(n, "gemma-3"):
		// Gemma 3 family supports ~128K token contexts for 4B/12B/27B variants
		return 131_072
	case strings.Contains(n, "gemma"):
		// older gemma family (small) → fall back to 8K
		return 8_192

	// Qwen open models (self-hosted, e.g. Qwen3 on vLLM)
	case strings.Contains(n, "qwen"):
		// Qwen3 family supports up to a 256K token context
		return 262_144
	}

	// final safe global default
	return 32_000
}

// GetLlmMaxOutputTokens returns the maximum output tokens for a given model.
// This is useful for setting the MaxTokens option to avoid small chunks and excessive looping.
func GetLlmMaxOutputTokens(model string) int {
	n := normalizeModel(model)

	switch {
	case strings.Contains(n, "gemini-3"):
		// Gemini 3 Flash/Pro support up to 65k output tokens.
		return 65536
	case strings.Contains(n, "gemini-2.5") || strings.Contains(n, "gemini-2-5"):
		// Gemini 2.5 supports up to 65k output tokens (important for thinking tokens).
		return 65536
	case strings.Contains(n, "gemini"):
		// Gemini 1.5/2.0 standard is ~8k.
		return 8192
	case strings.Contains(n, "claude-3-5"):
		// Claude 3.5 Sonnet specifically supports 8k now.
		return 8192
	case strings.Contains(n, "claude-3"):
		return 4096
	case strings.Contains(n, "claude"):
		// Claude 4.x and newer. Keyed on GENERATION rather than individual model
		// ids so a new point release cannot silently fall through to the caller's
		// floor — which is exactly how every Claude 4.x model ended up capped at
		// 4096 while its real ceiling was 128k.
		return anthropicMaxOutputTokens(n)
	case strings.Contains(n, "gpt-5"):
		// GPT-5 family documents a 128k output ceiling; held at half, as for Claude 4.6+.
		return 65536
	case oSeriesRE.MatchString(n):
		// OpenAI o-series reasoning models document a 100k output ceiling.
		return 65536
	case strings.Contains(n, "gpt-4o"):
		// GPT-4o supports up to 16k output tokens.
		return 16384
	case strings.Contains(n, "gpt-4"):
		return 4096
	case strings.Contains(n, "llama-3") || strings.Contains(n, "llama3"):
		return 8192
	case strings.Contains(n, "deepseek"):
		return 8192
	}

	return 0 // Unknown or let provider decide default
}

// anthropicMaxOutputTokens returns the output-token ceiling for a Claude 4.x-or-newer
// model, parsed from the model id rather than enumerated, so a new point release
// cannot silently fall through to the caller's floor.
//
// Values are deliberately at or BELOW each generation's documented ceiling:
// over-requesting max_tokens is a hard 400 from the provider, whereas
// under-requesting only costs headroom. 65536 is half of the 128k the 4.6+ and 5.x
// families document — ample for our longest observed response while still bounding
// a runaway generation.
//
// Returns 0 for anything it cannot place, which leaves the caller's existing floor
// in charge rather than guessing a ceiling for an unknown model.
func anthropicMaxOutputTokens(normalized string) int {
	major, minor, ok := anthropicGenerationForOutputCap(normalized)
	if !ok || major < 4 {
		return 0
	}
	if major > 4 || minor >= 6 {
		// Opus 4.6/4.7/4.8, Sonnet 4.6, and the 5 family document a 128k ceiling.
		return 65536
	}
	// Opus 4/4.1 cap at 32k — stay at the lowest ceiling in the 4.0-4.5 band so one
	// value is safe for every model in it.
	return 32000
}

// anthropicGenerationForOutputCap extracts major/minor from a Claude model id, across
// bare, vendor-prefixed and Bedrock shapes ("claude-sonnet-4-6", "anthropic/claude-...",
// "us.anthropic.claude-...-v1:0").
//
// The minor is bounded to two digits so a date suffix cannot be read as one:
// "claude-sonnet-4-20250514" is generation 4 with no minor, not 4.20250514. A missing
// minor reads as 0, so "claude-sonnet-5" is 5.0.
//
// NOTE (prod cherry-pick): upstream #36449 calls anthropicGeneration in
// thinking_capability.go, which is part of #36320 and has not been promoted to prod.
// This is a scoped local copy under a distinct name so the two cannot collide when
// #36320 lands here; fold it back into the shared parser at that point.
var (
	outputCapFamilyFirstRE  = regexp.MustCompile(`claude-[a-z]+-(\d+)(?:[-.](\d{1,2})\b)?`)
	outputCapVersionFirstRE = regexp.MustCompile(`claude-(\d+)[-.](\d{1,2})\b`)
)

func anthropicGenerationForOutputCap(m string) (major, minor int, ok bool) {
	// "claude-3-7-sonnet" puts the version first; check it before the family-first form,
	// whose [a-z]+ would otherwise fail to match the digits.
	if g := outputCapVersionFirstRE.FindStringSubmatch(m); g != nil {
		major, _ = strconv.Atoi(g[1])
		minor, _ = strconv.Atoi(g[2])
		return major, minor, true
	}
	if g := outputCapFamilyFirstRE.FindStringSubmatch(m); g != nil {
		major, _ = strconv.Atoi(g[1])
		if g[2] != "" {
			minor, _ = strconv.Atoi(g[2])
		}
		return major, minor, true
	}
	return 0, 0, false
}

// GetLlmDefaultThinkingLevel returns the default thinking level for a model.
// Returns "" for non-thinking models or models that use thinkingBudget instead (caller should skip ThinkingConfig).
func GetLlmDefaultThinkingLevel(model string) string {
	n := normalizeModel(model)
	switch {
	case strings.Contains(n, "gemini-2.5") || strings.Contains(n, "gemini-2-5"):
		return ""
	case strings.Contains(n, "gemini-3") && strings.Contains(n, "pro") && !strings.Contains(n, "3.1"):
		return "low"
	case strings.Contains(n, "gemini-3"):
		return "medium"
	}
	return ""
}

// ClampThinkingLevelForModel ensures the requested thinking level is supported by the model.
// Returns "none" for models that do not support thinking at all (caller should clear ThinkingConfig).
// Returns "low" for models that support thinking but not "minimal" (e.g. gemini-3.1-pro-preview).
// Returns the requested level unchanged if no clamping is needed.
func ClampThinkingLevelForModel(model, level string) string {
	n := normalizeModel(model)
	// flash-lite models do not support thinking at all — clear any thinking config.
	if strings.Contains(n, "flash-lite") || strings.Contains(n, "flashlite") {
		return "none"
	}
	if level != "minimal" {
		return level
	}
	// gemini-3 Pro variants require at least "low"; flash variants accept "minimal".
	if strings.Contains(n, "gemini-3") && !strings.Contains(n, "flash") {
		return "low"
	}
	if strings.Contains(n, "gemini-2.5-pro") || strings.Contains(n, "gemini-2-5-pro") {
		return "low"
	}
	return level
}

// GetLlmMinCacheTokens returns the minimum number of tokens required to create a
// cached content entry for the given model. Values sourced from Google AI documentation.
// Models not listed here do not support context caching.
func GetLlmMinCacheTokens(model string) int {
	n := normalizeModel(model)

	switch {
	// Gemini 2.5 Pro requires 4,096 tokens minimum
	case strings.Contains(n, "gemini-2.5-pro") || strings.Contains(n, "gemini-2-5-pro"):
		return 4_096
	// Gemini 2.5 Flash requires 1,024 tokens minimum
	case strings.Contains(n, "gemini-2.5-flash") || strings.Contains(n, "gemini-2-5-flash"):
		return 1_024
	// Gemini 2.0 Flash requires 1,024 tokens minimum
	case strings.Contains(n, "gemini-2.0-flash") || strings.Contains(n, "gemini-2-0-flash"):
		return 1_024
	// Gemini 1.5 Pro/Flash require 32,768 tokens minimum
	case strings.Contains(n, "gemini-1.5"):
		return 32_768
	// Gemini 3 Flash requires 1,024 tokens minimum (same as 2.x Flash family)
	case strings.Contains(n, "gemini-3-flash") || strings.Contains(n, "gemini-3.0-flash"):
		return 1_024
	// Gemini 3 Pro / other Gemini 3 variants — use 4,096
	case strings.Contains(n, "gemini-3"):
		return 4_096
	}

	// 0 means caching is not supported / unknown for this model
	return 0
}
