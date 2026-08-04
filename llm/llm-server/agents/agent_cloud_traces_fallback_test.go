package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTracesAgent_CloudFallbackRouting verifies a GCP/Azure trace provider
// routes the trace dispatcher to the dedicated CLI trace sub-agent. The
// fallbackTracesAgent wraps the primary agent, so we assert on the wrapped
// agent's name.
func TestNewTracesAgent_CloudFallbackRouting(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	cases := []struct {
		name     string
		provider string
		wantName string
	}{
		{"gcp lowercase", "gcp", GcpTracesAgentName},
		{"gcp mixed case", "GCP", GcpTracesAgentName},
		{"azure lowercase", "azure", AzureTracesAgentName},
		{"azure mixed case", "Azure", AzureTracesAgentName},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTracesAgent(ctx, "acct-1", services_server.ObservabilityProvider{Provider: c.provider})
			wrapper, ok := a.(*fallbackTracesAgent)
			require.True(t, ok, "expected *fallbackTracesAgent wrapper")
			require.Len(t, wrapper.agents, 1)
			assert.Equal(t, c.wantName, wrapper.agents[0].GetName())
		})
	}
}

func TestGcpTracesAgent_Shape(t *testing.T) {
	a := GcpTracesAgent{accountId: "acct-1"}
	assert.Equal(t, GcpTracesAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "gcloud_execute", "gcp traces agent must expose the gcloud CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "gcp traces agent needs shell_execute for the Cloud Trace v1 REST path")
}

func TestAzureTracesAgent_Shape(t *testing.T) {
	a := AzureTracesAgent{accountId: "acct-1"}
	assert.Equal(t, AzureTracesAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "azure_execute", "azure traces agent must expose the az CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "azure traces agent needs shell_execute for jq post-processing")
}
