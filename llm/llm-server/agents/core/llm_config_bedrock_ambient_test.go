package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The workspace pod running code analysis gets no AWS environment of its own,
// so it can only reach Bedrock with credentials llm-server hands it. llm-server
// itself authenticates through the AWS default chain, so that is what must be
// forwarded when nothing is configured explicitly.
func TestAmbientBedrockCredentials_FromDefaultChain(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAAMBIENTEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")
	t.Setenv("AWS_SESSION_TOKEN", "ambient-token")

	// Distinct region per test: the loaded config is cached by region, and a
	// shared key would serve another test's chain instead of this one's env.
	access, secret, token := ambientBedrockCredentials("us-test-ambient-1")

	assert.Equal(t, "AKIAAMBIENTEXAMPLE", access)
	assert.Equal(t, "ambient-secret", secret)
	assert.Equal(t, "ambient-token", token)
}

// A session token is optional — long-lived IAM user keys have none. It must not
// be invented, and its absence must not suppress the pair.
func TestAmbientBedrockCredentials_WithoutSessionToken(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIANOTOKENEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "no-token-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")

	access, secret, token := ambientBedrockCredentials("us-test-ambient-2")

	assert.Equal(t, "AKIANOTOKENEXAMPLE", access)
	assert.Equal(t, "no-token-secret", secret)
	assert.Empty(t, token)
}

// The config cache must be keyed per region, or a second region would silently
// reuse the first region's resolved config.
func TestAmbientAWSConfigFor_CachesPerRegion(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIACACHEEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "cache-secret")

	first, err := ambientAWSConfigFor("us-test-cache-1")
	require.NoError(t, err)
	second, err := ambientAWSConfigFor("us-test-cache-2")
	require.NoError(t, err)
	again, err := ambientAWSConfigFor("us-test-cache-1")
	require.NoError(t, err)

	assert.Equal(t, "us-test-cache-1", first.Region)
	assert.Equal(t, "us-test-cache-2", second.Region)
	assert.Equal(t, first.Region, again.Region, "same region must serve from cache")
}

// The ambient fallback is Bedrock-only. Every other provider authenticates with
// an API key, and handing them AWS credentials would be meaningless at best.
func TestResolveLLMConfigForForwarding_AmbientCredsAreBedrockOnly(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIASHOULDNOTLEAK")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-leak")

	pinGlobalModel(t, "googleai", "gemini-run-model")
	seedDBConfig(t, "acct-ambient-googleai", map[string]string{})

	fwd, err := ResolveLLMConfigForForwarding(newCtxWithKVs(), "acct-ambient-googleai", "agent_code_2", "")
	assert.NoError(t, err)
	require.NotNil(t, fwd)
	assert.Equal(t, "googleai", fwd.Provider)
	assert.Empty(t, fwd.AccessKey, "non-bedrock provider must not receive AWS credentials")
	assert.Empty(t, fwd.SecretKey)
	assert.Empty(t, fwd.SessionToken)
}

// The whole point of the change: a Bedrock account with no explicitly configured
// static credentials must still forward a usable pair, because the pod cannot
// resolve one for itself.
func TestResolveLLMConfigForForwarding_BedrockFallsBackToAmbient(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIABEDROCKAMBIENT")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "bedrock-ambient-secret")

	pinGlobalModel(t, "bedrock", "arn:aws:bedrock:us-west-2:1234:inference-profile/us.meta.llama4")
	seedDBConfig(t, "acct-ambient-bedrock", map[string]string{})

	fwd, err := ResolveLLMConfigForForwarding(newCtxWithKVs(), "acct-ambient-bedrock", "agent_code_2", "")
	assert.NoError(t, err)
	require.NotNil(t, fwd)
	assert.Equal(t, "bedrock", fwd.Provider)
	assert.Equal(t, "AKIABEDROCKAMBIENT", fwd.AccessKey)
	assert.Equal(t, "bedrock-ambient-secret", fwd.SecretKey)
}
