package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Regression: a per-tier api_type (e.g. the summary tier pointing at an
// OpenAI-compatible HuggingFace Dedicated Endpoint) must survive into the probe
// target's effective config. tierCredsOverlay previously omitted
// llm_tier_api_type_<tier>, so the HF summary target was probed with the native
// HF wire protocol instead of "openai" → POST {url}/v1/chat/completions, failing
// connectivity for an otherwise-valid config.
//
// cfg mirrors the exact map that reached llm-server (api/request.txt) after
// api-server decrypted + backfilled the global llm_provider_api_key.
func TestEnumerateProbeTargets_PerTierAPITypeSurvives(t *testing.T) {
	cfg := map[string]string{
		"llm_provider":                       "googleai",
		"llm_provider_api_key":               "AIza_FAKE_GLOBAL_KEY",
		"llm_model_name":                     "gemini-3-flash-preview",
		"llm_model_fallbacks":                "gemini-2.5-pro,gemini-2.5-flash",
		"llm_tier_provider_reasoning":        "googleai",
		"llm_tier_model_reasoning":           "gemini-3.5-flash",
		"llm_tier_model_fallbacks_reasoning": "gemini-3-flash-preview,gemini-2.5-pro,gemini-2.5-flash",
		"llm_tier_provider_retrieval":        "googleai",
		"llm_tier_model_retrieval":           "gemini-3-flash-preview",
		"llm_tier_model_fallbacks_retrieval": "gemini-2.5-pro,gemini-2.5-flash",
		"llm_tier_provider_summary":          "huggingface",
		"llm_tier_model_summary":             "Qwen/Qwen3.6-35B-A3B-FP8",
		"llm_tier_api_key_summary":           "hf_FAKE_SUMMARY_KEY",
		"llm_tier_api_endpoint_summary":      "https://example-endpoint.us-east-2.aws.endpoints.huggingface.cloud",
		"llm_tier_api_type_summary":          "openai",
		"llm_tier_model_fallbacks_summary":   "",
	}

	targets := enumerateProbeTargets(cfg)
	var hf *probeTarget
	for i := range targets {
		if targets[i].provider == "huggingface" {
			hf = &targets[i]
			break
		}
	}
	if !assert.NotNil(t, hf, "expected a huggingface summary probe target") {
		return
	}

	// The fix: tierCredsOverlay carries llm_tier_api_type_summary into the
	// target's effective cfg under the generic api_type key.
	assert.Equal(t, "openai", hf.cfg[cfgKeyAPIType],
		"summary tier api_type must reach the HF probe target")
	assert.Equal(t,
		"https://example-endpoint.us-east-2.aws.endpoints.huggingface.cloud",
		hf.cfg[cfgKeyAPIEndpoint], "summary tier endpoint must reach the HF target")
	assert.NotEmpty(t, hf.cfg[cfgKeyAPIKey], "summary tier api_key must reach the HF target")
}
