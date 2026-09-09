package googleai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

// Gemini 3.x rejects a request whose history replays a functionCall part without
// the thoughtSignature it originally returned ("Function call is missing a
// thought_signature in functionCall parts"). These cover the two hops that make
// the signature survive: reading the replay map off CallOptions, and attaching
// it to the converted genai part.

func TestThoughtSignaturesFromOptions(t *testing.T) {
	want := map[string][]byte{"call_1": []byte("sig")}
	got := thoughtSignaturesFromOptions(&llms.CallOptions{
		Metadata: map[string]any{MetadataThoughtSignatures: want},
	})
	assert.Equal(t, want, got)
}

func TestThoughtSignaturesFromOptions_AbsentOrWrongTypeIsNil(t *testing.T) {
	assert.Nil(t, thoughtSignaturesFromOptions(nil))
	assert.Nil(t, thoughtSignaturesFromOptions(&llms.CallOptions{}))
	assert.Nil(t, thoughtSignaturesFromOptions(&llms.CallOptions{Metadata: map[string]any{}}))
	// Must degrade, not panic: Metadata is a shared bag other layers write to
	// (CachedContentName, ThinkingLevel, ThinkingBudget).
	assert.Nil(t, thoughtSignaturesFromOptions(&llms.CallOptions{
		Metadata: map[string]any{MetadataThoughtSignatures: "wrong-type"},
	}))
}

func TestHasAnyThoughtSignature(t *testing.T) {
	assert.False(t, hasAnyThoughtSignature(nil))
	assert.False(t, hasAnyThoughtSignature([][]byte{nil, {}}))
	assert.True(t, hasAnyThoughtSignature([][]byte{nil, []byte("sig")}))
}

func TestConvertParts_AttachesThoughtSignatureToFunctionCall(t *testing.T) {
	parts := []llms.ContentPart{
		llms.ToolCall{
			ID:           "call_1",
			Type:         "function",
			FunctionCall: &llms.FunctionCall{Name: "kubectl_execute", Arguments: `{"command":"get pods"}`},
		},
	}
	converted, err := convertParts(context.Background(), parts,
		map[string][]byte{"call_1": []byte("opaque-sig")})

	assert.NoError(t, err)
	assert.Len(t, converted, 1)
	assert.NotNil(t, converted[0].FunctionCall)
	assert.Equal(t, "kubectl_execute", converted[0].FunctionCall.Name)
	assert.Equal(t, []byte("opaque-sig"), converted[0].ThoughtSignature)
}

// A tool call with no known signature must still convert — non-thinking models
// and non-Gemini providers never produce one.
func TestConvertParts_NoSignatureStillConverts(t *testing.T) {
	parts := []llms.ContentPart{
		llms.ToolCall{
			ID:           "call_1",
			Type:         "function",
			FunctionCall: &llms.FunctionCall{Name: "kubectl_execute", Arguments: `{"command":"get pods"}`},
		},
	}
	for _, signatures := range []map[string][]byte{nil, {"other_id": []byte("sig")}} {
		converted, err := convertParts(context.Background(), parts, signatures)
		assert.NoError(t, err)
		assert.Len(t, converted, 1)
		assert.Nil(t, converted[0].ThoughtSignature)
	}
}
