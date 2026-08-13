package agents

import (
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTraceAgentV2Enabled reflects the deploy-time gate.
func TestTraceAgentV2Enabled(t *testing.T) {
	orig := config.Config.TraceAgentV2Enabled
	t.Cleanup(func() { config.Config.TraceAgentV2Enabled = orig })

	config.Config.TraceAgentV2Enabled = true
	assert.True(t, traceAgentV2Enabled())
	config.Config.TraceAgentV2Enabled = false
	assert.False(t, traceAgentV2Enabled())
}

// firstAgentName returns the name of the single sub-agent the fallback wrapper was built
// with for a given provider.
func firstAgentName(t *testing.T, ctx *security.RequestContext, provider string) string {
	t.Helper()
	agent := newTracesAgent(ctx, "acct", services_server.ObservabilityProvider{Provider: provider})
	fb, ok := agent.(*fallbackTracesAgent)
	require.True(t, ok)
	require.Len(t, fb.agents, 1)
	return fb.agents[0].GetName()
}

// TestNewTracesAgent_V2Routing pins the consolidation decision:
//   - flag OFF → today's dedicated agents (byte-identical v1 behaviour).
//   - flag ON  → jaeger/chronosphere/dynatrace/unknown consolidate onto TracesDefaultAgentV2;
//     Datadog stays on its dedicated agent (facet syntax). (ClickHouse is exercised
//     separately — it resolves via the registry.)
func TestNewTracesAgent_V2Routing(t *testing.T) {
	orig := config.Config.TraceAgentV2Enabled
	t.Cleanup(func() { config.Config.TraceAgentV2Enabled = orig })
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("flag off keeps dedicated agents", func(t *testing.T) {
		config.Config.TraceAgentV2Enabled = false
		assert.NotEqual(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "jaeger"))
		assert.NotEqual(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "chronosphere"))
		// Unknown provider → v1 default agent.
		assert.Equal(t, TracesDefaultAgentName, firstAgentName(t, ctx, "dynatrace"))
	})

	t.Run("flag on consolidates non-clickhouse/non-datadog onto v2", func(t *testing.T) {
		config.Config.TraceAgentV2Enabled = true
		assert.Equal(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "jaeger"))
		assert.Equal(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "chronosphere"))
		assert.Equal(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "dynatrace"))
		assert.Equal(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "newrelic"))
		// Datadog stays on its dedicated facet agent even when v2 is on.
		assert.NotEqual(t, TracesDefaultV2AgentName, firstAgentName(t, ctx, "datadog"))
	})
}
