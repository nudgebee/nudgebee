package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveModelContextMap verifies the context window is keyed by MODEL name,
// pairing each scope's (model, context-size) — global / per-tier / per-agent —
// so the window can be looked up by whichever model the request finally uses
// (including a conversation/per-request override). A size with no model, a model
// with no size, and invalid/zero sizes contribute no entry.
func TestResolveModelContextMap(t *testing.T) {
	cases := []struct {
		name  string
		db    map[string]string
		model string
		want  int
	}{
		{"nil config → no entry", nil, "any-model", 0},
		{"global model+size", map[string]string{"llm_model_name": "gpt-4o", "llm_model_context_size": "16000"}, "gpt-4o", 16000},
		{"per-tier model+size", map[string]string{"llm_tier_model_summary": "Qwen/Q-FP8", "llm_tier_context_size_summary": "32768"}, "Qwen/Q-FP8", 32768},
		{"per-agent model+size", map[string]string{"llm_model_name_k8s_debug": "claude-x", "llm_model_context_size_k8s_debug": "8192"}, "claude-x", 8192},
		{"size without model → no entry", map[string]string{"llm_model_context_size": "16000"}, "gpt-4o", 0},
		{"model without size → no entry", map[string]string{"llm_model_name": "gpt-4o"}, "gpt-4o", 0},
		{"invalid size ignored", map[string]string{"llm_model_name": "gpt-4o", "llm_model_context_size": "abc"}, "gpt-4o", 0},
		{"zero ignored", map[string]string{"llm_model_name": "gpt-4o", "llm_model_context_size": "0"}, "gpt-4o", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, resolveModelContextMap(c.db)[c.model])
		})
	}
}

// TestResolveModelContextMap_OverrideTracksModel is the core of the per-model
// fix: a model configured for one tier is found by name even when a *different*
// tier (or a conversation override) selects it — the window follows the model.
func TestResolveModelContextMap_OverrideTracksModel(t *testing.T) {
	db := map[string]string{
		"llm_tier_model_summary":        "Qwen/Q-FP8",
		"llm_tier_context_size_summary": "32768",
		"llm_tier_model_reasoning":      "gemini-3-flash",
		// reasoning has no context size → falls back to model map, not present here
	}
	m := resolveModelContextMap(db)
	// A reasoning-tier (or conversation) override to the summary model resolves
	// the summary model's configured window — keyed by model, not by scope.
	assert.Equal(t, 32768, m["Qwen/Q-FP8"])
	assert.Equal(t, 0, m["gemini-3-flash"], "no configured size → no entry (model-map fallback applies)")
}

// TestResolveModelMaxContext verifies the resolved user value wins, else it
// falls back to the model map / default via GetLlmMaxTokenLength.
func TestResolveModelMaxContext(t *testing.T) {
	assert.Equal(t, 50000, ResolveModelMaxContext(&LLMConfigResolution{MaxContext: 50000}, "Qwen/Qwen3.6-35B-A3B-FP8"))
	assert.Equal(t, 262_144, ResolveModelMaxContext(&LLMConfigResolution{MaxContext: 0}, "Qwen/Qwen3.6-35B-A3B-FP8"))
	assert.Equal(t, 32_000, ResolveModelMaxContext(nil, "unknown-model"))
}
