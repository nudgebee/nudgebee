package proxy

import (
	"net/http/httptest"
	"testing"

	"nudgebee/llm-gateway/config"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secretBody = `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"my key is AKIAIOSFODNN7EXAMPLE"}]}`

func newFilterRC(body string) (*RequestContext, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)
	return &RequestContext{Gin: c, Provider: schemas.Anthropic, Body: []byte(body)}, rec
}

func withMode(t *testing.T, mode string) {
	prev := config.Config.EgressFilterMode
	config.Config.EgressFilterMode = mode
	t.Cleanup(func() { config.Config.EgressFilterMode = prev })
}

func TestFilterStage_OffAndClean(t *testing.T) {
	withMode(t, "") // off
	rc, _ := newFilterRC(secretBody)
	stop, err := filterStage{}.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)
	assert.Nil(t, rc.DLP)

	withMode(t, "detect")
	clean, _ := newFilterRC(`{"messages":[{"role":"user","content":"hello there"}]}`)
	stop, _ = filterStage{}.Handle(clean)
	assert.False(t, stop)
	assert.Nil(t, clean.DLP)
}

func TestFilterStage_DetectSignalsButAllows(t *testing.T) {
	withMode(t, "detect")
	rc, rec := newFilterRC(secretBody)
	stop, err := filterStage{}.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop, "detect must not block")
	require.NotNil(t, rc.DLP)
	assert.Equal(t, "detect", rc.DLP["mode"])
	assert.Contains(t, rc.DLP["rules"], "aws-access-key-id")
	assert.Contains(t, rec.Header().Get("x-nb-llm-dlp"), "aws-access-key-id")
}

func TestFilterStage_EnforceBlocks(t *testing.T) {
	withMode(t, "enforce")
	rc, rec := newFilterRC(secretBody)
	stop, err := filterStage{}.Handle(rc)
	require.NoError(t, err)
	assert.True(t, stop, "enforce must block")
	assert.Equal(t, "secret_blocked", rc.RejectReason)
	assert.Equal(t, 403, rec.Code)
	assert.Contains(t, rec.Body.String(), "secret")
}

func TestFilterStage_RedactRewritesBody(t *testing.T) {
	withMode(t, "redact")
	rc, _ := newFilterRC(secretBody)
	stop, err := filterStage{}.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop, "redact forwards")
	assert.NotContains(t, string(rc.Body), "AKIAIOSFODNN7EXAMPLE", "secret must be gone from the forwarded body")
	assert.Contains(t, string(rc.Body), "[REDACTED:aws-access-key-id]")
	assert.Equal(t, true, rc.DLP["redacted"])
}
