package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"testing"

	"nudgebee/llm/config"

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

// Every provider must have its usage keys read, because a provider whose naming
// nobody reads records zero tokens and therefore zero cost — silently, with no
// error anywhere. That is how provider=openai logged 89 calls at zero.
//
// The clients use three different conventions for the same two numbers:
//
//	InputTokens/OutputTokens         huggingface (in-house), anthropic
//	input_tokens/output_tokens       bedrock, googleai, vertexai, vertexai_endpoint
//	PromptTokens/CompletionTokens    azure (in-house), openai, custom
//
// Each case below uses the keys that provider's client actually emits, so adding
// a provider without wiring its naming fails here rather than in the cost report.
func TestGetDetailedTokenInfo_ReadsEveryProviderUsageNaming(t *testing.T) {
	tests := []struct {
		provider       string
		generationInfo map[string]any
		wantInput      int
		wantOutput     int
	}{
		{
			provider:       "huggingface (in-house)",
			generationInfo: map[string]any{"InputTokens": 1000, "OutputTokens": 500},
			wantInput:      1000, wantOutput: 500,
		},
		{
			provider:       "anthropic",
			generationInfo: map[string]any{"input_tokens": 1000, "output_tokens": 500},
			wantInput:      1000, wantOutput: 500,
		},
		{
			provider:       "bedrock",
			generationInfo: map[string]any{"input_tokens": 1000, "output_tokens": 500},
			wantInput:      1000, wantOutput: 500,
		},
		{
			// googleai emits both conventions; input_tokens is checked first and
			// is the one carrying its cache-inclusive semantics.
			provider: "googleai",
			generationInfo: map[string]any{
				"input_tokens": 1000, "output_tokens": 500,
				"PromptTokens": 1000, "CompletionTokens": 500,
				"NonCachedInputTokens": 1000,
			},
			wantInput: 1000, wantOutput: 500,
		},
		{
			provider:       "vertexai_endpoint",
			generationInfo: map[string]any{"input_tokens": 1000, "output_tokens": 500},
			wantInput:      1000, wantOutput: 500,
		},
		{
			provider:       "azure (in-house)",
			generationInfo: map[string]any{"PromptTokens": 1000, "CompletionTokens": 500},
			wantInput:      1000, wantOutput: 500,
		},
		{
			provider:       "openai / custom (langchaingo)",
			generationInfo: map[string]any{"PromptTokens": 1000, "CompletionTokens": 500, "TotalTokens": 1500},
			wantInput:      1000, wantOutput: 500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			resp := &llms.ContentResponse{
				Choices: []*llms.ContentChoice{{GenerationInfo: tc.generationInfo}},
			}
			info := getDetailedTokenInfo(resp, nil)
			assert.Equal(t, tc.wantInput, info.InputTokens, "input tokens unread for %s", tc.provider)
			assert.Equal(t, tc.wantOutput, info.OutputTokens, "output tokens unread for %s", tc.provider)
		})
	}
}

// OpenAI reports cached tokens as a SUBSET of prompt_tokens, the same convention
// Google AI uses — but langchaingo's OpenAI client emits no NonCachedInputTokens
// marker, so the marker check alone would let the additive branch fire and inflate
// input ~2x on every cache hit. That is the Gemini double-count, reintroduced for
// openai/azure/custom. InputTokens must stay exactly prompt_tokens.
func TestGetDetailedTokenInfo_OpenAICachedTokensAreNotDoubleCounted(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{GenerationInfo: map[string]any{
		"PromptTokens":       10000, // total, already includes the 8000 cached
		"CompletionTokens":   500,
		"PromptCachedTokens": 8000,
	}}}}

	info := getDetailedTokenInfo(resp, nil)

	assert.Equal(t, 10000, info.InputTokens, "prompt_tokens already includes cached; must NOT become 18000")
	assert.Equal(t, 8000, info.CacheReadTokens, "cache reads must be surfaced so the cached rate applies")
	assert.Equal(t, 500, info.OutputTokens)
	// OpenAI bills no separate cache write, unlike Anthropic's 1.25x creation rate.
	assert.Equal(t, 0, info.CacheCreationTokens)
}

// The Anthropic convention must be untouched by the OpenAI branch: there
// input_tokens is the fresh portion only, so cache reads still get added.
func TestGetDetailedTokenInfo_AnthropicStillAddsCacheRead(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{GenerationInfo: map[string]any{
		"input_tokens":         1500, // fresh only
		"output_tokens":        100,
		"CacheReadInputTokens": 20000,
	}}}}

	info := getDetailedTokenInfo(resp, nil)

	assert.Equal(t, 21500, info.InputTokens, "Anthropic input_tokens is fresh-only; total = fresh + cache_read")
	assert.Equal(t, 20000, info.CacheReadTokens)
}

// A provider-specific cache field must win over the generic OpenAI one when both
// somehow appear, so the more precise number is the one billed.
func TestGetDetailedTokenInfo_ProviderCacheFieldBeatsOpenAIField(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{GenerationInfo: map[string]any{
		"PromptTokens":         10000,
		"CompletionTokens":     100,
		"CacheReadInputTokens": 7000,
		"PromptCachedTokens":   9999,
	}}}}

	info := getDetailedTokenInfo(resp, nil)

	assert.Equal(t, 7000, info.CacheReadTokens)
}

// Precedence is load-bearing where a client emits more than one convention:
// the in-house naming carries the cache semantics getDetailedTokenInfo relies on.
func TestGetDetailedTokenInfo_InHouseNamingWinsOverOpenAI(t *testing.T) {
	resp := &llms.ContentResponse{Choices: []*llms.ContentChoice{{GenerationInfo: map[string]any{
		"InputTokens": 111, "OutputTokens": 222,
		"PromptTokens": 999, "CompletionTokens": 999,
	}}}}
	info := getDetailedTokenInfo(resp, nil)
	assert.Equal(t, 111, info.InputTokens)
	assert.Equal(t, 222, info.OutputTokens)
}

// TestResolveThinkingBudget guards the per-ModelTier thinking-token cap and its
// global-override precedence.
func TestResolveThinkingBudget(t *testing.T) {
	origGlobal := config.Config.LlmProviderThinkingBudget
	origReasoning := config.Config.LlmThinkingBudgetReasoning
	origRetrieval := config.Config.LlmThinkingBudgetRetrieval
	origSummary := config.Config.LlmThinkingBudgetSummary
	t.Cleanup(func() {
		config.Config.LlmProviderThinkingBudget = origGlobal
		config.Config.LlmThinkingBudgetReasoning = origReasoning
		config.Config.LlmThinkingBudgetRetrieval = origRetrieval
		config.Config.LlmThinkingBudgetSummary = origSummary
	})

	config.Config.LlmThinkingBudgetReasoning = 16000
	config.Config.LlmThinkingBudgetRetrieval = 8000
	config.Config.LlmThinkingBudgetSummary = 4000

	tests := []struct {
		name         string
		globalBudget int
		tier         ModelTier
		want         int
	}{
		{"reasoning tier uses its own budget", -1, ModelTierReasoning, 16000},
		{"retrieval tier uses its own budget", -1, ModelTierRetrieval, 8000},
		{"summary tier uses its own budget", -1, ModelTierSummary, 4000},
		{"empty tier falls back to retrieval (matches agentModelCategory default)", -1, "", 8000},
		{"unrecognized tier falls back to retrieval", -1, ModelTier("bogus"), 8000},
		{"global override wins over reasoning tier", 5000, ModelTierReasoning, 5000},
		{"global override wins over retrieval tier", 5000, ModelTierRetrieval, 5000},
		{"global override of 0 is respected — disables thinking, not treated as unset", 0, ModelTierReasoning, 0},
		{"negative global override does not override", -1, ModelTierReasoning, 16000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config.Config.LlmProviderThinkingBudget = tc.globalBudget
			got := resolveThinkingBudget(tc.tier)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveThinkingBudget_TierUncapped guards the tier-scoped "0" semantic: a
// per-tier budget of 0 means that tier is uncapped (-1), unlike the global
// override's 0, which means "disable thinking".
func TestResolveThinkingBudget_TierUncapped(t *testing.T) {
	origGlobal := config.Config.LlmProviderThinkingBudget
	origReasoning := config.Config.LlmThinkingBudgetReasoning
	t.Cleanup(func() {
		config.Config.LlmProviderThinkingBudget = origGlobal
		config.Config.LlmThinkingBudgetReasoning = origReasoning
	})

	config.Config.LlmProviderThinkingBudget = -1 // no global override
	config.Config.LlmThinkingBudgetReasoning = 0 // this tier deliberately left uncapped

	got := resolveThinkingBudget(ModelTierReasoning)
	assert.Equal(t, -1, got, "a tier budget of 0 (uncapped) must resolve to -1 (no override), not 0 (which would mean 'disable thinking')")
}

// optionCapturingLLMModel records the fully resolved CallOptions so a test can
// assert on exactly what got sent.
type optionCapturingLLMModel struct {
	lastOptions llms.CallOptions
}

func (m *optionCapturingLLMModel) GenerateContent(_ context.Context, _ []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	combined := llms.CallOptions{}
	for _, opt := range options {
		opt(&combined)
	}
	m.lastOptions = combined
	return newFakeContentResponse("ok", "stop"), nil
}

func (m *optionCapturingLLMModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	resp, err := m.GenerateContent(ctx, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, options...)
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Content, nil
}

// TestThinkingBudget_AppliedForCallerSetExplicitLevel pins a DELIBERATE
// REVERSAL of the previous contract.
//
// This test used to assert the opposite — that an explicit caller-set
// ThinkingLevel suppressed the budget entirely, on the reasoning that "only
// auto-defaulted calls should get capped". Production showed that leaves the
// fast-task paths (scratchpad summarizer, memory extract/vote/rerank,
// classification, acknowledgment, image utils) completely unbounded: measured
// on gemini-3.5-flash, such calls ran to ~62.9k thinking tokens — the model's
// own ceiling — for 250-300s, several emitting a single output token. The
// scratchpad summarizer asking for ThinkingLevelFastTask ("think as little as
// possible") was the call that thought the most.
//
// A level is only a hint the provider may exceed, so it is now paired with a
// numeric ceiling derived from that level (resolveThinkingBudgetForLevel).
// Sending both is safe: googleai forwards only the budget when both are
// present, because Gemini rejects a request carrying both.
func TestThinkingBudget_AppliedForCallerSetExplicitLevel(t *testing.T) {
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	origReasoning := config.Config.LlmThinkingBudgetReasoning
	origNative := config.Config.LlmThinkingLevelNativeEnabled
	config.Config.LlmProvider = "googleai"
	config.Config.LlmModel = "gemini-3-flash-preview"
	config.Config.LlmThinkingBudgetReasoning = 16000
	t.Cleanup(func() {
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
		config.Config.LlmThinkingBudgetReasoning = origReasoning
		config.Config.LlmThinkingLevelNativeEnabled = origNative
	})

	messages := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "summarize this")}

	// Native level ON (the default): Gemini 3 receives its level and NO budget, because
	// googleai suppresses the level whenever a budget is present. Sending the budget is
	// what kept every Gemini 3 call on the parameter Google documents as back-compat.
	t.Run("gemini 3: level reaches the wire, budget withheld", func(t *testing.T) {
		config.Config.LlmThinkingLevelNativeEnabled = true
		fake := &optionCapturingLLMModel{}
		withFakeLLMModel(t, fake)
		_, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "summary_agent", false, messages, true, WithThinkingLevel(ThinkingLevelFastTask))
		assert.NoError(t, err)
		assert.Nil(t, fake.lastOptions.Metadata["ThinkingBudget"],
			"a budget would suppress the level in googleai, which is the whole defect")
		assert.Equal(t, ThinkingLevelFastTask, fake.lastOptions.Metadata["ThinkingLevel"],
			"the caller's level must survive to the wire")
	})

	t.Run("gemini 3 auto path: level applied, no budget", func(t *testing.T) {
		config.Config.LlmThinkingLevelNativeEnabled = true
		fake := &optionCapturingLLMModel{}
		withFakeLLMModel(t, fake)
		_, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "logs", false, messages, true)
		assert.NoError(t, err)
		assert.Nil(t, fake.lastOptions.Metadata["ThinkingBudget"])
		assert.NotEmpty(t, fake.lastOptions.Metadata["ThinkingLevel"])
	})

	// Flag off restores the legacy pairing exactly, so rollback is a config flip.
	t.Run("flag off: legacy budget pairing restored", func(t *testing.T) {
		config.Config.LlmThinkingLevelNativeEnabled = false
		fake := &optionCapturingLLMModel{}
		withFakeLLMModel(t, fake)
		_, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "summary_agent", false, messages, true, WithThinkingLevel(ThinkingLevelFastTask))
		assert.NoError(t, err)
		assert.Equal(t, 2048, fake.lastOptions.Metadata["ThinkingBudget"],
			"disabling the flag must reproduce the pre-change behaviour byte for byte")
	})
}

// TestResolveThinkingBudgetForLevel pins the level→budget mapping added after a
// production incident: calls carrying an explicit ThinkingLevel bypassed the
// budget entirely (llm_common.go took the else branch, which only clamped the
// level), so a "fast task" scratchpad-compression call ran to ~62.9k thinking
// tokens and 250-300s. A level is a hint the provider may exceed; these bounds
// make it a ceiling while still honouring the caller's stated intent.
func TestResolveThinkingBudgetForLevel(t *testing.T) {
	origGlobal := config.Config.LlmProviderThinkingBudget
	origRetrieval := config.Config.LlmThinkingBudgetRetrieval
	origReasoning := config.Config.LlmThinkingBudgetReasoning
	t.Cleanup(func() {
		config.Config.LlmProviderThinkingBudget = origGlobal
		config.Config.LlmThinkingBudgetRetrieval = origRetrieval
		config.Config.LlmThinkingBudgetReasoning = origReasoning
	})
	config.Config.LlmProviderThinkingBudget = -1
	config.Config.LlmThinkingBudgetRetrieval = 8000
	config.Config.LlmThinkingBudgetReasoning = 16000

	t.Run("a low/fast-task level gets far less than the tier ceiling", func(t *testing.T) {
		got := resolveThinkingBudgetForLevel(ThinkingLevelFastTask, ModelTierRetrieval)
		assert.Equal(t, 2048, got,
			"fast task asked to think as little as possible — it must not get the full tier allowance")
		assert.Less(t, got, resolveThinkingBudget(ModelTierRetrieval))
	})

	t.Run("levels scale monotonically", func(t *testing.T) {
		minimal := resolveThinkingBudgetForLevel("minimal", ModelTierReasoning)
		low := resolveThinkingBudgetForLevel("low", ModelTierReasoning)
		medium := resolveThinkingBudgetForLevel("medium", ModelTierReasoning)
		high := resolveThinkingBudgetForLevel("high", ModelTierReasoning)
		assert.Less(t, minimal, low)
		assert.Less(t, low, medium)
		assert.Less(t, medium, high)
	})

	t.Run("the tier ceiling always wins over a higher level", func(t *testing.T) {
		// "high" maps to 16384, but a Retrieval-tier agent may not exceed 8000.
		assert.Equal(t, 8000, resolveThinkingBudgetForLevel("high", ModelTierRetrieval))
	})

	t.Run("case and whitespace do not defeat the mapping", func(t *testing.T) {
		assert.Equal(t, 2048, resolveThinkingBudgetForLevel("  LOW  ", ModelTierRetrieval))
	})

	t.Run("unrecognized level still gets bounded by the tier", func(t *testing.T) {
		assert.Equal(t, 8000, resolveThinkingBudgetForLevel("enthusiastic", ModelTierRetrieval))
	})

	t.Run("a deliberately uncapped tier is not re-capped via the level", func(t *testing.T) {
		config.Config.LlmThinkingBudgetRetrieval = 0 // operator opt-out per config docs
		assert.Equal(t, -1, resolveThinkingBudgetForLevel("low", ModelTierRetrieval),
			"config 0 means the operator uncapped this tier; a level must not reintroduce a cap")
		config.Config.LlmThinkingBudgetRetrieval = 8000
	})

	t.Run("none means no thinking config at all, never a positive budget", func(t *testing.T) {
		// ClampThinkingLevelForModel returns "none" for flash-lite, which cannot
		// think. Falling through to the tier budget would attach a positive
		// ceiling to a non-thinking model — isThinkingModel matches the
		// `gemini-3` prefix, so gemini-3.5-flash-lite would receive it. Live
		// path: memory_extractor / acknowledgment_agent / vote_subject pass an
		// explicit level and run predominantly on flash-lite. Caught in review
		// by gemini-code-assist on PR #36172.
		assert.Equal(t, -1, resolveThinkingBudgetForLevel(ThinkingLevelNone, ModelTierRetrieval))
		assert.Equal(t, -1, resolveThinkingBudgetForLevel("  NONE  ", ModelTierReasoning),
			"case and whitespace must not defeat the none guard")
	})

	t.Run("clamping a level to none on flash-lite yields no budget", func(t *testing.T) {
		// End-to-end shape of the regression: the caller asks for fast-task, the
		// clamp downgrades it to "none" for this model, and the result must be
		// no budget rather than the tier ceiling.
		clamped := ClampThinkingLevelForModel("gemini-3.5-flash-lite", ThinkingLevelFastTask)
		assert.Equal(t, ThinkingLevelNone, clamped)
		assert.Equal(t, -1, resolveThinkingBudgetForLevel(clamped, ModelTierRetrieval))
	})

	t.Run("global override caps the level mapping", func(t *testing.T) {
		config.Config.LlmProviderThinkingBudget = 100
		assert.Equal(t, 100, resolveThinkingBudgetForLevel("high", ModelTierReasoning))
		config.Config.LlmProviderThinkingBudget = -1
	})
}
