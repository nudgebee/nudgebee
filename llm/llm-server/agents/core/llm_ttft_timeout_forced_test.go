package core

// Deterministic, hermetic reproduction of the per-provider TTFT-timeout fix:
// the first attempt stalls with no streamed chunk, the timeout cancels it, and
// the retry succeeds. Prints each attempt's request/response for direct
// observability.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// stallThenSucceedModel is a hermetic llms.Model: attempt 1 blocks for
// stallFor with no streamed chunk; attempt 2+ streams one chunk and returns
// immediately. Mirrors the real googleai client's cancel-mid-stream error
// shape so the retry classification path is exercised for real.
type stallThenSucceedModel struct {
	mu       sync.Mutex
	calls    int
	stallFor time.Duration
}

func (m *stallThenSucceedModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	combined := llms.CallOptions{}
	for _, opt := range options {
		opt(&combined)
	}

	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()

	fmt.Printf("\n========== [attempt %d] REQUEST sent to LLM ==========\n", call)
	for i, msg := range messages {
		fmt.Printf("  message[%d] role=%s\n", i, msg.Role)
		for _, part := range msg.Parts {
			if tp, ok := part.(llms.TextContent); ok {
				text := tp.Text
				if len(text) > 400 {
					text = text[:400] + "...[truncated]"
				}
				fmt.Printf("    text: %s\n", strings.ReplaceAll(text, "\n", "\n    "))
			}
		}
	}
	if b, ok := combined.Metadata["ThinkingBudget"]; ok {
		fmt.Printf("  option: ThinkingBudget = %v\n", b)
	}
	if l, ok := combined.Metadata["ThinkingLevel"]; ok {
		fmt.Printf("  option: ThinkingLevel = %v\n", l)
	}
	fmt.Printf("  option: MaxTokens = %d\n", combined.MaxTokens)
	fmt.Printf("  streaming callback registered: %v\n", combined.StreamingFunc != nil)

	if call == 1 {
		fmt.Printf("  >> simulating a stalled stream: sleeping %s with ZERO chunks emitted\n", m.stallFor)
		select {
		case <-time.After(m.stallFor):
			fmt.Printf("  >> [attempt %d] stall duration elapsed WITHOUT cancellation (timeout did not fire!)\n", call)
			return newStubThinkingResponse("should not normally be reached"), nil
		case <-ctx.Done():
			elapsed := m.stallFor // upper bound; real elapsed logged by the caller
			fmt.Printf("  >> [attempt %d] CANCELLED before %s elapsed: %v\n", call, elapsed, ctx.Err())
			return nil, fmt.Errorf("error in stream mode: doRequest: error sending request: Post \"https://generativelanguage.googleapis.com/v1beta/models/gemini-3-flash-preview:streamGenerateContent?alt=sse\": %w", ctx.Err())
		}
	}

	// Retry: stream one chunk immediately, then return a real answer.
	if combined.StreamingFunc != nil {
		_ = combined.StreamingFunc(ctx, []byte("I"))
	}
	resp := newStubThinkingResponse("Root cause: services-server is hitting relay timeouts against nudgebee-agent; retry after backoff resolved it.")
	fmt.Printf("========== [attempt %d] RESPONSE received from LLM ==========\n", call)
	fmt.Printf("  content: %s\n", resp.Choices[0].Content)
	fmt.Printf("  stopReason: %s\n\n", resp.Choices[0].StopReason)
	return resp, nil
}

func (m *stallThenSucceedModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	resp, err := m.GenerateContent(ctx, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, options...)
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Content, nil
}

func (m *stallThenSucceedModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func newStubThinkingResponse(content string) *llms.ContentResponse {
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{
			Content:    content,
			StopReason: "stop",
			GenerationInfo: map[string]any{
				"PromptTokens":     1842,
				"CompletionTokens": 61,
				"ThinkingTokens":   310,
			},
		}},
	}
}

// gemini3ReasoningContext mirrors what LogAgent gets at runtime: ContextKeyModelTier
// stamped to Reasoning. Deliberately does not use ContextKeyLlmProviderOverride/
// ModelOverride — those mark the resolution as an explicit override, which
// disables retry entirely. The model is pinned via global config instead.
func gemini3ReasoningContext() *security.RequestContext {
	base := security.NewRequestContextForSuperAdmin()
	goCtx := context.WithValue(base.GetContext(), ContextKeyModelTier, ModelTierReasoning)
	return security.NewRequestContext(goCtx, base.GetSecurityContext(), base.GetLogger(), base.GetTracer(), base.GetMeter())
}

func TestTTFTTimeout_ForcedStall_CancelsAndRetries(t *testing.T) {
	origSeconds := config.Config.LlmProviderTTFTTimeoutSeconds
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	origRetries := config.Config.LlmProviderMaxRetries
	origThinkRate := config.Config.LlmProviderTTFTThinkingTokensPerSec
	// Scaled-down timeout for a fast CI run; the 30s production default is covered
	// by config's own default-value test. Enable is per-provider via env — googleai
	// is the current provider in this test, so we flip the matching env key.
	// Clear any ambient per-provider SECONDS override so the global fallback wins.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "true")
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_GOOGLEAI", "")
	config.Config.LlmProviderTTFTTimeoutSeconds = 1
	// This test covers watchdog cancel/retry mechanics, not the thinking-budget
	// deadline adjustment (see TestTTFTDeadlineSeconds). The request below carries a
	// 16k thinking budget, which would otherwise extend the scaled-down 1s deadline
	// past the stall and stop the watchdog from ever firing. 0 is the documented
	// kill switch for the adjustment.
	config.Config.LlmProviderTTFTThinkingTokensPerSec = 0
	config.Config.LlmProvider = "googleai"
	config.Config.LlmModel = "gemini-3-flash-preview"
	config.Config.LlmProviderMaxRetries = 5
	t.Cleanup(func() {
		config.Config.LlmProviderTTFTTimeoutSeconds = origSeconds
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
		config.Config.LlmProviderMaxRetries = origRetries
		config.Config.LlmProviderTTFTThinkingTokensPerSec = origThinkRate
	})

	// stallFor needs headroom above the timeout + retry backoff (~1s + jitter,
	// unscaled) or the "cut short" assertion below has no margin.
	fake := &stallThenSucceedModel{stallFor: 5 * time.Second}
	withFakeLLMModel(t, fake)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are an SRE expert investigating logs. MODE = INVESTIGATION."),
		llms.TextParts(llms.ChatMessageTypeHuman, "why is services-server showing errors in nudgebee namespace"),
	}

	fmt.Printf("\n############################################################\n")
	fmt.Printf("# Forced TTFT timeout scenario: attempt 1 stalls %s (> 1s timeout),\n", fake.stallFor)
	fmt.Printf("# attempt 2 succeeds immediately.\n")
	fmt.Printf("############################################################\n")

	start := time.Now()
	resp, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "logs", false, messages, true)
	elapsed := time.Since(start)

	fmt.Printf("\n############################################################\n")
	fmt.Printf("# RESULT: elapsed=%s, calls=%d, err=%v\n", elapsed, fake.callCount(), err)
	fmt.Printf("############################################################\n\n")

	if err != nil {
		t.Fatalf("expected eventual success after timeout-triggered retry, got error: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Content == "" {
		t.Fatalf("expected a non-empty final response, got: %+v", resp)
	}
	if fake.callCount() != 2 {
		t.Errorf("expected exactly 2 LLM calls (1 stalled + 1 retry), got %d", fake.callCount())
	}
	// Should land well under the stall duration, proving the timeout cut it short.
	if elapsed >= fake.stallFor {
		t.Errorf("timeout did not cut the stall short: took %s, expected well under the %s stall", elapsed, fake.stallFor)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("returned suspiciously fast (%s) — timeout may not have engaged at all (expected ~1-2s)", elapsed)
	}
}

// TestTTFTTimeout_ProviderNotEnabled_DoesNotCancel is the important safety
// invariant: with the per-provider ENABLED key unset (or false), a stalling
// call must run to completion, unretried — no cancellation regardless of the
// global seconds value.
func TestTTFTTimeout_ProviderNotEnabled_DoesNotCancel(t *testing.T) {
	origSeconds := config.Config.LlmProviderTTFTTimeoutSeconds
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	// Explicit "false" defends against an ambient LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI=true
	// in the developer's shell or a CI runner. Without it, the ambient value would
	// silently pass the test through the wrong branch.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "false")
	config.Config.LlmProviderTTFTTimeoutSeconds = 1
	config.Config.LlmProvider = "googleai"
	config.Config.LlmModel = "gemini-3-flash-preview"
	t.Cleanup(func() {
		config.Config.LlmProviderTTFTTimeoutSeconds = origSeconds
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
	})

	fake := &stallThenSucceedModel{stallFor: 1500 * time.Millisecond}
	withFakeLLMModel(t, fake)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "why is services-server showing errors"),
	}

	start := time.Now()
	resp, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "logs", false, messages, true)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, fake.callCount(), "provider-not-enabled must not trigger a retry — expected exactly 1 call")
	assert.GreaterOrEqual(t, elapsed, fake.stallFor,
		"call returned before its full stall duration elapsed — the timeout fired despite provider not being enabled")
}
