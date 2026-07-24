package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestModelSupportsTemperature(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		// Anthropic reasoning models (reject temperature)
		{"anthropic claude-sonnet-5", "anthropic", "claude-sonnet-5", false},
		{"anthropic claude-sonnet-5-20250901", "anthropic", "claude-sonnet-5-20250901", false},
		{"anthropic claude-opus-5", "anthropic", "claude-opus-5", false},
		{"anthropic claude-5-sonnet", "anthropic", "claude-5-sonnet", false},
		{"anthropic claude-5-opus", "anthropic", "claude-5-opus", false},

		// Anthropic standard models (support temperature)
		{"anthropic claude-sonnet-4-6", "anthropic", "claude-sonnet-4-6", true},
		{"anthropic claude-3-5-sonnet", "anthropic", "claude-3-5-sonnet", true},
		{"anthropic claude-3-7-sonnet", "anthropic", "claude-3-7-sonnet", true},
		{"anthropic claude-3-haiku", "anthropic", "claude-3-haiku", true},

		// OpenAI reasoning models (reject explicit/non-default temperature)
		{"openai o1", "openai", "o1", false},
		{"openai o1-preview", "openai", "o1-preview", false},
		{"openai o1-mini", "openai", "o1-mini", false},
		{"openai o3", "openai", "o3", false},
		{"openai o3-mini", "openai", "o3-mini", false},
		{"openai gpt-5", "openai", "gpt-5", false},
		{"openai gpt-5-mini", "openai", "gpt-5-mini", false},
		{"openai gpt-5-turbo", "openai", "gpt-5-turbo", false},

		// OpenAI standard models (support temperature)
		{"openai gpt-4o", "openai", "gpt-4o", true},
		{"openai gpt-4o-mini", "openai", "gpt-4o-mini", true},
		{"openai gpt-4-turbo", "openai", "gpt-4-turbo", true},
		{"openai gpt-3.5-turbo", "openai", "gpt-3.5-turbo", true},

		// Azure & custom reasoning models
		{"azure o1", "azure", "o1", false},
		{"azure slash-prefixed o1", "azure", "azure/o1", false},
		{"azure colon-prefixed o1", "azure", "azure:o1", false},
		{"bedrock dot-namespaced o1", "bedrock", "us.openai.o1", false},
		{"hyphen-prefixed o1", "azure", "prod-o1-mini", false},
		{"underscore-prefixed o3", "azure", "azure_o3-mini", false},
		{"hyphen-prefixed gpt-5", "custom", "team-gpt-5-mini", false},
		{"non-boundary o1", "custom", "foo1-preview", true},
		{"non-family gpt-50", "custom", "gpt-50", true},
		{"azure o3-mini", "azure", "o3-mini", false},
		{"azure gpt-5", "azure", "gpt-5", false},
		{"azure gpt-4o", "azure", "gpt-4o", true},
		{"custom o1", "custom", "o1", false},
		{"custom gpt-4o", "custom", "gpt-4o", true},

		// Bedrock models
		{"bedrock claude-sonnet-5", "bedrock", "anthropic.claude-sonnet-5", false},
		{"bedrock claude-3-5-sonnet", "bedrock", "anthropic.claude-3-5-sonnet", true},
		{"bedrock version colon", "bedrock", "bedrock:anthropic.claude-3-5-sonnet-20241022-v2:0", true},

		// Google AI models
		{"googleai gemini-2.5-flash", "googleai", "gemini-2.5-flash", true},
		{"googleai gemini-2.5-pro", "googleai", "gemini-2.5-pro", true},

		// Aggregators & Proxy providers (OpenRouter, LiteLLM, etc.)
		{"openrouter o3-mini", "openrouter", "o3-mini", false},
		{"litellm gpt-5", "litellm", "gpt-5", false},
		{"openrouter gpt-4o", "openrouter", "gpt-4o", true},
		{"litellm claude-sonnet-5", "litellm", "claude-sonnet-5", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ModelSupportsTemperature(tt.provider, tt.model)
			assert.Equal(t, tt.want, got, "ModelSupportsTemperature(%q, %q)", tt.provider, tt.model)
		})
	}
}

func TestWithoutTemperature(t *testing.T) {
	sharedMetadata := map[string]any{"existing": true}
	// Normal options with temperature 0.0
	options := []llms.CallOption{
		llms.WithTemperature(0.0),
		llms.WithMaxTokens(4096),
		func(o *llms.CallOptions) { o.Metadata = sharedMetadata },
	}

	// Append withoutTemperature
	options = append(options, withoutTemperature())

	opts := llms.CallOptions{}
	for _, opt := range options {
		opt(&opts)
	}

	assert.Equal(t, SentinelOmitTemperature, opts.Temperature)
	assert.Equal(t, 4096, opts.MaxTokens)
	assert.Equal(t, true, opts.Metadata["existing"])
	assert.Equal(t, true, opts.Metadata["without_temperature"])
	assert.NotContains(t, sharedMetadata, "without_temperature")
}

func TestTemperatureSanitizer_Anthropic(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"text","text":"pong"}]}`))
	}))
	defer server.Close()

	client := newAnthropicHTTPClient()
	client.client = server.Client()

	t.Run("strips temperature for claude-sonnet-5", func(t *testing.T) {
		reqBody := map[string]any{
			"model":       "claude-sonnet-5",
			"max_tokens":  16,
			"temperature": 0.0,
			"messages": []map[string]any{
				{"role": "user", "content": "ping"},
			},
		}
		bodyBytes, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/messages", bytes.NewReader(bodyBytes))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var sentPayload map[string]any
		err = json.Unmarshal(capturedBody, &sentPayload)
		require.NoError(t, err)
		assert.NotContains(t, sentPayload, "temperature", "claude-sonnet-5 payload must not contain temperature")
		assert.Equal(t, "claude-sonnet-5", sentPayload["model"])
	})

	t.Run("preserves temperature for claude-sonnet-4-6", func(t *testing.T) {
		reqBody := map[string]any{
			"model":       "claude-sonnet-4-6",
			"max_tokens":  16,
			"temperature": 0.0,
			"messages": []map[string]any{
				{"role": "user", "content": "ping"},
			},
		}
		bodyBytes, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/messages", bytes.NewReader(bodyBytes))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var sentPayload map[string]any
		err = json.Unmarshal(capturedBody, &sentPayload)
		require.NoError(t, err)
		assert.Contains(t, sentPayload, "temperature", "claude-sonnet-4-6 payload must contain temperature")
		assert.Equal(t, float64(0), sentPayload["temperature"])
		assert.Equal(t, "claude-sonnet-4-6", sentPayload["model"])
	})
}

func TestTemperatureSanitizer_OpenAI(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-123","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"pong"}}]}`))
	}))
	defer server.Close()

	client := newOpenAIHTTPClient()
	client.client = server.Client()

	t.Run("strips temperature for o3-mini", func(t *testing.T) {
		reqBody := map[string]any{
			"model":       "o3-mini",
			"max_tokens":  16,
			"temperature": 0.0,
			"messages": []map[string]any{
				{"role": "user", "content": "ping"},
			},
		}
		bodyBytes, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, server.URL+"/chat/completions", bytes.NewReader(bodyBytes))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var sentPayload map[string]any
		err = json.Unmarshal(capturedBody, &sentPayload)
		require.NoError(t, err)
		assert.NotContains(t, sentPayload, "temperature", "o3-mini payload must not contain temperature")
		assert.Equal(t, "o3-mini", sentPayload["model"])
	})

	t.Run("preserves temperature for gpt-4o", func(t *testing.T) {
		reqBody := map[string]any{
			"model":       "gpt-4o",
			"max_tokens":  16,
			"temperature": 0.0,
			"messages": []map[string]any{
				{"role": "user", "content": "ping"},
			},
		}
		bodyBytes, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodPost, server.URL+"/chat/completions", bytes.NewReader(bodyBytes))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var sentPayload map[string]any
		err = json.Unmarshal(capturedBody, &sentPayload)
		require.NoError(t, err)
		assert.Contains(t, sentPayload, "temperature", "gpt-4o payload must contain temperature")
		assert.Equal(t, float64(0), sentPayload["temperature"])
		assert.Equal(t, "gpt-4o", sentPayload["model"])
	})
}

func TestTemperatureSanitizer_RedirectReplaysSanitizedBody(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}

		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	bodyBytes := []byte(`{"model":"o3-mini","temperature":0,"messages":[{"role":"user","content":"ping"}]}`)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/redirect", bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/problem+json")

	client := newOpenAIHTTPClient(server.Client())
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var sentPayload map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &sentPayload))
	assert.NotContains(t, sentPayload, "temperature", "redirected payload must remain sanitized")
	assert.Equal(t, "o3-mini", sentPayload["model"])
}

func TestTemperatureSanitizer_ReturnsBodyReadError(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.com", io.NopCloser(errorReader{}))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := newOpenAIHTTPClient().Do(req)
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.ErrorContains(t, err, "temperature sanitizer: read request body: read failed")
}

func TestTemperatureSanitizer_BypassesNonJSONOrUnspecifiedBody(t *testing.T) {
	transportCalls := 0
	baseClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	})}
	for _, contentType := range []string{"", "not a media type", "multipart/form-data; boundary=test"} {
		req, err := http.NewRequest(http.MethodPost, "http://example.com", io.NopCloser(errorReader{}))
		require.NoError(t, err)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		resp, err := newOpenAIHTTPClient(baseClient).Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}
	assert.Equal(t, 3, transportCalls)
}
