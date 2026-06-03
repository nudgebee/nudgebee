package integrations

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// joinErrors collapses []error into a single string for order-independent
// `Contains` assertions across multiple expected messages.
func joinErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "|")
}

func cv(name, value string) core.IntegrationConfigValue {
	return core.IntegrationConfigValue{Name: name, Value: value}
}

// ----- config-shape (no I/O) ------------------------------------------------

func TestMCP_ValidateConfig_RejectsUnknownConnectionMode(t *testing.T) {
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "carrier-pigeon"),
		cv("url", "https://mcp.example/sse"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "connection_mode")
}

func TestMCP_ValidateConfig_RejectsUnknownTransport(t *testing.T) {
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("transport", "stdio"),
		cv("url", "https://mcp.example/sse"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "unsupported transport")
}

func TestMCP_ValidateConfig_URLChecks(t *testing.T) {
	cases := []struct {
		name    string
		urlVal  string
		wantMsg string
	}{
		{"missing", "", "url is required"},
		{"malformed", "::::not-a-url", "not a valid URL"},
		{"wrong-scheme", "ftp://mcp.example/sse", "scheme must be http or https"},
		{"missing-host", "https:///sse", "missing host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
				cv("connection_mode", "direct"),
				cv("url", tc.urlVal),
			}, "acc")
			require.NotEmpty(t, errs)
			assert.Contains(t, joinErrors(errs), tc.wantMsg)
		})
	}
}

func TestMCP_ValidateConfig_DirectAuth_PerTypeRequiredFields(t *testing.T) {
	// Each row asserts: with this auth_type and no other auth fields, validation
	// returns the configured complaints. URL is provided so the URL branch
	// passes — we want only the auth complaints in scope. We use a bogus URL
	// so that the live connectivity test (which runs after shape checks
	// pass) never gets a chance to fire; here we always expect shape errors.
	cases := []struct {
		authType string
		wantMsgs []string
	}{
		{"bearer", []string{"bearer_token is required"}},
		{"basic", []string{"username is required", "password is required"}},
		{"custom_header", []string{"custom_header_name is required", "custom_header_value is required"}},
		{"api_key", []string{"custom_header_value is required"}}, // name optional → defaults to X-API-Key
		{"oauth2", []string{"oauth_token_url is required", "oauth_client_id is required", "oauth_client_secret is required"}},
		{"unicorn", []string{"unsupported auth_type"}},
	}
	for _, tc := range cases {
		t.Run(tc.authType, func(t *testing.T) {
			errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
				cv("connection_mode", "direct"),
				cv("url", "https://mcp.example/sse"),
				cv("auth_type", tc.authType),
			}, "acc")
			joined := joinErrors(errs)
			for _, msg := range tc.wantMsgs {
				assert.Contains(t, joined, msg)
			}
		})
	}
}

func TestMCP_ValidateConfig_OAuth_InvalidTokenURL(t *testing.T) {
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", "https://mcp.example/sse"),
		cv("auth_type", "oauth2"),
		cv("oauth_token_url", "::::nope"),
		cv("oauth_client_id", "id"),
		cv("oauth_client_secret", "secret"),
	}, "acc")
	require.NotEmpty(t, errs)
	assert.Contains(t, joinErrors(errs), "oauth_token_url is not a valid URL")
}

func TestMCP_ValidateConfig_VmAgent_CredentialSourceLocal_SkipsAuthChecks(t *testing.T) {
	// In vm_agent mode with credential_source=local, the agent owns
	// credentials — cloud should not complain about missing bearer_token
	// even when auth_type=bearer is set in the form.
	// We point at an unreachable URL so the live test never runs (vm_agent
	// path doesn't run it), and there's no shape error to surface.
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "vm_agent"),
		cv("credential_source", "local"),
		cv("url", "https://mcp.example/sse"),
		cv("auth_type", "bearer"),
		// bearer_token intentionally omitted
	}, "acc")
	assert.Empty(t, errs, "vm_agent + local should not require cloud-side auth fields, got: %s", joinErrors(errs))
}

func TestMCP_ValidateConfig_VmAgent_CloudPushAppliesAuthChecks(t *testing.T) {
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "vm_agent"),
		cv("credential_source", "cloud_push"),
		cv("url", "https://mcp.example/sse"),
		cv("auth_type", "basic"),
		// username/password intentionally omitted
	}, "acc")
	require.NotEmpty(t, errs)
	joined := joinErrors(errs)
	assert.Contains(t, joined, "username is required")
	assert.Contains(t, joined, "password is required")
}

func TestMCP_ValidateConfig_VmAgent_RejectsUnknownCredentialSource(t *testing.T) {
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "vm_agent"),
		cv("credential_source", "from-a-postcard"),
		cv("url", "https://mcp.example/sse"),
	}, "acc")
	require.NotEmpty(t, errs)
	assert.Contains(t, joinErrors(errs), "credential_source must be")
}

// ----- live connectivity (httptest) -----------------------------------------

// jsonRPCInitOK is what a healthy MCP server returns to `initialize`.
const jsonRPCInitOK = `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`

func TestMCP_ValidateConfig_Direct_LiveTest_HappyPath_JSON(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jsonRPCInitOK))
	}))
	defer srv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", srv.URL),
	}, "acc")
	assert.Empty(t, errs)
	assert.Equal(t, "initialize", gotBody["method"], "validator should send the MCP initialize handshake")
}

func TestMCP_ValidateConfig_Direct_LiveTest_HappyPath_SSE(t *testing.T) {
	// Streamable-HTTP servers respond with text/event-stream. Validation
	// must parse the SSE envelope and still recognize JSON-RPC inside.
	// Cover the spec corners: CRLF line endings and the optional space after
	// `data:` — both legal per the SSE spec, both seen in the wild.
	cases := []struct {
		name string
		body string
	}{
		{"LF + space", "event: message\ndata: " + jsonRPCInitOK + "\n\n"},
		{"CRLF + space", "event: message\r\ndata: " + jsonRPCInitOK + "\r\n\r\n"},
		{"LF no space", "event: message\ndata:" + jsonRPCInitOK + "\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
				cv("connection_mode", "direct"),
				cv("url", srv.URL),
			}, "acc")
			assert.Empty(t, errs)
		})
	}
}

func TestMCP_ValidateConfig_Direct_LiveTest_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", srv.URL),
		cv("auth_type", "bearer"),
		cv("bearer_token", "wrong-token"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "rejected authentication")
}

func TestMCP_ValidateConfig_Direct_LiveTest_NonJSONRPCResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer srv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", srv.URL),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "does not look like JSON-RPC")
}

func TestMCP_ValidateConfig_Direct_LiveTest_JSONRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":0,"error":{"code":-32601,"message":"method not found"}}`))
	}))
	defer srv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", srv.URL),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "JSON-RPC error")
}

func TestMCP_ValidateConfig_Direct_LiveTest_UnreachableHost(t *testing.T) {
	// 127.0.0.1:1 is reliably unreachable; matches the hive_test convention.
	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", "http://127.0.0.1:1/sse"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "cannot reach MCP server")
}

func TestMCP_ValidateConfig_Direct_LiveTest_AuthHeadersSent(t *testing.T) {
	cases := []struct {
		name       string
		cfg        []core.IntegrationConfigValue
		wantHeader string
		wantValue  string
	}{
		{
			name: "bearer",
			cfg: []core.IntegrationConfigValue{
				cv("auth_type", "bearer"),
				cv("bearer_token", "abc123"),
			},
			wantHeader: "Authorization",
			wantValue:  "Bearer abc123",
		},
		{
			name: "api_key default header",
			cfg: []core.IntegrationConfigValue{
				cv("auth_type", "api_key"),
				cv("custom_header_value", "k-key"),
			},
			wantHeader: "X-API-Key",
			wantValue:  "k-key",
		},
		{
			name: "custom_header",
			cfg: []core.IntegrationConfigValue{
				cv("auth_type", "custom_header"),
				cv("custom_header_name", "X-Tenant"),
				cv("custom_header_value", "acme"),
			},
			wantHeader: "X-Tenant",
			wantValue:  "acme",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get(tc.wantHeader)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(jsonRPCInitOK))
			}))
			defer srv.Close()

			cfg := append([]core.IntegrationConfigValue{
				cv("connection_mode", "direct"),
				cv("url", srv.URL),
			}, tc.cfg...)
			errs := MCP{}.ValidateConfig(nil, cfg, "acc")
			require.Empty(t, errs)
			assert.Equal(t, tc.wantValue, got)
		})
	}
}

func TestMCP_ValidateConfig_Direct_LiveTest_BasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jsonRPCInitOK))
	}))
	defer srv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", srv.URL),
		cv("auth_type", "basic"),
		cv("username", "alice"),
		cv("password", "wonderland"),
	}, "acc")
	require.Empty(t, errs)
	assert.True(t, gotOK)
	assert.Equal(t, "alice", gotUser)
	assert.Equal(t, "wonderland", gotPass)
}

func TestMCP_ValidateConfig_Direct_LiveTest_OAuth2EndToEnd(t *testing.T) {
	// Stand up a fake token endpoint and a fake MCP server. Validator
	// should first exchange client_credentials, then call the MCP server
	// with Authorization: Bearer <token>.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.Form.Get("grant_type"))
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "cid", user)
		assert.Equal(t, "csec", pass)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"opaque-token","token_type":"Bearer"}`))
	}))
	defer tokenSrv.Close()

	var gotAuth string
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jsonRPCInitOK))
	}))
	defer mcpSrv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", mcpSrv.URL),
		cv("auth_type", "oauth2"),
		cv("oauth_token_url", tokenSrv.URL),
		cv("oauth_client_id", "cid"),
		cv("oauth_client_secret", "csec"),
	}, "acc")
	require.Empty(t, errs)
	assert.Equal(t, "Bearer opaque-token", gotAuth)
}

func TestMCP_ValidateConfig_Direct_LiveTest_OAuth2TokenEndpointError(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer tokenSrv.Close()

	errs := MCP{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("connection_mode", "direct"),
		cv("url", "http://127.0.0.1:1/sse"), // never reached; token exchange fails first
		cv("auth_type", "oauth2"),
		cv("oauth_token_url", tokenSrv.URL),
		cv("oauth_client_id", "cid"),
		cv("oauth_client_secret", "csec"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "oauth2: token endpoint returned 400")
}
