package core

// Deterministic, hermetic reproduction of the per-provider sustained-generation
// timeout: attempt 1 streams a first chunk immediately (so the TTFT watchdog's
// "no first token" condition is already false) and then keeps "generating"
// (simulated via a long sleep) well past the sustained-gen deadline. The
// watchdog must still cancel it — that's the entire point of this mechanism
// versus TTFT, which stops watching once the first chunk arrives. Attempt 2
// streams and returns immediately, proving the retry succeeds.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// streamThenHangModel is a hermetic llms.Model: attempt 1 emits one streamed
// chunk immediately (marking the call as "streaming" for TTFT purposes), then
// blocks for hangFor with no further chunks. Attempt 2+ streams one chunk and
// returns a real answer immediately.
type streamThenHangModel struct {
	mu      sync.Mutex
	calls   int
	hangFor time.Duration
}

func (m *streamThenHangModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	combined := llms.CallOptions{}
	for _, opt := range options {
		opt(&combined)
	}

	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()

	fmt.Printf("\n========== [attempt %d] REQUEST sent to LLM ==========\n", call)
	fmt.Printf("  streaming callback registered: %v\n", combined.StreamingFunc != nil)

	if call == 1 {
		if combined.StreamingFunc != nil {
			_ = combined.StreamingFunc(ctx, []byte("thinking..."))
		}
		fmt.Printf("  >> [attempt %d] first chunk emitted (wasStreaming=true), now hanging %s with no further chunks\n", call, m.hangFor)
		select {
		case <-time.After(m.hangFor):
			fmt.Printf("  >> [attempt %d] hang duration elapsed WITHOUT cancellation (sustained-gen timeout did not fire!)\n", call)
			return newStubThinkingResponse("should not normally be reached"), nil
		case <-ctx.Done():
			fmt.Printf("  >> [attempt %d] CANCELLED mid-generation (after streaming had already started): %v\n", call, ctx.Err())
			return nil, fmt.Errorf("error in stream mode: doRequest: error sending request: Post \"https://generativelanguage.googleapis.com/v1beta/models/gemini-3-flash-preview:streamGenerateContent?alt=sse\": %w", ctx.Err())
		}
	}

	// Retry: stream one chunk immediately, then return a real answer.
	if combined.StreamingFunc != nil {
		_ = combined.StreamingFunc(ctx, []byte("I"))
	}
	resp := newStubThinkingResponse("next tool call: fetch_logs_v3")
	fmt.Printf("========== [attempt %d] RESPONSE received from LLM ==========\n", call)
	fmt.Printf("  content: %s\n\n", resp.Choices[0].Content)
	return resp, nil
}

func (m *streamThenHangModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	resp, err := m.GenerateContent(ctx, []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, prompt)}, options...)
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Content, nil
}

func (m *streamThenHangModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestSustainedGenTimeout_ForcedHangAfterFirstToken_CancelsAndRetries(t *testing.T) {
	origSeconds := config.Config.LlmProviderSustainedGenTimeoutSeconds
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	origRetries := config.Config.LlmProviderMaxRetries
	origTTFTFlat := config.Config.LlmProviderTTFTTimeoutSeconds
	origTTFTRate := config.Config.LlmProviderTTFTThinkingTokensPerSec
	origHeadroom := config.Config.LlmProviderSustainedGenHeadroomSeconds
	// TTFT must stay OFF for this test — if it were on, its own "no first token"
	// condition is already false by the time it could fire (attempt 1 streams a
	// chunk immediately), so it would never trigger, but we disable it explicitly
	// so this test isolates the sustained-gen mechanism only.
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "false")
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_GOOGLEAI", "true")
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_SECONDS_GOOGLEAI", "")
	config.Config.LlmProviderSustainedGenTimeoutSeconds = 1
	// This test covers sustained-gen cancel/retry mechanics in isolation from the
	// TTFT-layering formula (see TestSustainedGenDeadlineSeconds). The request below
	// carries a 16k thinking budget (gemini3ReasoningContext), which would otherwise
	// push the layered deadline (max(ttftDeadline, floor) + headroom) well past the
	// 5s hang and stop the watchdog from ever firing. Zeroing the TTFT flat seconds
	// and thinking-rate adjustment, plus the headroom, restores the configured 1s
	// floor as the effective deadline.
	config.Config.LlmProviderTTFTTimeoutSeconds = 0
	config.Config.LlmProviderTTFTThinkingTokensPerSec = 0
	config.Config.LlmProviderSustainedGenHeadroomSeconds = 0
	config.Config.LlmProvider = "googleai"
	config.Config.LlmModel = "gemini-3-flash-preview"
	config.Config.LlmProviderMaxRetries = 5
	t.Cleanup(func() {
		config.Config.LlmProviderSustainedGenTimeoutSeconds = origSeconds
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
		config.Config.LlmProviderMaxRetries = origRetries
		config.Config.LlmProviderTTFTTimeoutSeconds = origTTFTFlat
		config.Config.LlmProviderTTFTThinkingTokensPerSec = origTTFTRate
		config.Config.LlmProviderSustainedGenHeadroomSeconds = origHeadroom
	})

	fake := &streamThenHangModel{hangFor: 5 * time.Second}
	withFakeLLMModel(t, fake)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "You are an SRE expert investigating logs. MODE = ROUTINE."),
		llms.TextParts(llms.ChatMessageTypeHuman, "get me logs for relay_server"),
	}

	fmt.Printf("\n############################################################\n")
	fmt.Printf("# Forced sustained-gen timeout scenario: attempt 1 streams a first\n")
	fmt.Printf("# chunk then hangs %s (> 1s timeout), attempt 2 succeeds immediately.\n", fake.hangFor)
	fmt.Printf("############################################################\n")

	start := time.Now()
	resp, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "logs_v3", false, messages, true)
	elapsed := time.Since(start)

	fmt.Printf("\n############################################################\n")
	fmt.Printf("# RESULT: elapsed=%s, calls=%d, err=%v\n", elapsed, fake.callCount(), err)
	fmt.Printf("############################################################\n\n")

	if err != nil {
		t.Fatalf("expected eventual success after sustained-gen-triggered retry, got error: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].Content == "" {
		t.Fatalf("expected a non-empty final response, got: %+v", resp)
	}
	if fake.callCount() != 2 {
		t.Errorf("expected exactly 2 LLM calls (1 hung + 1 retry), got %d", fake.callCount())
	}
	// Should land well under the hang duration, proving the timeout cut it short
	// DESPITE streaming having already started — the behavior TTFT cannot provide.
	if elapsed >= fake.hangFor {
		t.Errorf("timeout did not cut the hang short: took %s, expected well under the %s hang", elapsed, fake.hangFor)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("returned suspiciously fast (%s) — timeout may not have engaged at all (expected ~1-2s)", elapsed)
	}
}

// TestSustainedGenTimeout_ProviderNotEnabled_DoesNotCancel is the important
// safety invariant: with the per-provider ENABLED key unset (or false), a
// call that streams a first chunk and then keeps generating for a long time
// must run to completion, unretried — no cancellation regardless of the
// global seconds value. Mirrors TestTTFTTimeout_ProviderNotEnabled_DoesNotCancel.
func TestSustainedGenTimeout_ProviderNotEnabled_DoesNotCancel(t *testing.T) {
	origSeconds := config.Config.LlmProviderSustainedGenTimeoutSeconds
	origProvider := config.Config.LlmProvider
	origModel := config.Config.LlmModel
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_ENABLED_GOOGLEAI", "false")
	// Explicit "false" defends against an ambient LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_GOOGLEAI=true
	// in the developer's shell or a CI runner. Without it, the ambient value would
	// silently pass the test through the wrong branch.
	t.Setenv("LLM_PROVIDER_SUSTAINED_GEN_TIMEOUT_ENABLED_GOOGLEAI", "false")
	config.Config.LlmProviderSustainedGenTimeoutSeconds = 1
	config.Config.LlmProvider = "googleai"
	config.Config.LlmModel = "gemini-3-flash-preview"
	t.Cleanup(func() {
		config.Config.LlmProviderSustainedGenTimeoutSeconds = origSeconds
		config.Config.LlmProvider = origProvider
		config.Config.LlmModel = origModel
	})

	fake := &streamThenHangModel{hangFor: 1500 * time.Millisecond}
	withFakeLLMModel(t, fake)

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, "get me logs for relay_server"),
	}

	start := time.Now()
	resp, err := GenerateAndTrackLLMContent(gemini3ReasoningContext(), "", "", "", "", "logs_v3", false, messages, true)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, fake.callCount(), "provider-not-enabled must not trigger a retry — expected exactly 1 call")
	assert.GreaterOrEqual(t, elapsed, fake.hangFor,
		"call returned before its full hang duration elapsed — the timeout fired despite provider not being enabled")
}
