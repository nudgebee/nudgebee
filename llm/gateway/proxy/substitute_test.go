package proxy

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nudgebee/llm-gateway/engine"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/routing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strptr(s string) *string { return &s }

// --- parsing: client-native body → unified Responses request ---

func TestParseToResponses(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()

	anthropicBody := []byte(`{"model":"claude-opus-4-8","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	req, ok := parseToResponses(schemas.Anthropic, anthropicBody, bctx)
	require.True(t, ok)
	require.NotNil(t, req)

	geminiBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req, ok = parseToResponses(schemas.Gemini, geminiBody, bctx)
	require.True(t, ok)
	require.NotNil(t, req)

	// A non-translatable client (OpenAI uses the chat engine, not this path).
	_, ok = parseToResponses(schemas.OpenAI, anthropicBody, bctx)
	assert.False(t, ok)

	// Malformed body.
	_, ok = parseToResponses(schemas.Anthropic, []byte(`{not json`), bctx)
	assert.False(t, ok)
}

// --- usage mapping: unified Responses usage → metering shape ---

func TestResponsesUsage_Maps(t *testing.T) {
	resp := &schemas.BifrostResponsesResponse{
		Usage: &schemas.ResponsesResponseUsage{
			InputTokens:  100,
			OutputTokens: 40,
			TotalTokens:  140,
			InputTokensDetails: &schemas.ResponsesResponseInputTokens{
				CachedReadTokens:  30,
				CachedWriteTokens: 10,
			},
		},
	}
	u := responsesUsage(resp)
	require.NotNil(t, u)
	require.NotNil(t, u.LLMUsage)
	assert.Equal(t, 100, u.LLMUsage.PromptTokens)
	assert.Equal(t, 40, u.LLMUsage.CompletionTokens)
	assert.Equal(t, 140, u.LLMUsage.TotalTokens)
	require.NotNil(t, u.LLMUsage.PromptTokensDetails)
	assert.Equal(t, 30, u.LLMUsage.PromptTokensDetails.CachedReadTokens)
	assert.Equal(t, 10, u.LLMUsage.PromptTokensDetails.CachedWriteTokens)

	assert.Nil(t, responsesUsage(nil))
	assert.Nil(t, responsesUsage(&schemas.BifrostResponsesResponse{}))
}

// --- degradation: features the target can't honor are detected + signalled ---

func TestDetectDegradation(t *testing.T) {
	withCache := &schemas.BifrostResponsesRequest{
		Input: []schemas.ResponsesMessage{{CacheControl: &schemas.CacheControl{}}},
	}
	// Non-Anthropic target can't preserve Anthropic prompt caching.
	assert.Equal(t, []string{"prompt_caching"}, detectDegradation(withCache, schemas.Gemini))
	// Anthropic target preserves it — nothing dropped.
	assert.Empty(t, detectDegradation(withCache, schemas.Anthropic))

	withSignedThinking := &schemas.BifrostResponsesRequest{
		Input: []schemas.ResponsesMessage{{
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: []schemas.ResponsesMessageContentBlock{{Signature: strptr("sig")}},
			},
		}},
	}
	assert.Equal(t, []string{"thinking_signature"}, detectDegradation(withSignedThinking, schemas.OpenAI))
	assert.Empty(t, detectDegradation(withSignedThinking, schemas.Anthropic))

	// A plain request degrades nothing.
	plain := &schemas.BifrostResponsesRequest{
		Input: []schemas.ResponsesMessage{{Content: &schemas.ResponsesMessageContent{ContentStr: strptr("hi")}}},
	}
	assert.Empty(t, detectDegradation(plain, schemas.Gemini))
}

// --- re-encoding: unified Responses response → client-native JSON ---

func TestEncodeResponsesUnary(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	resp := &schemas.BifrostResponsesResponse{ID: strptr("resp_1"), Model: "gemini-3.1-flash"}

	// Anthropic client shape is JSON-marshalable.
	out, err := encodeResponsesUnary(schemas.Anthropic, bctx, resp)
	require.NoError(t, err)
	assert.True(t, json.Valid(out))

	// Gemini client shape is JSON-marshalable.
	out, err = encodeResponsesUnary(schemas.Gemini, bctx, resp)
	require.NoError(t, err)
	assert.True(t, json.Valid(out))

	// An unsupported client is an explicit error, not a silent bad encode.
	_, err = encodeResponsesUnary(schemas.OpenAI, bctx, resp)
	assert.Error(t, err)
}

// --- streaming framing: Anthropic uses named events, Gemini uses data: ---

func TestResponsesStreamEncoder_Framing(t *testing.T) {
	// Error frames are the deterministic, provider-shaped part we can assert without a
	// live stream (the success frames depend on core's per-chunk state machine).
	bErr := &schemas.BifrostError{Error: &schemas.ErrorField{Message: "boom"}}

	aEnc := newResponsesStreamEncoder(schemas.Anthropic)
	aFrame := string(aEnc.errorFrame(bErr))
	assert.Contains(t, aFrame, "event: error")
	assert.Contains(t, aFrame, "data: ")

	gEnc := newResponsesStreamEncoder(schemas.Gemini)
	gFrame := string(gEnc.errorFrame(bErr))
	assert.True(t, strings.HasPrefix(gFrame, "data: "))
	assert.True(t, strings.HasSuffix(gFrame, "\n\n"))
	require.NotNil(t, gEnc.geminiSt) // Gemini needs cross-chunk state
}

// --- metering: a substitution that fails before headers still records one row,
// attributed to the TARGET provider that actually ran (cost prices the executed model).

func TestSubstitute_MetersOnFailureToTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eng, err := engine.New(context.Background(), nil)
	require.NoError(t, err)
	defer eng.Shutdown()

	sink := &metering.CapturingSink{}
	h := &handler{client: eng.Client, sink: sink}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/anthropic/v1/messages", nil)

	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	rc := &RequestContext{
		Gin: c, Ctx: c.Request.Context(), Bctx: bctx,
		Provider: schemas.Anthropic, // client
		Body:     []byte(`{"model":"claude-opus-4-8","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`),
		Decision: routing.Decision{
			Reason:            routing.ReasonSubstitute,
			RequestedProvider: "anthropic", RequestedModel: "claude-opus-4-8",
			ResolvedProvider: "bogus-unconfigured-provider", ResolvedModel: "x",
		},
	}
	rm := &reqMeta{provider: schemas.Anthropic, model: "claude-opus-4-8", method: "POST", path: "/v1/messages", surface: surfaceNative, start: time.Now(), decision: rc.Decision}

	h.substitute(c, bctx, cancel, rc, rm)

	events := sink.Events()
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, "bogus-unconfigured-provider", ev.Provider, "metered against the target that ran")
	assert.Equal(t, "anthropic", ev.RequestedProvider)
	assert.Equal(t, "substitute", ev.RoutingReason)
	assert.NotEqual(t, 200, ev.StatusCode)
	// The swap is signalled to operator-facing layers.
	assert.Equal(t, "anthropic->bogus-unconfigured-provider", rec.Header().Get("x-nb-llm-substituted"))
}
