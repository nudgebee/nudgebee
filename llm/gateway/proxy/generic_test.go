package proxy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/engine"
	"nudgebee/llm-gateway/metering"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleChat_UnresolvableModelIs400 locks the edge guard: a model name that
// maps to no provider is rejected with an OpenAI-shaped 400 before any dispatch.
func TestHandleChat_UnresolvableModelIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prev := config.Config.MaxRequestBodyBytes
	config.Config.MaxRequestBodyBytes = 1 << 20
	defer func() { config.Config.MaxRequestBodyBytes = prev }()

	h := &handler{} // resolution fails before the pipeline/engine are touched

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", chatCompletionsPath, strings.NewReader(`{"model":"mystery-model-9000"}`))

	h.handleChat(c)

	require.Equal(t, 400, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_request")
}

// TestMarshalOpenAIChat_StripsExtraFields locks that Bifrost's internal extra_fields
// annotation (operator provider org/project ids + raw provider headers) never leaks
// to the client on the generic endpoint.
func TestMarshalOpenAIChat_StripsExtraFields(t *testing.T) {
	resp := &schemas.BifrostChatResponse{
		ID:     "chatcmpl-x",
		Object: "chat.completion",
		Model:  "claude-opus-4-8",
		ExtraFields: schemas.BifrostResponseExtraFields{
			Provider:          schemas.Anthropic,
			ResolvedModelUsed: "claude-opus-4-8",
		},
	}
	out, err := marshalOpenAIChat(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "extra_fields")
	assert.NotContains(t, string(out), "resolved_model_used")
	assert.Contains(t, string(out), `"object":"chat.completion"`)
}

// TestStreamChat_MetersOnPreHeaderFailure locks that a streaming chat request which
// fails before the first chunk still records exactly one metering row (all traffic
// is audited). Uses an unconfigured provider so the engine fails fast with no
// network call, mirroring the passthrough stream test.
func TestStreamChat_MetersOnPreHeaderFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	eng, err := engine.New(context.Background(), nil) // no providers configured
	require.NoError(t, err)
	defer eng.Shutdown()

	sink := &metering.CapturingSink{}
	h := &handler{client: eng.Client, sink: sink}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", chatCompletionsPath, strings.NewReader(`{"model":"x"}`))

	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	rc := &RequestContext{
		Gin: c, Ctx: c.Request.Context(), Bctx: bctx,
		Provider: schemas.ModelProvider("bogus-unconfigured-provider"),
		Model:    "x", Path: chatCompletionsPath, Body: []byte(`{"model":"x"}`), Streaming: true,
	}
	rm := &reqMeta{provider: rc.Provider, model: rc.Model, method: "POST", path: chatCompletionsPath, streaming: true, start: time.Now()}
	h.streamChat(c, bctx, cancel, rc, rm)

	events := sink.Events()
	require.Len(t, events, 1, "pre-header failure must still emit exactly one metering row")
	ev := events[0]
	assert.True(t, ev.Streaming)
	assert.Equal(t, "bogus-unconfigured-provider", ev.Provider)
	assert.NotEqual(t, 200, ev.StatusCode, "failed stream must not record a 200")
	assert.Zero(t, ev.TotalTokens)
}
