package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"
	integrationsModel "nudgebee/runbook/services/integrations"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/temporal"
)

func TestMCPTask_Execute_JSON(t *testing.T) {
	// Mock MCP Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Check for custom header if present
		if r.Header.Get("X-Custom-Auth") != "" {
			assert.Equal(t, "secret-token", r.Header.Get("X-Custom-Auth"))
		}

		var req JsonRpcRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-123")

		resp := JsonRpcResponse{
			JsonRPC: "2.0",
			ID:      req.ID,
		}

		switch req.Method {
		case "initialize":
			// Return dummy init result
			resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			// Return tool result
			if params, ok := req.Params.(map[string]any); ok && params["name"] == "error-tool" {
				resp.Result = json.RawMessage(`{"content":[{"type":"text","text":"Some Error"}],"isError":true}`)
			} else {
				resp.Result = json.RawMessage(`{"content":[{"type":"text","text":"Success"}],"isError":false}`)
			}
		default:
			t.Errorf("Unexpected method: %s", req.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	// Test Success
	params := map[string]any{
		"url":       ts.URL,
		"tool_name": "test-tool",
		"arguments": map[string]any{"arg": "val"},
	}

	result, err := task.Execute(ctx, params)
	assert.NoError(t, err)

	resMap, ok := result.(map[string]any)
	assert.True(t, ok)
	assert.False(t, resMap["isError"].(bool))
	content := resMap["content"].([]any)
	assert.Len(t, content, 1)
	assert.Equal(t, "Success", content[0].(map[string]any)["text"])

	// Test Tool Error (should return result with isError=true, not Go error)
	paramsErr := map[string]any{
		"url":       ts.URL,
		"tool_name": "error-tool",
		"arguments": map[string]any{"arg": "val"},
	}
	resultErr, errErr := task.Execute(ctx, paramsErr)
	assert.NoError(t, errErr)
	resMapErr, ok := resultErr.(map[string]any)
	assert.True(t, ok)
	assert.True(t, resMapErr["isError"].(bool))
	contentErr := resMapErr["content"].([]any)
	assert.Equal(t, "Some Error", contentErr[0].(map[string]any)["text"])

	// Test Custom Headers
	paramsHeaders := map[string]any{
		"url":       ts.URL,
		"tool_name": "test-tool",
		"arguments": map[string]any{"arg": "val"},
		"headers": map[string]any{
			"X-Custom-Auth": "secret-token",
		},
	}
	resultHeaders, errHeaders := task.Execute(ctx, paramsHeaders)
	assert.NoError(t, errHeaders)
	resMapHeaders, ok := resultHeaders.(map[string]any)
	assert.True(t, ok)
	assert.False(t, resMapHeaders["isError"].(bool))
}

func TestMCPTask_Execute_SSE(t *testing.T) {
	// Mock MCP Server with SSE
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Method == "initialize" {
			// Return SSE
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Mcp-Session-Id", "session-sse")

			resp := JsonRpcResponse{
				JsonRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`),
			}
			b, _ := json.Marshal(resp)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
			return
		}

		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		if req.Method == "tools/call" {
			w.Header().Set("Content-Type", "text/event-stream")
			resp := JsonRpcResponse{
				JsonRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{"content":[{"type":"text","text":"SSE Success"}],"isError":false}`),
			}
			b, _ := json.Marshal(resp)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
			return
		}
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	params := map[string]any{
		"url":       ts.URL,
		"tool_name": "test-tool",
	}

	result, err := task.Execute(ctx, params)
	assert.NoError(t, err)

	resMap, ok := result.(map[string]any)
	assert.True(t, ok)
	content := resMap["content"].([]any)
	assert.Equal(t, "SSE Success", content[0].(map[string]any)["text"])
}

// TestMCPTask_Execute_SSE_LargePayload exercises the scanner buffer fix.
// A single `data:` frame > 64 KiB (the default bufio.Scanner token size)
// previously broke with "bufio.Scanner: token too long". With the raised
// buffer cap the same frame must decode cleanly.
func TestMCPTask_Execute_SSE_LargePayload(t *testing.T) {
	// Build a tool-result payload whose JSON encoding comfortably exceeds 64 KiB.
	bigText := strings.Repeat("A", 200*1024)
	resultJSON := fmt.Sprintf(`{"content":[{"type":"text","text":%q}],"isError":false}`, bigText)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "text/event-stream")
			b, _ := json.Marshal(JsonRpcResponse{JsonRPC: "2.0", ID: req.ID,
				Result: json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			b, _ := json.Marshal(JsonRpcResponse{JsonRPC: "2.0", ID: req.ID, Result: json.RawMessage(resultJSON)})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
		}
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())
	result, err := task.Execute(ctx, map[string]any{"url": ts.URL, "tool_name": "big"})
	assert.NoError(t, err)
	resMap := result.(map[string]any)
	content := resMap["content"].([]any)
	assert.Equal(t, bigText, content[0].(map[string]any)["text"])
}

// TestMCPTask_Execute_SSE_MultilineDataFrame verifies the SSE parser
// concatenates consecutive `data:` lines with "\n" per the SSE spec. Previously
// each line was decoded independently, so servers that broke one JSON response
// across multiple data lines surfaced as "response not found in SSE stream".
func TestMCPTask_Execute_SSE_MultilineDataFrame(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			b, _ := json.Marshal(JsonRpcResponse{JsonRPC: "2.0", ID: req.ID,
				Result: json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)})
			_, _ = w.Write(b)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			// Split the JSON response across two `data:` lines at an
			// arbitrary point — a spec-compliant client must join them.
			idStr, _ := json.Marshal(req.ID)
			line1 := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,`, string(idStr))
			line2 := `"result":{"content":[{"type":"text","text":"joined"}],"isError":false}}`
			_, _ = fmt.Fprintf(w, "data: %s\ndata: %s\n\n", line1, line2)
		}
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())
	result, err := task.Execute(ctx, map[string]any{"url": ts.URL, "tool_name": "join"})
	assert.NoError(t, err)
	content := result.(map[string]any)["content"].([]any)
	assert.Equal(t, "joined", content[0].(map[string]any)["text"])
}

// TestMCPTask_Execute_UnsupportedContentType verifies that a deterministic
// client-side failure surfaces as a non-retryable Temporal ApplicationError,
// so Temporal doesn't burn the retry budget on a server that will always
// return the same bad content type.
func TestMCPTask_Execute_UnsupportedContentType(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())
	_, err := task.Execute(ctx, map[string]any{"url": ts.URL, "tool_name": "x"})
	assert.Error(t, err)
	var appErr *temporal.ApplicationError
	assert.ErrorAs(t, err, &appErr, "expected Temporal ApplicationError")
	if appErr != nil {
		assert.True(t, appErr.NonRetryable(), "parser errors should be non-retryable")
		assert.Equal(t, mcpErrTypeBadResponse, appErr.Type())
	}
}

func TestMCPTask_Execute_ExternalServer(t *testing.T) {

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

// newMCPAuthTestServer creates a mock MCP server that validates the Authorization
// header on every request and returns a standard tool result on success.
func newMCPAuthTestServer(_ *testing.T, validateAuth func(r *http.Request) bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validateAuth(r) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}

		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "auth-session")

		resp := JsonRpcResponse{JsonRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			resp.Result = json.RawMessage(`{"content":[{"type":"text","text":"Authenticated"}],"isError":false}`)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestMCPTask_OAuth2Auth(t *testing.T) {
	// OAuth token server
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"mcp-oauth-token","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	ts := newMCPAuthTestServer(t, func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer mcp-oauth-token"
	})
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(ctx, map[string]any{
		"url":                 ts.URL,
		"tool_name":           "test-tool",
		"auth_type":           "oauth2",
		"oauth_token_url":     tokenServer.URL,
		"oauth_client_id":     "mcp-client",
		"oauth_client_secret": "mcp-secret",
		"oauth_scope":         "mcp.read",
	})

	assert.NoError(t, err)
	resMap := result.(map[string]any)
	content := resMap["content"].([]any)
	assert.Equal(t, "Authenticated", content[0].(map[string]any)["text"])
}

func TestMCPTask_OAuth2MissingFields(t *testing.T) {
	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	_, err := task.Execute(ctx, map[string]any{
		"url":       "https://example.com/mcp",
		"tool_name": "test-tool",
		"auth_type": "oauth2",
		// Missing required OAuth fields
	})

	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "OAuth 2.0 requires") || strings.Contains(err.Error(), "authentication"))
}

func TestMCPTask_NoAuth(t *testing.T) {
	// Verify that requests without auth_type still work (no regression)
	ts := newMCPAuthTestServer(t, func(r *http.Request) bool {
		return true // Accept all
	})
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(ctx, map[string]any{
		"url":       ts.URL,
		"tool_name": "test-tool",
	})

	assert.NoError(t, err)
	resMap := result.(map[string]any)
	assert.False(t, resMap["isError"].(bool))
}

// Integration test: OAuth2 client_credentials with real Auth0 token + local MCP mock.
// Fetches a real token from Auth0, then verifies it's sent as Authorization header
// to the MCP server. Set these env vars to run:
//
//	OAUTH_TOKEN_URL=https://your-tenant.auth0.com/oauth/token
//	OAUTH_CLIENT_ID=...
//	OAUTH_CLIENT_SECRET=...
//	OAUTH_AUDIENCE=https://your-tenant.auth0.com/api/v2/
func TestMCPTask_OAuth2_Integration(t *testing.T) {
	tokenURL := os.Getenv("OAUTH_TOKEN_URL")
	clientID := os.Getenv("OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("OAUTH_CLIENT_SECRET")
	audience := os.Getenv("OAUTH_AUDIENCE")

	if tokenURL == "" || clientID == "" || clientSecret == "" {
		t.Skip("Skipping MCP OAuth2 integration test. Set OAUTH_TOKEN_URL, OAUTH_CLIENT_ID, OAUTH_CLIENT_SECRET to enable.")
	}

	// Clean up global OAuth state

	// Local MCP server that captures the Authorization header
	var receivedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")

		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "integration-session")

		resp := JsonRpcResponse{JsonRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			resp.Result = json.RawMessage(`{"content":[{"type":"text","text":"OAuth OK"}],"isError":false}`)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	result, err := task.Execute(ctx, map[string]any{
		"url":                 ts.URL,
		"tool_name":           "test-tool",
		"auth_type":           "oauth2",
		"oauth_token_url":     tokenURL,
		"oauth_client_id":     clientID,
		"oauth_client_secret": clientSecret,
		"oauth_audience":      audience,
	})

	assert.NoError(t, err)
	resMap := result.(map[string]any)
	content := resMap["content"].([]any)
	assert.Equal(t, "OAuth OK", content[0].(map[string]any)["text"])

	assert.NotEmpty(t, receivedAuth, "Authorization header should be present")
	assert.True(t, strings.HasPrefix(receivedAuth, "Bearer "), "Should start with 'Bearer '")
	t.Logf("MCP OAuth2 token (first 50 chars): %.50s...", receivedAuth)
}

// E2E test: Real Keycloak (OAuth provider) + real MCP server with JWT validation.
// Tests the full flow: fetch token from Keycloak → call real MCP server → server validates JWT → returns tool result.
//
// Prerequisites (see tests/mcp-oauth-server/README.md):
//  1. cd tests/mcp-oauth-server && docker compose up -d   (starts Keycloak)
//  2. cd tests/mcp-oauth-server && npm install && npm start (starts MCP server)
//
// Then run:
//
//	MCP_E2E_TEST=1 go test ./internal/tasks/ai/ -run TestMCPTask_OAuth2_E2E -v
//
// TestMCPTask_DecryptIntegrationConfig verifies that encrypted integration config
// values (like oauth_client_secret) are properly decrypted when building the configMap.
// This exercises the same logic used in executeViaIntegration.
func TestMCPTask_DecryptIntegrationConfig(t *testing.T) {
	// Set up a test encryption key (32 bytes = 64 hex chars)
	testKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	originalKey := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = testKey
	defer func() { config.Config.NudgebeeEncryptionKey = originalKey }()

	plainSecret := "my-super-secret-oauth-client-secret"
	encrypted, err := common.Encrypt(plainSecret)
	assert.NoError(t, err)
	assert.NotEqual(t, plainSecret, encrypted)

	// Simulate integration config values as returned by api-server
	values := []integrationsModel.IntegrationConfigValue{
		{Name: "url", Value: "https://mcp.example.com", IsEncrypted: false},
		{Name: "auth_type", Value: "oauth2", IsEncrypted: false},
		{Name: "oauth_client_id", Value: "my-client-id", IsEncrypted: false},
		{Name: "oauth_client_secret", Value: encrypted, IsEncrypted: true},
		{Name: "oauth_token_url", Value: "https://auth.example.com/token", IsEncrypted: false},
		{Name: "empty_encrypted", Value: "", IsEncrypted: true}, // edge case: empty encrypted field
	}

	// Build configMap using the same logic as executeViaIntegration
	configMap := make(map[string]string)
	for _, v := range values {
		val := v.Value
		if v.IsEncrypted && val != "" {
			decrypted, err := common.Decrypt(val)
			assert.NoError(t, err, "Failed to decrypt field %q", v.Name)
			val = decrypted
		}
		configMap[v.Name] = val
	}

	// Verify plaintext fields pass through unchanged
	assert.Equal(t, "https://mcp.example.com", configMap["url"])
	assert.Equal(t, "oauth2", configMap["auth_type"])
	assert.Equal(t, "my-client-id", configMap["oauth_client_id"])
	assert.Equal(t, "https://auth.example.com/token", configMap["oauth_token_url"])

	// Verify encrypted field was decrypted
	assert.Equal(t, plainSecret, configMap["oauth_client_secret"], "Encrypted oauth_client_secret should be decrypted to plaintext")

	// Verify empty encrypted field stays empty (no decrypt attempt)
	assert.Equal(t, "", configMap["empty_encrypted"])
}

// TestMCPTask_DecryptIntegrationConfig_BadHex verifies that non-hex encrypted
// values produce a clear error rather than silently passing through.
func TestMCPTask_DecryptIntegrationConfig_BadHex(t *testing.T) {
	testKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	originalKey := config.Config.NudgebeeEncryptionKey
	config.Config.NudgebeeEncryptionKey = testKey
	defer func() { config.Config.NudgebeeEncryptionKey = originalKey }()

	_, err := common.Decrypt("not-valid-hex")
	assert.Error(t, err, "Invalid hex should fail")
}

func TestMCPTask_OAuth2_E2E(t *testing.T) {
	// if os.Getenv("MCP_E2E_TEST") == "" {
	// 	t.Skip("Skipping MCP OAuth2 E2E test. Set MCP_E2E_TEST=1 to enable. Requires Keycloak + MCP server running (see tests/mcp-oauth-server/README.md).")
	// }

	mcpURL := os.Getenv("MCP_SERVER_URL")
	if mcpURL == "" {
		mcpURL = "http://localhost:3001/mcp"
	}
	tokenURL := os.Getenv("MCP_E2E_TOKEN_URL")
	if tokenURL == "" {
		tokenURL = "http://localhost:8080/realms/mcp-test/protocol/openid-connect/token"
	}
	clientID := os.Getenv("MCP_E2E_CLIENT_ID")
	if clientID == "" {
		clientID = "runbook-server"
	}
	clientSecret := os.Getenv("MCP_E2E_CLIENT_SECRET")
	if clientSecret == "" {
		clientSecret = "test-secret"
	}
	audience := os.Getenv("MCP_E2E_AUDIENCE")
	if audience == "" {
		audience = "mcp-test-server"
	}

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

// newMCPListToolsTestServer creates a mock MCP server that completes the
// initialize → tools/list handshake. validateAuth lets the test require a
// specific Authorization header (returning 401 if it's missing/wrong).
func newMCPListToolsTestServer(_ *testing.T, validateAuth func(r *http.Request) bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validateAuth(r) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}

		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "list-session")

		resp := JsonRpcResponse{JsonRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			resp.Result = json.RawMessage(`{"tools":[{"name":"echo","description":"Echo input"},{"name":"whoami","description":"Identify caller"}]}`)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestMCPTask_ListTools_DirectURL_OAuth2(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"list-tools-token","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	ts := newMCPListToolsTestServer(t, func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer list-tools-token"
	})
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	tools, err := task.ListTools(ctx, map[string]any{
		"url":                 ts.URL,
		"auth_type":           "oauth2",
		"oauth_token_url":     tokenServer.URL,
		"oauth_client_id":     "client",
		"oauth_client_secret": "secret",
		"oauth_scope":         "mcp.read",
	})
	assert.NoError(t, err)
	assert.Len(t, tools, 2)
	assert.Equal(t, "echo", tools[0].Name)
	assert.Equal(t, "whoami", tools[1].Name)
}

func TestMCPTask_ListTools_DirectURL_OAuth2_MissingFields(t *testing.T) {
	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	_, err := task.ListTools(ctx, map[string]any{
		"url":       "http://mcp-oauth.example.test/mcp",
		"auth_type": "oauth2",
		// missing oauth_token_url / oauth_client_id / oauth_client_secret
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth 2.0 requires")
}

func TestMCPTask_ListTools_DirectURL_NoAuth(t *testing.T) {
	// No auth required — should still work (regression check).
	ts := newMCPListToolsTestServer(t, func(r *http.Request) bool { return true })
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	tools, err := task.ListTools(ctx, map[string]any{"url": ts.URL})
	assert.NoError(t, err)
	assert.Len(t, tools, 2)
}

func TestMCPTask_ListTools_DirectURL_401Surfaces(t *testing.T) {
	// Server requires auth but caller provided none — error should be clear.
	ts := newMCPListToolsTestServer(t, func(r *http.Request) bool {
		return r.Header.Get("Authorization") != ""
	})
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	_, err := task.ListTools(ctx, map[string]any{"url": ts.URL})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

// TestCoerceToMap covers the exact failure mode from the merged #28227 follow-up:
// a workflow param rendered by a template may arrive at Execute() as the raw
// resolved value — which is a plain string when the template pointed at a
// Config (model.Config.Value is a string column). The old silent type assertion
// would drop such values and send an empty map to the MCP server. After the fix,
// a JSON-object string round-trips cleanly, and garbage produces a loud error
// instead of a silent drop.
func TestCoerceToMap(t *testing.T) {
	t.Run("nil passes through", func(t *testing.T) {
		m, err := coerceToMap(nil)
		assert.NoError(t, err)
		assert.Nil(t, m)
	})

	t.Run("map passes through unchanged", func(t *testing.T) {
		in := map[string]any{"k": "v", "n": float64(1)}
		m, err := coerceToMap(in)
		assert.NoError(t, err)
		assert.Equal(t, in, m)
	})

	t.Run("JSON object string parses", func(t *testing.T) {
		// This is the user's scenario: Config 'dev-pg' stored as a JSON string,
		// referenced in mcp.arguments via `{{ Configs['dev-pg'] }}`. ProcessValue
		// resolves to the raw string; coerceToMap unwraps it.
		m, err := coerceToMap(`{"host":"pg","port":5432}`)
		assert.NoError(t, err)
		assert.Equal(t, "pg", m["host"])
		assert.Equal(t, float64(5432), m["port"])
	})

	t.Run("whitespace-only string returns nil (treated as unset)", func(t *testing.T) {
		m, err := coerceToMap("   ")
		assert.NoError(t, err)
		assert.Nil(t, m)
	})

	t.Run("non-JSON string returns descriptive error", func(t *testing.T) {
		_, err := coerceToMap("not json at all")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected a JSON object")
	})

	t.Run("JSON array rejected (not an object)", func(t *testing.T) {
		_, err := coerceToMap(`[1,2,3]`)
		assert.Error(t, err)
	})

	t.Run("wrong type (int) returns type error", func(t *testing.T) {
		_, err := coerceToMap(42)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expected object (map)")
	})
}

// TestMCPTask_Execute_ArgumentsAsJSONStringFromTemplate is the end-to-end proof
// that #28227's template → Configs → mcp.arguments path now delivers data to
// the MCP server. Simulates the executor's flow: ProcessValue has already
// resolved the template to a JSON string (because Configs stores strings), and
// Execute() must unpack it before calling the tool.
func TestMCPTask_Execute_ArgumentsAsJSONStringFromTemplate(t *testing.T) {
	var seenToolArgs map[string]any
	var seenAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		var req JsonRpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		resp := JsonRpcResponse{JsonRPC: "2.0", ID: req.ID}

		switch req.Method {
		case "initialize":
			resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1"}}`)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/call":
			if p, ok := req.Params.(map[string]any); ok {
				if a, ok := p["arguments"].(map[string]any); ok {
					seenToolArgs = a
				}
			}
			resp.Result = json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"isError":false}`)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	// Both arguments and headers arrive as JSON-encoded strings — the exact
	// shape ProcessValue produces when the user templates `{{ Configs['x'] }}`.
	params := map[string]any{
		"url":       ts.URL,
		"tool_name": "echo-tool",
		"arguments": `{"query":"SELECT 1","limit":10}`,
		"headers":   `{"Authorization":"Bearer resolved-from-config"}`,
	}

	result, err := task.Execute(ctx, params)
	assert.NoError(t, err)
	resMap := result.(map[string]any)
	assert.False(t, resMap["isError"].(bool))

	// Tool received the parsed arguments, not an empty map.
	assert.Equal(t, "SELECT 1", seenToolArgs["query"])
	assert.Equal(t, float64(10), seenToolArgs["limit"])
	// Custom headers from the string form made it onto the HTTP request.
	assert.Equal(t, "Bearer resolved-from-config", seenAuth)
}

// TestMCPTask_Execute_MalformedArgumentsStringReturnsError ensures garbage in
// a template-resolved string yields a clear error instead of a silent empty map.
func TestMCPTask_Execute_MalformedArgumentsStringReturnsError(t *testing.T) {
	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("", "", "", slog.Default())

	_, err := task.Execute(ctx, map[string]any{
		"url":       "https://example.com/mcp",
		"tool_name": "test-tool",
		"arguments": "not a JSON object",
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid 'arguments'")
}

// TestMCPTask_Execute_IntegrationPathCoercesArgumentsBeforeBranch verifies the
// integration_id path also benefits from coerceToMap. Before this fix, Execute
// dispatched to executeViaIntegration() which had its own silent
// `params["arguments"].(map[string]any)` cast — bypassing the coercion entirely
// when the user picked `connection_mode = integration`.
//
// We assert two things without standing up a full relay/integration mock:
//  1. A malformed JSON string for `arguments` errors out at the top of Execute,
//     before the integration_id resolution call. This proves coercion runs
//     ahead of the branch.
//  2. A valid JSON object string for `arguments` is mutated into a real map on
//     the params map, so executeViaIntegration's downstream cast (line 618)
//     sees the right type. We check this via a stub of the params map.
func TestMCPTask_Execute_IntegrationPathCoercesArgumentsBeforeBranch(t *testing.T) {
	task := &MCPTask{}
	ctx := testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", slog.Default())

	t.Run("malformed string fails before integration_id resolution", func(t *testing.T) {
		// Use a non-existent integration_id; if coercion runs first, we never
		// reach the resolution call and the error is the coercion error.
		_, err := task.Execute(ctx, map[string]any{
			"integration_id": "does-not-exist-but-doesnt-matter",
			"tool_name":      "test-tool",
			"arguments":      "not a JSON object",
		})
		assert.Error(t, err)
		// "invalid 'arguments'" — not "failed to resolve integration" — confirms
		// coercion ran first.
		assert.Contains(t, err.Error(), "invalid 'arguments'")
	})

	t.Run("valid JSON string is unwrapped on params before branch", func(t *testing.T) {
		// Mutating params and then triggering an integration-resolution failure
		// lets us inspect what the integration branch would have seen. The
		// resolution will fail (no real integration service in the test ctx),
		// so we just check the params map state after Execute returns.
		params := map[string]any{
			"integration_id": "missing",
			"tool_name":      "test-tool",
			"arguments":      `{"key":"value","n":7}`,
		}
		_, _ = task.Execute(ctx, params)

		// Coercion mutated params before any branch: arguments is now a real map.
		args, ok := params["arguments"].(map[string]any)
		assert.True(t, ok, "executeViaIntegration would have seen %T, want map[string]any", params["arguments"])
		assert.Equal(t, "value", args["key"])
		assert.Equal(t, float64(7), args["n"])
	})
}
