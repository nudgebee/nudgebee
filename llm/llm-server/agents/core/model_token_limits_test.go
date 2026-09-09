package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// withFakeModelLimitsCatalog swaps the catalog fetch seam for a fixed map and
// clears the cache, restoring both when the test ends.
func withFakeModelLimitsCatalog(t *testing.T, catalog map[string]modelTokenLimits) {
	t.Helper()
	orig := fetchModelTokenLimits
	fetchModelTokenLimits = func(string) (map[string]modelTokenLimits, error) { return catalog, nil }
	modelLimitsMu.Lock()
	modelLimitsCache = map[string]modelLimitsEntry{}
	modelLimitsMu.Unlock()
	t.Cleanup(func() {
		fetchModelTokenLimits = orig
		modelLimitsMu.Lock()
		modelLimitsCache = map[string]modelLimitsEntry{}
		modelLimitsMu.Unlock()
	})
}

func TestResolveMaxOutputTokens_CatalogResolution(t *testing.T) {
	withFakeModelLimitsCatalog(t, map[string]modelTokenLimits{
		"anthropic:claude-sonnet-4-6": {MaxOutput: 65536},
		"openai:gpt-4o":               {MaxOutput: 16384},
		// Same model under two providers: the model-only fallback must pick the
		// lexicographically smallest provider deterministically.
		"bedrock:shared-model": {MaxOutput: 1111},
		"custom:shared-model":  {MaxOutput: 2222},
		"custom:nemotron-nano": {MaxOutput: 8192, MaxContext: 131072},
	})

	// Exact provider:model hit.
	assert.Equal(t, 16384, ResolveMaxOutputTokens("", "openai", "gpt-4o"))
	// Case-insensitive.
	assert.Equal(t, 16384, ResolveMaxOutputTokens("", "OpenAI", "GPT-4o"))
	// Provider unknown → model-only fallback.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "", "claude-sonnet-4-6"))
	// Model-only fallback is deterministic across providers.
	assert.Equal(t, 1111, ResolveMaxOutputTokens("", "", "shared-model"))
	// Vendor-prefixed id resolves via normalizeModel ("anthropic." stripped).
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "", "anthropic.claude-sonnet-4-6"))
	// No row anywhere → 0, the caller applies its floor.
	assert.Equal(t, 0, ResolveMaxOutputTokens("", "custom", "totally-unknown"))
	// The model-only wrapper used by chunk sizing.
	assert.Equal(t, 8192, GetLlmMaxOutputTokens("nemotron-nano"))
}

// Bedrock cross-region ids stack a region segment, a vendor segment, hyphenated
// version numbers and an invocation suffix — none of which appear on the bare
// dotted catalog rows the seed writes. Any one of them missing canonicalization
// re-creates #36449's floor regression for the exact customer id that motivated
// it, so each decoration is pinned here.
func TestResolveMaxOutputTokens_BedrockCrossRegionIds(t *testing.T) {
	withFakeModelLimitsCatalog(t, map[string]modelTokenLimits{
		"anthropic:claude-sonnet-4.6": {MaxOutput: 65536},
		"anthropic:claude-opus-4.8":   {MaxOutput: 65536},
	})

	// The id from the #36449 customer trace: region + vendor + hyphenated version.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "bedrock", "us.anthropic.claude-sonnet-4-6"))
	// Vendor-prefixed without a region segment.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "bedrock", "anthropic.claude-sonnet-4-6"))
	// Full Bedrock form: region + vendor + version + date + invocation suffix.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "bedrock", "us.anthropic.claude-sonnet-4-6-20260115-v1:0"))
	// EU region profile.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "bedrock", "eu.anthropic.claude-opus-4-8-v1:0"))
	// Dotted-but-prefixed (Vertex-style) id resolves too.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "vertexai", "anthropic.claude-sonnet-4.6"))
	// Vertex's custom endpoint form uses a slash rather than a dotted vendor prefix.
	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "custom", "vertex/claude-sonnet-4-6"))
}

// The canonical alias is indexed at catalog load as well, so a tenant row
// stored under a full Bedrock id serves lookups made with the bare form.
func TestModelLimitsCatalog_IndexesCanonicalAlias(t *testing.T) {
	orig := fetchModelTokenLimits
	// Simulate the fetch layer by feeding rows through the same aliasing the
	// real loader applies.
	fetchModelTokenLimits = func(string) (map[string]modelTokenLimits, error) {
		out := map[string]modelTokenLimits{}
		raw := "us.anthropic.claude-sonnet-4-6-v1:0"
		out["bedrock:"+raw] = modelTokenLimits{MaxOutput: 65536}
		if c := canonicalModelID(raw); c != raw {
			out["bedrock:"+c] = modelTokenLimits{MaxOutput: 65536}
		}
		return out, nil
	}
	modelLimitsMu.Lock()
	modelLimitsCache = map[string]modelLimitsEntry{}
	modelLimitsMu.Unlock()
	t.Cleanup(func() {
		fetchModelTokenLimits = orig
		modelLimitsMu.Lock()
		modelLimitsCache = map[string]modelLimitsEntry{}
		modelLimitsMu.Unlock()
	})

	assert.Equal(t, 65536, ResolveMaxOutputTokens("", "bedrock", "claude-sonnet-4.6"))
}

func TestCanonicalModelID(t *testing.T) {
	cases := map[string]string{
		"us.anthropic.claude-sonnet-4-6":               "claude-sonnet-4.6",
		"us.anthropic.claude-sonnet-4-6-20260115-v1:0": "claude-sonnet-4.6-20260115",
		"anthropic.claude-opus-4-8":                    "claude-opus-4.8",
		"eu.meta.llama3-1-70b-instruct-v1:0":           "llama3.1-70b-instruct",
		"models/gemini-embedding-001":                  "gemini-embedding-001",
		"vertex/claude-sonnet-4-6":                     "claude-sonnet-4.6",
		"gpt-4o":                                       "gpt-4o",
		"claude-sonnet-4.6":                            "claude-sonnet-4.6",
	}
	for in, want := range cases {
		assert.Equal(t, want, canonicalModelID(in), in)
	}
}

func TestResolveMaxOutputTokens_FetchFailure(t *testing.T) {
	orig := fetchModelTokenLimits
	fetchModelTokenLimits = func(string) (map[string]modelTokenLimits, error) {
		return nil, errors.New("db unavailable")
	}
	modelLimitsMu.Lock()
	modelLimitsCache = map[string]modelLimitsEntry{}
	modelLimitsMu.Unlock()
	t.Cleanup(func() {
		fetchModelTokenLimits = orig
		modelLimitsMu.Lock()
		modelLimitsCache = map[string]modelLimitsEntry{}
		modelLimitsMu.Unlock()
	})

	assert.Equal(t, 0, ResolveMaxOutputTokens("", "openai", "gpt-4o"))
}

func TestResolveModelMaxContext_Layering(t *testing.T) {
	withFakeModelLimitsCatalog(t, map[string]modelTokenLimits{
		"custom:nemotron-nano": {MaxOutput: 8192, MaxContext: 131072},
	})

	// 1. Config-resolved value wins over everything.
	res := &LLMConfigResolution{Provider: "custom", MaxContext: 64000}
	assert.Equal(t, 64000, ResolveModelMaxContext(res, "nemotron-nano"))

	// 2. Catalog row when the config field is blank.
	res = &LLMConfigResolution{Provider: "custom"}
	assert.Equal(t, 131072, ResolveModelMaxContext(res, "nemotron-nano"))

	// 3. Code model map / global default when the catalog has nothing.
	assert.Equal(t, 32_000, ResolveModelMaxContext(nil, "unknown-model"))
	assert.Equal(t, 262_144, ResolveModelMaxContext(nil, "Qwen/Qwen3-32B"))
}
