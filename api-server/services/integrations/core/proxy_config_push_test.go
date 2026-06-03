package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsProxyIntegrationType_DualModeAndAlwaysProxy pins the type-only check
// against the create-path and enable-path duplicate-check skip-lists in
// integration_config.go. Both call sites use IsProxyIntegrationType to allow
// multiple integrations per account for these types — if any entry here
// regresses, the one-per-account block silently returns for that type and
// users can't save a second integration. There's no test for the
// per-account block itself (it lives in a function with heavy DB plumbing),
// so this is the regression guard.
func TestIsProxyIntegrationType_DualModeAndAlwaysProxy(t *testing.T) {
	// Dual-mode types: multiple per account legitimate in both k8s and
	// vm_agent modes. Each row addresses a distinct backend by name at
	// execute time via the LLM tool's IdentifyConfig.
	dualMode := []string{
		"postgresql",
		"mysql",
		"clickhouse",
		"mssql",
		"oracle",
		"redis",
		"ssh",
		"mcp",
	}
	for _, typ := range dualMode {
		t.Run("dual_"+typ, func(t *testing.T) {
			assert.True(t, IsProxyIntegrationType(typ),
				"dual-mode type %q must be considered proxy-eligible so duplicates per account are allowed", typ)
		})
	}

	// Always-proxy types: no k8s equivalent, multiple per account inherent.
	alwaysProxy := []string{"http_proxy", "mongodb_proxy", "kafka_proxy"}
	for _, typ := range alwaysProxy {
		t.Run("always_"+typ, func(t *testing.T) {
			assert.True(t, IsProxyIntegrationType(typ),
				"always-proxy type %q must be considered proxy-eligible", typ)
		})
	}

	// Single-instance types: a sample of restricted-to-one-per-account types,
	// asserting the predicate stays false for them. If this flips, we'd
	// silently start allowing duplicates that the resolver can't disambiguate.
	singleInstance := []string{
		"datadog",
		"jira",
		"github",
		"argocd",
		"pagerduty",
		"signoz",
		"prometheus",
		"loki",
	}
	for _, typ := range singleInstance {
		t.Run("single_"+typ, func(t *testing.T) {
			assert.False(t, IsProxyIntegrationType(typ),
				"single-instance type %q must NOT be considered proxy-eligible", typ)
		})
	}
}

// TestIsProxyIntegration_ConfigAware verifies the config-aware variant: for
// dual-mode types, only `connection_mode=vm_agent` is treated as a proxy.
// This predicate still drives the proxy-connectivity test path further down
// in UpsertIntegrationConfig — only vm_agent rows need a live forager probe.
func TestIsProxyIntegration_ConfigAware(t *testing.T) {
	vmAgent := []IntegrationConfigValue{{Name: "connection_mode", Value: "vm_agent"}}
	k8s := []IntegrationConfigValue{{Name: "connection_mode", Value: "k8s"}}
	none := []IntegrationConfigValue{}

	for _, typ := range []string{"postgresql", "mysql", "clickhouse", "mssql", "oracle", "redis", "ssh", "mcp"} {
		t.Run(typ+"/vm_agent_is_proxy", func(t *testing.T) {
			assert.True(t, IsProxyIntegration(typ, vmAgent))
		})
		t.Run(typ+"/k8s_is_not_proxy", func(t *testing.T) {
			assert.False(t, IsProxyIntegration(typ, k8s))
		})
		t.Run(typ+"/no_mode_is_not_proxy", func(t *testing.T) {
			assert.False(t, IsProxyIntegration(typ, none))
		})
	}

	// Always-proxy types ignore connection_mode entirely.
	for _, typ := range []string{"http_proxy", "mongodb_proxy", "kafka_proxy"} {
		t.Run(typ+"/always_proxy_regardless_of_mode", func(t *testing.T) {
			assert.True(t, IsProxyIntegration(typ, k8s))
			assert.True(t, IsProxyIntegration(typ, none))
		})
	}
}
