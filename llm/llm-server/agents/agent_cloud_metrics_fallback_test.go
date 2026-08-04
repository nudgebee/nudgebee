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

// TestNewMetricsAgent_CloudFallbackRouting verifies that a GCP/Azure metrics
// provider (resolved from the account's cloud type by cloudFallbackProvider)
// routes the metrics dispatcher to the dedicated CLI metrics sub-agent, and that
// AWS / unknown providers keep the existing Prometheus default.
func TestNewMetricsAgent_CloudFallbackRouting(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	cases := []struct {
		name     string
		provider string
		wantName string
	}{
		{"gcp lowercase", "gcp", GcpMetricsAgentName},
		{"gcp mixed case", "GCP", GcpMetricsAgentName},
		{"azure lowercase", "azure", AzureMetricsAgentName},
		{"azure mixed case", "Azure", AzureMetricsAgentName},
		{"aws keeps prometheus", "aws", PrometheusAgentName},
		{"empty keeps prometheus", "", PrometheusAgentName},
		{"unknown keeps prometheus", "something-else", PrometheusAgentName},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newMetricsAgent(ctx, "acct-1", services_server.ObservabilityProvider{Provider: c.provider})
			wrapper, ok := a.(*metricsAgent)
			require.True(t, ok, "expected *metricsAgent wrapper")
			assert.Equal(t, c.wantName, wrapper.agent.GetName())
		})
	}
}

func TestGcpMetricsAgent_Shape(t *testing.T) {
	a := GcpMetricsAgent{accountId: "acct-1"}
	assert.Equal(t, GcpMetricsAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "gcloud_execute", "gcp metrics agent must expose the gcloud CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "gcp metrics agent needs shell_execute for the Cloud Monitoring v3 REST path")
}

func TestAzureMetricsAgent_Shape(t *testing.T) {
	a := AzureMetricsAgent{accountId: "acct-1"}
	assert.Equal(t, AzureMetricsAgentName, a.GetName())
	assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())

	names := toolNames(a.GetSupportedTools(security.NewRequestContextForSuperAdmin()))
	assert.Contains(t, names, "azure_execute", "azure metrics agent must expose the az CLI tool")
	assert.Contains(t, names, toolcore.ToolExecuteShellCommand, "azure metrics agent needs shell_execute for jq post-processing")
}
