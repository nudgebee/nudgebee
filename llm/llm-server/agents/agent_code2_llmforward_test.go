package agents

import (
	"bytes"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardedLLMConfigToMap(t *testing.T) {
	full := &core.ForwardedLLMConfig{
		Provider:    "googleai",
		Model:       "gemini-3-flash-preview",
		ApiKey:      "secret-key",
		ApiEndpoint: "https://example.com",
		ApiVersion:  "2024-01",
		ApiType:     "azure",
		Region:      "us-west-2",
	}
	m := forwardedLLMConfigToMap(full)
	assert.Equal(t, "googleai", m["provider"])
	assert.Equal(t, "gemini-3-flash-preview", m["model"])
	assert.Equal(t, "secret-key", m["api_key"])
	assert.Equal(t, "https://example.com", m["endpoint"])
	assert.Equal(t, "2024-01", m["api_version"])
	assert.Equal(t, "azure", m["api_type"])
	assert.Equal(t, "us-west-2", m["region"])

	// Empty optional fields are omitted; provider is always present.
	sparse := forwardedLLMConfigToMap(&core.ForwardedLLMConfig{Provider: "openai", ApiKey: "k"})
	assert.Equal(t, "openai", sparse["provider"])
	assert.Equal(t, "k", sparse["api_key"])
	_, hasEndpoint := sparse["endpoint"]
	assert.False(t, hasEndpoint, "empty endpoint must be omitted")
	_, hasModel := sparse["model"]
	assert.False(t, hasModel, "empty model must be omitted")
}

// Bedrock authenticates with an AWS credential triple rather than an API key.
// This hop is a hand-written map, so a field added to ForwardedLLMConfig but
// not here is silently dropped and the pod falls back to the AWS default
// credential chain — which on GKE dead-ends at an IMDS dial timeout.
func TestForwardedLLMConfigToMap_BedrockCredentials(t *testing.T) {
	t.Run("forwards a complete credential triple", func(t *testing.T) {
		m := forwardedLLMConfigToMap(&core.ForwardedLLMConfig{
			Provider:     "bedrock",
			Model:        "arn:aws:bedrock:us-west-2:1234:inference-profile/us.meta.llama4",
			AccessKey:    "AKIAEXAMPLE",
			SecretKey:    "shhh",
			SessionToken: "session",
		})
		assert.Equal(t, "AKIAEXAMPLE", m["access_key"])
		assert.Equal(t, "shhh", m["secret_key"])
		assert.Equal(t, "session", m["session_token"])
	})

	t.Run("omits the optional session token", func(t *testing.T) {
		m := forwardedLLMConfigToMap(&core.ForwardedLLMConfig{
			Provider:  "bedrock",
			AccessKey: "AKIAEXAMPLE",
			SecretKey: "shhh",
		})
		assert.Equal(t, "AKIAEXAMPLE", m["access_key"])
		_, hasToken := m["session_token"]
		assert.False(t, hasToken, "empty session token must be omitted")
	})

	t.Run("omits a half-set pair", func(t *testing.T) {
		// A static credentials provider built from half a pair is a hard error
		// in the AWS SDK, not a fall-through to the pod's own chain — so an
		// incomplete pair must not cross the wire at all.
		m := forwardedLLMConfigToMap(&core.ForwardedLLMConfig{
			Provider:  "bedrock",
			AccessKey: "AKIAEXAMPLE",
		})
		_, hasAccess := m["access_key"]
		_, hasSecret := m["secret_key"]
		assert.False(t, hasAccess, "lone access key must be omitted")
		assert.False(t, hasSecret)
	})
}

// The workspace pod no longer carries an LLM_PROVIDER_API_KEY fallback
// (#38009), so resolveLLMConfigForDispatch must fail closed: a resolution
// that comes back empty must block dispatch rather than let a caller proceed
// with no usable LLM credentials. ResolveLLMConfigForForwarding contractually
// short-circuits to (nil, nil) before touching the DB when accountId is
// empty, so this exercises the guard without needing any DB setup.
func TestResolveLLMConfigForDispatch_NoAccountScope_ReturnsError(t *testing.T) {
	ctx := newCapturingContext(&bytes.Buffer{})

	llmCfg, err := resolveLLMConfigForDispatch(ctx, "" /* accountId */, AgentCodeAnalyzer, "conv-1", "code")

	require.Error(t, err)
	assert.Nil(t, llmCfg)
	assert.Contains(t, err.Error(), "code:")
}
