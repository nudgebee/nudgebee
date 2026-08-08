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
	assert.Equal(t, []any{"openai", "anthropic", "gemini", "vertex", "bedrock", "custom"}, schema.Properties["provider"].Enum)

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

	// Vertex structured fields — shown only for vertex; SA JSON encrypted + multiline.
	assert.Equal(t, map[string]any{"provider": "vertex"}, schema.Properties["project_id"].ShowWhen)
	assert.Equal(t, map[string]any{"provider": "vertex"}, schema.Properties["service_account_json"].ShowWhen)
	assert.True(t, schema.Properties["service_account_json"].IsEncrypted, "SA JSON must be encrypted")
	assert.True(t, schema.Properties["service_account_json"].Multiline, "SA JSON should render multiline")

	// region is shared by Vertex + Bedrock.
	assert.Equal(t, map[string]any{"provider": []any{"vertex", "bedrock"}}, schema.Properties["region"].ShowWhen)

	// Bedrock structured fields — shown only for bedrock; secret + session encrypted.
	assert.Equal(t, map[string]any{"provider": "bedrock"}, schema.Properties["access_key"].ShowWhen)
	assert.Equal(t, map[string]any{"provider": "bedrock"}, schema.Properties["secret_key"].ShowWhen)
	assert.True(t, schema.Properties["secret_key"].IsEncrypted, "secret_key must be encrypted")
	assert.True(t, schema.Properties["session_token"].IsEncrypted, "session_token must be encrypted")
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

	// A non-http(s) scheme is rejected by the URL shape check (http + https are both allowed;
	// the SSRF boundary is the gateway's dial-time IP check, gated by the private-endpoints opt-in).
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "custom", "base_url": "ftp://host/v1", "models": "m",
	}), ""), "non-http(s) base_url must be rejected")
	// http is now allowed (for in-cluster endpoints).
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "custom", "base_url": "http://vllm.internal/v1", "models": "m",
	}), ""), "http base_url must be accepted")

	// Vertex: project + region + a well-formed service-account JSON.
	validSA := `{"type":"service_account","client_email":"x@y.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"}`
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "vertex", "project_id": "p", "region": "us-central1", "service_account_json": validSA}), ""))
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "vertex", "service_account_json": validSA}), ""), "vertex without project/region must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "vertex", "project_id": "p", "region": "us-central1", "service_account_json": "not json"}), ""), "malformed SA JSON must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "vertex", "project_id": "p", "region": "us-central1", "service_account_json": `{"type":"service_account"}`}), ""), "SA JSON missing client_email/private_key must error")
	// Edit flow: an unchanged SA JSON arrives as the redaction mask — must NOT fail format validation.
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "vertex", "project_id": "p", "region": "us-central1", "service_account_json": "*********************************"}), ""), "masked SA JSON must skip format validation")

	// Bedrock: static access + secret + region.
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "access_key": "AKIA", "secret_key": "sk", "region": "us-east-1"}), ""))
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "secret_key": "sk", "region": "us-east-1"}), ""), "bedrock without access_key must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "access_key": "AKIA", "region": "us-east-1"}), ""), "bedrock without secret_key must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "access_key": "AKIA", "secret_key": "sk"}), ""), "bedrock without region must error")

	// Missing / unsupported provider.
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"api_key": "sk-x"}), ""), "missing provider must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "groq", "api_key": "x"}), ""), "unsupported provider must error")
}
