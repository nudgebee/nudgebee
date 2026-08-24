package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLLMSecretEnvVars asserts the code-analysis pod is handed ONLY the minimal
// LLM_* keys (plus the non-sensitive BASE_URL) via SecretKeyRef — never the
// whole nudgebee secret, and never the master encryption key — so app-infra
// secrets can't reach untrusted command execution inside the pod.
func TestLLMSecretEnvVars(t *testing.T) {
	const secretName = "nudgebee"
	env := LLMSecretEnvVars(secretName)

	wantKeys := []string{
		"LLM_PROVIDER",
		"LLM_MODEL_NAME",
		"LLM_PROVIDER_API_KEY",
		"LLM_PROVIDER_API_ENDPOINT",
		"LLM_PROVIDER_REGION",
		"LLM_PROVIDER_API_VERSION",
		"LLM_PROVIDER_API_TYPE",
		"LLM_PROVIDER_MAX_RETRIES",
		"BASE_URL",
	}
	require.Len(t, env, len(wantKeys), "exactly the minimal LLM_* allowlist plus BASE_URL")

	got := map[string]bool{}
	for _, e := range env {
		got[e.Name] = true

		// Every entry must be sourced from the named secret via SecretKeyRef,
		// keyed by its own name, and optional (so a missing key doesn't crash
		// pod scheduling).
		require.NotNil(t, e.ValueFrom, "%s must use ValueFrom", e.Name)
		require.NotNil(t, e.ValueFrom.SecretKeyRef, "%s must use SecretKeyRef", e.Name)
		assert.Equal(t, secretName, e.ValueFrom.SecretKeyRef.Name)
		assert.Equal(t, e.Name, e.ValueFrom.SecretKeyRef.Key)
		require.NotNil(t, e.ValueFrom.SecretKeyRef.Optional)
		assert.True(t, *e.ValueFrom.SecretKeyRef.Optional, "%s must be optional", e.Name)
		assert.Empty(t, e.Value, "%s must not carry an inline plaintext value", e.Name)
	}

	for _, k := range wantKeys {
		assert.True(t, got[k], "expected key %s", k)
	}

	// The master key and other app-infra secrets must never be in the allowlist.
	for _, forbidden := range []string{
		"NUDGEBEE_ENCRYPTION_KEY",
		"APP_DATABASE_URL",
		"RABBIT_MQ_PASSWORD",
		"NEXTAUTH_SECRET",
	} {
		assert.False(t, got[forbidden], "forbidden key %s must not be injected", forbidden)
	}
}
