package core

import (
	"errors"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleQuotaErrorNamesTheRealCause covers the incident where a HuggingFace 503
// surfaced to users as "quota exceeded on model Qwen/...". The endpoint-unavailable
// path deliberately reuses handleQuotaError for its fallback loop, so without an
// explicit cause the terminal error always claimed a quota problem — sending anyone
// debugging it to the provider's billing page.
//
// A tier with no fallback configured is a deliberate choice (surface the failure
// rather than silently answer from another model), which makes this error string the
// user-facing contract.
func TestHandleQuotaErrorNamesTheRealCause(t *testing.T) {
	upstream := errors.New("huggingface endpoint circuit open (service unavailable) for model Qwen/Test")

	newRC := func() *retryContext {
		return &retryContext{
			ctx:             security.NewRequestContextForSuperAdmin(),
			agentId:         "memory_compose",
			currentModel:    "Qwen/Test",
			currentProvider: "huggingface",
			lastErr:         upstream,
		}
	}

	t.Run("unavailable does not claim quota", func(t *testing.T) {
		_, _, err := handleQuotaError(newRC(), nil, false, fallbackCauseUnavailable)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "quota",
			"a 503 must not be reported as a quota problem")
		assert.Contains(t, err.Error(), "Qwen/Test")
		assert.Contains(t, err.Error(), "huggingface")
		assert.Contains(t, err.Error(), "no fallback model is configured")
		assert.ErrorIs(t, err, upstream, "the upstream cause must stay wrapped for callers")
	})

	t.Run("genuine quota still says quota", func(t *testing.T) {
		_, _, err := handleQuotaError(newRC(), nil, false, fallbackCauseQuota)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "quota exceeded")
		assert.Contains(t, err.Error(), "Qwen/Test")
		assert.Contains(t, err.Error(), "no fallback model is configured")
		assert.ErrorIs(t, err, upstream, "quota branch must also preserve the upstream cause via %%w")
	})
}

// TestHandleQuotaErrorExhaustedFallbacksNamesTheRealCause covers the branch
// where fallback models ARE configured but every candidate is unreachable
// (the exhaustion path at the bottom of handleQuotaError). Before the fix
// this path returned "quota exceeded on all available models" regardless of
// the original cause, so a broken HuggingFace endpoint with fallbacks defined
// still misdirected operators to the billing page.
//
// Pre-populating triedModels short-circuits tryFallbackModel to a non-quota
// "already tried" error, so we reach the exhaustion path without an LLM call.
func TestHandleQuotaErrorExhaustedFallbacksNamesTheRealCause(t *testing.T) {
	upstream := errors.New("huggingface endpoint circuit open (service unavailable) for model Qwen/Test")
	fallbacks := []string{"gpt-4o-mini", "gpt-3.5"}

	newRC := func() *retryContext {
		return &retryContext{
			ctx:             security.NewRequestContextForSuperAdmin(),
			agentId:         "memory_compose",
			currentModel:    "Qwen/Test",
			currentProvider: "huggingface",
			lastErr:         upstream,
			triedModels: map[string]bool{
				fallbacks[0]: true,
				fallbacks[1]: true,
			},
		}
	}

	t.Run("unavailable exhaustion does not claim quota", func(t *testing.T) {
		_, _, err := handleQuotaError(newRC(), fallbacks, false, fallbackCauseUnavailable)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "quota",
			"exhausted 503 fallbacks must not be reported as a quota problem")
		assert.Contains(t, err.Error(), "Qwen/Test")
		assert.Contains(t, err.Error(), "huggingface")
		assert.Contains(t, err.Error(), "currently unavailable")
		assert.ErrorIs(t, err, upstream, "the upstream cause must stay wrapped for callers")
	})

	t.Run("quota exhaustion still says quota", func(t *testing.T) {
		_, _, err := handleQuotaError(newRC(), fallbacks, false, fallbackCauseQuota)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "quota exceeded on primary model Qwen/Test")
		assert.Contains(t, err.Error(), "2 fallback model(s) also failed",
			"count reports len(fallbackModels)=2, not len(triedModels) which includes the primary")
		assert.ErrorIs(t, err, upstream, "exhausted quota branch must preserve the upstream cause via %%w")
	})
}

// TestHandleQuotaErrorNilLastErrDoesNotRenderPercentBangW covers the circuit-open
// dispatch path: line 2682 calls handleQuotaError(fallbackCauseUnavailable) BEFORE
// setting rc.lastErr because the primary was skipped by the circuit breaker without
// an attempt. Wrapping a nil error with %w would produce "%!w(<nil>)" and hide the
// real cause. Both the no-fallback and exhausted-fallback branches must substitute
// ErrLLMServiceUnavailable in that case.
func TestHandleQuotaErrorNilLastErrDoesNotRenderPercentBangW(t *testing.T) {
	newRC := func() *retryContext {
		return &retryContext{
			ctx:             security.NewRequestContextForSuperAdmin(),
			agentId:         "memory_compose",
			currentModel:    "Qwen/Test",
			currentProvider: "huggingface",
			// lastErr deliberately nil — simulates the circuit-open dispatch path.
		}
	}

	t.Run("no fallback configured", func(t *testing.T) {
		rc := newRC()
		_, meta, err := handleQuotaError(rc, nil, false, fallbackCauseUnavailable)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "%!w",
			"nil rc.lastErr must not render as %%!w(<nil>) — substitute the sentinel instead")
		assert.Contains(t, err.Error(), "currently unavailable")
		assert.ErrorIs(t, err, ErrLLMServiceUnavailable,
			"nil-lastErr fallback should wrap the ErrLLMServiceUnavailable sentinel so callers can still errors.Is()")
		require.NotNil(t, meta)
		assert.Equal(t, "failure", meta.RequestStatus,
			"terminal failure must not be recorded as success when rc.lastErr started nil (buildCallMetadata gate)")
		require.NotNil(t, meta.ErrorMessage)
		assert.NotEmpty(t, *meta.ErrorMessage)
	})

	t.Run("exhausted fallbacks", func(t *testing.T) {
		fallbacks := []string{"a", "b"}
		rc := newRC()
		rc.triedModels = map[string]bool{"a": true, "b": true}
		_, meta, err := handleQuotaError(rc, fallbacks, false, fallbackCauseUnavailable)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "%!w",
			"nil rc.lastErr must not render as %%!w(<nil>) on exhaustion either")
		assert.Contains(t, err.Error(), "currently unavailable")
		assert.ErrorIs(t, err, ErrLLMServiceUnavailable)
		require.NotNil(t, meta)
		assert.Equal(t, "failure", meta.RequestStatus,
			"exhausted-fallback terminal must not record as success in metrics")
		require.NotNil(t, meta.ErrorMessage)
	})
}

func TestFallbackCauseReason(t *testing.T) {
	assert.Equal(t, "Endpoint unavailable", fallbackCauseUnavailable.reason())
	assert.Equal(t, "Quota/rate limit error detected", fallbackCauseQuota.reason())
	assert.Equal(t, "Unknown fallback cause", fallbackCause(99).reason(),
		"switch default must catch future causes explicitly instead of masquerading as quota")
}
