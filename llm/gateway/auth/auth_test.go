package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/config"
)

func TestCheckConfig(t *testing.T) {
	orig := config.Config
	t.Cleanup(func() { config.Config = orig })

	t.Run("user_auths is allowed", func(t *testing.T) {
		config.Config.AuthMode = "user_auths"
		config.Config.AuthAllowInsecure = false
		assert.NoError(t, CheckConfig())
	})

	t.Run("static is allowed", func(t *testing.T) {
		config.Config.AuthMode = "static"
		config.Config.AuthAllowInsecure = false
		assert.NoError(t, CheckConfig())
	})

	t.Run("none WITHOUT ack is refused (fail-closed)", func(t *testing.T) {
		config.Config.AuthMode = "none"
		config.Config.AuthAllowInsecure = false
		assert.Error(t, CheckConfig(), "must refuse to boot as an open proxy by accident")
	})

	t.Run("none WITH explicit ack is allowed", func(t *testing.T) {
		config.Config.AuthMode = "none"
		config.Config.AuthAllowInsecure = true
		assert.NoError(t, CheckConfig())
	})
}

func TestNoneValidator_AllowsEverything(t *testing.T) {
	v := noneValidator{}
	assert.False(t, v.RequiresToken())
	id, ok := v.Validate("")
	assert.True(t, ok)
	assert.Equal(t, Identity{}, id)
}

func TestDenyValidator_RejectsEverything(t *testing.T) {
	v := denyValidator{}
	assert.True(t, v.RequiresToken())
	_, ok := v.Validate("anything")
	assert.False(t, ok)
}

func TestStaticValidator(t *testing.T) {
	v := &staticValidator{
		token:    "secret-tok",
		identity: Identity{TenantID: "t1", UserID: "u1", TokenID: "static"},
	}
	require.True(t, v.RequiresToken())

	id, ok := v.Validate("secret-tok")
	require.True(t, ok)
	assert.Equal(t, "t1", id.TenantID)
	assert.Equal(t, "u1", id.UserID)

	_, ok = v.Validate("wrong")
	assert.False(t, ok)
	_, ok = v.Validate("")
	assert.False(t, ok)
}

func TestStaticValidator_EmptyConfiguredTokenRejects(t *testing.T) {
	v := &staticValidator{token: ""}
	_, ok := v.Validate("anything")
	assert.False(t, ok)
}

func newCtx(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, rec
}

func TestExtractToken_AllSlots(t *testing.T) {
	t.Run("x-api-key", func(t *testing.T) {
		c, _ := newCtx("POST", "/anthropic/v1/messages")
		c.Request.Header.Set("x-api-key", "tok-anthropic")
		assert.Equal(t, "tok-anthropic", extractToken(c))
	})
	t.Run("authorization bearer", func(t *testing.T) {
		c, _ := newCtx("POST", "/openai/v1/chat/completions")
		c.Request.Header.Set("Authorization", "Bearer tok-openai")
		assert.Equal(t, "tok-openai", extractToken(c))
	})
	t.Run("x-goog-api-key", func(t *testing.T) {
		c, _ := newCtx("POST", "/genai/v1beta/models/x:generateContent")
		c.Request.Header.Set("x-goog-api-key", "tok-gemini")
		assert.Equal(t, "tok-gemini", extractToken(c))
	})
	t.Run("gemini query key", func(t *testing.T) {
		c, _ := newCtx("POST", "/genai/v1beta/models/x:generateContent?key=tok-q")
		assert.Equal(t, "tok-q", extractToken(c))
	})
	t.Run("none", func(t *testing.T) {
		c, _ := newCtx("POST", "/anthropic/v1/messages")
		assert.Equal(t, "", extractToken(c))
	})
}

func TestMiddleware_StaticMode(t *testing.T) {
	v := &staticValidator{token: "good", identity: Identity{TenantID: "t1", TokenID: "static"}}
	mw := Middleware(v)

	t.Run("valid token → identity in ctx, proceeds", func(t *testing.T) {
		c, rec := newCtx("POST", "/anthropic/v1/messages")
		c.Request.Header.Set("x-api-key", "good")
		mw(c)
		assert.False(t, c.IsAborted())
		assert.Equal(t, "t1", FromContext(c).TenantID)
		assert.NotEqual(t, 401, rec.Code)
	})

	t.Run("bad token → 401", func(t *testing.T) {
		c, rec := newCtx("POST", "/anthropic/v1/messages")
		c.Request.Header.Set("x-api-key", "nope")
		mw(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 401, rec.Code)
		assert.Contains(t, rec.Body.String(), "authentication_error")
	})

	t.Run("missing token → 401", func(t *testing.T) {
		c, rec := newCtx("POST", "/anthropic/v1/messages")
		mw(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 401, rec.Code)
	})
}

func TestMiddleware_NoneMode_NoTokenOK(t *testing.T) {
	mw := Middleware(noneValidator{})
	c, rec := newCtx("POST", "/anthropic/v1/messages")
	mw(c)
	assert.False(t, c.IsAborted())
	assert.NotEqual(t, 401, rec.Code)
	assert.Equal(t, Identity{}, FromContext(c))
}

func TestFromContext_Unset(t *testing.T) {
	c, _ := newCtx("GET", "/health")
	assert.Equal(t, Identity{}, FromContext(c))
}
