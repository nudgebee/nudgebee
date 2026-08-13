package core

import (
	"context"
	"errors"
	"fmt"
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
