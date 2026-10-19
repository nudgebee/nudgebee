package integrations

import (
	"errors"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one configured fallback (#36339): a reference to an existing slot, or a
// slot of its own — validated at save, never both.

func stubFallbackRef(t *testing.T, fn func(tenantId, integrationId, accountId string) (string, map[string]string, error)) {
	t.Helper()
	prev := llmFallbackRefConfig
	llmFallbackRefConfig = fn
	t.Cleanup(func() { llmFallbackRefConfig = prev })
}

func TestValidateConfiguredFallback(t *testing.T) {
	gemini := map[string]string{"llm_provider": "googleai", "llm_model_name": "gemini-3.1-pro-preview", "llm_provider_api_key": "k",
		core.IntegrationConfigName: "gemini-primary"}
	with := func(extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range gemini {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	sc := &security.SecurityContext{}
	gateway := map[string]string{"llm_provider": "openai", "llm_model_name": "gpt-5.6-terra", "llm_model_fallbacks": "gpt-5.6-luna",
		"llm_tier_model_reasoning": "gpt-5.6-sol", "llm_tier_provider_summary": "openai"}
	stubFallbackRef(t, func(_, id, acct string) (string, map[string]string, error) {
		if id == "11111111-1111-1111-1111-111111111111" && acct == "acct-1" {
			return "gateway-openai", gateway, nil
		}
		if id == "22222222-2222-2222-2222-222222222222" {
			return "gemini-primary", gemini, nil
		}
		return "", nil, errors.New("sql: no rows in result set")
	})

	cases := []struct {
		name  string
		extra map[string]string
		acct  string
		want  string // "" = valid
	}{
		{"none configured", nil, "acct-1", ""},
		{"new: openai with its own key", map[string]string{"llm_fallback_provider": "openai", "llm_fallback_model": "gpt-5.6-terra", "llm_fallback_api_key": "fk"}, "acct-1", ""},
		{"new: missing required key", map[string]string{"llm_fallback_provider": "openai", "llm_fallback_model": "gpt-5.6-terra"}, "acct-1", "llm_fallback_api_key is required"},
		{"new: custom needs its endpoint", map[string]string{"llm_fallback_provider": "custom", "llm_fallback_model": "m", "llm_fallback_api_key": "fk"}, "acct-1", "llm_fallback_api_endpoint is required"},
		{"new: missing model", map[string]string{"llm_fallback_provider": "openai", "llm_fallback_api_key": "fk"}, "acct-1", "llm_fallback_model is required"},
		{"new: unknown provider", map[string]string{"llm_fallback_provider": "nope", "llm_fallback_model": "m"}, "acct-1", `llm_fallback_provider "nope" is invalid`},
		{"both shapes set", map[string]string{"llm_fallback_provider": "openai", "llm_fallback_ref_source": "env:global", "llm_fallback_ref_model": "m"}, "acct-1", "not both"},
		{"existing: another integration's primary", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111", "llm_fallback_ref_model": "gpt-5.6-terra"}, "acct-1", ""},
		{"existing: that slot's same-provider fallback", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111", "llm_fallback_ref_model": "gpt-5.6-luna"}, "acct-1", ""},
		{"existing: a tier slot", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111:tier:reasoning", "llm_fallback_ref_model": "gpt-5.6-sol"}, "acct-1", ""},
		// Mirrors llm-server's TestGetAllConfiguredModels_CategoryWithoutOwnModelIsNotReferenceable:
		// the picker never lists a category without its own model, so save rejects it too.
		{"existing: category without its own model", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111:tier:summary", "llm_fallback_ref_model": "gpt-5.6-terra"}, "acct-1", `does not serve model "gpt-5.6-terra"`},
		{"existing: model the slot does not serve", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111", "llm_fallback_ref_model": "gpt-4o"}, "acct-1", `does not serve model "gpt-4o"`},
		{"existing: not visible to this account (or disabled)", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111", "llm_fallback_ref_model": "gpt-5.6-terra"}, "acct-2", "not an enabled LLM integration available to account acct-2"},
		{"existing: its own primary", map[string]string{"llm_fallback_ref_source": "db:22222222-2222-2222-2222-222222222222", "llm_fallback_ref_model": "gemini-3.1-pro-preview",
			core.IntegrationID: "22222222-2222-2222-2222-222222222222"}, "acct-1", "cannot be the model it falls back from"},
		{"existing: its own primary while renaming in the same save", map[string]string{"llm_fallback_ref_source": "db:22222222-2222-2222-2222-222222222222", "llm_fallback_ref_model": "gemini-3.1-pro-preview",
			core.IntegrationID: "22222222-2222-2222-2222-222222222222", core.IntegrationConfigName: "renamed-primary"}, "acct-1", "cannot be the model it falls back from"},
		// Per-category fallbacks (_<tier> keys) get the same checks, named by their own keys.
		{"category: new fallback with its own key", map[string]string{"llm_fallback_provider_reasoning": "openai", "llm_fallback_model_reasoning": "gpt-5.6-terra", "llm_fallback_api_key_reasoning": "fk"}, "acct-1", ""},
		{"category: new fallback missing its key", map[string]string{"llm_fallback_provider_summary": "openai", "llm_fallback_model_summary": "gpt-5.6-terra"}, "acct-1", "llm_fallback_api_key_summary is required"},
		{"category: both shapes", map[string]string{"llm_fallback_provider_retrieval": "openai", "llm_fallback_ref_source_retrieval": "env:global", "llm_fallback_ref_model_retrieval": "m"}, "acct-1", "retrieval fallback as an existing model or as a new one"},
		{"category: may fall back to the integration's own primary", map[string]string{"llm_fallback_ref_source_reasoning": "db:22222222-2222-2222-2222-222222222222", "llm_fallback_ref_model_reasoning": "gemini-3.1-pro-preview",
			core.IntegrationID: "22222222-2222-2222-2222-222222222222"}, "acct-1", ""},
		{"category: not to its own category slot", map[string]string{"llm_fallback_ref_source_reasoning": "db:22222222-2222-2222-2222-222222222222:tier:reasoning", "llm_fallback_ref_model_reasoning": "m",
			core.IntegrationID: "22222222-2222-2222-2222-222222222222"}, "acct-1", "reasoning fallback cannot be the model it falls back from"},
		{"category: reference the account cannot see", map[string]string{"llm_fallback_ref_source_summary": "db:11111111-1111-1111-1111-111111111111", "llm_fallback_ref_model_summary": "gpt-5.6-terra"}, "acct-2", "not an enabled LLM integration available to account acct-2"},
		// OAuth and extra headers on a new fallback follow the main model's rules.
		{"new: custom on OAuth needs no api key", map[string]string{"llm_fallback_provider": "custom", "llm_fallback_model": "m", "llm_fallback_api_endpoint": "https://gw.example/v1",
			"llm_fallback_auth_type": "oauth_client_credentials", "llm_fallback_oauth_token_url": "https://idp.example/token", "llm_fallback_oauth_client_id": "c", "llm_fallback_oauth_client_secret": "s",
			"llm_fallback_extra_headers": `{"X-Project":"p"}`}, "acct-1", ""},
		{"new: OAuth missing its client secret", map[string]string{"llm_fallback_provider": "custom", "llm_fallback_model": "m", "llm_fallback_api_endpoint": "https://gw.example/v1",
			"llm_fallback_auth_type": "oauth_client_credentials", "llm_fallback_oauth_token_url": "https://idp.example/token", "llm_fallback_oauth_client_id": "c"}, "acct-1", "llm_fallback_oauth_client_secret is required"},
		{"new: OAuth only on custom or azure", map[string]string{"llm_fallback_provider": "openai", "llm_fallback_model": "m", "llm_fallback_api_key": "fk", "llm_fallback_auth_type": "oauth_client_credentials"}, "acct-1", "llm_fallback_auth_type oauth_client_credentials is only supported for the custom and azure providers"},
		{"category: header may not carry auth", map[string]string{"llm_fallback_provider_reasoning": "custom", "llm_fallback_model_reasoning": "m", "llm_fallback_api_key_reasoning": "fk",
			"llm_fallback_api_endpoint_reasoning": "https://gw.example/v1", "llm_fallback_extra_headers_reasoning": `{"Authorization":"x"}`}, "acct-1", `llm_fallback_extra_headers_reasoning must not set "Authorization"`},
		{"existing: whole config", map[string]string{"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111:all", "llm_fallback_ref_model": "gpt-5.6-terra"}, "acct-1", "not a single configured slot"},
		{"existing: env slot is checked by llm-server", map[string]string{"llm_fallback_ref_source": "env:tier:reasoning", "llm_fallback_ref_model": "m"}, "acct-1", ""},
		{"existing: missing model", map[string]string{"llm_fallback_ref_source": "env:global"}, "acct-1", "llm_fallback_ref_model is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validateConfiguredFallback(sc, with(c.extra), c.acct)
			if c.want == "" {
				assert.Empty(t, errs)
				return
			}
			assert.Truef(t, errorsContain(errs, c.want), "want %q, got %v", c.want, errs)
		})
	}
}

// Editing a referenced integration is allowed even when it stops serving the model a
// dependent references (only delete/disable is blocked, see checkLLMFallbackNotReferenced).
// The dependent goes stale: llm-server skips it at runtime ("Configured fallback
// unavailable"), and its next save names the problem instead of storing it again.
func TestReferencedIntegrationEditLeavesDependentStaleUntilResaved(t *testing.T) {
	referenced := map[string]string{"llm_provider": "openai", "llm_model_name": "gpt-5.6-terra"}
	stubFallbackRef(t, func(_, _, _ string) (string, map[string]string, error) { return "gateway-openai", referenced, nil })
	dependent := map[string]string{"llm_provider": "googleai", "llm_model_name": "gemini-3.1-pro-preview", "llm_provider_api_key": "k",
		"llm_fallback_ref_source": "db:11111111-1111-1111-1111-111111111111", "llm_fallback_ref_model": "gpt-5.6-terra"}
	sc := &security.SecurityContext{}

	require.Empty(t, validateConfiguredFallback(sc, dependent, "acct-1"))

	referenced["llm_model_name"] = "gpt-5.6-luna" // the referenced integration's own edit
	errs := validateConfiguredFallback(sc, dependent, "acct-1")
	assert.Truef(t, errorsContain(errs, `"gateway-openai" does not serve model "gpt-5.6-terra"`), "got %v", errs)
}

func TestFallbackKeysAreSecretAwareAndDeletable(t *testing.T) {
	for _, k := range []string{"llm_fallback_api_key", "llm_fallback_access_key", "llm_fallback_secret_key",
		"llm_fallback_api_key_reasoning", "llm_fallback_secret_key_summary", "llm_fallback_oauth_client_secret", "llm_fallback_oauth_client_secret_retrieval"} {
		assert.True(t, core.IsLLMSecretFieldName(k), "%s must be encrypted, redacted and omit-to-keep", k)
	}
	for _, k := range []string{"llm_fallback_ref_source", "llm_fallback_ref_model", "llm_fallback_provider", "llm_fallback_model", "llm_fallback_api_endpoint", "llm_fallback_context_size",
		"llm_fallback_ref_source_reasoning", "llm_fallback_provider_summary", "llm_fallback_api_endpoint_retrieval",
		"llm_fallback_auth_type", "llm_fallback_oauth_client_id_summary", "llm_fallback_extra_headers", "llm_fallback_deployment_name", "llm_fallback_disable_thinking_reasoning"} {
		assert.False(t, core.IsLLMSecretFieldName(k), k)
		assert.True(t, LLM{}.AllowsDynamicKey(k), "%s must be accepted at save", k)
	}
}

func TestTestConnectionForAccountsRejectsForeignAccounts(t *testing.T) {
	rc := security.NewRequestContext(t.Context(), &security.SecurityContext{}, nil, nil, nil)
	err := LLM{}.TestConnectionForAccounts(rc, llmConfig(map[string]string{"llm_provider": "openai"}), []string{"acct-of-another-tenant"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no access to account acct-of-another-tenant")
}
