package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A hung call must be distinguishable from an ordinary failure in the DB: it costs the
// full per-call deadline, so blending it into "failure" hides the largest single source
// of latency in the system.
func TestRequestStatusForError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil error", nil, "failure"},
		{"plain failure", errors.New("boom"), "failure"},
		{"quota error", errors.New("429 too many requests"), "failure"},
		{"bare deadline", context.DeadlineExceeded, "timeout"},
		{
			"wrapped first-token timeout",
			fmt.Errorf("first token timeout after 95s on gemini-3.5-flash: %w", context.DeadlineExceeded),
			"timeout",
		},
		{
			"deadline wrapped twice",
			fmt.Errorf("error in stream mode: %w",
				fmt.Errorf("call failed: %w", context.DeadlineExceeded)),
			"timeout",
		},
		// context.Canceled is a caller-side abort (user navigated away, parent gave up),
		// not a hung provider — it must not be counted as a provider timeout.
		{"canceled is not a timeout", context.Canceled, "failure"},
		// A message that merely mentions a deadline, without wrapping the sentinel,
		// is not reliable enough to classify as a timeout.
		{"deadline-shaped string only", errors.New("context deadline exceeded"), "failure"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, requestStatusForError(tc.err))
		})
	}
}

// Usage must be attributed to the model that actually produced the response. Recording
// the caller's original model makes a fallback-served response look like it came from
// the model that failed, and makes fallback_from_model equal llm_model.
func TestBuildCallMetadataCarriesServedModel(t *testing.T) {
	t.Parallel()

	t.Run("records the model that served the response", func(t *testing.T) {
		t.Parallel()
		rc := &retryContext{
			currentModel:    "gemini-3-flash-preview",
			currentProvider: "googleai",
			lastLatency:     12.4,
		}
		md := buildCallMetadata(rc, true)

		assert.Equal(t, "gemini-3-flash-preview", md.ServedModel)
		assert.Equal(t, "googleai", md.ServedProvider)
		assert.Equal(t, "success", md.RequestStatus)
	})

	t.Run("served model differs from the primary after a fallback", func(t *testing.T) {
		t.Parallel()
		primary := "gemini-3.5-flash"
		rc := &retryContext{
			currentModel:      "gemini-3-flash-preview",
			currentProvider:   "googleai",
			fallbackFromModel: &primary,
		}
		md := buildCallMetadata(rc, true)

		assert.NotEqual(t, *md.FallbackFromModel, md.ServedModel,
			"a fallback-served row must not attribute usage to the model it fell back from")
	})
}

// A TTFT timeout must classify as a timeout even though the underlying cancel
// surfaces as context.Canceled, and a plain caller-side cancel must not.
func TestRequestStatusForTTFTTimeout(t *testing.T) {
	t.Parallel()

	ttft := fmt.Errorf("%w: model did not emit a first token within %ds, timeout — retrying same model: %w",
		ErrTTFTTimeout, 110, context.Canceled)

	assert.Equal(t, "timeout", requestStatusForError(ttft),
		"watchdog-abandoned calls must be queryable as timeouts, not blend into failures")
	assert.Equal(t, "failure", requestStatusForError(context.Canceled),
		"a caller-side abort is not a provider timeout")
}

// The watchdog error's wording is load-bearing for retry routing: isTransientError
// matches on "timeout" to trigger a same-model retry, and the string must NOT contain
// "deadline exceeded" or the call is routed to fallback models instead — which would
// drop a Reasoning-tier call onto a weaker model. Wrapping ErrTTFTTimeout must not
// have disturbed either property.
func TestTTFTTimeoutErrorPreservesRetryRouting(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("%w: model did not emit a first token within %ds, timeout — retrying same model: %w",
		ErrTTFTTimeout, 110, context.Canceled)
	msg := strings.ToLower(err.Error())

	assert.True(t, strings.HasPrefix(msg, "ttft timeout:"),
		"message shape is relied on in logs and dashboards")
	assert.Contains(t, msg, "timeout", "isTransientError keys on this substring")
	assert.NotContains(t, msg, "deadline exceeded",
		"would reroute to fallback models instead of a same-model retry")
	assert.True(t, isTransientError(err), "must route to the same-model retry path")
}

// A TTFT timeout is persisted exactly once, at the watchdog site in tryWithModel.
// The two downstream sites that would otherwise persist the same abandoned attempt —
// tryFallbackModel and the end-of-sequence failure row in GenerateAndTrackLLMContent —
// must skip it, or one timed-out call is counted two or three times and the deadline
// tuning that reads these rows is calibrated against inflated numbers.
//
// This is a real regression that shipped once: the guard in recordAbandonedAttempt was
// widened from errors.Is(DeadlineExceeded) to requestStatusForError == "timeout", which
// silently made ErrTTFTTimeout match at both sites.
func TestTTFTTimeoutRecordedOnlyAtSource(t *testing.T) {
	t.Parallel()

	ttft := fmt.Errorf("%w: model did not emit a first token within %ds, timeout — retrying same model: %w",
		ErrTTFTTimeout, 110, context.Canceled)

	assert.True(t, alreadyRecordedAtSource(ttft),
		"downstream sites must skip a TTFT timeout — the watchdog already wrote its row")

	// It still classifies as a timeout, so the single row it does produce is queryable.
	assert.Equal(t, "timeout", requestStatusForError(ttft))

	// Every other failure must still be recorded downstream exactly as before.
	for _, other := range []error{
		context.DeadlineExceeded,
		context.Canceled,
		errors.New("429 rate limited"),
		errors.New("llm returned empty content"),
		nil,
	} {
		assert.False(t, alreadyRecordedAtSource(other),
			"only TTFT timeouts are recorded at source; %v must still be recorded downstream", other)
	}
}
