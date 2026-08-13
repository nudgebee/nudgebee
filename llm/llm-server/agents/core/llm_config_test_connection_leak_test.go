package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Bug 1: a tier/agent that overrides the provider must NOT inherit the global
// provider's provider-scoped fields (endpoint, region, aws keys, api_type,
// api_version). Repro of the reported failure: global=huggingface with an
// endpoint, reasoning tier switches to googleai with its own key but no
// endpoint — the global HF endpoint leaked into the googleai client, pointing
// Gemini at the HF URL → auth failure.
func TestEnumerateProbeTargets_NoForeignProviderLeak(t *testing.T) {
	cfg := map[string]string{
		"llm_provider":              "huggingface",
		"llm_model_name":            "qwen",
		"llm_provider_api_key":      "hf_GLOBAL",
		"llm_provider_api_endpoint": "https://GLOBAL.hf.cloud",
		"llm_provider_api_version":  "2024-globalver",
		"llm_provider_api_type":     "openai",
		"llm_provider_region":       "us-east-2",
		"llm_provider_access_key":   "AKIA_GLOBAL",
		"llm_provider_secret_key":   "SECRET_GLOBAL",

		"llm_tier_provider_reasoning": "googleai",
		"llm_tier_model_reasoning":    "gemini",
		"llm_tier_api_key_reasoning":  "AIza-REASONING", // only the key is tier-scoped
	}

	targets := enumerateProbeTargets(cfg)
	var g *probeTarget
	for i := range targets {
		if targets[i].provider == "googleai" {
			g = &targets[i]
			break
		}
	}
	if !assert.NotNil(t, g, "expected a googleai probe target") {
		return
	}

	assert.Equal(t, "AIza-REASONING", g.cfg[cfgKeyAPIKey], "tier key must survive")
	// None of the global huggingface provider-scoped fields may leak.
	assert.Empty(t, g.cfg[cfgKeyAPIEndpoint], "global HF endpoint must NOT leak into googleai")
	assert.Empty(t, g.cfg[cfgKeyAPIVersion], "global api_version must NOT leak")
	assert.Empty(t, g.cfg[cfgKeyAPIType], "global api_type must NOT leak")
	assert.Empty(t, g.cfg[cfgKeyRegion], "global region must NOT leak")
	assert.Empty(t, g.cfg[cfgKeyAccessKey], "global access_key must NOT leak")
	assert.Empty(t, g.cfg[cfgKeySecretKey], "global secret_key must NOT leak")
}

// Bug 2: two targets sharing (provider, model) but with DIFFERENT credentials
// must be probed separately, not collapsed. Here retrieval reuses the global
// (openai, qwen) but with its own api key — that key must be probed.
func TestEnumerateProbeTargets_DistinctCredsNotCollapsed(t *testing.T) {
	cfg := map[string]string{
		"llm_provider":               "openai",
		"llm_model_name":             "qwen",
		"llm_provider_api_key":       "sk-global",
		"llm_tier_model_retrieval":   "qwen",
		"llm_tier_api_key_retrieval": "sk-retrieval-DIFFERENT",
	}

	targets := enumerateProbeTargets(cfg)
	keys := map[string]bool{}
	for _, tg := range targets {
		if tg.provider == "openai" && tg.model == "qwen" {
			keys[tg.cfg[cfgKeyAPIKey]] = true
		}
	}
	assert.True(t, keys["sk-global"], "global key must be probed")
	assert.True(t, keys["sk-retrieval-DIFFERENT"], "retrieval's distinct key must be probed (not collapsed)")
}
