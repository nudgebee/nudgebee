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
		{"unknown-model", 16000},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetLlmMaxTokenLength(tt.model))
		})
	}
}
