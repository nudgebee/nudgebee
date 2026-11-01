package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// Follow-up suggestions are a short JSON list; without an explicit level the model
// spent 520-608 thinking tokens on 66 output tokens (#39745).
func TestConversationSuggestionCallOptions_UseFastThinking(t *testing.T) {
	combined := llms.CallOptions{}
	for _, opt := range conversationSuggestionCallOptions() {
		opt(&combined)
	}
	assert.Equal(t, ThinkingLevelFastTask, combined.Metadata["ThinkingLevel"])
	assert.True(t, combined.JSONMode, "the response is parsed as JSON")
	assert.InDelta(t, 0.7, combined.Temperature, 1e-9)
}

func TestStoredConversationSuggestions(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	request := ConversationSuggestionRequest{ConversationId: "c1", AccountId: "a1", MessageId: "m1"}
	str := func(s string) *string { return &s }

	t.Run("stored suggestions are returned", func(t *testing.T) {
		got, ok := StoredConversationSuggestions(ctx, request, ConversationMessage{Suggestions: str(`[{"message":"Why is the pod restarting?"}]`)})
		assert.True(t, ok)
		assert.Equal(t, ConversationSuggestionResponse{
			ConversationId: "c1",
			AccountId:      "a1",
			MessageId:      "m1",
			Suggestions:    []ConversationSuggestionMessageResponse{{Message: "Why is the pod restarting?"}},
		}, got)
	})

	for name, stored := range map[string]*string{
		"no stored suggestions": nil,
		"empty list":            str(`[]`),
		"unparseable":           str(`not json`),
	} {
		t.Run(name+" falls through to generation", func(t *testing.T) {
			_, ok := StoredConversationSuggestions(ctx, request, ConversationMessage{Suggestions: stored})
			assert.False(t, ok)
		})
	}
}
