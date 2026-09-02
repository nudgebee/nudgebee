package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The models actually in production, from llm_conversation_token_usage over 14 days.
// Pinning these specifically because a mis-classification here is silent: the wrong
// wire format is a 400 the retry ladder swallows, and the wrong level is invisible.
func TestThinkingCapabilityFor_ProductionModels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model  string
		format thinkingWireFormat
		allow  []string
	}{
		// gemini-3.x flash family: full level set, including flash-lite.
		{"gemini-3.5-flash", thinkingGeminiLevel, levelsFull},
		{"gemini-3.6-flash", thinkingGeminiLevel, levelsFull},
		{"gemini-3-flash-preview", thinkingGeminiLevel, levelsFull},
		// The regression that motivated this table: the previous
		// `strings.Contains(n, "flash-lite") -> none` rule silently disabled thinking
		// control for these two, which support levels and DO think in production
		// (~148 and ~192 thinking tokens/call).
		{"gemini-3.5-flash-lite", thinkingGeminiLevel, levelsFull},
		{"gemini-3.1-flash-lite-preview", thinkingGeminiLevel, levelsFull},
		// Pro variants have no "minimal" floor.
		{"gemini-3.1-pro-preview", thinkingGeminiLevel, levelsNoMinimal},
		// gemini-3-pro documents ONLY low and high. A "medium is a safe middle"
		// assumption 400s here.
		{"gemini-3-pro-preview", thinkingGeminiLevel, levelsLowHigh},
		// gemini < 3: deliberately uncontrolled.
		{"gemini-2.5-flash", thinkingUnsupported, nil},
		{"gemini-2.5-flash-lite", thinkingUnsupported, nil},
		{"gemini-2.5-pro", thinkingUnsupported, nil},
		{"gemini-1.5-pro", thinkingUnsupported, nil},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			got := thinkingCapabilityFor(tc.model)
			assert.Equal(t, tc.format, got.Format)
			assert.Equal(t, tc.allow, got.Allowed)
		})
	}
}

// Anthropic's wire format is decided by model generation, not by vendor or platform.
// Getting this wrong is not a soft failure: budget_tokens on 4.7+ returns a 400.
func TestThinkingCapabilityFor_AnthropicGenerations(t *testing.T) {
	t.Parallel()

	// 4.6+ : effort only. budget_tokens is deprecated on 4.6 and rejected on 4.7+.
	for _, m := range []string{
		"claude-sonnet-4-6", "claude-opus-4-6",
		"anthropic.claude-sonnet-4-6-v1:0", // Bedrock-style model id
		"claude-opus-4-7", "claude-sonnet-5", "claude-opus-5",
	} {
		t.Run("effort/"+m, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, thinkingAnthropicEffort, thinkingCapabilityFor(m).Format,
				"4.6+ must use output_config.effort — budget_tokens 400s on 4.7+")
		})
	}

	// <= 4.5 : the legacy budget is the only mode available.
	for _, m := range []string{"claude-3-7-sonnet", "claude-sonnet-4-5", "claude-haiku-4-5"} {
		t.Run("budget/"+m, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, thinkingAnthropicBudget, thinkingCapabilityFor(m).Format)
		})
	}
}

// An unclassified model must never gain thinking configuration by accident: sending
// nothing is today's behaviour for anything unrecognised, so the table can only ever
// preserve or improve it, never regress it.
func TestThinkingCapabilityFor_UnknownSendsNothing(t *testing.T) {
	t.Parallel()

	for _, m := range []string{"", "  ", "Qwen/Qwen3.6-35B-A3B-FP8", "llama-4", "some-future-model"} {
		got := thinkingCapabilityFor(m)
		assert.Equal(t, thinkingUnsupported, got.Format, "model %q must send no thinking config", m)
		assert.Empty(t, got.clampLevel(ThinkingLevelHigh), "unsupported models resolve to no level")
	}
}

func TestClampLevel(t *testing.T) {
	t.Parallel()

	full := thinkingCapabilityFor("gemini-3.5-flash")        // minimal..high
	noMin := thinkingCapabilityFor("gemini-3.1-pro-preview") // low..high
	lowHigh := thinkingCapabilityFor("gemini-3-pro-preview") // low, high only

	t.Run("permitted levels pass through", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, ThinkingLevelMinimal, full.clampLevel(ThinkingLevelMinimal))
		assert.Equal(t, ThinkingLevelHigh, full.clampLevel(ThinkingLevelHigh))
	})

	t.Run("below the floor clamps up to the floor", func(t *testing.T) {
		t.Parallel()
		// Asking for less than the model allows must yield its minimum, not its
		// (higher) default — the caller wanted less thinking, so give the least legal.
		assert.Equal(t, ThinkingLevelLow, noMin.clampLevel(ThinkingLevelMinimal))
	})

	t.Run("unsupported middle clamps to the weaker neighbour", func(t *testing.T) {
		t.Parallel()
		// gemini-3-pro has no "medium": low and high are equidistant, and the tie
		// must break toward less thinking, never more.
		assert.Equal(t, ThinkingLevelLow, lowHigh.clampLevel(ThinkingLevelMedium))
	})

	t.Run("none and empty resolve to no level", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, full.clampLevel(ThinkingLevelNone))
		assert.Empty(t, full.clampLevel(""))
	})
}

// The Anthropic and OpenAI rows exist so the mapping is complete, but nothing may
// consume them until those adapters are wired. Gating clampThinkingLevel on
// "not unsupported" instead of "is Gemini" silently routed every Claude and GPT model
// through the new clamp — a behaviour change this change explicitly claims not to make.
func TestClampThinkingLevel_NonGeminiKeepsLegacyPath(t *testing.T) {
	t.Parallel()

	for _, model := range []string{
		"claude-sonnet-4-6",                // thinkingAnthropicEffort
		"anthropic.claude-sonnet-4-6-v1:0", // Bedrock model id
		"claude-3-7-sonnet",                // thinkingAnthropicBudget
		"gpt-5",                            // thinkingOpenAIEffort
	} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			for _, lvl := range []string{ThinkingLevelMinimal, ThinkingLevelLow, ThinkingLevelHigh} {
				assert.Equal(t, ClampThinkingLevelForModel(model, lvl), clampThinkingLevel(model, lvl),
					"non-Gemini models must resolve identically to the legacy clamp")
			}
		})
	}
}

// An unreleased Gemini 3 Pro must not inherit the flash "minimal" allowance: every
// documented Pro excludes it, and gemini-3.1-pro rejects it live with a 400.
func TestThinkingCapabilityFor_FutureGemini3ProExcludesMinimal(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"gemini-3.5-pro", "gemini-3.7-pro-preview", "gemini-3-pro-next"} {
		cap := thinkingCapabilityFor(model)
		assert.Equal(t, thinkingGeminiLevel, cap.Format)
		assert.NotContains(t, cap.Allowed, ThinkingLevelMinimal,
			"unlisted Gemini 3 Pro must not be offered minimal — it would 400")
		// And the clamp must degrade it rather than fail.
		assert.Equal(t, ThinkingLevelLow, cap.clampLevel(ThinkingLevelMinimal))
	}
}

// Real-world model ids carry date suffixes and vendor prefixes. Every one of these
// shapes appears in some deployment, and a miss resolves to "send nothing" — silent,
// not a failure. Added after a live probe found claude-sonnet-4-20250514 falling
// through: it contains neither "claude-4" nor "claude-sonnet-4-5".
func TestThinkingCapabilityFor_ModelIdVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model  string
		format thinkingWireFormat
	}{
		// date suffixes
		{"gemini-3.5-flash-001", thinkingGeminiLevel},
		{"gemini-3.1-pro-preview-0514", thinkingGeminiLevel},
		{"claude-sonnet-4-6-20260501", thinkingAnthropicEffort},
		{"claude-opus-4-5-20251101", thinkingAnthropicBudget},
		{"claude-sonnet-4-20250514", thinkingAnthropicBudget},
		{"claude-3-7-sonnet-20250219", thinkingAnthropicBudget},
		// vendor / registry prefixes
		{"google/gemini-3.5-flash", thinkingGeminiLevel},
		{"publishers/google/models/gemini-3.5-flash", thinkingGeminiLevel},
		{"anthropic/claude-sonnet-4-6", thinkingAnthropicEffort},
		{"anthropic.claude-sonnet-4-6-v1:0", thinkingAnthropicEffort},
		{"us.anthropic.claude-sonnet-4-6-v1:0", thinkingAnthropicEffort},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.format, thinkingCapabilityFor(tc.model).Format)
		})
	}

	// Generation still beats family: a dated 4.6 must not fall into the <=4.5 budget row.
	assert.Equal(t, thinkingAnthropicEffort, thinkingCapabilityFor("claude-sonnet-4-6-20260501").Format,
		"4.6+ is matched before the claude-sonnet-4 family prefix")
}

// Generation decides the Anthropic wire format, across every family. Enumerating
// family x generation pairs is how claude-sonnet-4-7 and -4-8 went missing: only the
// opus variants were listed, so a Sonnet 4.7 fell through to the "claude-sonnet-4"
// family prefix and would have been sent budget_tokens — a 400 on 4.7+, not a soft
// failure. This is the failure mode the family prefix itself introduced.
func TestThinkingCapabilityFor_AnthropicGenerationAcrossFamilies(t *testing.T) {
	t.Parallel()

	effort := []string{
		"claude-sonnet-4-6", "claude-sonnet-4-7", "claude-sonnet-4-8",
		"claude-haiku-4-6", "claude-haiku-4-7", "claude-opus-4-7", "claude-opus-4-8",
		"claude-sonnet-4-7-20260301", "us.anthropic.claude-sonnet-4-7-v1:0",
		"claude-sonnet-5", "claude-haiku-5", "claude-opus-5",
	}
	for _, m := range effort {
		t.Run("effort/"+m, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, thinkingAnthropicEffort, thinkingCapabilityFor(m).Format,
				"4.6+ across ALL families must use effort — budget_tokens 400s on 4.7+")
		})
	}

	budget := []string{
		"claude-sonnet-4-5", "claude-sonnet-4-20250514",
		"claude-opus-4-5-20251101", "claude-3-7-sonnet-20250219",
	}
	for _, m := range budget {
		t.Run("budget/"+m, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, thinkingAnthropicBudget, thinkingCapabilityFor(m).Format,
				"<=4.5 has no effort parameter; the legacy budget is the only control")
		})
	}
}

// A numeric budget is attached only for models whose native control IS a budget.
// Gating on "is Gemini level" instead left gemini-2.5 receiving a budget — contradicting
// the table's own claim to have dropped it — and would send budget_tokens to Claude 4.7+
// once that adapter is wired, where it is a 400.
func TestNativeLevel_BudgetOnlyForBudgetNativeModels(t *testing.T) {
	t.Parallel()

	for _, m := range []string{
		"gemini-3.5-flash",  // level-native
		"gemini-2.5-flash",  // deliberately uncontrolled
		"claude-sonnet-4-6", // effort-native
		"gpt-5",             // effort-native
		"Qwen/Qwen3.6-35B",  // unclassified
	} {
		assert.NotEqual(t, thinkingAnthropicBudget, thinkingCapabilityFor(m).Format,
			"%s must not be classified budget-native, or it would receive a numeric budget", m)
	}

	// The one family that genuinely needs it.
	assert.Equal(t, thinkingAnthropicBudget, thinkingCapabilityFor("claude-sonnet-4-5").Format)
}

// Version matching is parsed, not enumerated, because enumeration has already failed
// twice here: claude-sonnet-4-7/-4-8 were missing from the effort list, and the family
// prefix added to fix that would have swallowed an unlisted claude-sonnet-4-9 into the
// legacy budget row — a hard 400 on 4.7+. These cases are deliberately versions nobody
// has enumerated anywhere.
func TestAnthropicGeneration_UnenumeratedVersions(t *testing.T) {
	t.Parallel()

	for _, m := range []string{"claude-sonnet-4-9", "claude-sonnet-6", "claude-haiku-4-7", "claude-opus-4-8"} {
		t.Run("effort/"+m, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, thinkingAnthropicEffort, thinkingCapabilityFor(m).Format,
				"any generation >= 4.6 must use effort, listed or not")
		})
	}

	// Pre-3.7 has no thinking control we can express; it must not fall into either row.
	for _, m := range []string{"claude-3-5-sonnet", "claude-2"} {
		t.Run("unsupported/"+m, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, thinkingUnsupported, thinkingCapabilityFor(m).Format)
		})
	}
}

// A date suffix must not be parsed as a minor version: claude-sonnet-4-20250514 is
// generation 4, not 4.20250514, and reading it as the latter would classify a Sonnet 4
// as effort-capable and send it a parameter it does not accept.
func TestAnthropicGeneration_DateSuffixIsNotAMinorVersion(t *testing.T) {
	t.Parallel()

	major, minor, ok := anthropicGeneration("claude-sonnet-4-20250514")
	assert.True(t, ok)
	assert.Equal(t, 4, major)
	assert.Equal(t, 0, minor, "an 8-digit date must not be read as a minor version")

	major, minor, ok = anthropicGeneration("claude-sonnet-4-6-20260501")
	assert.True(t, ok)
	assert.Equal(t, 4, major)
	assert.Equal(t, 6, minor, "a real minor must still parse when a date follows it")
}

// Only the deprecated preview is low/high-only. Future Pro models must keep "medium",
// which is a common default — restricting them without evidence would be a silent
// downgrade rather than a safety measure.
func TestGemini3Pro_OnlyPreviewIsLowHighOnly(t *testing.T) {
	t.Parallel()

	assert.Equal(t, levelsLowHigh, thinkingCapabilityFor("gemini-3-pro-preview").Allowed)
	for _, m := range []string{"gemini-3-pro-next", "gemini-3.5-pro", "gemini-3.7-pro"} {
		assert.Contains(t, thinkingCapabilityFor(m).Allowed, ThinkingLevelMedium,
			"%s must keep medium — only the deprecated preview is documented low/high-only", m)
	}
}
