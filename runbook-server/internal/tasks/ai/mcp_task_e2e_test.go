//go:build e2e

package ai

import (
	"log/slog"
	"nudgebee/runbook/internal/tasks/testutils"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// getEnvWithFallback returns the value of env var key, or fallback when it is
// unset or empty.
func getEnvWithFallback(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestMCPTask_Execute_ExternalServer(t *testing.T) {
	// Hits a live external MCP server at http://localhost:3000/messages, which is
	// absent on a bare runner. Gate on MCP_E2E_TEST so it only runs when an
	// operator has stood the server up.
	testutils.RequireEnv(t, "MCP_E2E_TEST")

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"url":       "http://localhost:3000/messages",
		"tool_name": "echo",
		"arguments": map[string]any{"message": "Hello, MCP!"},
	}

	result, err := task.Execute(ctx, params)
	assert.NoError(t, err)

	resMap, ok := result.(map[string]any)
	assert.True(t, ok)
	content := resMap["content"].([]any)
	assert.Equal(t, "Hello Hello, MCP!", content[0].(map[string]any)["text"])
}

func TestMCPTask_OAuth2_E2E(t *testing.T) {
	// Requires Keycloak + MCP server running (see tests/mcp-oauth-server/README.md).
	// Set MCP_E2E_TEST=1 to enable.
	testutils.RequireEnv(t, "MCP_E2E_TEST")

	mcpURL := getEnvWithFallback("MCP_SERVER_URL", "http://localhost:3001/mcp")
	tokenURL := getEnvWithFallback("MCP_E2E_TOKEN_URL", "http://localhost:8080/realms/mcp-test/protocol/openid-connect/token")
	clientID := getEnvWithFallback("MCP_E2E_CLIENT_ID", "runbook-server")
	clientSecret := getEnvWithFallback("MCP_E2E_CLIENT_SECRET", "test-secret")
	audience := getEnvWithFallback("MCP_E2E_AUDIENCE", "mcp-test-server")

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("e2e-tenant", "e2e-account", "e2e-user", slog.Default())

	// Test 1: Successful authenticated call with valid credentials
	t.Run("AuthenticatedCall", func(t *testing.T) {

		result, err := task.Execute(ctx, map[string]any{
			"url":                 mcpURL,
			"tool_name":           "echo",
			"arguments":           map[string]any{"message": "hello from e2e"},
			"auth_type":           "oauth2",
			"oauth_token_url":     tokenURL,
			"oauth_client_id":     clientID,
			"oauth_client_secret": clientSecret,
			"oauth_audience":      audience,
		})

		assert.NoError(t, err, "Authenticated MCP call should succeed")
		resMap, ok := result.(map[string]any)
		assert.True(t, ok)
		content := resMap["content"].([]any)
		assert.Contains(t, content[0].(map[string]any)["text"], "hello from e2e")
		t.Logf("Echo response: %v", content[0].(map[string]any)["text"])
	})

	// Test 2: Unauthenticated call should fail with 401
	t.Run("UnauthenticatedCall", func(t *testing.T) {
		_, err := task.Execute(ctx, map[string]any{
			"url":       mcpURL,
			"tool_name": "echo",
			"arguments": map[string]any{"message": "should fail"},
		})

		assert.Error(t, err, "Unauthenticated call should fail")
		assert.Contains(t, err.Error(), "401", "Should get 401 Unauthorized")
		t.Logf("Expected error: %v", err)
	})

	// Test 3: Bad credentials should fail at token fetch
	t.Run("BadCredentials", func(t *testing.T) {

		_, err := task.Execute(ctx, map[string]any{
			"url":                 mcpURL,
			"tool_name":           "echo",
			"arguments":           map[string]any{"message": "should fail"},
			"auth_type":           "oauth2",
			"oauth_token_url":     tokenURL,
			"oauth_client_id":     "nonexistent-client",
			"oauth_client_secret": "wrong-secret",
			"oauth_audience":      audience,
		})

		assert.Error(t, err, "Bad credentials should fail")
		t.Logf("Expected error: %v", err)
	})

	// Test 4: Token with wrong audience should be rejected by MCP server
	t.Run("WrongAudience", func(t *testing.T) {

		// Use the unauthorized-client which has no audience scope mapped
		_, err := task.Execute(ctx, map[string]any{
			"url":                 mcpURL,
			"tool_name":           "echo",
			"arguments":           map[string]any{"message": "wrong audience"},
			"auth_type":           "oauth2",
			"oauth_token_url":     tokenURL,
			"oauth_client_id":     "unauthorized-client",
			"oauth_client_secret": "bad-secret",
			"oauth_audience":      audience,
		})

		assert.Error(t, err, "Token with wrong audience should be rejected")
		assert.Contains(t, err.Error(), "401", "Should get 401 from MCP server")
		t.Logf("Expected error: %v", err)
	})

	// Test 5: Whoami tool (tests different tool on same server)
	t.Run("WhoamiTool", func(t *testing.T) {

		result, err := task.Execute(ctx, map[string]any{
			"url":                 mcpURL,
			"tool_name":           "whoami",
			"arguments":           map[string]any{},
			"auth_type":           "oauth2",
			"oauth_token_url":     tokenURL,
			"oauth_client_id":     clientID,
			"oauth_client_secret": clientSecret,
			"oauth_audience":      audience,
		})

		assert.NoError(t, err, "Whoami call should succeed")
		resMap := result.(map[string]any)
		content := resMap["content"].([]any)
		assert.Contains(t, content[0].(map[string]any)["text"], "Authenticated")
		t.Logf("Whoami response: %v", content[0].(map[string]any)["text"])
	})
}
