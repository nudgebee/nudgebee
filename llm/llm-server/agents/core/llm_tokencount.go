package core

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"                        // OpenAI/GPT + fallback
	anthropic "github.com/qhenkart/anthropic-tokenizer-go" // Claude
)

var anthropicTokenizer *anthropic.Tokenizer
var modelEncodingMap = map[string]*tiktoken.Tiktoken{}
var modelEncodingMutex = &sync.RWMutex{}

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
	defaultEncodingName := "cl100k_base"

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
	}

	// final safe global default (smallest common supported window among old widely-used models)
	return 16_000
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
