package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Output-token ceilings moved to the llm_model_pricing catalog (V878) —
// resolution behavior is covered in model_token_limits_test.go, and the seed
// values live in the migration rather than a code table.

func TestGetLlmMaxTokenLength(t *testing.T) {
	tests := []struct {
		model    string
		expected int
	}{
		{"gemini-3-pro", 2_000_000},
		{"gemini-3-flash", 1_000_000},
		{"gemini-3-anything", 1_000_000},
		{"gemini-1.5-pro", 2_000_000},
		{"gemini-1.5-flash", 1_000_000},
		{"gpt-4-0613", 8192},
		{"Qwen/Qwen3.6-35B-A3B-FP8", 262_144},
		{"unknown-model", 32_000},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetLlmMaxTokenLength(tt.model))
		})
	}
}

// The cache minimum decides whether prompt caching runs at all, so a model the
// table fails to recognise does not error — it silently stops caching and just
// costs money. The Flash family must therefore match on any minor version.
//
// Regression for #38511: the Flash branch previously listed only
// `gemini-3-flash` and `gemini-3.0-flash`, so every dotted release fell through
// to the Pro catch-all and was told it needed 4,096 tokens. On dev that
// discarded 50 of 84 cache attempts in 24h.
func TestGetLlmMinCacheTokens(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected int
	}{
		// The models actually configured on dev — every one of these returned
		// 4,096 before the fix.
		{"dotted flash, default model", "gemini-3.7-flash", 1_024},
		{"dotted flash, retrieval tier", "gemini-3.6-flash", 1_024},
		{"dotted flash-lite, summary tier", "gemini-3.5-flash-lite", 1_024},
		{"dotted flash-lite, fallback", "gemini-3.1-flash-lite", 1_024},

		// Names the old form did match — must not regress.
		{"undotted flash", "gemini-3-flash", 1_024},
		{"zero-minor flash", "gemini-3.0-flash", 1_024},

		// Gemini 3 non-Flash keeps the higher floor.
		{"dotted pro", "gemini-3.5-pro", 4_096},
		{"undotted pro", "gemini-3-pro", 4_096},
		{"unknown gemini-3 variant", "gemini-3-ultra", 4_096},

		// Earlier families unchanged.
		{"2.5 flash", "gemini-2.5-flash", 1_024},
		{"2.5 flash-lite", "gemini-2.5-flash-lite", 1_024},
		{"2.5 pro", "gemini-2.5-pro", 4_096},
		{"2.0 flash", "gemini-2.0-flash", 1_024},
		{"1.5 pro", "gemini-1.5-pro", 32_768},

		// Vendor-prefixed names normalise before matching.
		{"vendor-prefixed dotted flash", "google.gemini-3.5-flash", 1_024},

		// 0 means "caching unsupported / unknown", not "no minimum".
		{"non-gemini model", "gpt-4-0613", 0},
		{"unknown model", "some-unknown-model", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetLlmMinCacheTokens(tt.model),
				"model %q resolves to the wrong cache minimum", tt.model)
		})
	}
}
