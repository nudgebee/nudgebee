package core

import "testing"

func TestIsLLMSecretFieldName(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
	}{
		// Global secret fields.
		{"global api key", "llm_provider_api_key", true},
		{"global access key", "llm_provider_access_key", true},
		{"global secret key", "llm_provider_secret_key", true},
		{"global session token", "llm_provider_session_token", true},

		// Summary-agent variant in static schema.
		{"summary-agent api key", "llm_provider_api_key_summary_agent", true},

		// Per-agent dynamic variants (written via AllowsDynamicKey).
		{"per-agent api key", "llm_provider_api_key_k8s_debug", true},
		{"per-agent access key", "llm_provider_access_key_aws_debug", true},
		{"per-agent secret key", "llm_provider_secret_key_aws_debug", true},
		{"per-agent session token", "llm_provider_session_token_aws_debug", true},

		// Non-secret config fields — must NOT be flagged.
		{"provider", "llm_provider", false},
		{"model name", "llm_model_name", false},
		{"api endpoint", "llm_provider_api_endpoint", false},
		{"api version", "llm_provider_api_version", false},
		{"region", "llm_provider_region", false},
		{"api type", "llm_provider_api_type", false},
		{"adapter id", "llm_provider_adapter_id", false},
		{"fallbacks", "llm_model_fallbacks", false},
		{"tier provider", "llm_tier_provider_reasoning", false},
		{"tier model", "llm_tier_model_summary", false},
		{"per-agent model override", "llm_model_name_k8s_debug", false},
		{"per-agent fallbacks", "llm_model_fallbacks_aws_debug", false},

		// Unrelated keys (other integrations) — must NOT be flagged.
		{"token field (webhook)", "token", false},
		{"jira host", "host", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsLLMSecretFieldName(tc.input)
			if got != tc.expected {
				t.Errorf("IsLLMSecretFieldName(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestIsLLMOverrideKeyForDelete(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
	}{
		// Tier overrides — all delete-eligible.
		{"tier provider", "llm_tier_provider_reasoning", true},
		{"tier model", "llm_tier_model_summary", true},
		{"tier fallbacks", "llm_tier_model_fallbacks_retrieval", true},

		// Per-agent overrides — delete-eligible.
		{"agent provider", "llm_provider_k8s_debug_2", true},
		{"agent model", "llm_model_name_aws_debug", true},
		{"agent fallbacks", "llm_model_fallbacks_workflow_builder", true},

		// Global keys — NOT delete-eligible (UI requires non-empty).
		{"global provider", "llm_provider", false},
		{"global model", "llm_model_name", false},
		{"global fallbacks", "llm_model_fallbacks", false},

		// Secret-shaped keys — excluded (omit-to-keep, not delete).
		{"global api key", "llm_provider_api_key", false},
		{"agent api key", "llm_provider_api_key_k8s_debug_2", false},
		{"agent access key", "llm_provider_access_key_aws_debug", false},
		{"agent secret key", "llm_provider_secret_key_aws_debug", false},
		{"agent session token", "llm_provider_session_token_aws_debug", false},

		// Unrelated keys — never delete.
		{"empty", "", false},
		{"random", "host", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isLLMOverrideKeyForDelete(tc.input)
			if got != tc.expected {
				t.Errorf("isLLMOverrideKeyForDelete(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}
