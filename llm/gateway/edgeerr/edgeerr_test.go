package edgeerr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func render(provider string, status int, code, msg string) (int, map[string]any) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	Write(c, provider, status, code, msg)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestWrite_GeminiCarriesCodeAndStatus(t *testing.T) {
	// The bug this fixes: a 429 must carry error.code=429 + error.status so the
	// google-genai SDK reads it as a retryable 429 rather than "Error 0".
	code, body := render(Gemini, http.StatusTooManyRequests, "rate_limit_exceeded", "slow down")
	assert.Equal(t, http.StatusTooManyRequests, code)
	err := body["error"].(map[string]any)
	assert.Equal(t, float64(429), err["code"])
	assert.Equal(t, "RESOURCE_EXHAUSTED", err["status"])
	assert.Equal(t, "slow down", err["message"])
}

func TestWrite_AnthropicShape(t *testing.T) {
	_, body := render(Anthropic, http.StatusForbidden, "model_not_allowed", "blocked")
	assert.Equal(t, "error", body["type"])
	err := body["error"].(map[string]any)
	assert.Equal(t, "permission_error", err["type"])
	assert.Equal(t, "blocked", err["message"])
}

func TestWrite_OpenAIShape(t *testing.T) {
	_, body := render(OpenAI, http.StatusTooManyRequests, "rate_limit_exceeded", "slow")
	err := body["error"].(map[string]any)
	assert.Equal(t, "rate_limit_error", err["type"])
	assert.Equal(t, "rate_limit_exceeded", err["code"])
}

func TestWrite_OpenAINotFoundIsInvalidRequest(t *testing.T) {
	// OpenAI returns invalid_request_error for a 404 (e.g. unknown model).
	_, body := render(OpenAI, http.StatusNotFound, "not_found", "no such model")
	assert.Equal(t, "invalid_request_error", body["error"].(map[string]any)["type"])
}

func TestWrite_UnknownProviderNeutral(t *testing.T) {
	code, body := render("", http.StatusUnauthorized, "authentication_error", "bad token")
	assert.Equal(t, http.StatusUnauthorized, code)
	err := body["error"].(map[string]any)
	assert.Equal(t, "authentication_error", err["type"])
	assert.Equal(t, "bad token", err["message"])
}

func TestWrite_GeminiStatusMapping(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:          "UNAUTHENTICATED",
		http.StatusForbidden:             "PERMISSION_DENIED",
		http.StatusRequestEntityTooLarge: "INVALID_ARGUMENT",
		http.StatusInternalServerError:   "INTERNAL",
		http.StatusServiceUnavailable:    "UNAVAILABLE",
	}
	for status, want := range cases {
		_, body := render(Gemini, status, "x", "m")
		assert.Equal(t, want, body["error"].(map[string]any)["status"], "status %d", status)
	}
}
