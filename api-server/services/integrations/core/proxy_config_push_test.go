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

// Prometheus is dual-mode but keeps the one-per-account rule in direct mode, so
// the type-only predicate stays false while the config-aware one follows
// connection_mode.
func TestPrometheus_ProxyEligibility(t *testing.T) {
	assert.False(t, IsProxyIntegrationType("prometheus"))
	assert.True(t, IsProxyCapableType("prometheus"))

	vm := []IntegrationConfigValue{{Name: "connection_mode", Value: "vm_agent"}}
	direct := []IntegrationConfigValue{{Name: "connection_mode", Value: "direct"}}
	assert.True(t, IsProxyIntegration("prometheus", vm))
	assert.False(t, IsProxyIntegration("prometheus", direct))
	assert.False(t, IsProxyIntegration("prometheus", nil), "rows saved before connection_mode existed are direct")
}

func TestBuildSingleDatasourceConfig_Prometheus(t *testing.T) {
	ds, err := BuildSingleDatasourceConfig("prometheus", []IntegrationConfigValue{
		{Name: "connection_mode", Value: "vm_agent"},
		{Name: "prometheus_url", Value: "https://prom.corp:9090/"},
		{Name: "auth_type", Value: "basic"},
		{Name: "prometheus_username", Value: "writer"},
		{Name: "prometheus_password", Value: "s3cret"},
		{Name: "prometheus_additional_labels", Value: `cluster="x"`},
		{Name: "default_metrics_provider", Value: "true"},
	})
	assert.NoError(t, err)
	assert.Equal(t, "prometheus", ds.Type)
	assert.Equal(t, "http-proxy", ds.ProxyType)
	assert.Equal(t, "https://prom.corp:9090", ds.Config["base_url"])
	assert.Equal(t, "basic", ds.Config["auth_type"])
	assert.Equal(t, map[string]string{"username": "writer", "password": "s3cret"}, ds.Credentials)

	// Nothing prometheus_* and no secret may remain in the plain config map.
	for k := range ds.Config {
		assert.NotContains(t, k, "prometheus_", "config key %q leaked", k)
	}
	assert.NotContains(t, ds.Config, "default_metrics_provider")
	assert.NotContains(t, ds.Config, "connection_mode")
}

func TestNormalizePrometheusProxyDatasource_Auth(t *testing.T) {
	t.Run("bearer", func(t *testing.T) {
		cfg := map[string]any{"prometheus_url": "http://p:9090", "auth_type": "bearer_token", "prometheus_bearer_token": "tok"}
		creds := map[string]string{}
		normalizePrometheusProxyDatasource(cfg, creds)
		assert.Equal(t, "bearer", cfg["auth_type"])
		assert.Equal(t, "tok", creds["bearer_token"])
	})
	t.Run("none sends no auth_type", func(t *testing.T) {
		cfg := map[string]any{"prometheus_url": "http://p:9090", "auth_type": "none"}
		normalizePrometheusProxyDatasource(cfg, map[string]string{})
		assert.NotContains(t, cfg, "auth_type")
	})
}
