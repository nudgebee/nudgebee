package googleai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tmc/langchaingo/llms"
)

// TestWithBaseURL verifies the option plumbs the gateway base URL into Options
// and leaves it empty by default (direct-to-Google). Pure — runs in CI without
// credentials.
func TestWithBaseURL(t *testing.T) {
	var opts Options
	if opts.BaseURL != "" {
		t.Fatalf("expected empty BaseURL by default, got %q", opts.BaseURL)
	}

	const gw = "http://ai-gateway.nudgebee.svc.cluster.local:8080/genai"
	WithBaseURL(gw)(&opts)
	if opts.BaseURL != gw {
		t.Fatalf("WithBaseURL: got %q, want %q", opts.BaseURL, gw)
	}

	// Empty override is a no-op relative to the default (keeps SDK default URL).
	opts2 := Options{BaseURL: gw}
	WithBaseURL("")(&opts2)
	if opts2.BaseURL != "" {
		t.Fatalf("WithBaseURL(\"\"): got %q, want empty", opts2.BaseURL)
	}
}

// TestGoogleAIGatewayCacheRoundTrip exercises the dogfood path end-to-end: a
// cached-content create → reference round trip routed through the NB AI Gateway
// instead of Google directly. It proves the generation client and the caching
// helper, both retargeted at the same gateway base URL + token, resolve to the
// same Google key (the create-vs-reference key-stability constraint from the
// issue) — a cache created through the gateway must produce a cache HIT when
// referenced through the gateway.
//
// Env-gated: set LLM_GATEWAY_URL to the gateway's /genai base URL and
// LLM_PROVIDER_API_KEY to the NB token the gateway accepts. Skips otherwise, so
// CI without a live gateway is unaffected.
func TestGoogleAIGatewayCacheRoundTrip(t *testing.T) {
	gatewayURL := os.Getenv("LLM_GATEWAY_URL")
	apiKey := os.Getenv("LLM_PROVIDER_API_KEY")
	if gatewayURL == "" || apiKey == "" {
		t.Skip("set LLM_GATEWAY_URL and LLM_PROVIDER_API_KEY to run the gateway round-trip test")
	}

	modelName := os.Getenv("LLM_MODEL_NAME")
	if modelName == "" {
		modelName = "gemini-2.5-flash-lite-preview-09-2025"
	}

	ctx := context.Background()

	// Caching helper routed through the gateway.
	helper, err := NewCachingHelper(ctx, WithAPIKey(apiKey), WithBaseURL(gatewayURL))
	if err != nil {
		t.Fatalf("failed to init caching helper through gateway: %v", err)
	}

	// A prompt large enough to clear Gemini's minimum cache token threshold.
	systemPrompt := strings.Repeat(
		"You are a meticulous SRE assistant. Cite evidence, avoid speculation, and "+
			"explain the causal chain behind every incident you diagnose. ", 400)

	cached, err := helper.CreateCachedContent(ctx, modelName,
		[]llms.MessageContent{{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: systemPrompt}},
		}},
		10*time.Minute,
		"nb-gateway-roundtrip-test",
	)
	if err != nil {
		t.Fatalf("create cached content through gateway failed: %v", err)
	}
	defer func() {
		if delErr := helper.DeleteCachedContent(ctx, cached.Name); delErr != nil {
			t.Logf("cleanup: failed to delete cache %s: %v", cached.Name, delErr)
		}
	}()
	t.Logf("cache created through gateway: %s", cached.Name)

	// Reference the cache from a generation call routed through the SAME gateway.
	llm, err := New(ctx, WithAPIKey(apiKey), WithBaseURL(gatewayURL))
	if err != nil {
		t.Fatalf("failed to init generation client through gateway: %v", err)
	}

	resp, err := llm.GenerateContent(ctx, []llms.MessageContent{{
		Role:  llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{llms.TextContent{Text: "Summarize your operating principles in one sentence."}},
	}}, func(co *llms.CallOptions) {
		co.Model = modelName
		if co.Metadata == nil {
			co.Metadata = make(map[string]any)
		}
		co.Metadata["CachedContentName"] = cached.Name
	})
	if err != nil {
		t.Fatalf("generation referencing gateway cache failed: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("no choices returned from gateway-routed generation")
	}

	// The create and the reference resolved to the same Google key through the
	// gateway iff we get a cache HIT (cached tokens > 0). A key mismatch would
	// instead surface as a 403/miss above or zero cached tokens here.
	cachedTokens, ok := resp.Choices[0].GenerationInfo["CachedTokens"].(int32)
	if !ok || cachedTokens <= 0 {
		t.Fatalf("expected a cache HIT through the gateway (cached tokens > 0); got %v (key-stability constraint violated)", resp.Choices[0].GenerationInfo["CachedTokens"])
	}
	t.Logf("gateway round-trip cache HIT: %d cached tokens", cachedTokens)
}
