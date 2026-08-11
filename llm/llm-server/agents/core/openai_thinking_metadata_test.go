package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// recordingModel captures the CallOptions its GenerateContent received (after applying them),
// so a test can assert what the inner client would have seen.
type recordingModel struct{ opts llms.CallOptions }

func (m *recordingModel) GenerateContent(_ context.Context, _ []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	for _, o := range options {
		o(&m.opts)
	}
	return &llms.ContentResponse{}, nil
}

func (m *recordingModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", nil
}

// TestStripInternalThinkingMetadata: the decorator removes the llm-server-internal
// ThinkingBudget/ThinkingLevel keys (which WithThinkingBudget/WithThinkingLevel set) from the
// Metadata the inner OpenAI client sees, while leaving other metadata intact — so strict
// providers never receive non-string metadata.
func TestStripInternalThinkingMetadata(t *testing.T) {
	inner := &recordingModel{}
	w := wrapStripInternalThinkingMetadata(inner)

	// Add "trace" additively (not via llms.WithMetadata, which may replace the map) so all three
	// keys are present when the strip option runs — otherwise the assertion could pass vacuously.
	addTrace := func(o *llms.CallOptions) {
		if o.Metadata == nil {
			o.Metadata = map[string]any{}
		}
		o.Metadata["trace"] = "keep-me"
	}
	_, err := w.GenerateContent(context.Background(), nil,
		WithThinkingBudget(4000), // → Metadata["ThinkingBudget"] = 4000 (int)
		WithThinkingLevel("low"), // → Metadata["ThinkingLevel"] = "low"
		addTrace,
	)
	require.NoError(t, err)

	_, hasBudget := inner.opts.Metadata[metaKeyThinkingBudget]
	_, hasLevel := inner.opts.Metadata[metaKeyThinkingLevel]
	assert.False(t, hasBudget, "ThinkingBudget must be stripped before reaching the OpenAI client")
	assert.False(t, hasLevel, "ThinkingLevel must be stripped before reaching the OpenAI client")
	assert.Equal(t, "keep-me", inner.opts.Metadata["trace"], "unrelated metadata is preserved")
}

// TestStripInternalThinkingMetadata_DoesNotMutateCallerMap: the strip must CLONE, never delete
// in place — an in-place delete would corrupt a caller's shared/reused Metadata map and risk a
// fatal concurrent-map access. The caller's original map must survive untouched.
func TestStripInternalThinkingMetadata_DoesNotMutateCallerMap(t *testing.T) {
	shared := map[string]any{metaKeyThinkingBudget: 4000, metaKeyThinkingLevel: "low", "trace": "keep-me"}
	setShared := func(o *llms.CallOptions) { o.Metadata = shared } // reference, not a copy

	inner := &recordingModel{}
	_, err := wrapStripInternalThinkingMetadata(inner).GenerateContent(context.Background(), nil, setShared)
	require.NoError(t, err)

	// Caller's original map is untouched (all three keys still present)...
	assert.Contains(t, shared, metaKeyThinkingBudget, "caller's map must not be mutated in place")
	assert.Contains(t, shared, metaKeyThinkingLevel)
	assert.Len(t, shared, 3)
	// ...while the inner client received a cleaned copy.
	assert.NotContains(t, inner.opts.Metadata, metaKeyThinkingBudget)
	assert.NotContains(t, inner.opts.Metadata, metaKeyThinkingLevel)
	assert.Equal(t, "keep-me", inner.opts.Metadata["trace"])
}

// TestStripInternalThinkingMetadata_NoThinkingKeys: when neither internal key is present, the
// metadata passes through unchanged (the strip takes the early-return, no-copy path).
func TestStripInternalThinkingMetadata_NoThinkingKeys(t *testing.T) {
	original := map[string]any{"trace": "keep", "n": 5}
	setMeta := func(o *llms.CallOptions) { o.Metadata = original }

	inner := &recordingModel{}
	_, err := wrapStripInternalThinkingMetadata(inner).GenerateContent(context.Background(), nil, setMeta)
	require.NoError(t, err)
	assert.Equal(t, original, inner.opts.Metadata)
}

// TestStripInternalThinkingMetadata_OnlyInternalKeys: when the internal keys are the ONLY
// metadata entries, Metadata is dropped to nil (not left as an empty {} on the wire).
func TestStripInternalThinkingMetadata_OnlyInternalKeys(t *testing.T) {
	inner := &recordingModel{}
	_, err := wrapStripInternalThinkingMetadata(inner).GenerateContent(context.Background(), nil,
		WithThinkingBudget(4000),
		WithThinkingLevel("low"),
	)
	require.NoError(t, err)
	assert.Nil(t, inner.opts.Metadata, "metadata containing only internal keys must be dropped to nil")
}

// TestWrapStripInternalThinkingMetadata_NilPassthrough: a nil model is returned unchanged.
func TestWrapStripInternalThinkingMetadata_NilPassthrough(t *testing.T) {
	assert.Nil(t, wrapStripInternalThinkingMetadata(nil))
}
