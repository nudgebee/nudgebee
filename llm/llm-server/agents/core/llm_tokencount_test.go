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
