package bedrock

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestProcessMessages_CachedContent(t *testing.T) {
	cc := &llms.CacheControl{Type: "ephemeral"}
	messages := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
			llms.WithCacheControl(llms.TextContent{Text: "system"}, cc),
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "question"}}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.WithCacheControl(llms.ToolCallResponse{ToolCallID: "t1", Name: "kubectl", Content: "ok"}, cc),
		}},
	}

	out, err := processMessages(context.Background(), messages)
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, "system", out[0].Content)
	assert.Same(t, cc, out[0].CacheControl)
	assert.Nil(t, out[1].CacheControl)
	assert.Equal(t, "tool_call_response", out[2].Type)
	assert.Contains(t, out[2].Content, `"tool_call_id":"t1"`)
	assert.Same(t, cc, out[2].CacheControl)
}
