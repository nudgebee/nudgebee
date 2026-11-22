package runtime

import (
	"context"
	"log/slog"
	"nudgebee/llm/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

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
