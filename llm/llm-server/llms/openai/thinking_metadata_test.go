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

// applyThenStrip applies the caller's options and then the strip, in the order GenerateContent does.
func applyThenStrip(options ...llms.CallOption) llms.CallOptions {
	opts := llms.CallOptions{}
	for _, o := range options {
		o(&opts)
	}
	stripThinkingMetadataOption(&opts)
	return opts
}

// TestStripInternalThinkingMetadata_DoesNotMutateCallerMap: the strip must CLONE, never delete
// in place — an in-place delete would corrupt a caller's shared/reused Metadata map and risk a
// fatal concurrent-map access. The caller's original map must survive untouched.
func TestStripInternalThinkingMetadata_DoesNotMutateCallerMap(t *testing.T) {
	shared := map[string]any{metaKeyThinkingBudget: 4000, metaKeyThinkingLevel: "low", "trace": "keep-me"}
	setShared := func(o *llms.CallOptions) { o.Metadata = shared } // reference, not a copy

	opts := applyThenStrip(setShared)

	// Caller's original map is untouched (all three keys still present)...
	assert.Contains(t, shared, metaKeyThinkingBudget, "caller's map must not be mutated in place")
	assert.Contains(t, shared, metaKeyThinkingLevel)
	assert.Len(t, shared, 3)
	// ...while the request gets a cleaned copy.
	assert.NotContains(t, opts.Metadata, metaKeyThinkingBudget)
	assert.NotContains(t, opts.Metadata, metaKeyThinkingLevel)
	assert.Equal(t, "keep-me", opts.Metadata["trace"])
}

// TestStripInternalThinkingMetadata_NoThinkingKeys: when neither internal key is present, the
// metadata passes through unchanged (the strip takes the early-return, no-copy path).
func TestStripInternalThinkingMetadata_NoThinkingKeys(t *testing.T) {
	original := map[string]any{"trace": "keep", "n": 5}
	opts := applyThenStrip(func(o *llms.CallOptions) { o.Metadata = original })
	assert.Equal(t, original, opts.Metadata)
}

// TestGenerateContent_StripsInternalThinkingMetadata: the internal keys never reach the wire, and
// when they were the only metadata no `metadata` field is sent at all.
func TestGenerateContent_StripsInternalThinkingMetadata(t *testing.T) {
	t.Parallel()

	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	llm, err := New(WithToken("test-key"), WithBaseURL(server.URL), WithModel("test-model"))
	require.NoError(t, err)
	msgs := []llms.MessageContent{llms.TextParts(llms.ChatMessageTypeHuman, "hi")}

	_, err = llm.GenerateContent(context.Background(), msgs,
		llms.WithMetadata(map[string]any{metaKeyThinkingBudget: 4000, metaKeyThinkingLevel: "low", "trace": "keep-me"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"trace": "keep-me"}, body["metadata"])

	_, err = llm.GenerateContent(context.Background(), msgs,
		llms.WithMetadata(map[string]any{metaKeyThinkingBudget: 4000}))
	require.NoError(t, err)
	assert.NotContains(t, body, "metadata", "metadata holding only internal keys is dropped")
}
