package config

import (
	"testing"

	"github.com/spf13/viper"
)

// TestOrchestratorModeDefaultsToLean guards the k8s orchestrator default.
//
// Lean is the mode dev/QA has run without regression, and it is materially cheaper
// per investigation than delegating (reduced tool core + minimal prompt). The default
// lives in a viper.SetDefault call, so a silent revert to "delegating" would not fail
// anything else — hence this test.
//
// This arrived from prod (hotfix 4c8f3d92e) covering k8s AND aws. The aws row is
// dropped here rather than carried across the backmerge: #32503 Phase 1 collapsed the
// cloud orchestrators to lean-only, so llm_server_aws_orchestrator_mode no longer
// exists on main — no struct field, no SetDefault, and zero readers. A test asserting
// a deleted key's default guards nothing; the invariant it stood for (cloud is lean)
// is now structural rather than configurable.
//
// The env vars are cleared rather than skipped around: viper.AutomaticEnv() is active
// (config.go), so an operator override in the ambient environment would otherwise
// decide the result. Clearing keeps the test hermetic and always asserting.
//
// t.Setenv to "" is sufficient to clear: viper is configured without AllowEmptyEnv,
// so an empty value is treated as unset and the compiled default is used. The env key
// is the UPPERCASED viper key — AutomaticEnv is registered with no prefix and no
// key replacer, so the lookup is strings.ToUpper(key).
func TestOrchestratorModeDefaultsToLean(t *testing.T) {
	for _, tc := range []struct {
		key    string
		envVar string
	}{
		{"llm_server_k8s_orchestrator_mode", "LLM_SERVER_K8S_ORCHESTRATOR_MODE"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.envVar, "")

			if got := viper.GetString(tc.key); got != "lean" {
				t.Errorf("%s = %q, want \"lean\"", tc.key, got)
			}
		})
	}
}

// TestOrchestratorModeEnvOverridesDefault pins the documented rollback path: setting
// the env var back to "delegating" must win over the compiled default, without a code
// revert. It also proves the clearing in the test above is doing real work — if the
// env were not consulted at all, this test could not fail.
func TestOrchestratorModeEnvOverridesDefault(t *testing.T) {
	t.Setenv("LLM_SERVER_K8S_ORCHESTRATOR_MODE", "delegating")

	if got := viper.GetString("llm_server_k8s_orchestrator_mode"); got != "delegating" {
		t.Errorf("env override = %q, want \"delegating\" — rollback path is broken", got)
	}
}
