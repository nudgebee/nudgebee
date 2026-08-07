package integrations

import (
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func TestLLMGateway_NameCategory(t *testing.T) {
	g := LLMGateway{}
	assert.Equal(t, "llm_gateway", g.Name())
	assert.Equal(t, core.IntegrationLLM, g.Category())
	// Opts out of the account_id requirement (tenant-scoped credential resolution).
	assert.True(t, g.TenantScoped())
	var _ core.TenantScopedIntegration = g
}

func TestLLMGateway_ConfigSchema(t *testing.T) {
	schema := LLMGateway{}.ConfigSchema()

	assert.Equal(t, core.ToolSchemaTypeObject, schema.Type)
	assert.True(t, schema.Testable, "must render the Test Connection button")
	assert.Equal(t, []string{"provider"}, schema.Required)

	// provider is the umbrella selector.
	assert.Equal(t, []any{"openai", "anthropic", "gemini", "custom"}, schema.Properties["provider"].Enum)

	// api_key is encrypted; required (shows *) for the known providers, optional for custom.
	assert.True(t, schema.Properties["api_key"].IsEncrypted, "api_key must be encrypted")
	assert.Equal(t, map[string]any{"provider": []any{"openai", "anthropic", "gemini"}}, schema.Properties["api_key"].RequiredWhen)
	// Visible for every provider (incl. custom) — otherwise required_when alone hides it.
	assert.Equal(t, map[string]any{"provider": []any{"openai", "anthropic", "gemini", "custom"}}, schema.Properties["api_key"].ShowWhen)

	// base_url + models are custom-only (ShowWhen provider=custom).
	assert.Equal(t, map[string]any{"provider": "custom"}, schema.Properties["base_url"].ShowWhen)
	assert.Equal(t, map[string]any{"provider": "custom"}, schema.Properties["models"].ShowWhen)

	// No account binding — the gateway resolves per tenant, not per account.
	assert.NotContains(t, schema.Properties, "account_id")
}

func TestLLMGateway_ValidateConfig(t *testing.T) {
	g := LLMGateway{}
	cv := func(kv map[string]string) []core.IntegrationConfigValue {
		out := make([]core.IntegrationConfigValue, 0, len(kv))
		for k, v := range kv {
			out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
		}
		return out
	}
	// ValidateConfig ignores the SecurityContext (unused), so nil is fine here.

	// Known provider: only api_key is required.
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "openai", "api_key": "sk-x"}), ""))
	errs := g.ValidateConfig(nil, cv(map[string]string{"provider": "openai"}), "")
	assert.NotEmpty(t, errs, "openai without api_key must error")

	// Custom: base_url (SSRF-validated) + models required; api_key optional.
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "custom", "base_url": "https://ep.example/v1", "models": "Qwen/Qwen3.6-35B-A3B-FP8",
	}), ""))
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "custom", "models": "m"}), ""), "custom without base_url must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "custom", "base_url": "https://ep.example/v1"}), ""), "custom without models must error")

	// A private/internal custom URL is rejected by the SSRF shape check.
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "custom", "base_url": "http://localhost/v1", "models": "m",
	}), ""), "http/loopback base_url must be rejected")

	// Missing / unsupported provider.
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"api_key": "sk-x"}), ""), "missing provider must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "api_key": "x"}), ""), "unsupported provider must error")
}
