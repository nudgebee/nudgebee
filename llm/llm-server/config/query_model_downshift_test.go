package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestQueryModelDownshiftConfig(t *testing.T) {
	const key = "llm_server_orchestrator_query_model_downshift_enabled"
	// The old planner-specific key is no longer an input. In particular, it
	// must not silently enable downshifting when the new flag is left at default.
	t.Setenv("LLM_SERVER_REACT3_QUERY_MODEL_DOWNSHIFT_ENABLED", "true")
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{"default remains off", "", false},
		{"explicit off", "false", false},
		{"explicit on", "true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LLM_SERVER_ORCHESTRATOR_QUERY_MODEL_DOWNSHIFT_ENABLED", tc.value)
			require.True(t, viper.IsSet(key), "key must be registered for environment unmarshalling")
			var loaded appConfig
			require.NoError(t, viper.Unmarshal(&loaded))
			require.Equal(t, tc.want, loaded.LlmServerOrchestratorQueryModelDownshiftEnabled)
		})
	}
}
