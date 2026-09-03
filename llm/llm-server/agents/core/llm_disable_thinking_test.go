package core

import "testing"

// llm_disable_thinking is read from the integration config, never inferred from
// the model name: which deployed models ramble is a property of the server's
// chat template, not of the family.
func TestGetLLMDisableThinking_PinnedResolution(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  *LLMConfigResolution
		want bool
	}{
		{"pinned flag on", &LLMConfigResolution{PinnedConfigSource: "db:x:all", PinnedDisableThinking: true}, true},
		{"pinned flag off", &LLMConfigResolution{PinnedConfigSource: "db:x:all"}, false},
		{"nil resolution, no account", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := getLLMDisableThinking("", tc.res); got != tc.want {
				t.Errorf("getLLMDisableThinking = %v, want %v", got, tc.want)
			}
		})
	}
}

// The pinned path skips the layered walk entirely, so readDbSlotInto must carry
// the flag onto the resolution for every scope — the same gap that lost
// MaxContext on pinned configs.
func TestReadDbSlotInto_CarriesDisableThinking(t *testing.T) {
	for _, scope := range []string{"global", "tier", "agent"} {
		t.Run(scope, func(t *testing.T) {
			// Each scope reads its model/provider from scope-specific keys.
			cfg := map[string]string{
				"llm_provider":              "custom",
				"llm_model_name":            "Qwen/Qwen3.6-35B-A3B-FP8",
				"llm_tier_provider_summary": "custom",
				"llm_tier_model_summary":    "Qwen/Qwen3.6-35B-A3B-FP8",
				"llm_provider_summary":      "custom",
				"llm_model_name_summary":    "Qwen/Qwen3.6-35B-A3B-FP8",
				"llm_disable_thinking":      "true",
			}
			res := &LLMConfigResolution{}
			if _, err := readDbSlotInto(res, cfg, &parsedConfigSource{Scope: scope, Name: "summary"}); err != nil {
				t.Fatal(err)
			}
			if !res.PinnedDisableThinking {
				t.Errorf("scope %q: PinnedDisableThinking not carried from config", scope)
			}
		})
	}
}

// Absent or non-"true" values must leave thinking untouched — the flag is
// strictly opt-in so existing configs keep the provider default.
func TestReadDbSlotInto_DisableThinkingDefaultsOff(t *testing.T) {
	for _, val := range []string{"", "false", "no", "1"} {
		res := &LLMConfigResolution{}
		cfg := map[string]string{"llm_provider": "custom", "llm_model_name": "m"}
		if val != "" {
			cfg["llm_disable_thinking"] = val
		}
		if _, err := readDbSlotInto(res, cfg, &parsedConfigSource{Scope: "global"}); err != nil {
			t.Fatal(err)
		}
		if res.PinnedDisableThinking {
			t.Errorf("value %q must not enable the flag", val)
		}
	}
}
