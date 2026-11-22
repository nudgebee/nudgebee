package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm/config"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkedHealth(t *testing.T, raw json.RawMessage) mcpHealth {
	t.Helper()
	var h mcpHealth
	require.NoError(t, json.Unmarshal(raw, &h))
	return h
}

// Check now is authoritative: a connected server that fails is reported down at
// once instead of after the 2-minute hold, and every pod is told.
func TestCheckMCPIntegration_DirectSkipsTheHold(t *testing.T) {
	store := useMemMCPHealthStore(t)
	stub := newMCPRelayStub(t)
	stub.set(func(string, string) (int, string) { return http.StatusBadGateway, mcpRefusedBody })
	var notified []string
	orig := MCPIntegrationHealthChanged
	MCPIntegrationHealthChanged = func(id string, accounts []string) { notified = append(notified, id+" "+fmt.Sprint(accounts)) }
	t.Cleanup(func() { MCPIntegrationHealthChanged = orig })

	checked := time.Now().Add(-time.Minute)
	store.health["int-direct"] = mcpHealth{mcpHealthAllScopes: {Status: mcpHealthConnected, LastCheckedAt: &checked}}
	target := MCPCheckTarget{
		TenantID: "t1",
		cfg:      mcpIntegrationConfig{IntegrationID: "int-direct", IntegrationName: "direct", ConnectionMode: "direct"},
		enabled:  true,
		accounts: []string{"acc-1", "acc-2"},
	}

	raw, err := CheckMCPIntegration(target)
	require.NoError(t, err)
	h := checkedHealth(t, raw)
	assert.Equal(t, mcpHealthUnreachable, h[mcpHealthAllScopes].Status)
	assert.Equal(t, 1, stub.discovered("int-direct"), "one discovery covers every account of a direct integration")
	assert.Equal(t, []string{"int-direct []"}, notified)

	// Back up: connected again with its tool count.
	stub.set(func(string, string) (int, string) { return http.StatusOK, mcpToolsListBody("query") })
	raw, err = CheckMCPIntegration(target)
	require.NoError(t, err)
	h = checkedHealth(t, raw)
	assert.Equal(t, mcpHealthConnected, h[mcpHealthAllScopes].Status)
	assert.Equal(t, 1, *h[mcpHealthAllScopes].ToolCount)
	assert.Len(t, notified, 2)
}

// A vm_agent integration is reached through each account's own agent, so each
// linked account is checked and stored under its own scope.
func TestCheckMCPIntegration_VMAgentChecksEveryAccount(t *testing.T) {
	useMemMCPHealthStore(t)
	stub := newMCPRelayStub(t)
	stub.set(func(string, string) (int, string) { return http.StatusOK, mcpToolsListBody("query") })
	t.Cleanup(func() {
		for _, a := range []string{"acc-1", "acc-2", "acc-3"} {
			mcpIntegrationToolCacheInstance.delete(a)
		}
	})

	raw, err := CheckMCPIntegration(MCPCheckTarget{
		cfg:      mcpIntegrationConfig{IntegrationID: "int-vm", IntegrationName: "vm", ConnectionMode: "vm_agent", DatasourceKey: "local:vm"},
		enabled:  true,
		accounts: []string{"acc-1", "acc-2", "acc-3"},
	})
	require.NoError(t, err)
	h := checkedHealth(t, raw)
	assert.Equal(t, 3, stub.discovered(""), "vm_agent requests name the datasource, not the integration")
	for _, a := range []string{"acc-1", "acc-2", "acc-3"} {
		assert.Equal(t, mcpHealthConnected, h[a].Status, a)
	}
	assert.NotContains(t, h, mcpHealthAllScopes)
}

func TestCheckMCPIntegration_Refusals(t *testing.T) {
	useMemMCPHealthStore(t)
	stub := newMCPRelayStub(t)
	stub.set(func(string, string) (int, string) { return http.StatusOK, mcpToolsListBody("query") })
	cfg := mcpIntegrationConfig{IntegrationID: "int-r", IntegrationName: "r", ConnectionMode: "direct"}

	_, err := CheckMCPIntegration(MCPCheckTarget{cfg: cfg, enabled: false, accounts: []string{"acc-1"}})
	assert.ErrorIs(t, err, ErrMCPIntegrationNotEnabled)
	_, err = CheckMCPIntegration(MCPCheckTarget{cfg: cfg, enabled: true})
	assert.ErrorIs(t, err, ErrMCPIntegrationNoAccounts)
	assert.Equal(t, 0, stub.discovered("int-r"))
}

// When the relay itself can't be reached the check learns nothing about the
// server, so it says so instead of returning the old verdict as if fresh.
func TestCheckMCPIntegration_RelayUnreachableIsInconclusive(t *testing.T) {
	store := useMemMCPHealthStore(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	orig := config.Config.RelayServerEndpoint
	config.Config.RelayServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.RelayServerEndpoint = orig })

	_, err := CheckMCPIntegration(MCPCheckTarget{
		cfg:      mcpIntegrationConfig{IntegrationID: "int-norelay", IntegrationName: "norelay", ConnectionMode: "direct"},
		enabled:  true,
		accounts: []string{"acc-1"},
	})
	assert.ErrorIs(t, err, ErrMCPCheckInconclusive)
	assert.Empty(t, store.entry("int-norelay", mcpHealthAllScopes).Status)
}

func TestLoadMCPCheckTargetFromDB(t *testing.T) {
	mock := registerMockMetastore(t)

	mock.ExpectQuery(`FROM integrations i\s+WHERE i.id = \$1::uuid AND i.type = 'mcp'`).WithArgs("int-1").
		WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "status", "accounts"}).
			AddRow("t1", "enabled", []byte(`["acc-1","acc-2"]`)))
	mock.ExpectQuery(`LEFT JOIN integration_config_values icv`).WithArgs("int-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "config_name", "config_value", "config_encrypted"}).
			AddRow("int-1", "My Server", "connection_mode", "vm_agent", false).
			AddRow("int-1", "My Server", "datasource_key", "local:my", false))
	got, err := loadMCPCheckTargetFromDB("int-1")
	require.NoError(t, err)
	assert.Equal(t, "t1", got.TenantID)
	assert.True(t, got.enabled)
	assert.Equal(t, []string{"acc-1", "acc-2"}, got.accounts)
	assert.Equal(t, "vm_agent", got.cfg.ConnectionMode)
	assert.Equal(t, "local:my", got.cfg.DatasourceKey)
	assert.Equal(t, "My Server", got.cfg.IntegrationName)

	// Not an MCP integration, or gone.
	mock.ExpectQuery(`FROM integrations i`).WithArgs("int-2").WillReturnRows(sqlmock.NewRows([]string{"tenant_id", "status", "accounts"}))
	_, err = loadMCPCheckTargetFromDB("int-2")
	assert.ErrorIs(t, err, ErrMCPIntegrationNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
