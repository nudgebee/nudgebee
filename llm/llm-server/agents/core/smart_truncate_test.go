package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	llmruntime "nudgebee/llm/llms/runtime"
)

func TestSmartTruncateToolOutput(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxBytes int
		contains []string
	}{
		{
			name:     "Small content, no truncation",
			content:  "small content",
			maxBytes: 100,
			contains: []string{"small content"},
		},
		{
			name:     "Truncation needed",
			content:  strings.Repeat("A", 2000) + "MIDDLE" + strings.Repeat("B", 2000),
			maxBytes: 1500,
			contains: []string{"TRUNCATED", "please use specific filters"},
		},
		{
			name:     "Very small maxBytes",
			content:  "some relatively long content",
			maxBytes: 5,
			contains: []string{""}, // Should return empty string when maxBytes < len(suffix)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := llmruntime.SmartTruncateToolOutput(tt.content, tt.maxBytes)
			if len(tt.content) > tt.maxBytes {
				assert.LessOrEqual(t, len(result), tt.maxBytes, "Result must not exceed maxBytes")
				for _, c := range tt.contains {
					assert.Contains(t, strings.ToLower(result), strings.ToLower(c))
				}
			} else {
				assert.Equal(t, tt.content, result)
			}
		})
	}
}
