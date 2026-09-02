package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleEmbeddings_UnresolvableModelIs400 locks the edge guard on the embeddings
// endpoint: a model that maps to no provider (and no tenant custom upstream) is
// rejected with an OpenAI-shaped 400 before any dispatch, and the reject is metered.
func TestHandleEmbeddings_UnresolvableModelIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prev := config.Config.MaxRequestBodyBytes
	config.Config.MaxRequestBodyBytes = 1 << 20
	t.Cleanup(func() { config.Config.MaxRequestBodyBytes = prev })

	sink := &metering.CapturingSink{}
	h := &handler{sink: sink} // resolution fails before the pipeline/engine are touched

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", embeddingsPath, strings.NewReader(`{"model":"mystery-embed-9000","input":"hi"}`))

	h.handleEmbeddings(c)

	require.Equal(t, 400, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_request")
	require.Len(t, sink.Events(), 1)
	assert.Equal(t, 400, sink.Events()[0].StatusCode)
	assert.Contains(t, sink.Events()[0].Attributes, "unknown_model")
}

// TestMarshalOpenAIEmbedding_StripsExtraFields locks that the response is emitted in
// clean OpenAI shape (data/model/object/usage) and Bifrost's internal extra_fields
// annotation never leaks to the client.
func TestMarshalOpenAIEmbedding_StripsExtraFields(t *testing.T) {
	resp := &schemas.BifrostEmbeddingResponse{
		Object: "list",
		Model:  "text-embedding-3-small",
		Data: []schemas.EmbeddingData{{
			Index:     0,
			Object:    "embedding",
			Embedding: schemas.EmbeddingStruct{EmbeddingArray: []float64{0.1, 0.2}},
		}},
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 3, TotalTokens: 3},
		ExtraFields: schemas.BifrostResponseExtraFields{
			Provider:          schemas.OpenAI,
			ResolvedModelUsed: "text-embedding-3-small",
		},
	}
	out, err := marshalOpenAIEmbedding(resp)
	require.NoError(t, err)
	s := string(out)
	assert.NotContains(t, s, "extra_fields")
	assert.NotContains(t, s, "resolved_model_used")
	assert.Contains(t, s, `"object":"list"`)
	assert.Contains(t, s, `"embedding":[0.1,0.2]`)
	assert.Contains(t, s, `"model":"text-embedding-3-small"`)
}

// TestEmbeddingUsage lifts the response usage into the metering shape and is nil-safe.
func TestEmbeddingUsage(t *testing.T) {
	assert.Nil(t, embeddingUsage(nil))

	u := &schemas.BifrostLLMUsage{PromptTokens: 5, TotalTokens: 5}
	got := embeddingUsage(&schemas.BifrostEmbeddingResponse{Usage: u})
	require.NotNil(t, got)
	assert.Same(t, u, got.LLMUsage)
}
