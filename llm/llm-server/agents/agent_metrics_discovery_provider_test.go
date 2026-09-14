package agents

import (
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discoveryToolProviders returns the provider each metric-discovery tool in the
// list was built with, keyed by tool name.
func discoveryToolProviders(t *testing.T, list []toolcore.NBTool) map[string]string {
	t.Helper()
	providers := map[string]string{}
	for _, tool := range list {
		switch typed := tool.(type) {
		case tools.MetricsListTool:
			providers[typed.Name()] = typed.Provider
		case tools.ListMetricsLabelsTool:
			providers[typed.Name()] = typed.Provider
		case tools.ListMetricsLabelValuesTool:
			providers[typed.Name()] = typed.Provider
		case tools.MetricsSeriesMatchTool:
			providers[typed.Name()] = typed.Provider
		}
	}
	return providers
}

// PrometheusAgent is the fallback for every backend without a dedicated agent, so
// building its discovery tools with a literal "prometheus" made them enumerate a
// provider the account may not have configured. Discovery then came back empty and
// the agent guessed metric names from its Kubernetes priors.
//
// The account here resolves to nothing, so the expected value is the "prometheus"
// default — what this pins is that every discovery tool takes its provider from the
// same resolver, which is what breaks if one is hardcoded again.
func TestPrometheusAgentDiscoveryToolsShareTheResolvedProvider(t *testing.T) {
	const accountId = "00000000-0000-0000-0000-000000000000"
	want := tools.MetricsDiscoveryProvider(accountId)

	agent := PrometheusAgent{accountId: accountId}
	providers := discoveryToolProviders(t, agent.GetSupportedTools(security.NewRequestContextForSuperAdmin()))

	require.NotEmpty(t, providers, "the agent must expose metric-discovery tools")
	for name, got := range providers {
		assert.Equal(t, want, got, "%s must use the resolved metrics provider, not a hardcoded one", name)
	}
}

// The promql agent writes PromQL for whichever backend the account uses, so its
// discovery tools have to enumerate that backend too.
func TestPromqlAgentDiscoveryToolsShareTheResolvedProvider(t *testing.T) {
	const accountId = "00000000-0000-0000-0000-000000000000"
	want := tools.MetricsDiscoveryProvider(accountId)

	agent := &PromqlAgent{accountId: accountId}
	providers := discoveryToolProviders(t, agent.GetSupportedTools(security.NewRequestContextForSuperAdmin()))

	require.NotEmpty(t, providers, "the agent must expose metric-discovery tools")
	for name, got := range providers {
		assert.Equal(t, want, got, "%s must use the resolved metrics provider, not a hardcoded one", name)
	}
}
