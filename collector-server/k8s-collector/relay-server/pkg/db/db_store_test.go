package db

import (
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
