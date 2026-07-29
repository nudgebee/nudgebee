package proxy

import (
	"net/http"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestParseBody(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
	}{
		{"anthropic non-stream", `{"model":"claude-haiku-4-5","max_tokens":8}`, "claude-haiku-4-5", false},
		{"anthropic stream", `{"model":"claude-sonnet-4-6","stream":true}`, "claude-sonnet-4-6", true},
		{"no model", `{"max_tokens":8}`, "", false},
		{"empty body", ``, "", false},
		{"garbage json", `not json`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, stream := parseBody([]byte(tc.body))
			assert.Equal(t, tc.wantModel, model)
			assert.Equal(t, tc.wantStream, stream)
		})
	}
}

func TestSafeHeaders_StripsAuthAndInternal(t *testing.T) {
	h := http.Header{
		"Authorization":     {"Bearer secret"},
		"X-Api-Key":         {"sk-secret"},
		"X-Goog-Api-Key":    {"AIza-secret"},
		"Cookie":            {"session=x"},
		"Accept-Encoding":   {"gzip"},
		"Content-Length":    {"123"},
		"X-Bf-Internal":     {"nope"},
		"Anthropic-Version": {"2023-06-01"},
		"Content-Type":      {"application/json"},
	}
	out := safeHeaders(h)

	// Sensitive / hop-by-hop / internal headers must not be forwarded.
	for _, k := range []string{"authorization", "x-api-key", "x-goog-api-key", "cookie", "accept-encoding", "content-length", "x-bf-internal"} {
		_, present := out[k]
		assert.False(t, present, "header %q must be stripped", k)
	}
	// Safe provider headers pass through (lowercased).
	assert.Equal(t, "2023-06-01", out["anthropic-version"])
	assert.Equal(t, "application/json", out["content-type"])
}

func TestStripPath(t *testing.T) {
	cases := []struct {
		name, full, mount, want string
	}{
		// Anthropic/OpenAI: strip only the mount prefix (base URL has no version).
		{"anthropic", "/anthropic/v1/messages", "/anthropic", "/v1/messages"},
		{"openai", "/openai/v1/chat/completions", "/openai", "/v1/chat/completions"},
		// Gemini: strip mount + version (base URL already includes /v1beta) — the
		// regression that caused .../v1beta/v1beta/... -> 404.
		{"gemini v1beta", "/genai/v1beta/models/gemini-2.5-flash:generateContent", "/genai", "/models/gemini-2.5-flash:generateContent"},
		{"gemini v1", "/genai/v1/models/x:generateContent", "/genai", "/models/x:generateContent"},
		{"gemini no version", "/genai/models/x", "/genai", "/models/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stripPath(tc.full, tc.mount))
		})
	}
}

func TestModelFromPath(t *testing.T) {
	assert.Equal(t, "gemini-2.5-flash", modelFromPath(schemas.Gemini, "/models/gemini-2.5-flash:generateContent"))
	assert.Equal(t, "gemini-2.5-flash", modelFromPath(schemas.Gemini, "/models/gemini-2.5-flash"))
	assert.Equal(t, "", modelFromPath(schemas.Gemini, "/v1/messages"))
	assert.Equal(t, "", modelFromPath(schemas.Anthropic, "/models/x:generateContent")) // only Gemini
}

func TestPrefixProviderMapping(t *testing.T) {
	assert.Equal(t, schemas.Anthropic, prefixProvider["/anthropic"])
	assert.Equal(t, schemas.OpenAI, prefixProvider["/openai"])
	assert.Equal(t, schemas.Gemini, prefixProvider["/genai"])
	_, ok := prefixProvider["/unknown"]
	assert.False(t, ok)
}
