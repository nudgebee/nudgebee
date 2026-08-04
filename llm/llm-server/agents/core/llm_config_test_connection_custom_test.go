package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: buildLLMFromConfig's provider switch is separate from the runtime
// resolver's (llm_config.go), and "custom" was added to the latter only. Every
// custom config therefore failed Test Connection with `unknown llm_provider
// "custom"` before reaching the network — which blocked saving one through the
// UI, since the form gates on the probe passing.
func TestBuildLLMFromConfig_Custom(t *testing.T) {
	t.Run("builds against the supplied endpoint", func(t *testing.T) {
		llm, err := buildLLMFromConfig(ProviderCustom, "google/gemma-4-31b-it:free", map[string]string{
			cfgKeyProvider:    ProviderCustom,
			cfgKeyAPIKey:      "sk-fake-key",
			cfgKeyAPIEndpoint: "https://openrouter.ai/api/v1",
		})
		require.NoError(t, err)
		assert.NotNil(t, llm)
	})

	// Without the endpoint the OpenAI client would silently default to
	// api.openai.com, so a user holding a valid OpenAI key would see the probe
	// pass and the runtime — which requires the endpoint — fail afterwards.
	t.Run("rejects a missing endpoint rather than defaulting to OpenAI", func(t *testing.T) {
		for _, endpoint := range []string{"", "   "} {
			_, err := buildLLMFromConfig(ProviderCustom, "google/gemma-4-31b-it:free", map[string]string{
				cfgKeyProvider:    ProviderCustom,
				cfgKeyAPIKey:      "sk-fake-key",
				cfgKeyAPIEndpoint: endpoint,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "requires llm_provider_api_endpoint")
		}
	})
}
