package core

import (
	"context"
	"log/slog"
	"nudgebee/llm/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
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
			result := SmartTruncateToolOutput(tt.content, tt.maxBytes)
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

func TestApplyPreflightMessageSizeCap(t *testing.T) {
	ctx := security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil)

	const largeSize = 1024 * 1024
	messages := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: strings.Repeat("X", largeSize)}, // 1MB
			},
		},
	}

	// Default is 512KB now
	capped := applyPreflightMessageSizeCap(ctx, messages, "test-agent")

	assert.Equal(t, len(messages), len(capped))
	textPart := capped[0].Parts[0].(llms.TextContent)
	assert.LessOrEqual(t, len(textPart.Text), 512*1024, "Result must not exceed the default cap")
	assert.Contains(t, textPart.Text, "TRUNCATED")
}
