package proxy

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandle_RejectsOversizedBody locks review Finding 3: a request body over the
// configured limit is rejected with 413 before any provider call. No engine is
// needed because the limit is enforced at the edge, ahead of the passthrough.
func TestHandle_RejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prev := config.Config.MaxRequestBodyBytes
	config.Config.MaxRequestBodyBytes = 1024 // 1 KiB for the test
	defer func() { config.Config.MaxRequestBodyBytes = prev }()

	h := &handler{client: nil, sink: &metering.CapturingSink{}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	oversized := bytes.Repeat([]byte("x"), 4096)
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", bytes.NewReader(oversized))

	h.handle(c)

	require.Equal(t, 413, rec.Code)
	assert.Contains(t, rec.Body.String(), "request_too_large")
}

func TestHandle_UnknownPrefixIs404(t *testing.T) {
	// Routing guard: an unmounted prefix is rejected before any body read or
	// provider call (client stays nil, never dereferenced).
	gin.SetMode(gin.TestMode)
	h := &handler{client: nil, sink: &metering.CapturingSink{}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/unknownprefix/v1/x", bytes.NewReader([]byte(`{"ok":true}`)))

	h.handle(c)
	assert.Equal(t, 404, rec.Code)
}
