package proxy

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm-gateway/engine"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/routing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRetryable(t *testing.T) {
	for _, s := range []int{429, 500, 502, 503, 504} {
		assert.True(t, isRetryable(s), "status %d should be retryable", s)
	}
	for _, s := range []int{200, 400, 401, 403, 404, 413, 422, 501} {
		assert.False(t, isRetryable(s), "status %d should not be retryable", s)
	}
}

func TestFailoverTarget(t *testing.T) {
	// Empty provider = the addressed provider; model passes through.
	p, m := failoverTarget("anthropic", routing.Endpoint{Model: "claude-haiku-4-5"})
	assert.Equal(t, schemas.Anthropic, p)
	assert.Equal(t, "claude-haiku-4-5", m)

	// Explicit cross-provider target.
	p, m = failoverTarget("anthropic", routing.Endpoint{Provider: "gemini", Model: "gemini-3.1-flash"})
	assert.Equal(t, schemas.Gemini, p)
	assert.Equal(t, "gemini-3.1-flash", m)
}

func TestTryFailoverUnary_NoAttemptWhenIneligible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sink := &metering.CapturingSink{}
	h := &handler{sink: sink} // no client needed — must not dispatch

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()

	// Not retryable, even with a fallback configured.
	rm := &reqMeta{decision: routing.Decision{RequestedProvider: "anthropic", Fallbacks: []routing.Endpoint{{Provider: "gemini", Model: "g"}}}}
	assert.False(t, h.tryFailoverUnary(c, bctx, rm, 400))

	// Retryable, but no fallbacks configured.
	rm = &reqMeta{decision: routing.Decision{RequestedProvider: "anthropic"}}
	assert.False(t, h.tryFailoverUnary(c, bctx, rm, 429))

	assert.Empty(t, sink.Events(), "an ineligible failover must not meter anything")
}

func TestTryFailoverUnary_AllFallbacksFailNoWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eng, err := engine.New(context.Background(), nil) // no providers configured
	require.NoError(t, err)
	defer eng.Shutdown()

	sink := &metering.CapturingSink{}
	h := &handler{client: eng.Client, sink: sink}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()

	rm := &reqMeta{
		body:  []byte(`{"model":"claude-opus-4-8","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`),
		start: time.Now(),
		decision: routing.Decision{
			RequestedProvider: "anthropic", RequestedModel: "claude-opus-4-8",
			Fallbacks: []routing.Endpoint{{Provider: "bogus-unconfigured-provider", Model: "x"}},
		},
	}
	// The single fallback dispatches to an unconfigured provider and fails; failover
	// returns false so the caller surfaces the original primary error. A failed
	// attempt writes nothing and meters nothing (only a served fallback would).
	assert.False(t, h.tryFailoverUnary(c, bctx, rm, 429))
	assert.Empty(t, rec.Body.String(), "a failed fallback attempt must not write a response")
	assert.Empty(t, sink.Events(), "a failed fallback attempt must not meter")
}
