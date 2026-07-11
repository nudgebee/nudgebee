package agents

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// TestPrometheusDiscoveryWiring asserts the metrics_series_match tool is registered and
// its prompt guidance is present, so the agent can resolve a workload's real metric
// families on an empty templated query instead of reporting N/A. It is unconditional —
// there is no flag gating this discovery.
func TestPrometheusDiscoveryWiring(t *testing.T) {
	agent := PrometheusAgent{accountId: "test"}

	// Tool is registered on the agent.
	registered := false
	for _, tl := range agent.GetSupportedTools(nil) {
		if tl.Name() == "metrics_series_match" {
			registered = true
			break
		}
	}
	assert.True(t, registered, "metrics_series_match tool must be registered on the prometheus agent")

	prompt := agent.GetSystemPrompt(nil, core.NBAgentRequest{})

	// Prompt instructions tell the agent to discover families on an empty result.
	joined := strings.Join(prompt.Instructions, "\n")
	assert.Contains(t, joined, "discover, don't assume", "discovery guidance must be in the system prompt")
	assert.Contains(t, joined, "metrics_series_match", "prompt must point the agent at metrics_series_match")

	// ToolUsage documents the tool.
	if _, ok := prompt.ToolUsage["metrics_series_match"]; !ok {
		t.Error("metrics_series_match must have a ToolUsage entry")
	}

	// Discovery must not hardcode this environment's metric families.
	assert.NotContains(t, joined, "rpc_server", "must not hardcode a specific metric family")
	assert.NotContains(t, joined, "http_server_", "must not hardcode a specific metric family")
}
