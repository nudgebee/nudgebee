//go:build integration

package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/workspace"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVMAgentWorkspaceUnwrap is an end-to-end integration test for the
// workspace → VM-agent (forager) path.
//
// Why this exists:
//   - When workspace mode is enabled, tool_mssql.go wraps the query in
//     `sqlcmd -d "db" -Q "SQL" -s "\t" -W` and hands it to the workspace
//     manager.
//   - The workspace pod's shim intercepts the sqlcmd call and posts it back to
//     llm-server /api/v1/workspace/execute with the full wrapped string as
//     `command`.
//   - handleWorkspaceExecute then calls ExecuteContainerJob(..., raw=true),
//     which for a vm_agent datasource routes into executeViaProxyAgent.
//   - Pre-fix (forager < commit 5754003 / 2026-03-27): the wrapped string
//     reached MSSQL as SQL and produced `Incorrect syntax near 'Q'` / `'d'`.
//   - Post-fix: ExecuteContainerJob strips the sqlcmd wrapping before
//     dispatching, so the forager (any version) receives pure SQL.
//
// This test exercises the full path against a live llm-server, relay, and a
// VM-agent datasource. Gate is `integration` — run it manually with a relay
// and forager reachable from the test binary.
//
// Config sources (first-wins):
//  1. Process env vars
//  2. .env file in the llm-server directory (same file `make run` uses)
//
// Variables:
//
//	LLM_SERVER_URL         default http://localhost:9999 (port-forward or `make run`)
//	LLM_SERVER_JWT_SECRET  default "default-jwt-secret" (matches config.go default)
//	NB_TEST_ACCOUNT_ID     required — account with a connected proxy/forager
//	NB_TEST_TENANT_ID      required — tenant for that account (for JWT claim)
//	NB_TEST_MSSQL_CONFIG   required — integration name (config_name) of an mssql
//	                       vm_agent datasource whose forager is CONNECTED
//
// Run:
//
//	go test -v -tags=integration ./api/... -run TestVMAgentWorkspaceUnwrap
func TestVMAgentWorkspaceUnwrap(t *testing.T) {
	env := loadEnvWithDotfile(t)

	baseURL := envOr(env, "LLM_SERVER_URL", "http://localhost:9999")
	jwtSecret := envOr(env, "LLM_SERVER_JWT_SECRET", "default-jwt-secret")
	accountID := env["NB_TEST_ACCOUNT_ID"]
	tenantID := env["NB_TEST_TENANT_ID"]
	mssqlConfig := env["NB_TEST_MSSQL_CONFIG"]
	if accountID == "" || tenantID == "" || mssqlConfig == "" {
		t.Skip("set NB_TEST_ACCOUNT_ID, NB_TEST_TENANT_ID, NB_TEST_MSSQL_CONFIG (process env or .env) to run")
	}

	token := mintWorkspaceToken(t, jwtSecret, accountID, tenantID)

	cases := []struct {
		name    string
		command string
	}{
		// All three of these hit the vm_agent path. Pre-fix foragers fail the
		// second and third with "Incorrect syntax near 'd'/'Q'".
		{
			name:    "raw_sql_baseline",
			command: "SELECT 1",
		},
		{
			name:    "sqlcmd_with_database_flag",
			command: `sqlcmd -d "master" -Q "SELECT 1" -s "	" -W`,
		},
		{
			name:    "sqlcmd_without_database_flag",
			command: `sqlcmd -Q "SELECT 1" -s "	" -W`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := postWorkspaceExecute(t, baseURL, token, map[string]any{
				"account_id":  accountID,
				"tool":        "sqlcmd",
				"command":     tc.command,
				"config_name": mssqlConfig,
				"arguments":   map[string]any{"database": "master"},
			})
			require.Equal(t, http.StatusOK, status, "body: %s", body)
			// The relay wraps the forager's db_query reply; on success the
			// forager returns a JSON with `"row_count": 1` / `"rows":[[1]]`.
			assert.Contains(t, body, `"row_count":1`, "expected a single-row success result, got: %s", body)
			assert.NotContains(t, body, "Incorrect syntax", "unwrap regressed: forager/MSSQL saw sqlcmd text as SQL")
		})
	}
}

func mintWorkspaceToken(t *testing.T, secret, accountID, tenantID string) string {
	t.Helper()
	claims := workspace.WorkspaceTokenClaims{
		AccountId: accountID,
		TenantId:  tenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

// loadEnvWithDotfile returns a map of env vars, starting from the process
// environment and overlaid with non-overriding values from the llm-server
// .env file (same file `make run` uses). Process env wins if both set a key.
func loadEnvWithDotfile(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	// Walk up from the test file location looking for the .env sibling of the
	// llm-server go.mod. The test runs with CWD = package dir (api/), so
	// ../.env is the llm-server root.
	for _, candidate := range []string{".env", filepath.Join("..", ".env")} {
		f, err := os.Open(candidate)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			k := strings.TrimSpace(line[:eq])
			v := strings.TrimSpace(line[eq+1:])
			v = strings.Trim(v, `"'`)
			// Upper-case the key since config.go uses viper which is case-insensitive
			// and the .env mixes cases. We normalize to UPPER so lookups work.
			upper := strings.ToUpper(k)
			if _, set := out[upper]; !set {
				out[upper] = v
			}
		}
		_ = f.Close()
		break
	}
	return out
}

func envOr(env map[string]string, key, fallback string) string {
	if v, ok := env[key]; ok && v != "" {
		return v
	}
	return fallback
}

func postWorkspaceExecute(t *testing.T, baseURL, token string, body map[string]any) (int, string) {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/v1/workspace/execute", bytes.NewReader(buf))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}
