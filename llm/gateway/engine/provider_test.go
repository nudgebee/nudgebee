package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProvider(t *testing.T) {
	cases := map[string]schemas.ModelProvider{
		"anthropic":         schemas.Anthropic,
		"Anthropic":         schemas.Anthropic,
		"  openai  ":        schemas.OpenAI,
		"bedrock":           schemas.Bedrock,
		"gemini":            schemas.Gemini,
		"googleai":          schemas.Gemini, // llm-server name maps to Bifrost Gemini
		"google":            schemas.Gemini,
		"vertexai":          schemas.Vertex,
		"vertexai_endpoint": schemas.Vertex,
		"azure":             schemas.Azure,
		"huggingface":       schemas.HuggingFace,
		"hf":                schemas.HuggingFace,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, NormalizeProvider(in))
		})
	}
}

func TestBuildCred_APIKeyProvider(t *testing.T) {
	provider, cred, ok := buildCred(ProviderCredsConfig{
		Provider: "anthropic",
		APIKey:   "sk-ant-real",
		Endpoint: "https://custom.example.com",
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Anthropic, provider)
	assert.Equal(t, "sk-ant-real", cred.key.Value.Val)
	assert.Equal(t, "https://custom.example.com", cred.endpoint)
	assert.Nil(t, cred.key.BedrockKeyConfig)
}

func TestBuildCred_HuggingFaceAPIKey(t *testing.T) {
	// HuggingFace is a plain api-key provider (bearer token), so it takes the same
	// default path as the other key-based providers — no structured cloud config.
	provider, cred, ok := buildCred(ProviderCredsConfig{
		Provider: "huggingface",
		APIKey:   "hf_realtoken",
	})
	require.True(t, ok)
	assert.Equal(t, schemas.HuggingFace, provider)
	assert.Equal(t, "hf_realtoken", cred.key.Value.Val)
	assert.Nil(t, cred.key.BedrockKeyConfig)
}

func TestBuildCred_MissingAPIKey(t *testing.T) {
	_, _, ok := buildCred(ProviderCredsConfig{Provider: "anthropic", APIKey: ""})
	assert.False(t, ok, "no credential material → not usable")
}

func TestBuildCred_BedrockCloudBYO(t *testing.T) {
	// The multi-tenant differentiator: structured cloud creds, not an api key.
	provider, cred, ok := buildCred(ProviderCredsConfig{
		Provider:     "bedrock",
		AccessKey:    "AKIA...",
		SecretKey:    "secret",
		SessionToken: "session",
		Region:       "us-west-2",
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Bedrock, provider)
	require.NotNil(t, cred.key.BedrockKeyConfig)
	bk := cred.key.BedrockKeyConfig
	assert.Equal(t, "AKIA...", bk.AccessKey.Val)
	assert.Equal(t, "secret", bk.SecretKey.Val)
	require.NotNil(t, bk.SessionToken)
	assert.Equal(t, "session", bk.SessionToken.Val)
	require.NotNil(t, bk.Region)
	assert.Equal(t, "us-west-2", bk.Region.Val)
	assert.Empty(t, cred.key.Value.Val, "cloud creds must not set the api-key Value")
}

func TestBuildCred_BedrockKeyless_IRSA(t *testing.T) {
	// Operator Bedrock with no static keys is valid: Bifrost then uses the AWS default
	// credential chain (IRSA / instance role). The key still carries the region.
	provider, cred, ok := buildCred(ProviderCredsConfig{Provider: "bedrock", Region: "eu-west-1"})
	require.True(t, ok, "operator bedrock may run keyless via IRSA")
	assert.Equal(t, schemas.Bedrock, provider)
	require.NotNil(t, cred.key.BedrockKeyConfig)
	bk := cred.key.BedrockKeyConfig
	assert.Empty(t, bk.AccessKey.Val, "keyless: no static access key")
	assert.Empty(t, bk.SecretKey.Val, "keyless: no static secret key")
	require.NotNil(t, bk.Region)
	assert.Equal(t, "eu-west-1", bk.Region.Val)
}

func TestBuildCred_BedrockPartialCreds(t *testing.T) {
	// Access without secret (or vice versa) is a misconfiguration, not a keyless setup.
	_, _, ok := buildCred(ProviderCredsConfig{Provider: "bedrock", AccessKey: "AKIA...", Region: "us-west-2"})
	assert.False(t, ok, "bedrock with access but no secret → rejected (partial creds)")

	_, _, ok = buildCred(ProviderCredsConfig{Provider: "bedrock", SecretKey: "secret", Region: "us-west-2"})
	assert.False(t, ok, "bedrock with secret but no access → rejected (partial creds)")
}

func TestBuildTenantKey_BedrockStatic(t *testing.T) {
	provider, key, ok := BuildTenantKey(ProviderCredsConfig{
		Provider: "bedrock", AccessKey: "AKIA...", SecretKey: "secret", Region: "us-west-2",
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Bedrock, provider)
	assert.Equal(t, "bedrock-tenant", key.ID)
	require.NotNil(t, key.BedrockKeyConfig)
	assert.Equal(t, "AKIA...", key.BedrockKeyConfig.AccessKey.Val)
	assert.Equal(t, "secret", key.BedrockKeyConfig.SecretKey.Val)
}

func TestBuildTenantKey_BedrockKeylessRejected(t *testing.T) {
	// SECURITY: a tenant must NOT get a keyless Bedrock key — it would sign with the
	// gateway pod's IAM role (the operator's identity). Empty creds → not usable, so
	// the request falls back to the operator default instead of borrowing the role.
	_, _, ok := BuildTenantKey(ProviderCredsConfig{Provider: "bedrock", Region: "us-west-2"})
	assert.False(t, ok, "tenant bedrock without static creds must not resolve (no IRSA borrowing)")
}

func TestBuildCred_Azure(t *testing.T) {
	// Operator Azure with api-key + endpoint: endpoint on AzureKeyConfig, key in Value.
	provider, cred, ok := buildCred(ProviderCredsConfig{
		Provider: "azure", APIKey: "az-key", Endpoint: "https://my-resource.openai.azure.com",
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Azure, provider)
	require.NotNil(t, cred.key.AzureKeyConfig)
	assert.Equal(t, "https://my-resource.openai.azure.com", cred.key.AzureKeyConfig.Endpoint.Val)
	assert.Equal(t, "az-key", cred.key.Value.Val)

	// Operator keyless (managed identity): endpoint set, no key → usable, empty Value.
	_, cred2, ok2 := buildCred(ProviderCredsConfig{Provider: "azure", Endpoint: "https://r.openai.azure.com"})
	require.True(t, ok2, "operator azure may run keyless via managed identity")
	require.NotNil(t, cred2.key.AzureKeyConfig)
	assert.Empty(t, cred2.key.Value.Val)

	// Endpoint is required.
	_, _, ok3 := buildCred(ProviderCredsConfig{Provider: "azure", APIKey: "az-key"})
	assert.False(t, ok3, "azure without an endpoint is not usable")

	// Endpoint is sanitized: a pasted trailing slash + whitespace is trimmed (Bifrost's
	// chat path would otherwise build a double-slash URL).
	_, cred4, ok4 := buildCred(ProviderCredsConfig{
		Provider: "azure", APIKey: "az-key", Endpoint: "  https://r.openai.azure.com/  ",
	})
	require.True(t, ok4)
	assert.Equal(t, "https://r.openai.azure.com", cred4.key.AzureKeyConfig.Endpoint.Val)
}

func TestBuildTenantKey_Azure(t *testing.T) {
	// Tenant Azure BYO: endpoint + static api-key → structured key with endpoint + Value.
	provider, key, ok := BuildTenantKey(ProviderCredsConfig{
		Provider: "azure", APIKey: "az-key", Endpoint: "https://tenant.openai.azure.com",
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Azure, provider)
	assert.Equal(t, "azure-tenant", key.ID)
	require.NotNil(t, key.AzureKeyConfig)
	assert.Equal(t, "https://tenant.openai.azure.com", key.AzureKeyConfig.Endpoint.Val)
	assert.Equal(t, "az-key", key.Value.Val)

	// SECURITY: tenant Azure with an endpoint but NO api-key must be rejected — keyless
	// would sign with the pod's managed identity (the operator's identity), not the tenant's.
	_, _, ok2 := BuildTenantKey(ProviderCredsConfig{Provider: "azure", Endpoint: "https://tenant.openai.azure.com"})
	assert.False(t, ok2, "tenant azure without a static api-key must not borrow the pod's managed identity")
}

func TestBuildCred_Vertex(t *testing.T) {
	// Operator Vertex with a service-account JSON: project + region + creds on the key.
	provider, cred, ok := buildCred(ProviderCredsConfig{
		Provider: "vertex", ProjectID: "my-proj", Region: "us-central1", APIKey: `{"type":"service_account"}`,
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Vertex, provider)
	require.NotNil(t, cred.key.VertexKeyConfig)
	assert.Equal(t, "my-proj", cred.key.VertexKeyConfig.ProjectID.Val)
	assert.Equal(t, "us-central1", cred.key.VertexKeyConfig.Region.Val)
	assert.Equal(t, `{"type":"service_account"}`, cred.key.VertexKeyConfig.AuthCredentials.Val)

	// Operator keyless (ADC / Workload Identity): project + region, no creds → usable.
	_, cred2, ok2 := buildCred(ProviderCredsConfig{Provider: "vertex", ProjectID: "my-proj", Region: "us-central1"})
	require.True(t, ok2, "operator vertex may run keyless via ADC / Workload Identity")
	require.NotNil(t, cred2.key.VertexKeyConfig)
	assert.Empty(t, cred2.key.VertexKeyConfig.AuthCredentials.Val, "keyless: no service-account JSON")

	// Project and region are both required.
	_, _, okNoProj := buildCred(ProviderCredsConfig{Provider: "vertex", Region: "us-central1"})
	assert.False(t, okNoProj, "vertex without a project is not usable")
	_, _, okNoRegion := buildCred(ProviderCredsConfig{Provider: "vertex", ProjectID: "my-proj"})
	assert.False(t, okNoRegion, "vertex without a region is not usable")

	// Whitespace is trimmed: padded project/region/creds are cleaned, and a whitespace-only
	// credential is treated as keyless (not a broken cred).
	_, cred3, ok3 := buildCred(ProviderCredsConfig{
		Provider: "vertex", ProjectID: "  my-proj  ", Region: "  us-central1\n", APIKey: "   ",
	})
	require.True(t, ok3)
	assert.Equal(t, "my-proj", cred3.key.VertexKeyConfig.ProjectID.Val)
	assert.Equal(t, "us-central1", cred3.key.VertexKeyConfig.Region.Val)
	assert.Empty(t, cred3.key.VertexKeyConfig.AuthCredentials.Val, "whitespace-only creds → keyless")
}

func TestBuildTenantKey_Vertex(t *testing.T) {
	// Tenant Vertex BYO: project + region + static service-account JSON → structured key.
	provider, key, ok := BuildTenantKey(ProviderCredsConfig{
		Provider: "vertex", ProjectID: "t-proj", Region: "us-central1", APIKey: `{"type":"service_account"}`,
	})
	require.True(t, ok)
	assert.Equal(t, schemas.Vertex, provider)
	assert.Equal(t, "vertex-tenant", key.ID)
	require.NotNil(t, key.VertexKeyConfig)
	assert.Equal(t, "t-proj", key.VertexKeyConfig.ProjectID.Val)
	assert.Equal(t, `{"type":"service_account"}`, key.VertexKeyConfig.AuthCredentials.Val)

	// SECURITY: tenant Vertex with project+region but NO service-account JSON must be
	// rejected — keyless would use the pod's ADC (the operator's identity), not the tenant's.
	_, _, ok2 := BuildTenantKey(ProviderCredsConfig{Provider: "vertex", ProjectID: "t-proj", Region: "us-central1"})
	assert.False(t, ok2, "tenant vertex without static creds must not borrow the pod's ADC identity")
}

func TestBuildCred_SelfHosted(t *testing.T) {
	// Ollama/vLLM/SGL are reached by base URL with an optional bearer token.
	// Endpoint set, no key → usable (empty Value); endpoint carried on the cred.
	provider, cred, ok := buildCred(ProviderCredsConfig{Provider: "ollama", Endpoint: "http://ollama:11434"})
	require.True(t, ok, "self-hosted with an endpoint is usable without a key")
	assert.Equal(t, schemas.Ollama, provider)
	assert.Empty(t, cred.key.Value.Val, "no bearer token → empty Value")
	assert.Equal(t, "http://ollama:11434", cred.endpoint, "endpoint flows to the provider config BaseURL")

	// Endpoint + optional bearer token.
	_, cred2, ok2 := buildCred(ProviderCredsConfig{Provider: "vllm", Endpoint: "http://vllm:8000", APIKey: "tok"})
	require.True(t, ok2)
	assert.Equal(t, "tok", cred2.key.Value.Val)

	// No endpoint → not usable (nowhere to send the request).
	_, _, ok3 := buildCred(ProviderCredsConfig{Provider: "sgl", APIKey: "tok"})
	assert.False(t, ok3, "self-hosted without an endpoint is not usable")
}

func TestBuildTenantKey_SelfHostedRejected(t *testing.T) {
	// Self-hosted is operator infrastructure: the tenant resolver does not carry a base
	// URL, so a tenant self-hosted config is not usable and falls back to the operator.
	_, _, ok := BuildTenantKey(ProviderCredsConfig{Provider: "ollama", APIKey: "tok"})
	assert.False(t, ok, "tenant self-hosted has no endpoint → not a usable tenant credential")

	// Hard boundary: rejected even if an endpoint is somehow present — a tenant DirectKey
	// cannot carry a base URL, so allowing it would silently route to the operator's server.
	_, _, ok2 := BuildTenantKey(ProviderCredsConfig{Provider: "ollama", Endpoint: "http://ollama:11434", APIKey: "tok"})
	assert.False(t, ok2, "tenant self-hosted must be rejected even with an endpoint (operator-only)")
}

func TestBuildTenantKey_APIKey(t *testing.T) {
	provider, key, ok := BuildTenantKey(ProviderCredsConfig{Provider: "anthropic", APIKey: "sk-ant-tenant"})
	require.True(t, ok)
	assert.Equal(t, schemas.Anthropic, provider)
	assert.Equal(t, "anthropic-tenant", key.ID)
	assert.Equal(t, "sk-ant-tenant", key.Value.Val)
	assert.Nil(t, key.BedrockKeyConfig)
}

func TestNBAccount_ReturnsKeyForConfiguredProvider(t *testing.T) {
	creds := map[schemas.ModelProvider]providerCred{
		schemas.Anthropic: {key: schemas.Key{Value: schemas.SecretVar{Val: "sk-ant"}, Models: schemas.WhiteList{"*"}}},
	}
	a := newNBAccount(creds, []schemas.ModelProvider{schemas.Anthropic})

	keys, err := a.GetKeysForProvider(context.Background(), schemas.Anthropic)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, "sk-ant", keys[0].Value.Val)
	assert.Equal(t, schemas.WhiteList{"*"}, keys[0].Models, "wildcard so core's pool doesn't deny by default")

	providers, _ := a.GetConfiguredProviders()
	assert.Equal(t, []schemas.ModelProvider{schemas.Anthropic}, providers)
}

func TestNBAccount_NoKeyForUnconfiguredProvider(t *testing.T) {
	a := newNBAccount(map[schemas.ModelProvider]providerCred{}, nil)
	keys, err := a.GetKeysForProvider(context.Background(), schemas.Gemini)
	require.NoError(t, err)
	assert.Empty(t, keys, "no cred → no key (core fails cleanly rather than using wrong creds)")
}

func TestNBAccount_ConfigForProvider(t *testing.T) {
	// Released core errors on a nil config, so every configured provider gets a
	// real config; an empty BaseURL lets the provider client default it.
	a := newNBAccount(map[schemas.ModelProvider]providerCred{
		schemas.Anthropic: {key: schemas.Key{}},
		schemas.OpenAI:    {key: schemas.Key{}, endpoint: "https://proxy.example.com"},
	}, nil)

	cfg, err := a.GetConfigForProvider(schemas.Anthropic)
	require.NoError(t, err)
	require.NotNil(t, cfg, "configured provider must get a config, not nil")
	assert.Empty(t, cfg.NetworkConfig.BaseURL, "no override → empty so the client defaults it")

	cfg2, err := a.GetConfigForProvider(schemas.OpenAI)
	require.NoError(t, err)
	require.NotNil(t, cfg2)
	assert.Equal(t, "https://proxy.example.com", cfg2.NetworkConfig.BaseURL)

	// Standard provider with no operator cred → still a default config, so a tenant
	// BYO key (injected per request as a DirectKey) can serve it without the operator
	// having pre-configured the provider. Empty BaseURL → the client defaults it.
	cfg3, err := a.GetConfigForProvider(schemas.Gemini)
	require.NoError(t, err)
	require.NotNil(t, cfg3, "standard provider must get a config so tenant BYO works")
	assert.Empty(t, cfg3.NetworkConfig.BaseURL)

	// Unknown/non-standard provider (e.g. a typo) → nil so core rejects it cleanly.
	cfg4, err := a.GetConfigForProvider(schemas.ModelProvider("not-a-real-provider"))
	require.NoError(t, err)
	assert.Nil(t, cfg4)
}
