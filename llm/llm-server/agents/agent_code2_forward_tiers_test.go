package agents

import (
	"encoding/json"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codeAnalysisLLMConfig mirrors the code-analysis handler's inbound llm_config
// shape (api/handlers/agentic_analyze.go). The two services are separate Go
// modules, so the seam can only be pinned by round-tripping the JSON the way
// the wire actually does. That is precisely the gap this test closes: unit
// tests existed on the producer side (ResolveLLMConfigForForwarding populates
// Tiers) and on the consumer side (the handler maps tiers onto router/fixer/
// review), both passed, and per-role tiering was still dead in production
// because forwardedLLMConfigToMap silently dropped the field between them.
type codeAnalysisLLMConfig struct {
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model,omitempty"`
	ApiKey   string            `json:"api_key,omitempty"`
	Tiers    map[string]string `json:"tiers,omitempty"`
}

// resolveTierModels replicates the consumer's tier mapping so the assertion is
// about the roles that actually end up on different models, not about map keys.
func resolveTierModels(cfg codeAnalysisLLMConfig) (router, fixer, review, specialist string) {
	router, fixer, review, specialist = "", "", "", cfg.Model
	if m := cfg.Tiers["retrieval"]; m != "" {
		router, fixer = m, m
	}
	if m := cfg.Tiers["summary"]; m != "" {
		review = m
	}
	if m := cfg.Tiers["reasoning"]; m != "" {
		specialist = m
	}
	return
}

func TestForwardedLLMConfigToMap_TiersSurviveTheWire(t *testing.T) {
	fwd := &core.ForwardedLLMConfig{
		Provider: "googleai",
		Model:    "run-pro-model",
		ApiKey:   "test-key",
		Tiers: map[string]string{
			string(core.ModelTierRetrieval): "cheap-exec-model",
			string(core.ModelTierSummary):   "cheap-summary-model",
		},
	}

	raw, err := json.Marshal(forwardedLLMConfigToMap(fwd))
	require.NoError(t, err)

	var got codeAnalysisLLMConfig
	require.NoError(t, json.Unmarshal(raw, &got))

	router, fixer, review, specialist := resolveTierModels(got)
	assert.Equal(t, "cheap-exec-model", router, "router must not fall back to the run model")
	assert.Equal(t, "cheap-exec-model", fixer, "fixer must not fall back to the run model")
	assert.Equal(t, "cheap-summary-model", review, "reviewer must not fall back to the run model")
	assert.Equal(t, "run-pro-model", specialist)
}

func TestForwardedLLMConfigToMap_NoTiersOmitsKey(t *testing.T) {
	fwd := &core.ForwardedLLMConfig{Provider: "googleai", Model: "run-pro-model", ApiKey: "k"}

	m := forwardedLLMConfigToMap(fwd)

	_, present := m["tiers"]
	assert.False(t, present, "tiers must be omitted entirely when unresolved, not sent empty")
}
