package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestMergeSystemMessages_CollapsesToOne: multiple system messages interleaved with a human
// message are joined into a single system message at the first one's position, in order, leaving
// the human message untouched — this is the shape planner_react_3.go actually sends.
func TestMergeSystemMessages_CollapsesToOne(t *testing.T) {
	got := mergeSystemMessageContents([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "base prompt"),
		llms.TextParts(llms.ChatMessageTypeSystem, "account context"),
		llms.TextParts(llms.ChatMessageTypeSystem, "agent prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "what is broken?"),
	})

	require.Len(t, got, 2, "the three system messages must collapse into one")
	assert.Equal(t, llms.ChatMessageTypeSystem, got[0].Role)
	require.Len(t, got[0].Parts, 1)
	assert.Equal(t, "base prompt\n\naccount context\n\nagent prompt", got[0].Parts[0].(llms.TextContent).Text)

	assert.Equal(t, llms.ChatMessageTypeHuman, got[1].Role)
	require.Len(t, got[1].Parts, 1)
	assert.Equal(t, "what is broken?", got[1].Parts[0].(llms.TextContent).Text)
}

// TestMergeSystemMessages_SingleSystemMessagePassesThroughUnchanged: the common case (one system
// message) must not be rebuilt — same slice, same content.
func TestMergeSystemMessages_SingleSystemMessagePassesThroughUnchanged(t *testing.T) {
	original := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "base prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "hello"),
	}
	assert.Equal(t, original, mergeSystemMessageContents(original))
}

// TestMergeSystemMessages_NoSystemMessages: nothing to merge, messages pass through unchanged.
func TestMergeSystemMessages_NoSystemMessages(t *testing.T) {
	original := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")}
	assert.Equal(t, original, mergeSystemMessageContents(original))
}

// TestMergeSystemMessages_SkipsEmptyText: an empty-text system message contributes nothing to the
// join, so it doesn't leave stray blank lines in the merged prompt.
func TestMergeSystemMessages_SkipsEmptyText(t *testing.T) {
	got := mergeSystemMessageContents([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "base prompt"),
		llms.TextParts(llms.ChatMessageTypeSystem, ""),
		llms.TextParts(llms.ChatMessageTypeSystem, "agent prompt"),
	})

	require.Len(t, got, 1)
	require.Len(t, got[0].Parts, 1)
	assert.Equal(t, "base prompt\n\nagent prompt", got[0].Parts[0].(llms.TextContent).Text)
}

// TestMergeSystemMessages_AllEmptyKeepsOnePart: when every merged system message is empty text
// with no other parts, the merged message still carries one (empty) TextContent part rather than
// zero — some providers reject a message with no content parts at all.
func TestMergeSystemMessages_AllEmptyKeepsOnePart(t *testing.T) {
	got := mergeSystemMessageContents([]llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, ""),
		llms.TextParts(llms.ChatMessageTypeSystem, ""),
	})

	require.Len(t, got, 1)
	require.Len(t, got[0].Parts, 1)
	assert.Equal(t, "", got[0].Parts[0].(llms.TextContent).Text)
}

// TestGenerateContent_MergedSystemMessagesOption: only a client built WithMergedSystemMessages
// sends one system message; a plain client sends them as they came.
func TestGenerateContent_MergedSystemMessagesOption(t *testing.T) {
	t.Parallel()

	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body.Messages = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	msgs := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "base prompt"),
		llms.TextParts(llms.ChatMessageTypeSystem, "agent prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "hi"),
	}

	plain, err := New(WithToken("test-key"), WithBaseURL(server.URL), WithModel("test-model"))
	require.NoError(t, err)
	_, err = plain.GenerateContent(context.Background(), msgs)
	require.NoError(t, err)
	assert.Len(t, body.Messages, 3, "a plain client keeps every system message")

	merging, err := New(WithToken("test-key"), WithBaseURL(server.URL), WithModel("test-model"), WithMergedSystemMessages())
	require.NoError(t, err)
	_, err = merging.GenerateContent(context.Background(), msgs)
	require.NoError(t, err)
	require.Len(t, body.Messages, 2, "the two system messages are sent as one")
	assert.Equal(t, "system", body.Messages[0]["role"])
}
