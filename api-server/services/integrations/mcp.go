package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(MCP{})
}

const IntegrationMCP = "mcp"

type MCP struct{}

func (m MCP) Name() string {
	return IntegrationMCP
}

func (m MCP) Category() core.IntegrationCategory {
	return core.IntegrationCategoryProxy
}

func (m MCP) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"connection_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "Connection mode",
				Default:     "direct",
				Enum:        []any{"direct", "vm_agent"},
				Priority:    90,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of MCP integration",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			// Transport selection
			"transport": {
				Type:        core.ToolSchemaTypeString,
				Description: "MCP server transport type",
				Default:     "http",
				Enum:        []any{"http"},
				Priority:    85,
			},
			// HTTP transport fields (both direct and vm_agent)
			"url": {
				Type:         core.ToolSchemaTypeString,
				Description:  "URL of the MCP server",
				ShowWhen:     map[string]any{"transport": "http"},
				RequiredWhen: map[string]any{"transport": "http"},
				Priority:     80,
			},
			// Auth fields (direct mode)
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type",
				Default:     "none",
				Enum:        []any{"none", "bearer", "basic", "api_key", "custom_header", "oauth2"},
				ShowWhen:    map[string]any{"connection_mode": "direct", "transport": "http"},
				Priority:    78,
			},
			"bearer_token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Bearer token",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"auth_type": "bearer", "connection_mode": "direct"},
				Priority:    74,
			},
			"username": {
				Type:     core.ToolSchemaTypeString,
				ShowWhen: map[string]any{"auth_type": "basic", "connection_mode": "direct"},
				Priority: 72,
			},
			"password": {
				Type:        core.ToolSchemaTypeString,
				IsEncrypted: true,
				ShowWhen:    map[string]any{"auth_type": "basic", "connection_mode": "direct"},
				Priority:    70,
			},
			"custom_header_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom header name",
				ShowWhen:    map[string]any{"auth_type": []any{"api_key", "custom_header"}, "connection_mode": "direct"},
				Priority:    68,
			},
			"custom_header_value": {
				Type:        core.ToolSchemaTypeString,
				Description: "Custom header value",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"auth_type": []any{"api_key", "custom_header"}, "connection_mode": "direct"},
				Priority:    66,
			},
			// OAuth 2.0 client_credentials fields
			"oauth_token_url": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth 2.0 token endpoint URL",
				ShowWhen:     map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				RequiredWhen: map[string]any{"auth_type": "oauth2"},
				Priority:     64,
			},
			"oauth_client_id": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth 2.0 client ID",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				RequiredWhen: map[string]any{"auth_type": "oauth2"},
				Priority:     62,
			},
			"oauth_client_secret": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth 2.0 client secret",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				RequiredWhen: map[string]any{"auth_type": "oauth2"},
				Priority:     60,
			},
			"oauth_scope": {
				Type:        core.ToolSchemaTypeString,
				Description: "OAuth 2.0 scope (space-separated)",
				ShowWhen:    map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				Priority:    58,
			},
			"oauth_audience": {
				Type:        core.ToolSchemaTypeString,
				Description: "OAuth 2.0 audience / resource",
				ShowWhen:    map[string]any{"auth_type": "oauth2", "connection_mode": "direct"},
				Priority:    56,
			},
			// VM agent credential fields
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where MCP server credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "local"},
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "transport": "http"},
				Priority:    76,
			},
			// LLM instructions
			"llm_instructions": {
				Type:        core.ToolSchemaTypeString,
				Description: "Instructions for the LLM agent on when and how to use this MCP server's tools",
				Multiline:   true,
				Priority:    30,
			},
			// Hidden proxy_type for vm_agent mode config push
			"proxy_type": {
				Type:     core.ToolSchemaTypeString,
				Default:  "mcp-proxy",
				Hidden:   true,
				Priority: 4,
			},
		},
	}
}

// mcpTestTimeout caps the per-call connectivity probe so a save click can't
// hang the request thread. Picked to match the relay-server's MCP path
// (mcp_direct.go uses a 120s upstream timeout, but validation only needs to
// know whether the server speaks JSON-RPC, so a tight bound is fine).
const mcpTestTimeout = 15 * time.Second

// mcpProtocolVersion matches what the relay-server sends in performMCPInitialize
// so a server that accepts production traffic also accepts the probe.
const mcpProtocolVersion = "2025-03-26"

var mcpTestHTTPClient = &http.Client{Timeout: mcpTestTimeout}

func (m MCP) ValidateConfig(_ *security.SecurityContext, config []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string, len(config))
	for _, c := range config {
		configMap[c.Name] = strings.TrimSpace(c.Value)
	}

	connectionMode := configMap["connection_mode"]
	if connectionMode == "" {
		connectionMode = "direct"
	}
	if connectionMode != "direct" && connectionMode != "vm_agent" {
		return []error{fmt.Errorf("connection_mode must be 'direct' or 'vm_agent'")}
	}

	transport := configMap["transport"]
	if transport == "" {
		transport = "http"
	}
	if transport != "http" {
		return []error{fmt.Errorf("unsupported transport: %s", transport)}
	}

	var errs []error

	mcpURL := configMap["url"]
	if mcpURL == "" {
		errs = append(errs, fmt.Errorf("url is required for HTTP transport"))
	} else {
		parsed, err := url.ParseRequestURI(mcpURL)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("url is not a valid URL: %w", err))
		case parsed.Scheme != "http" && parsed.Scheme != "https":
			errs = append(errs, fmt.Errorf("url scheme must be http or https, got %q", parsed.Scheme))
		case parsed.Host == "":
			errs = append(errs, fmt.Errorf("url is missing host"))
		}
	}

	// Mode-specific config-shape checks.
	switch connectionMode {
	case "direct":
		errs = append(errs, validateDirectAuthFields(configMap)...)
	case "vm_agent":
		credSource := configMap["credential_source"]
		if credSource == "" {
			credSource = "cloud_push"
		}
		if credSource != "cloud_push" && credSource != "local" {
			errs = append(errs, fmt.Errorf("credential_source must be 'cloud_push' or 'local'"))
		}
		// When credentials are pushed from cloud, the same field rules as
		// direct mode apply. When stored locally on the VM, cloud has no
		// credential to inspect — the agent owns that side.
		if credSource == "cloud_push" {
			errs = append(errs, validateDirectAuthFields(configMap)...)
		}
	}

	if len(errs) > 0 {
		return errs
	}

	// Live connectivity test for direct mode. vm_agent connectivity is
	// exercised by the framework via relay.TestProxyDatasourceConfig in
	// CreateIntegrationConfig, so we skip it here.
	if connectionMode == "direct" {
		if err := testDirectMCPConnectivity(mcpURL, configMap); err != nil {
			return []error{err}
		}
	}

	return nil
}

// validateDirectAuthFields checks that the per-auth-type required fields are
// present. The schema also carries RequiredWhen for OAuth, but that's a
// frontend-only tripwire — every API/RPC-direct create still has to pass
// through here.
func validateDirectAuthFields(c map[string]string) []error {
	authType := c["auth_type"]
	if authType == "" {
		authType = "none"
	}

	var errs []error
	switch authType {
	case "none":
	case "bearer":
		if c["bearer_token"] == "" {
			errs = append(errs, fmt.Errorf("bearer_token is required for bearer auth"))
		}
	case "basic":
		if c["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for basic auth"))
		}
		if c["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for basic auth"))
		}
	case "api_key", "custom_header":
		if c["custom_header_value"] == "" {
			errs = append(errs, fmt.Errorf("custom_header_value is required for %s auth", authType))
		}
		// api_key falls back to X-API-Key on the wire when name is unset
		// (matches injectMCPAuth in mcp_direct.go), so only custom_header
		// strictly requires a name.
		if authType == "custom_header" && c["custom_header_name"] == "" {
			errs = append(errs, fmt.Errorf("custom_header_name is required for custom_header auth"))
		}
	case "oauth2":
		if c["oauth_token_url"] == "" {
			errs = append(errs, fmt.Errorf("oauth_token_url is required for oauth2 auth"))
		} else if _, err := url.ParseRequestURI(c["oauth_token_url"]); err != nil {
			errs = append(errs, fmt.Errorf("oauth_token_url is not a valid URL"))
		}
		if c["oauth_client_id"] == "" {
			errs = append(errs, fmt.Errorf("oauth_client_id is required for oauth2 auth"))
		}
		if c["oauth_client_secret"] == "" {
			errs = append(errs, fmt.Errorf("oauth_client_secret is required for oauth2 auth"))
		}
	default:
		errs = append(errs, fmt.Errorf("unsupported auth_type: %s", authType))
	}
	return errs
}

// testDirectMCPConnectivity issues an MCP `initialize` JSON-RPC handshake
// against the configured URL. Mirrors the relay-server's mcp_direct.go path
// so an integration that validates here also validates at runtime.
func testDirectMCPConnectivity(mcpURL string, c map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mcpTestTimeout)
	defer cancel()

	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "nudgebee-integration-validator",
				"version": "1.0",
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, bytes.NewReader(initBody))
	if err != nil {
		return fmt.Errorf("build MCP initialize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if err := applyMCPAuth(ctx, req, c); err != nil {
		return fmt.Errorf("MCP auth setup failed: %w", err)
	}

	resp, err := mcpTestHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach MCP server at %s: %w", mcpURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read MCP response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("MCP server rejected authentication (HTTP %d): %s", resp.StatusCode, truncateBody(body, 200))
	case resp.StatusCode >= 400:
		return fmt.Errorf("MCP server returned HTTP %d: %s", resp.StatusCode, truncateBody(body, 200))
	}

	// Streamable-HTTP servers respond with text/event-stream. Strip
	// `data: ` lines so we can inspect the JSON-RPC envelope either way.
	payload := body
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		payload = []byte(extractSSEPayload(body))
	}

	if !bytes.Contains(payload, []byte(`"jsonrpc"`)) {
		return fmt.Errorf("MCP server response does not look like JSON-RPC: %s", truncateBody(payload, 200))
	}
	if bytes.Contains(payload, []byte(`"error"`)) {
		return fmt.Errorf("MCP server returned JSON-RPC error: %s", truncateBody(payload, 200))
	}
	return nil
}

func applyMCPAuth(ctx context.Context, req *http.Request, c map[string]string) error {
	switch c["auth_type"] {
	case "", "none":
	case "basic":
		req.SetBasicAuth(c["username"], c["password"])
	case "bearer":
		if t := c["bearer_token"]; t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
	case "api_key", "custom_header":
		name := c["custom_header_name"]
		if c["auth_type"] == "api_key" && name == "" {
			name = "X-API-Key"
		}
		if name != "" {
			req.Header.Set(name, c["custom_header_value"])
		}
	case "oauth2":
		token, err := fetchMCPOAuthToken(ctx, c["oauth_token_url"], c["oauth_client_id"], c["oauth_client_secret"], c["oauth_scope"], c["oauth_audience"])
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

func fetchMCPOAuthToken(ctx context.Context, tokenURL, clientID, clientSecret, scope, audience string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	if scope != "" {
		form.Set("scope", scope)
	}
	if audience != "" {
		form.Set("audience", audience)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth2: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := mcpTestHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth2: token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("oauth2: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth2: token endpoint returned %d: %s", resp.StatusCode, truncateBody(body, 200))
	}

	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("oauth2: invalid token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("oauth2: token response missing access_token")
	}
	return out.AccessToken, nil
}

func extractSSEPayload(body []byte) string {
	var out strings.Builder
	// Per the SSE spec, lines may be terminated by \n, \r\n, or \r, and the
	// space after `data:` is optional.
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")
		if data == "[DONE]" {
			break
		}
		out.WriteString(data)
	}
	if out.Len() == 0 {
		return string(body)
	}
	return out.String()
}

func truncateBody(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
