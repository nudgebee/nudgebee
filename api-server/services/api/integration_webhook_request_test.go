package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// A field of the wrong type is the caller's mistake: it answers 400 instead of
// panicking into an empty 500.
func TestWebhookRequestRejectsWrongTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handleIntegrationApis(r, nil, nil, slog.Default())
	for name, body := range map[string]string{
		"headers not an object": `{"url":"https://x","headers":["a"],"payload":"{}"}`,
		"payload not a string":  `{"url":"https://x","headers":{},"payload":{}}`,
		"url missing":           `{"headers":{},"payload":"{}"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/integration/webhook_request", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			assert.NotPanics(t, func() { r.ServeHTTP(rec, req) })
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}
