package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLlmMaxTokenLength_GeminiModels(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		wantLimit int
	}{
		{
			name:      "Gemini 2.5 Flash Lite Preview",
			model:     "gemini-2.5-flash-lite-preview-09-2025",
			wantLimit: 1_000_000,
		},
		{
			name:      "Gemini 2.5 Flash",
			model:     "gemini-2.5-flash",
			wantLimit: 1_000_000,
		},
		{
			name:      "Gemini 2.0 Flash",
			model:     "gemini-2.0-flash",
			wantLimit: 1_048_576,
		},
		{
			name:      "Gemini 1.5 Pro",
			model:     "gemini-1.5-pro",
			wantLimit: 2_000_000,
		},
		{
			name:      "Gemini 1.5 Flash",
			model:     "gemini-1.5-flash",
			wantLimit: 1_000_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLlmMaxTokenLength(tt.model)
			assert.Equal(t, tt.wantLimit, got, "GetLlmMaxTokenLength(%s) = %d, want %d", tt.model, got, tt.wantLimit)
		})
	}
}

func TestCacheTokenLimitValidation(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		tokenCount    int32
		shouldExceed  bool
		expectedLimit int
		testRationale string
	}{
		{
			name:          "Gemini 2.5 Flash Lite - Within Limit",
			model:         "gemini-2.5-flash-lite-preview-09-2025",
			tokenCount:    500_000,
			shouldExceed:  false,
			expectedLimit: 1_000_000,
			testRationale: "Token count within 1M limit should not exceed",
		},
		{
			name:          "Gemini 2.5 Flash Lite - Exceeds Limit (Original Error Case)",
			model:         "gemini-2.5-flash-lite-preview-09-2025",
			tokenCount:    3_909_831,
			shouldExceed:  true,
			expectedLimit: 1_000_000,
			testRationale: "Original failing case with 3.9M tokens should exceed 1M limit",
		},
		{
			name:          "Gemini 2.0 Flash - At Exact Limit",
			model:         "gemini-2.0-flash",
			tokenCount:    1_048_576,
			shouldExceed:  false,
			expectedLimit: 1_048_576,
			testRationale: "Token count at exact limit should not exceed",
		},
		{
			name:          "Gemini 2.0 Flash - One Over Limit",
			model:         "gemini-2.0-flash",
			tokenCount:    1_048_577,
			shouldExceed:  true,
			expectedLimit: 1_048_576,
			testRationale: "Token count one over limit should exceed",
		},
		{
			name:          "Gemini 1.5 Pro - Large But Valid",
			model:         "gemini-1.5-pro",
			tokenCount:    1_500_000,
			shouldExceed:  false,
			expectedLimit: 2_000_000,
			testRationale: "1.5M tokens within 2M limit should not exceed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxTokens := GetLlmMaxTokenLength(tt.model)
			assert.Equal(t, tt.expectedLimit, maxTokens, "Expected token limit for %s", tt.model)

			exceeds := tt.tokenCount > int32(maxTokens)
			assert.Equal(t, tt.shouldExceed, exceeds,
				"%s: tokenCount=%d, maxTokens=%d, shouldExceed=%v but got %v",
				tt.testRationale, tt.tokenCount, maxTokens, tt.shouldExceed, exceeds)
		})
	}
}
