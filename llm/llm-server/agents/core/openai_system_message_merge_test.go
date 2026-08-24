package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// messageRecordingModel captures the messages its GenerateContent received, so a test can assert
// what the inner client would have seen on the wire.
type messageRecordingModel struct{ messages []llms.MessageContent }

func (m *messageRecordingModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	m.messages = messages
	return &llms.ContentResponse{}, nil
}

func (m *messageRecordingModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", nil
}

// TestMergeSystemMessages_CollapsesToOne: multiple system messages interleaved with a human
// message are joined into a single system message at the first one's position, in order, leaving
// the human message untouched — this is the shape planner_react_3.go actually sends.
func TestMergeSystemMessages_CollapsesToOne(t *testing.T) {
	inner := &messageRecordingModel{}
	w := wrapMergeSystemMessages(inner)

	_, err := w.GenerateContent(context.Background(), []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "base prompt"),
		llms.TextParts(llms.ChatMessageTypeSystem, "account context"),
		llms.TextParts(llms.ChatMessageTypeSystem, "agent prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "what is broken?"),
	})
	require.NoError(t, err)

	require.Len(t, inner.messages, 2, "the three system messages must collapse into one")
	assert.Equal(t, llms.ChatMessageTypeSystem, inner.messages[0].Role)
	require.Len(t, inner.messages[0].Parts, 1)
	assert.Equal(t, "base prompt\n\naccount context\n\nagent prompt", inner.messages[0].Parts[0].(llms.TextContent).Text)

	assert.Equal(t, llms.ChatMessageTypeHuman, inner.messages[1].Role)
	require.Len(t, inner.messages[1].Parts, 1)
	assert.Equal(t, "what is broken?", inner.messages[1].Parts[0].(llms.TextContent).Text)
}

// TestMergeSystemMessages_SingleSystemMessagePassesThroughUnchanged: the common case (one system
// message) must not be rebuilt — same slice, same content.
func TestMergeSystemMessages_SingleSystemMessagePassesThroughUnchanged(t *testing.T) {
	inner := &messageRecordingModel{}
	w := wrapMergeSystemMessages(inner)

	original := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, "base prompt"),
		llms.TextParts(llms.ChatMessageTypeHuman, "hello"),
	}
	_, err := w.GenerateContent(context.Background(), original)
	require.NoError(t, err)
	assert.Equal(t, original, inner.messages)
}

// TestMergeSystemMessages_NoSystemMessages: nothing to merge, messages pass through unchanged.
func TestMergeSystemMessages_NoSystemMessages(t *testing.T) {
	inner := &messageRecordingModel{}
	w := wrapMergeSystemMessages(inner)

	original := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hello")}
	_, err := w.GenerateContent(context.Background(), original)
	require.NoError(t, err)
	assert.Equal(t, original, inner.messages)
}

// TestWrapMergeSystemMessages_NilPassthrough: a nil model is returned unchanged.
func TestWrapMergeSystemMessages_NilPassthrough(t *testing.T) {
	assert.Nil(t, wrapMergeSystemMessages(nil))
}
