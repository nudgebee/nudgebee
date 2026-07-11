package proxy

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm-gateway/engine"
	"nudgebee/llm-gateway/metering"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStream_MetersOnPreHeaderFailure locks review Finding 1: a streaming request
// that fails before the first chunk must still record a metering row (all traffic
// is audited). Uses a provider that is not statically configured and not
// auto-initialisable, so core fails fast without any network call.
func TestStream_MetersOnPreHeaderFailure(t *testing.T) {
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
	req := &schemas.BifrostPassthroughRequest{
		Provider: schemas.ModelProvider("bogus-unconfigured-provider"),
		Method:   "POST",
		Path:     "/v1/messages",
	}
	rm := &reqMeta{provider: req.Provider, req: req, streaming: true, start: time.Now()}
	h.stream(c, bctx, cancel, rm)

	events := sink.Events()
	require.Len(t, events, 1, "pre-header failure must still emit exactly one metering row")
	ev := events[0]
	assert.True(t, ev.Streaming)
	assert.Equal(t, "bogus-unconfigured-provider", ev.Provider)
	assert.NotEqual(t, 200, ev.StatusCode, "failed stream must not record a 200")
	assert.Zero(t, ev.TotalTokens)
}
