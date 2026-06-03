package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/config"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgreSQLIntegration_ValidateConnection_Success(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	accountId := env[testenv.Account]
	tenantId := env[testenv.Tenant]
	sc := security.NewSecurityContextForTenantAdmin(tenantId)

	err := PostgreSql{}.ValidateConfig(sc, []core.IntegrationConfigValue{
		{
			Name:  "k8s_secret",
			Value: "pg-secret",
		},
	}, accountId)
	assert.Nil(t, err)

}

// TestPostgreSQLProxyAgent_Execute tests executing a postgres query through
// the relay for a vm_agent integration registered by the proxy agent.
// Sends a db_query action with X-NB-Agent-Type: proxy header.
func TestPostgreSQLProxyAgent_Execute(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	if config.Config.RelayServerEndpoint == "" {
		t.Skip("relay_server_endpoint not configured")
	}

	accountId := env[testenv.Account]
	datasourceKey := "local:nudgebee-postgres" // agent-side datasource key
	agentType := "K8s"                         // agent type that serves this datasource

	query := `SELECT pid, datname, usename, state, query FROM pg_stat_activity WHERE state = 'active' LIMIT 5`

	relayRequest := map[string]any{
		"body": map[string]any{
			"account_id":  accountId,
			"action_name": "db_query",
			"action_params": map[string]any{
				"datasource_id": datasourceKey,
				"query":         query,
				"timeout_ms":    float64(30000),
			},
			"origin": "test",
		},
		"no_sinks": true,
		"cache":    false,
	}

	reqBody, err := json.Marshal(relayRequest)
	require.NoError(t, err)

	url := fmt.Sprintf("%s/request", config.Config.RelayServerEndpoint)
	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-SECRET-KEY", config.Config.RelayServerSecretKey)
	req.Header.Set("X-NB-Agent-Type", agentType)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err, "HTTP request to relay failed")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	t.Logf("relay response status: %d", resp.StatusCode)
	t.Logf("relay response body: %s", string(body))

	require.Equal(t, 200, resp.StatusCode, "relay returned non-200: %s", string(body))

	var agentResp map[string]any
	err = json.Unmarshal(body, &agentResp)
	require.NoError(t, err, "failed to parse agent response")

	// The proxy agent returns: {"status_code":200,"request_id":"...","data":"{...}"}
	dataStr, ok := agentResp["data"].(string)
	require.True(t, ok, "expected 'data' field to be a string, got: %T", agentResp["data"])

	var dbResult map[string]any
	err = json.Unmarshal([]byte(dataStr), &dbResult)
	require.NoError(t, err, "failed to parse db_query result")

	t.Logf("db_query result: %v", dbResult)
	assert.Contains(t, dbResult, "columns", "response should contain columns")
	assert.Contains(t, dbResult, "rows", "response should contain rows")
	assert.Contains(t, dbResult, "row_count", "response should contain row_count")
}

// TestPostgreSQLProxyAgent_ValidateVMAgent tests ValidateConfig for vm_agent mode.
func TestPostgreSQLProxyAgent_ValidateVMAgent(t *testing.T) {
	errs := PostgreSql{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		{Name: "connection_mode", Value: "vm_agent"},
		{Name: "host", Value: "localhost"},
		{Name: "port", Value: "5432"},
		{Name: "username", Value: "testuser"},
		{Name: "password", Value: "testpass"},
	}, testenv.FakeAccountID)

	assert.Empty(t, errs, "vm_agent validation should pass with valid config: %v", errs)
}

// TestPostgreSQLProxyAgent_ValidateVMAgent_MissingHost tests vm_agent validation
// with only connection_mode (as the proxy agent registers it).
func TestPostgreSQLProxyAgent_ValidateVMAgent_MissingHost(t *testing.T) {
	errs := PostgreSql{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		{Name: "connection_mode", Value: "vm_agent"},
	}, testenv.FakeAccountID)

	// Agent-registered integrations only have connection_mode — no host/credentials.
	// Validation should flag this.
	assert.NotEmpty(t, errs, "vm_agent validation should fail when host is missing")
}
