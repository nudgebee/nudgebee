package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBody(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
		wantOK     bool
	}{
		{"anthropic non-stream", `{"model":"claude-haiku-4-5","max_tokens":8}`, "claude-haiku-4-5", false, true},
		{"anthropic stream", `{"model":"claude-sonnet-4-6","stream":true}`, "claude-sonnet-4-6", true, true},
		{"no model", `{"max_tokens":8}`, "", false, true},
		{"empty body", ``, "", false, true},
		{"not json", `not json`, "", false, true},
		{"not an object", `["model"]`, "", false, true},
		{"empty object", ` { } `, "", false, true},
		{"nested model is not the model", `{"metadata":{"model":"x"},"model":"gpt-5"}`, "gpt-5", false, true},
		{"strings holding brackets and quotes", `{"a":"}{\"model\":\"x\"","b":[{"c":"]"}],"model":"gpt-5","n":-1.5e3,"t":true,"z":null}`, "gpt-5", false, true},
		{"escaped key is the same key", `{"mod\u0065l":"gpt-5"}`, "gpt-5", false, true},
		// encoding/json would read "gpt-4o-mini" here while a provider reads "model": the
		// policy would judge one model and the provider serve another.
		{"case variant after model", `{"model":"gpt-5","MODEL":"gpt-4o-mini"}`, "gpt-5", false, false},
		{"case variant before model", `{"Model":"gpt-4o-mini","model":"gpt-5"}`, "gpt-5", false, false},
		{"case variant alone", `{"MODEL":"gpt-5"}`, "", false, false},
		{"escaped case variant", `{"model":"gpt-5","\u004dodel":"gpt-4o-mini"}`, "gpt-5", false, false},
		{"model twice", `{"model":"gpt-4o-mini","model":"gpt-5"}`, "gpt-5", false, false},
		{"case variant of stream is not stream", `{"model":"gpt-5","Stream":true}`, "gpt-5", false, true},
		// Invalid JSON: Python's json reads past NaN and takes the last "model".
		{"NaN hides a second model", `{"model":"gpt-4o-mini","x":NaN,"model":"gpt-5"}`, "", false, false},
		{"second object", `{"model":"gpt-4o-mini"} {"model":"gpt-5"}`, "", false, false},
		{"unterminated", `{"model":"gpt-4o-mini"`, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, stream, ok := parseBody([]byte(tc.body))
			assert.Equal(t, tc.wantModel, model)
			assert.Equal(t, tc.wantStream, stream)
			assert.Equal(t, tc.wantOK, ok)
		})
	}
}

// FuzzParseBody checks the in-place member scan against encoding/json's own tokenizer on
// any valid JSON object: both must see the same top-level "model" and "stream".
func FuzzParseBody(f *testing.F) {
	for _, seed := range []string{
		`{"model":"gpt-5","stream":true}`,
		`{"a":{"b":["}",{"model":"x"}]},"model":"m","c":"\\\""}`,
		`{ "x" : 1e-3 , "model" : "y" , "stream" : false }`,
		`{"model":"a","Model":"b"}`,
		`{}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		model, stream, ok := parseBody(body)
		obj := bytes.TrimLeft(body, " \t\r\n")
		if len(obj) == 0 || obj[0] != '{' || !json.Valid(obj) {
			return
		}
		dec := json.NewDecoder(bytes.NewReader(obj))
		_, _ = dec.Token()
		wantModel, wantStream, exact, variant := "", false, 0, false
		for dec.More() {
			tok, err := dec.Token()
			require.NoError(t, err)
			key := tok.(string)
			var value json.RawMessage
			require.NoError(t, dec.Decode(&value))
			switch {
			case key == "model":
				exact++
				_ = json.Unmarshal(value, &wantModel)
			case strings.EqualFold(key, "model"):
				variant = true
			case key == "stream":
				_ = json.Unmarshal(value, &wantStream)
			}
		}
		assert.Equal(t, exact <= 1 && !variant, ok)
		assert.Equal(t, wantModel, model)
		assert.Equal(t, wantStream, stream)
	})
}

func TestCleanPath(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/v1/messages", "/v1/messages", true},
		{"/v1/models/", "/v1/models/", true}, // a trailing slash is kept
		{"", "", true},
		// Resolved the way Bifrost's HTTP client would resolve them when sending.
		{"/v1/models/../messages", "/v1/messages", true},
		{"/v1/responses/../files", "/v1/files", true},
		{"//v1/messages", "/v1/messages", true},
		{"/v1/./messages", "/v1/messages", true},
		{"/models/../../v1alpha/files", "/v1alpha/files", true}, // cannot climb above the mount
		// The client would decode or split these again, so no cleaning settles them.
		{"/v1/models/%2e%2e/messages", "", false},
		{"/v1/models/x?/../../messages", "", false},
		{"/v1/models#/../messages", "", false},
		{`/v1/models\..\messages`, "", false},
		{"/v1/mess\x00ages", "", false},
		{"x/models", "", false}, // a version segment the mount did not strip whole
	}
	for _, tc := range cases {
		got, ok := cleanPath(tc.in)
		assert.Equal(t, tc.ok, ok, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
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
