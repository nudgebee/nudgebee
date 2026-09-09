package core

import (
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// withSustainedGenHeadroom sets LlmProviderSustainedGenHeadroomSeconds for the
// duration of a test and restores it afterwards. Mirrors withTTFTConfig
// (llm_ttft_deadline_test.go).
func withSustainedGenHeadroom(t *testing.T, seconds int) {
	t.Helper()
	prev := config.Config.LlmProviderSustainedGenHeadroomSeconds
	config.Config.LlmProviderSustainedGenHeadroomSeconds = seconds
	t.Cleanup(func() { config.Config.LlmProviderSustainedGenHeadroomSeconds = prev })
}

// Exercises the layering formula added to close the gap the PR #36332 review
// found: sustainedGenDeadlineSeconds must derive its deadline from the same
// thinking-aware TTFT calculation rather than a flat constant, so a flat
// sustained-gen timeout can no longer outrace TTFT on higher thinking levels
// and silently make TTFT's own deadline unreachable.
func TestSustainedGenDeadlineSeconds(t *testing.T) {
	withTTFTConfig(t, 100, 240) // TTFT thinking-rate 100 tok/s, ceiling 240s
	withSustainedGenHeadroom(t, 0)

	// No thinking on the call — TTFT deadline equals its flat seconds (30), which
	// is below the sustained-gen floor (60). The floor wins.
	assert.Equal(t, 60, sustainedGenDeadlineSeconds(60, "googleai", llms.CallOptions{}),
		"floor must win when the TTFT deadline is shorter")

	// "medium" thinking pushes the TTFT deadline (30 + 8192/100 = 111) past the
	// flat 60s floor — this is exactly the production scenario the review flagged
	// (gemini-3.5-flash's default level). TTFT's deadline must win.
	got := sustainedGenDeadlineSeconds(60, "googleai", optsWithLevel("medium"))
	assert.Equal(t, 111, got, "TTFT deadline must win once thinking pushes it past the flat floor")

	// "high" thinking (gemini-3-flash-preview's default): TTFT deadline is 30 +
	// 16384/100 = 193s, still far past the 60s floor.
	got = sustainedGenDeadlineSeconds(60, "googleai", optsWithLevel("high"))
	assert.Equal(t, 193, got, "TTFT deadline must win for high thinking levels too")
}

// The headroom must be added on top of whichever of (TTFT deadline, floor) is
// larger, in every branch — that's what guarantees sustained-gen always fires
// strictly after TTFT would have had its chance, instead of racing it.
func TestSustainedGenDeadlineSeconds_HeadroomAppliesToBothBranches(t *testing.T) {
	withTTFTConfig(t, 100, 240)

	withSustainedGenHeadroom(t, 15)
	assert.Equal(t, 75, sustainedGenDeadlineSeconds(60, "googleai", llms.CallOptions{}),
		"headroom must be added on top of the floor branch")
	assert.Equal(t, 208, sustainedGenDeadlineSeconds(60, "googleai", optsWithLevel("high")),
		"headroom must be added on top of the TTFT-deadline branch")
}

// The whole point of the fix: for any floor/thinking-level combination, the
// resulting deadline must exceed the TTFT deadline that would apply to the
// same call whenever headroom > 0 — otherwise the two watchdogs can still
// race instead of firing in the intended order.
func TestSustainedGenDeadlineSeconds_AlwaysExceedsTTFTDeadline(t *testing.T) {
	withTTFTConfig(t, 100, 240)
	withSustainedGenHeadroom(t, 30)

	levels := []string{"minimal", "low", "medium", "high"}
	floors := []int{10, 60, 300}

	for _, level := range levels {
		for _, floor := range floors {
			ttft := ttftDeadlineSeconds(resolveTTFTFlatSeconds("googleai"), optsWithLevel(level))
			got := sustainedGenDeadlineSeconds(floor, "googleai", optsWithLevel(level))
			assert.Greater(t, got, ttft,
				"level=%s floor=%d: sustained-gen deadline (%d) must exceed the TTFT deadline (%d)", level, floor, got, ttft)
		}
	}
}

// resolveTTFTFlatSeconds honors the per-provider TTFT-seconds override, so a
// provider-specific TTFT tuning also flows into the derived sustained-gen
// deadline without a separate sustained-gen-side override.
func TestSustainedGenDeadlineSeconds_UsesPerProviderTTFTOverride(t *testing.T) {
	withTTFTConfig(t, 0, 240) // rate 0: no thinking adjustment, pure flat value
	withSustainedGenHeadroom(t, 0)
	t.Setenv("LLM_PROVIDER_TTFT_TIMEOUT_SECONDS_HUGGINGFACE", "90")

	assert.Equal(t, 90, sustainedGenDeadlineSeconds(60, "huggingface", llms.CallOptions{}),
		"per-provider TTFT seconds override must flow into the derived sustained-gen deadline")
}
