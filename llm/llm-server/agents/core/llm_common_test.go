package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestHuggingFaceLLM(t *testing.T) {
	model, err := GetLlmModelWithProvider("huggingface", "", false, "", "")
	if err != nil {
		t.Skip("Skipping HuggingFace test: ", err)
	}
	assert.NotNil(t, model)

	respone, err := model.GenerateContent(context.Background(), []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "generate prometheus query for top 5 pods with max memory")})
	assert.Nil(t, err)
	assert.NotNil(t, respone)
	assert.True(t, len(respone.Choices) > 0)
	assert.True(t, len(respone.Choices[0].Content) > 0)
}

func TestSanatizeMarkdownCodeBlock(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Standard markdown code block with language",
			input:    "```go\npackage main\n\nfunc main() {}\n```",
			expected: "package main\n\nfunc main() {}",
		},
		{
			name:     "Markdown code block without language",
			input:    "```\nSome text\n```",
			expected: "Some text",
		},
		{
			name:     "String with only triple backticks",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: "{\"key\": \"value\"}",
		},
		{
			name:     "String with single backticks",
			input:    "`inline code`",
			expected: "inline code",
		},
		{
			name:     "String with leading/trailing whitespace and markdown",
			input:    "  ```python\nprint('hello')\n```  ",
			expected: "print('hello')",
		},
		{
			name:     "String with no markdown",
			input:    "Just a regular string.",
			expected: "Just a regular string.",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "String with mixed backticks",
			input:    "```text\nThis is `inline` within a block\n```",
			expected: "This is `inline` within a block",
		},
		{
			name:     "String starting with single backtick",
			input:    "`start`",
			expected: "start",
		},
		{
			name:     "String ending with single backtick",
			input:    "end`",
			expected: "end`",
		},
		{
			name:     "within markdown quotes",
			input:    "```markdown\nend\n```\n\n",
			expected: "end",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := SanatizeMarkdownCodeBlock(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

// TestIsTokenLimitError exercises the boundary between real prompt-overflow
// errors (which should route to handleTokenLimitError) and other 4xx-class
// failures that must NOT match — over-broad substring matches like bare
// "too large" or "400" + "token" send non-token-limit errors into the
// summarization handler, which then early-exits with a misleading
// internal-error response.
func TestIsTokenLimitError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// Genuine token-limit / context-window errors.
		{"openai context length", errors.New("This model's maximum context length is 8192 tokens"), true},
		{"anthropic prompt too long", errors.New("400 prompt is too long: 250000 tokens"), true},
		{"bedrock input is too long", errors.New("ValidationException: Input is too long for requested model"), true},
		{"bedrock input too large", errors.New("Input is too large for requested model"), true},
		{"explicit context window phrase", errors.New("request exceeds the model's context window"), true},
		{"too many tokens phrase", errors.New("the request has too many tokens"), true},
		{"openai 400 token limit", errors.New("400 Bad Request: token limit exceeded"), true},

		// Should NOT classify as token-limit.
		{"http 413 payload too large", errors.New("413: request body too large"), false},
		{"bedrock validation 400 invalid token format", errors.New("400 ValidationException: Invalid bearer token format"), false},
		{"throttling exception", errors.New("ThrottlingException: rate exceeded"), false},
		{"bedrock service unavailable", errors.New("ServiceUnavailableException: service temporarily unavailable"), false},
		{"empty error", nil, false},
		{"bare 400 with no qualifier", errors.New("400 Bad Request"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTokenLimitError(tc.err))
		})
	}
}

// TestIsTokenLimitError_PreservesWrappedChain ensures that when
// handleTokenLimitError wraps the underlying provider error with %w, the
// wrapped chain still classifies correctly — important so retried/wrapped
// errors aren't misrouted on a second pass.
func TestIsTokenLimitError_PreservesWrappedChain(t *testing.T) {
	inner := errors.New("Input is too large for requested model")
	wrapped := fmt.Errorf("failed to handle token limit error after 1 iterations: %w", inner)
	assert.True(t, isTokenLimitError(wrapped))
}

// TestLargestTextMessageIndex covers the helper used by the summarization
// fallback when no single message exceeds the per-message threshold but the
// total prompt still overflows.
func TestLargestTextMessageIndex(t *testing.T) {
	t.Run("returns largest text message", func(t *testing.T) {
		msgs := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("a", 100)}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("b", 50)}}},
			{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: strings.Repeat("c", 200)}}},
		}
		idx, tokens := largestTextMessageIndex(msgs, []int{100, 50, 200})
		assert.Equal(t, 2, idx)
		assert.Equal(t, 200, tokens)
	})

	t.Run("skips non-text parts", func(t *testing.T) {
		msgs := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "small"}}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.ToolCall{ID: "t1"}}},
		}
		idx, _ := largestTextMessageIndex(msgs, []int{10, 9999})
		assert.Equal(t, 0, idx, "tool-call message must not be picked even though it has the higher token count")
	})

	t.Run("returns -1 when no text messages", func(t *testing.T) {
		msgs := []llms.MessageContent{
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.ToolCall{ID: "t1"}}},
		}
		idx, tokens := largestTextMessageIndex(msgs, []int{100})
		assert.Equal(t, -1, idx)
		assert.Equal(t, -1, tokens)
	})

	t.Run("handles empty parts slice", func(t *testing.T) {
		msgs := []llms.MessageContent{
			{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{}},
			{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "ok"}}},
		}
		idx, _ := largestTextMessageIndex(msgs, []int{0, 5})
		assert.Equal(t, 1, idx)
	})
}

func TestClassifyUserFacingLLMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error // nil means "no known class — fall back to generic"
	}{
		{"nil error", nil, nil},
		{"quota exceeded", errors.New("googleai: quota exceeded for model"), ErrLLMRateLimited},
		{"http 429", errors.New("provider returned status 429 Too Many Requests"), ErrLLMRateLimited},
		{"rate limit", errors.New("rate limit reached"), ErrLLMRateLimited},
		// Real prod error: Bedrock 429 throttling wrapped as a "token limit
		// error". Must classify as rate-limited, not request-too-large, even
		// though the string contains "token limit"/"too many tokens".
		{"bedrock 429 throttle wrapped as token limit", errors.New("failed to handle token limit error after 1 iterations: operation error Bedrock Runtime: Converse, https response error StatusCode: 429, RequestID: abc, ThrottlingException: Too many tokens, please wait before trying again.\nfailed to get rate limit token, retry quota exceeded, 4 available, 5 requested"), ErrLLMRateLimited},
		{"token limit", errors.New("token limit exceeded: context length too long"), ErrLLMRequestTooLarge},
		{"genuine context overflow", errors.New("input is too long for the requested model"), ErrLLMRequestTooLarge},
		{"transient timeout", errors.New("request timed out"), ErrLLMServiceUnavailable},
		{"transient 503", errors.New("upstream returned 503"), ErrLLMServiceUnavailable},
		{"unknown error", errors.New("some unexpected parse failure"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyUserFacingLLMError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestUserFacingLLMErrors_CarryHTTPCodes locks in the standard HTTP status
// codes so they aren't accidentally changed — upstream layers branch on them.
func TestUserFacingLLMErrors_CarryHTTPCodes(t *testing.T) {
	assert.Equal(t, 429, ErrLLMRateLimited.Code)
	assert.Equal(t, 413, ErrLLMRequestTooLarge.Code)
	assert.Equal(t, 503, ErrLLMServiceUnavailable.Code)
	// Message must be user-safe (the Error() value is what reaches the UI).
	assert.Contains(t, ErrLLMRateLimited.Error(), "capacity")
}

// Pins the provider-conditional handling of cache-read tokens inside
// getDetailedTokenInfo. Anthropic and Google AI disagree on whether
// "input_tokens" already includes cached tokens; the addition on the
// non-cached-includes-cache branch is correct only for the Anthropic
// convention. Without this test a well-meaning refactor could re-introduce
// the ~2× Gemini double-count that inflated cost + cache-hit metrics.
func TestGetDetailedTokenInfo_CacheReadHandling_ProviderConventions(t *testing.T) {
	tests := []struct {
		name            string
		generationInfo  map[string]any
		wantInputTokens int
		wantCacheRead   int
	}{
		{
			name: "anthropic: InputTokens is fresh-only, must add cache_read",
			generationInfo: map[string]any{
				"input_tokens":         1500, // fresh only per Anthropic
				"output_tokens":        100,
				"CacheReadInputTokens": 20000,
			},
			wantInputTokens: 21500, // fresh(1500) + cache(20000)
			wantCacheRead:   20000,
		},
		{
			name: "google ai: PromptTokenCount already includes cache, must NOT add",
			generationInfo: map[string]any{
				"input_tokens":         21709, // total incl. cache per Gemini docs
				"output_tokens":        68,
				"CachedTokens":         20416,
				"CacheReadInputTokens": 20416,
				"NonCachedInputTokens": 1293, // marker: presence means "already includes cache"
			},
			wantInputTokens: 21709, // stays as-is; NOT 42125
			wantCacheRead:   20416,
		},
		{
			name: "no cache active: additive path is a no-op",
			generationInfo: map[string]any{
				"input_tokens":  5000,
				"output_tokens": 200,
			},
			wantInputTokens: 5000,
			wantCacheRead:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &llms.ContentResponse{
				Choices: []*llms.ContentChoice{{GenerationInfo: tc.generationInfo}},
			}
			info := getDetailedTokenInfo(resp, nil)
			assert.Equal(t, tc.wantInputTokens, info.InputTokens,
				"InputTokens must equal fresh+cache; check the provider-conditional add in getDetailedTokenInfo")
			assert.Equal(t, tc.wantCacheRead, info.CacheReadTokens)
		})
	}
}
