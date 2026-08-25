package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestResolveK8sOrchestratorOverride_PerRequestMode locks the mode →
// target-handle mapping for the per-request signal source. Every value
// added to the UI dropdown or the API contract must appear here or the
// caller will silently fall through to the router's default selection.
//
// After the #32503 Phase 1 collapse only "native" and "lean" remain valid;
// "delegating" and "direct" are treated as unknown modes (fall through to no
// override, letting the primary handle serve the request).
func TestResolveK8sOrchestratorOverride_PerRequestMode(t *testing.T) {
	prev := config.Config.K8sOrchestratorMode
	defer func() { config.Config.K8sOrchestratorMode = prev }()
	config.Config.K8sOrchestratorMode = "" // isolate from env

	cases := []struct {
		mode string
		want string
	}{
		{K8sOrchestratorModeNative, AgentK8sOrchestratorNativeName},
		{K8sOrchestratorModeLean, AgentK8sOrchestratorName},
		{"", ""},
		{"unknown_mode", ""},
		// Case + whitespace tolerance
		{"NATIVE", AgentK8sOrchestratorNativeName},
		{"  lean ", AgentK8sOrchestratorName},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			req := core.NBAgentRequest{
				AccountId:   "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				QueryConfig: toolcore.NBQueryConfig{K8sOrchestratorMode: tc.mode},
			}
			assert.Equal(t, tc.want, ResolveK8sOrchestratorOverride(req),
				"per-request mode=%q", tc.mode)
		})
	}
}

// TestResolveK8sOrchestratorOverride_EnvMode covers the boot-time /
// fleet-wide source. Same mapping; separate test so the two precedence
// legs stay independently guarded.
func TestResolveK8sOrchestratorOverride_EnvMode(t *testing.T) {
	prev := config.Config.K8sOrchestratorMode
	defer func() { config.Config.K8sOrchestratorMode = prev }()

	cases := []struct {
		mode string
		want string
	}{
		{K8sOrchestratorModeNative, AgentK8sOrchestratorNativeName},
		{K8sOrchestratorModeLean, AgentK8sOrchestratorName},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			config.Config.K8sOrchestratorMode = tc.mode
			req := core.NBAgentRequest{AccountId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
			assert.Equal(t, tc.want, ResolveK8sOrchestratorOverride(req),
				"env mode=%q", tc.mode)
		})
	}
}

// TestResolveK8sOrchestratorOverride_PerRequestBeatsEnv confirms the
// precedence: per-request wins even when env picks a different variant.
// This is what enables per-benchmark-test A/B when env is set to one mode.
func TestResolveK8sOrchestratorOverride_PerRequestBeatsEnv(t *testing.T) {
	prev := config.Config.K8sOrchestratorMode
	defer func() { config.Config.K8sOrchestratorMode = prev }()
	config.Config.K8sOrchestratorMode = K8sOrchestratorModeLean

	req := core.NBAgentRequest{
		AccountId:   "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		QueryConfig: toolcore.NBQueryConfig{K8sOrchestratorMode: K8sOrchestratorModeNative},
	}
	assert.Equal(t, AgentK8sOrchestratorNativeName, ResolveK8sOrchestratorOverride(req),
		"per-request override must win over env")
}
