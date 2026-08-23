package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLlmMaxOutputTokens(t *testing.T) {
	tests := []struct {
		model    string
		expected int
	}{
		{"gemini-3-pro", 65536},
		{"gemini-2.5-pro", 65536},
		{"gemini-2-5-pro", 65536},
		{"gemini-2.5-flash", 65536},
		{"gemini-1.5-pro", 8192},
		{"gemini-2.0-pro", 8192},
		{"gpt-4o", 16384},
		{"gpt-4", 4096},
		{"claude-3-5-sonnet", 8192},
		{"unknown-model", 0},

		// Claude 4.x / 5.x. Before these entries existed every one of these models
		// fell through to the caller's 4096 floor — 3% of the 128k they actually
		// support — so 44% of one customer's calls stopped at max_tokens and paid
		// the continuation loop.
		{"claude-sonnet-4-6", 65536},
		{"claude-opus-4-6", 65536},
		{"claude-opus-4-7", 65536},
		{"claude-opus-4-8", 65536},
		{"claude-sonnet-5", 65536},
		{"claude-opus-5", 65536},

		// 4.0-4.5 band: held at the lowest ceiling in the band (Opus 4/4.1 cap at
		// 32k) so one value is safe for every model in it.
		{"claude-opus-4", 32000},
		{"claude-opus-4-1", 32000},
		{"claude-sonnet-4-5", 32000},

		// The exact Bedrock id from the customer trace. normalizeModel strips a
		// single-segment vendor prefix ("anthropic.") but not the cross-region
		// "us." one, so this must resolve on the substring rather than on a
		// fully-normalized id.
		{"us.anthropic.claude-sonnet-4-6", 65536},
		{"anthropic.claude-opus-4-8", 65536},

		// Date-suffixed ids must not be read as a minor version.
		{"claude-sonnet-4-6-20251114", 65536},

		// OpenAI had the same hole: neither GPT-5 nor the o-series matched any case,
		// so both sat on the 4096 floor despite documenting 128k / 100k ceilings.
		{"gpt-5", 65536},
		{"gpt-5-mini", 65536},
		{"o1", 65536},
		{"o3-mini", 65536},
		{"o4-mini", 65536},
		{"openai.o3", 65536},
		// normalizeModel strips at most one vendor segment from a fixed list, so a
		// region/platform qualifier survives it. These reached the 4096 floor under
		// a HasPrefix check.
		{"azure.openai.o1-mini", 65536},
		{"us.openai.o3", 65536},
		{"azure.o3-mini", 65536},
		// Two-digit release: the version must not be read as a single digit.
		{"o10-mini", 65536},
		{"azure.openai.o10", 65536},
		// ...while an unrelated id that merely contains the characters must NOT be
		// captured — the failure mode a plain Contains check would have.
		{"some-o1-lookalike", 0},
		{"model-o1", 0},

		// Gemini was already clean — the `gemini-3` prefix catches every point
		// release, which is the property the Claude cases were missing.
		{"gemini-3.1-flash-lite-preview", 65536},
		{"gemini-3.5-flash", 65536},
		{"gemini-3.6-flash", 65536},

		// Pre-4 Claude keeps its existing values — this change adds a band, it does
		// not re-tune the ones already there. (3.7 sits on the generic claude-3
		// case at 4096, below what it actually supports; it retired 2026-02-19, so
		// that is left alone rather than widening this fix.)
		{"claude-3-7-sonnet", 4096},
		{"claude-3-opus", 4096},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetLlmMaxOutputTokens(tt.model))
		})
	}
}

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
