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
	"nudgebee/llm-gateway/routing"

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

	sink := &metering.CapturingSink{}
	h := &handler{sink: sink} // resolution fails before the pipeline/engine are touched

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", chatCompletionsPath, strings.NewReader(`{"model":"mystery-model-9000"}`))

	h.handleChat(c)

	require.Equal(t, 400, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid_request")
	// The rejection is recorded for the audit trail (status + reason).
	require.Len(t, sink.Events(), 1)
	assert.Equal(t, 400, sink.Events()[0].StatusCode)
	assert.Contains(t, sink.Events()[0].Attributes, "unknown_model")
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

// TestHandleChat_HonorsSubstitution locks that a cross-provider substitution rule
// takes effect on the /v1 generic endpoint too (not just native mounts): the request
// dispatches to the resolved target, the swap is signalled, and the usage row is
// attributed to the target with reason=substitute on the generic surface.
func TestHandleChat_HonorsSubstitution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prev := config.Config.MaxRequestBodyBytes
	config.Config.MaxRequestBodyBytes = 1 << 20
	defer func() { config.Config.MaxRequestBodyBytes = prev }()

	eng, err := engine.New(context.Background(), nil)
	require.NoError(t, err)
	defer eng.Shutdown()

	// A rule substituting openai/gpt-5 → an unconfigured provider (so dispatch fails
	// fast, but only AFTER the substitution has been applied and metered).
	router := routing.NewEngine([]routing.Rule{{
		ID: "sub", Enabled: true,
		Match:  routing.Match{Provider: "openai", Model: "gpt-5"},
		Target: routing.Target{Endpoint: routing.Endpoint{Provider: "bogus-unconfigured", Model: "x"}},
	}})
	sink := &metering.CapturingSink{}
	h := &handler{client: eng.Client, sink: sink, pipeline: NewPipeline(routeStage{router: router})}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", chatCompletionsPath, strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`))

	h.handleChat(c)

	assert.Equal(t, "openai->bogus-unconfigured", rec.Header().Get("x-nb-llm-substituted"))
	events := sink.Events()
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, "bogus-unconfigured", ev.Provider, "dispatched + metered against the substitution target")
	assert.Equal(t, "openai", ev.RequestedProvider)
	assert.Equal(t, "substitute", ev.RoutingReason)
	assert.Contains(t, ev.Attributes, `"surface":"generic"`) // arrived on /v1, still substituted
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
