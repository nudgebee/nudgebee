package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/llms/openai"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// newMetadataRecordingOpenAI returns an OpenAI client on a fake server and a func reporting the
// last request's `metadata` field (and whether it was sent at all).
func newMetadataRecordingOpenAI(t *testing.T) (llms.Model, func() (any, bool)) {
	t.Helper()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(server.Close)

	llm, err := openai.New(openai.WithToken("test-key"), openai.WithBaseURL(server.URL), openai.WithModel("test-model"))
	require.NoError(t, err)
	return llm, func() (any, bool) {
		m, ok := body["metadata"]
		return m, ok
	}
}

// TestStripInternalThinkingMetadata: the OpenAI client removes the llm-server-internal
// ThinkingBudget/ThinkingLevel keys (which WithThinkingBudget/WithThinkingLevel set) from the
// request's metadata, while leaving other metadata intact — so strict providers never receive
// non-string metadata.
func TestStripInternalThinkingMetadata(t *testing.T) {
	llm, metadata := newMetadataRecordingOpenAI(t)

	// Add "trace" additively (not via llms.WithMetadata, which may replace the map) so all three
	// keys are present when the strip runs — otherwise the assertion could pass vacuously.
	addTrace := func(o *llms.CallOptions) {
		if o.Metadata == nil {
			o.Metadata = map[string]any{}
		}
		o.Metadata["trace"] = "keep-me"
	}
	_, err := llm.GenerateContent(context.Background(),
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")},
		WithThinkingBudget(4000), // → Metadata["ThinkingBudget"] = 4000 (int)
		WithThinkingLevel("low"), // → Metadata["ThinkingLevel"] = "low"
		addTrace,
	)
	require.NoError(t, err)

	got, sent := metadata()
	require.True(t, sent)
	assert.Equal(t, map[string]any{"trace": "keep-me"}, got, "only unrelated metadata reaches the wire")
}

// TestStripInternalThinkingMetadata_OnlyInternalKeys: when the internal keys are the ONLY
// metadata entries, no metadata is sent (not an empty {} on the wire).
func TestStripInternalThinkingMetadata_OnlyInternalKeys(t *testing.T) {
	llm, metadata := newMetadataRecordingOpenAI(t)
	_, err := llm.GenerateContent(context.Background(),
		[]llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")},
		WithThinkingBudget(4000),
		WithThinkingLevel("low"),
	)
	require.NoError(t, err)
	_, sent := metadata()
	assert.False(t, sent, "metadata containing only internal keys must be dropped")
}
