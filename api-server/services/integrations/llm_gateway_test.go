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
	assert.Equal(t, []any{"openai", "anthropic", "gemini", "vertex", "vertex_openai", "bedrock", "custom"}, schema.Properties["provider"].Enum)

	// api_key is encrypted; required (shows *) for the known providers, optional for custom.
	assert.True(t, schema.Properties["api_key"].IsEncrypted, "api_key must be encrypted")
	assert.Equal(t, map[string]any{"provider": []any{"openai", "anthropic", "gemini"}}, schema.Properties["api_key"].RequiredWhen)
	// Visible for every provider (incl. custom) — otherwise required_when alone hides it.
	assert.Equal(t, map[string]any{"provider": []any{"openai", "anthropic", "gemini", "custom"}}, schema.Properties["api_key"].ShowWhen)

	// base_url is custom-only; models is shared by custom + vertex_openai (both matched by model).
	assert.Equal(t, map[string]any{"provider": "custom"}, schema.Properties["base_url"].ShowWhen)
	assert.Equal(t, map[string]any{"provider": []any{"custom", "vertex_openai"}}, schema.Properties["models"].ShowWhen)

	// No account binding — the gateway resolves per tenant, not per account.
	assert.NotContains(t, schema.Properties, "account_id")

	// Vertex structured fields — shared by vertex + vertex_openai; SA JSON encrypted + multiline.
	assert.Equal(t, map[string]any{"provider": []any{"vertex", "vertex_openai"}}, schema.Properties["project_id"].ShowWhen)
	assert.Equal(t, map[string]any{"provider": []any{"vertex", "vertex_openai"}}, schema.Properties["service_account_json"].ShowWhen)
	assert.True(t, schema.Properties["service_account_json"].IsEncrypted, "SA JSON must be encrypted")
	assert.True(t, schema.Properties["service_account_json"].Multiline, "SA JSON should render multiline")

	// region is shared by Vertex, vertex_openai, and Bedrock.
	assert.Equal(t, map[string]any{"provider": []any{"vertex", "vertex_openai", "bedrock"}}, schema.Properties["region"].ShowWhen)

	// endpoint is an optional host override, shown only for vertex_openai (not required).
	assert.Equal(t, map[string]any{"provider": "vertex_openai"}, schema.Properties["endpoint"].ShowWhen)
	assert.Nil(t, schema.Properties["endpoint"].RequiredWhen, "endpoint must be optional")

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

	// vertex_openai (Vertex OpenAI-compatible / MaaS): Vertex creds + models (matched by model).
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": validSA, "models": "google/gemma-3-27b-it-maas",
	}), ""), "well-formed vertex_openai config must pass")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": validSA,
	}), ""), "vertex_openai without models must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "region": "global", "service_account_json": validSA, "models": "m",
	}), ""), "vertex_openai without project_id must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "us-central1", "models": "m",
	}), ""), "vertex_openai without service_account_json must error")
	// endpoint is optional: a bare googleapis host passes, a full dedicated-endpoint URL
	// (prediction.vertexai.goog) passes, a non-Vertex host is rejected.
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": validSA, "models": "m", "endpoint": "https://aiplatform.googleapis.com",
	}), ""), "vertex_openai with a valid host endpoint must pass")
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "us-central1", "service_account_json": validSA, "models": "m",
		"endpoint": "https://456.us-central1-1234567890.prediction.vertexai.goog/v1beta1/projects/1234567890/locations/us-central1/endpoints/456/chat/completions",
	}), ""), "vertex_openai with a dedicated-endpoint URL must pass")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "global", "service_account_json": validSA, "models": "m", "endpoint": "internal.evil.com",
	}), ""), "vertex_openai with a non-Vertex endpoint must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "asia-southeast1", "service_account_json": validSA, "models": "m", "endpoint": "mg-endpoint-abc.asia-southeast1-000000000000.prediction.vertexai.goog",
	}), ""), "vertex_openai with a bare dedicated host (no path) must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "asia-southeast1", "service_account_json": validSA, "models": "m", "endpoint": "mg-endpoint-abc.asia-southeast1-000000000000.prediction.vertexai.goog?foo=bar",
	}), ""), "vertex_openai dedicated host with only a query must error (matches request-time parsing)")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{
		"provider": "vertex_openai", "project_id": "p", "region": "asia-southeast1", "service_account_json": validSA, "models": "m", "endpoint": "mg-endpoint-abc.asia-southeast1-000000000000.prediction.vertexai.goog/v1",
	}), ""), "vertex_openai dedicated host with only a trailing /v1 must error")

	// Bedrock: static access + secret + region.
	assert.Empty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "access_key": "AKIA", "secret_key": "sk", "region": "us-east-1"}), ""))
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "secret_key": "sk", "region": "us-east-1"}), ""), "bedrock without access_key must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "access_key": "AKIA", "region": "us-east-1"}), ""), "bedrock without secret_key must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "bedrock", "access_key": "AKIA", "secret_key": "sk"}), ""), "bedrock without region must error")

	// Missing / unsupported provider.
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"api_key": "sk-x"}), ""), "missing provider must error")
	assert.NotEmpty(t, g.ValidateConfig(nil, cv(map[string]string{"provider": "groq", "api_key": "x"}), ""), "unsupported provider must error")
}
