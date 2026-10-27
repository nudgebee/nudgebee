package config

import (
	"reflect"
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
		"llm_server_tool_reported_failure_mode",
		"llm_server_tool_reported_failure_enforce_tools",

		"llm_server_cli_pipeline_status_enabled",
		"llm_server_remediation_followup_max_runs",
		"llm_server_watch_max_origin_per_tenant",
		"llm_server_watch_max_origin_duration_sec",
	} {
		assert.True(t, known[key],
			"%s has no viper.SetDefault, so its environment variable will never reach Config", key)
	}
}

// unregisteredKeys are tagged fields that still have no viper.SetDefault, so
// their environment variables are ignored today. Registering one starts
// applying any value a deployment already sets, so each needs its own check;
// remove a key from here when it gets its default.
var unregisteredKeys = map[string]bool{
	"otel_traces_exporter":                             true,
	"otel_exporter_otlp_traces_endpoint":               true,
	"otel_metrics_exporter":                            true,
	"otel_exporter_otlp_metrics_endpoint":              true,
	"otel_logs_exporter":                               true,
	"llm_server_log_provider_override":                 true,
	"llm_model_lite_name":                              true,
	"llm_server_fetch_logs_wall_clock_timeout_seconds": true,
	"llm_server_image_max_per_message":                 true,
	"llm_server_image_max_size_mb":                     true,
	"llm_server_security_mode":                         true,
	"llm_server_skill_selection_top_k":                 true,
	"llm_server_scratchpad_summary_max_len":            true,
	"llm_server_scratchpad_summary_min_bytes":          true,
}

// Every tagged field must be settable from the environment, so a new key added
// with a tag and no default fails here instead of being silently ignored.
func TestEveryConfigKeyIsRegisteredWithViper(t *testing.T) {
	known := map[string]bool{}
	for _, k := range viper.AllKeys() {
		known[k] = true
	}
	tp := reflect.TypeOf(Config)
	for i := 0; i < tp.NumField(); i++ {
		key := tp.Field(i).Tag.Get("mapstructure")
		if key == "" || key == "-" {
			continue
		}
		if unregisteredKeys[key] {
			assert.False(t, known[key], "%s is registered now; remove it from unregisteredKeys", key)
			continue
		}
		assert.True(t, known[key],
			"%s has no viper.SetDefault, so its environment variable will never reach Config", key)
	}
}

// The pipeline-status fix is on unless an operator turns it off; a default left
// unregistered reads as false and silently disables it.
func TestCliPipelineStatusDefaultsOn(t *testing.T) {
	assert.True(t, viper.GetBool("llm_server_cli_pipeline_status_enabled"))
}

// These defaults equal the fallbacks the code applies when the value is 0
// (llms/runtime preflightMaxMessageBytesDefault, getScratchpadSummaryTimeout),
// so registering the keys changes nothing until an environment sets them.
func TestRegisteredDefaultsMatchCodeFallbacks(t *testing.T) {
	assert.Equal(t, 512*1024, viper.GetInt("llm_server_preflight_max_message_bytes"))
	assert.Equal(t, 10000, viper.GetInt("llm_server_scratchpad_summary_timeout_ms"))
}
