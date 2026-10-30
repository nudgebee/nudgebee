package config

import (
	"testing"

	"github.com/spf13/viper"

	"github.com/stretchr/testify/assert"
)

// A mapstructure tag is not enough to make an environment variable reach the
// struct. viper.AutomaticEnv only exposes a key to Unmarshal once viper knows
// the key exists, so a field whose key was never registered stays at its zero
// value however the environment is set — silently, with no error anywhere.
//
// That is not hypothetical: every key below was added with a tag and no
// default, and all of them read back empty from a real process with the
// variable exported. Asserting registration catches it at build time instead
// of in a deployment where the switch simply appears not to work.
func TestLLMEnvKeysAreRegisteredWithViper(t *testing.T) {
	known := map[string]bool{}
	for _, k := range viper.AllKeys() {
		known[k] = true
	}

	for _, key := range []string{
		"llm_disable_thinking",
		"llm_auth_type",
		"llm_oauth_token_url",
		"llm_oauth_client_id",
		"llm_oauth_client_secret",
		"llm_oauth_scope",
		"llm_extra_headers",
		"llm_provider_deployment_name",
		"llm_fallback_ref_source",
		"llm_fallback_ref_model",
	} {
		assert.True(t, known[key],
			"%s has no viper.SetDefault, so its environment variable will never reach Config", key)
	}
}
