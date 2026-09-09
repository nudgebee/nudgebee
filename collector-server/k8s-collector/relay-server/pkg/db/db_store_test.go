package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Pins the proxy-type → integration-type mapping used by datasource
// auto-registration. A proxy type missing here is silently skipped by
// UpsertAgentDatasources, so the datasource never becomes an integration —
// exactly the failure mode that kept discovery datasources invisible
// server-side.
func TestProxyTypeToIntegrationType(t *testing.T) {
	cases := []struct {
		proxyType string
		dsType    string
		want      string
	}{
		{"db-proxy", "postgresql", "postgresql"},
		{"db-proxy", "", ""},
		{"redis-proxy", "", "redis"},
		{"http-proxy", "elastic_search", "elastic_search"},
		{"http-proxy", "http", ""},
		{"mongo-proxy", "", "mongodb_proxy"},
		{"kafka-proxy", "", "kafka_proxy"},
		{"ssh-proxy", "", "ssh"},
		{"mcp-proxy", "", "mcp"},
		{"discovery-proxy", "discovery", "discovery"},
		{"discovery-proxy", "", "discovery"},
		{"unknown-proxy", "", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, proxyTypeToIntegrationType(tc.proxyType, tc.dsType),
			"proxyType=%s dsType=%s", tc.proxyType, tc.dsType)
	}
}

// UpsertAgentDatasources defaults a discovery integration's scan target to the
// agent's own account so the "Scan account for vulnerabilities" button works
// for a self-hosted forager without a separate
// integrations_upsert_discovery_target call. The insert must stay conditional
// on no discovery_target row already existing — it runs on every
// datasource_inventory message (every reconnect), and dropping that guard
// would clobber a target a user deliberately pointed at another cloud account.
// ON CONFLICT DO NOTHING keeps concurrent callers that both pass the guard
// from erroring on the duplicate own-account tuple.
func TestDefaultDiscoveryTargetSQL_IsIdempotentAndTargetScoped(t *testing.T) {
	sql := strings.ToLower(defaultDiscoveryTargetSQL)
	assert.Contains(t, sql, "'discovery_target'")
	assert.Contains(t, sql, "where not exists", "must not overwrite a user-set discovery target on reconnect")
	assert.NotContains(t, sql, "delete", "defaulting the target must never remove an existing association")
	assert.Contains(t, sql, "on conflict", "must handle concurrent inserts of the same default target gracefully")
}

// Proxy types the server addresses per-datasource must get routing config
// values (datasource_key, agent_type, connection_mode) — without
// datasource_key the server can resolve the integration but cannot address
// the datasource on the agent. discovery-proxy belongs here: services-server
// targets a specific discovery datasource via datasource_key.
func TestIsDualModeProxy_CoversServerAddressedProxyTypes(t *testing.T) {
	for _, proxyType := range []string{"db-proxy", "redis-proxy", "http-proxy", "ssh-proxy", "mcp-proxy", "discovery-proxy"} {
		assert.True(t, isDualModeProxy(proxyType), proxyType)
	}
	assert.False(t, isDualModeProxy("unknown-proxy"))
}
