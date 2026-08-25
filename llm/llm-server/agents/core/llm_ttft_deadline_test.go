package core

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// withTTFTConfig sets the thinking-adjustment knobs for the duration of a test and
// restores them afterwards, so cases cannot leak into each other via the global config.
func withTTFTConfig(t *testing.T, ratePerSec, maxSeconds int) {
	t.Helper()
	prevRate := config.Config.LlmProviderTTFTThinkingTokensPerSec
	prevMax := config.Config.LlmProviderTTFTTimeoutMaxSeconds
	config.Config.LlmProviderTTFTThinkingTokensPerSec = ratePerSec
	config.Config.LlmProviderTTFTTimeoutMaxSeconds = maxSeconds
	t.Cleanup(func() {
		config.Config.LlmProviderTTFTThinkingTokensPerSec = prevRate
		config.Config.LlmProviderTTFTTimeoutMaxSeconds = prevMax
	})
}

func optsWithBudget(tokens int) llms.CallOptions {
	o := llms.CallOptions{}
	WithThinkingBudget(tokens)(&o)
	return o
}

func optsWithLevel(level string) llms.CallOptions {
	o := llms.CallOptions{}
	WithThinkingLevel(level)(&o)
	return o
}

func TestTTFTDeadlineSeconds(t *testing.T) {
	cases := []struct {
		name string
		flat int
		opts llms.CallOptions
		want int
	}{
		// No thinking on the call — the deadline is exactly what it is today. The
		// adjustment must never shorten a deadline, only extend one.
		{"no metadata keeps flat deadline", 30, llms.CallOptions{}, 30},
		{"zero budget keeps flat deadline", 30, optsWithBudget(0), 30},

		// Budget-derived: flat + budget/rate at 100 tok/s.
		{"retrieval tier budget", 30, optsWithBudget(8000), 110},
		{"reasoning tier budget", 30, optsWithBudget(16000), 190},
		{"summary tier budget", 30, optsWithBudget(4000), 70},
		{"small budget", 30, optsWithBudget(512), 35},

		// Clamped to the configured ceiling.
		{"absurd budget clamps to max", 30, optsWithBudget(10_000_000), 240},

		// String ThinkingLevel API maps through the same level->budget table.
		{"level low", 30, optsWithLevel("low"), 50},
		{"level medium", 30, optsWithLevel("medium"), 111},
		{"level high", 30, optsWithLevel("high"), 193},
		// "none" is dropped by WithThinkingLevel, so no thinking metadata is attached.
		{"level none keeps flat deadline", 30, optsWithLevel("none"), 30},
		{"unknown level keeps flat deadline", 30, optsWithLevel("bogus"), 30},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTTFTConfig(t, 100, 240)
			assert.Equal(t, tc.want, ttftDeadlineSeconds(tc.flat, tc.opts))
		})
	}
}

// A negative budget means "uncapped thinking" — there is no allowance to convert into
// time, so the deadline must go to the ceiling rather than guessing low and truncating
// a call that may legitimately think for minutes.
//
// The metadata is built directly rather than via WithThinkingBudget because that helper
// drops negative values; this exercises the sentinel reaching ttftDeadlineSeconds by any
// other route (e.g. a tier resolving to -1 when an operator uncaps thinking).
func TestTTFTDeadlineUncappedThinking(t *testing.T) {
	withTTFTConfig(t, 100, 240)

	o := llms.CallOptions{Metadata: map[string]any{"ThinkingBudget": -1}}
	assert.Equal(t, 240, ttftDeadlineSeconds(30, o),
		"an uncapped budget has no allowance to derive, so it must use the ceiling, not a low guess")
}

// Rate 0 is the documented kill switch: it restores the pre-existing flat behaviour
// for every call, which is the escape hatch if the adjustment ever misbehaves.
func TestTTFTDeadlineAdjustmentDisabled(t *testing.T) {
	withTTFTConfig(t, 0, 240)

	assert.Equal(t, 30, ttftDeadlineSeconds(30, optsWithBudget(16000)))
	assert.Equal(t, 30, ttftDeadlineSeconds(30, llms.CallOptions{}))
}

// A misconfigured ceiling must never produce a deadline shorter than the flat one,
// which would make the watchdog more aggressive than it is today.
func TestTTFTDeadlineNeverShortensFlatDeadline(t *testing.T) {
	withTTFTConfig(t, 100, 10) // ceiling below the flat deadline

	assert.Equal(t, 30, ttftDeadlineSeconds(30, optsWithBudget(8000)))

	withTTFTConfig(t, 100, 0) // ceiling unset
	assert.Equal(t, 30, ttftDeadlineSeconds(30, optsWithBudget(8000)))
}

// The deadline must clear the measured p95 time-to-first-token for its thinking budget,
// or the watchdog would abandon calls that were reasoning normally rather than hung.
func TestTTFTDeadlineExceedsObservedP95(t *testing.T) {
	withTTFTConfig(t, 100, 240)

	cases := []struct {
		name        string
		budget      int
		observedP95 int // seconds, measured over 4 days of production calls
	}{
		{"sub-1k thinking", 512, 7},
		{"1k-4k thinking", 4000, 15},
		{"4k-8k thinking", 8000, 32},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ttftDeadlineSeconds(30, optsWithBudget(tc.budget))
			assert.Greater(t, got, 2*tc.observedP95,
				"deadline must exceed 2x the observed p95 TTFT to avoid cutting healthy calls")
		})
	}
}

// The watchdog defaults on only where the hang is measured. Arming it against a
// provider whose TTFT profile we have not measured risks cutting merely-slow calls.
func TestTTFTTimeoutDefaultEnabled(t *testing.T) {
	assert.True(t, ttftTimeoutDefaultEnabled("googleai"))

	for _, p := range []string{"huggingface", "bedrock", "azure", "openai", "vertex", ""} {
		assert.False(t, ttftTimeoutDefaultEnabled(p), "provider %q must stay opt-in", p)
	}
}

// A timed-out attempt must report the time it actually burned. tryWithModel returns
// early on every failure path, so latency has to be recorded before that branching —
// otherwise rc.lastLatency still holds the previous attempt's value and the timeout
// row understates the stall, which is the one number the row exists to capture.
func TestRecordAbandonedAttemptUsesCurrentAttemptLatency(t *testing.T) {
	rc := &retryContext{
		lastLatency: 110.5,
		lastErr:     fmt.Errorf("ttft timeout: %w", context.DeadlineExceeded),
	}

	md := buildCallMetadata(rc, false)
	assert.InDelta(t, 110.5, md.LatencySeconds, 0.001,
		"metadata must carry the stalled attempt's latency, not a stale earlier value")
	assert.Equal(t, "timeout", requestStatusForError(rc.lastErr))
}

// Only timeouts are recorded as abandoned attempts; other failures are already covered
// by the end-of-sequence failure row, so recording them here would double-count.
func TestAbandonedAttemptOnlyRecordsTimeouts(t *testing.T) {
	assert.Equal(t, "failure", requestStatusForError(errors.New("429 rate limited")))
	assert.Equal(t, "timeout", requestStatusForError(
		fmt.Errorf("first token timeout: %w", context.DeadlineExceeded)))
}
