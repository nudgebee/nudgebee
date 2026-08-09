package proxy

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnownChatKeys: the reflected set covers the schema fields we must never treat
// as extras — most importantly `model` (routing may rewrite it) and `messages`, plus
// representative ChatParameters fields and the flat reasoning shorthands.
func TestKnownChatKeys(t *testing.T) {
	for _, k := range []string{
		"model", "messages", "stream", "max_tokens", "temperature", "tools",
		"reasoning", "reasoning_effort", "reasoning_max_tokens", "reasoning_display",
	} {
		assert.Truef(t, knownChatKeys[k], "expected %q to be a known chat key", k)
	}
	assert.False(t, knownChatKeys["chat_template_kwargs"], "provider-specific extras must NOT be known keys")
}

// TestApplyChatExtraParams_CapturesUnknown: provider-specific keys outside the schema
// (chat_template_kwargs, extra_body) are captured into ExtraParams and flow through
// ToBifrostChatRequest onto the request params, while known keys are left alone.
func TestApplyChatExtraParams_CapturesUnknown(t *testing.T) {
	body := []byte(`{
		"model": "Qwen/Qwen3",
		"messages": [{"role":"user","content":"hi"}],
		"temperature": 0.2,
		"chat_template_kwargs": {"enable_thinking": false},
		"extra_body": {"foo": 1}
	}`)

	var req openai.OpenAIChatRequest
	require.NoError(t, json.Unmarshal(body, &req))
	applyChatExtraParams(&req, body)

	// Extras captured on the request...
	require.NotNil(t, req.ExtraParams)
	ctk, ok := req.ExtraParams["chat_template_kwargs"].(map[string]any)
	require.True(t, ok, "chat_template_kwargs should be captured as an object")
	assert.Equal(t, false, ctk["enable_thinking"])
	assert.Contains(t, req.ExtraParams, "extra_body")

	// ...and NOT the known keys (model/messages/temperature).
	assert.NotContains(t, req.ExtraParams, "model")
	assert.NotContains(t, req.ExtraParams, "messages")
	assert.NotContains(t, req.ExtraParams, "temperature")

	// The extras ride through onto the unified request params so passthrough lanes
	// (vLLM) forward them upstream.
	breq := req.ToBifrostChatRequest(nil)
	require.NotNil(t, breq.Params)
	assert.Contains(t, breq.Params.ExtraParams, "chat_template_kwargs")
}

// TestApplyChatExtraParams_KnownKeysAreCaseInsensitive: Go's json matching is
// case-insensitive, so an oddly-cased known key (e.g. "Model", "Temperature") parses
// into the struct — it must NOT also be captured as an extra, or it'd be duplicated
// upstream (and could shadow the routed model).
func TestApplyChatExtraParams_KnownKeysAreCaseInsensitive(t *testing.T) {
	body := []byte(`{"Model":"x","Messages":[],"Temperature":0.5,"chat_template_kwargs":{"enable_thinking":false}}`)

	var req openai.OpenAIChatRequest
	require.NoError(t, json.Unmarshal(body, &req))
	applyChatExtraParams(&req, body)

	assert.NotContains(t, req.ExtraParams, "Model")
	assert.NotContains(t, req.ExtraParams, "Temperature")
	assert.Contains(t, req.ExtraParams, "chat_template_kwargs") // genuine extra still captured
}

// TestApplyChatExtraParams_PreservesLargeIntPrecision: a large int64 in an extra (an
// ID / timestamp) must survive verbatim, not get rounded through float64. It's kept as
// json.Number, which re-serializes to the exact literal when merged upstream.
func TestApplyChatExtraParams_PreservesLargeIntPrecision(t *testing.T) {
	const bigID = int64(9223372036854775807) // math.MaxInt64 — unrepresentable as float64
	body := []byte(`{"model":"x","messages":[],"vendor_id":9223372036854775807}`)

	var req openai.OpenAIChatRequest
	require.NoError(t, json.Unmarshal(body, &req))
	applyChatExtraParams(&req, body)

	num, ok := req.ExtraParams["vendor_id"].(json.Number)
	require.Truef(t, ok, "expected json.Number, got %T", req.ExtraParams["vendor_id"])
	got, err := num.Int64()
	require.NoError(t, err)
	assert.Equal(t, bigID, got)
}

// TestApplyChatExtraParams_NoExtras: a plain request gets no ExtraParams (nil), so we
// never send an empty extras map or clobber anything.
func TestApplyChatExtraParams_NoExtras(t *testing.T) {
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`)
	var req openai.OpenAIChatRequest
	require.NoError(t, json.Unmarshal(body, &req))
	applyChatExtraParams(&req, body)
	assert.Nil(t, req.ExtraParams, "no unknown keys → no ExtraParams (reasoning_effort is a known shorthand)")
}
