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

func TestBuildCred_BedrockMissingKeys(t *testing.T) {
	_, _, ok := buildCred(ProviderCredsConfig{Provider: "bedrock", Region: "us-west-2"})
	assert.False(t, ok, "bedrock without access/secret key → not usable")
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
