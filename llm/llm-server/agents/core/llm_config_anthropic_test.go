package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// The transport must rewrite a string `system` into the cacheable block-array
// form and leave everything else in the payload untouched.
func TestAnthropicSystemCacheTransport_RewritesStringSystem(t *testing.T) {
	prev := config.Config.LlmEnableCaching
	config.Config.LlmEnableCaching = true
	t.Cleanup(func() { config.Config.LlmEnableCaching = prev })

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := anthropicCacheHTTPClient()
	resp, err := client.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4-6","system":"You are Nubi.","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	_ = resp.Body.Close()

	blocks, ok := got["system"].([]any)
	require.True(t, ok, "system must become a block array, got %T", got["system"])
	require.Len(t, blocks, 1)
	block := blocks[0].(map[string]any)
	assert.Equal(t, "You are Nubi.", block["text"])
	assert.Equal(t, map[string]any{"type": "ephemeral"}, block["cache_control"])
	assert.Equal(t, "claude-sonnet-4-6", got["model"], "other fields untouched")

	// A body with an already-array system passes through unchanged.
	got = nil
	resp, err = client.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m","system":[{"type":"text","text":"x"}],"messages":[]}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	_, isArr := got["system"].([]any)
	assert.True(t, isArr)
	assert.Len(t, got["system"], 1)
}

// With caching disabled the transport must leave the body untouched.
func TestAnthropicSystemCacheTransport_NoRewriteWhenCachingDisabled(t *testing.T) {
	prev := config.Config.LlmEnableCaching
	config.Config.LlmEnableCaching = false
	t.Cleanup(func() { config.Config.LlmEnableCaching = prev })

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	client := anthropicCacheHTTPClient()
	resp, err := client.Post(srv.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-sonnet-4-6","system":"You are Nubi.","messages":[]}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	_, isString := got["system"].(string)
	assert.True(t, isString, "system stays a plain string when caching is disabled")
}

// With global caching enabled, a request whose context carries the
// ContextKeyDisableCaching opt-out (custom agents with dynamic system
// prompts) must pass through unrewritten.
func TestAnthropicSystemCacheTransport_HonorsPerRequestOptOut(t *testing.T) {
	prev := config.Config.LlmEnableCaching
	config.Config.LlmEnableCaching = true
	t.Cleanup(func() { config.Config.LlmEnableCaching = prev })

	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &got))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ctx := context.WithValue(context.Background(), ContextKeyDisableCaching, true)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-6","system":"query: dynamic content","messages":[]}`))
	require.NoError(t, err)
	resp, err := anthropicCacheHTTPClient().Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	_, isString := got["system"].(string)
	assert.True(t, isString, "system stays a plain string when the request opts out of caching")
}

type fakeChoicesModel struct{ resp *llms.ContentResponse }

func (f *fakeChoicesModel) GenerateContent(context.Context, []llms.MessageContent, ...llms.CallOption) (*llms.ContentResponse, error) {
	return f.resp, nil
}
func (f *fakeChoicesModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", nil
}

// A [thinking, text] response must surface the text choice first; a plain
// text response is untouched.
func TestAnthropicChoiceNormalizer_PromotesTextChoice(t *testing.T) {
	inner := &fakeChoicesModel{resp: &llms.ContentResponse{Choices: []*llms.ContentChoice{
		{Content: "", GenerationInfo: map[string]any{"ThinkingContent": "hmm"}},
		{Content: "<thought_action>real answer</thought_action>"},
	}}}
	resp, err := wrapAnthropicChoiceNormalizer(inner).GenerateContent(context.Background(), nil)
	require.NoError(t, err)
	assert.Contains(t, resp.Choices[0].Content, "real answer")
	assert.Len(t, resp.Choices, 2, "thinking choice preserved behind")

	single := &fakeChoicesModel{resp: &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: "only"}}}}
	resp, err = wrapAnthropicChoiceNormalizer(single).GenerateContent(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "only", resp.Choices[0].Content)
}
