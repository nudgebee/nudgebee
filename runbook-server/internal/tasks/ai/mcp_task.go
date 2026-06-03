package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/runbook/common"
	integrationsService "nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/relay"

	"nudgebee/runbook/internal/tasks/integrations"
	"nudgebee/runbook/internal/tasks/types"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
)

// Cap on a single SSE event payload. MCP tools can return large result sets
// (DB query dumps, log blobs) in a single `data:` frame; 64 KiB — the default
// bufio.Scanner token size — is too small. Keep in sync with the largest
// realistic tool response we want to support.
const mcpMaxSSEEventBytes = 10 * 1024 * 1024

// Cap on how much of a non-2xx response body we read before giving up, to
// avoid OOM from a misbehaving server returning a multi-MB error page.
const mcpMaxErrorBodyBytes = 64 * 1024

// MCP-specific Temporal error type names. Surface them in logs/metadata so
// deterministic client-side failures don't burn activity retries.
const (
	mcpErrTypeSSEStream   = "MCPSSEStreamError"
	mcpErrTypeBadResponse = "MCPBadResponseError"
)

// parseMCPSSEResponse reads an SSE body and returns the JSON-RPC response whose
// `id` matches reqId. It implements the subset of the SSE wire format MCP uses:
//   - Lines beginning with "data:" are the event payload; consecutive `data:`
//     lines in one event are concatenated with "\n" per the HTML spec.
//   - A blank line terminates an event; we attempt to parse the accumulated
//     payload as a JsonRpcResponse and match reqId.
//   - Other SSE fields (event:, id:, retry:, comments) are ignored.
//
// Returned errors for deterministic failures (scanner overflow, no response
// found) are wrapped as non-retryable Temporal ApplicationErrors so the
// activity isn't retried against a server that will produce the same payload.
func parseMCPSSEResponse(body io.Reader, reqId any) (*JsonRpcResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), mcpMaxSSEEventBytes)

	var data strings.Builder
	flush := func() *JsonRpcResponse {
		if data.Len() == 0 {
			return nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		var rpcResp JsonRpcResponse
		if err := json.Unmarshal([]byte(payload), &rpcResp); err != nil {
			return nil
		}
		if rpcResp.ID == reqId {
			return &rpcResp
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if resp := flush(); resp != nil {
				return resp, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			v := strings.TrimPrefix(line, "data:")
			v = strings.TrimPrefix(v, " ") // optional single space per SSE spec
			data.WriteString(v)
			data.WriteByte('\n')
		}
		// Ignore event:/id:/retry:/comment lines.
	}

	// Many MCP servers close the connection without a trailing blank line;
	// flush any pending payload before checking scanner.Err.
	if resp := flush(); resp != nil {
		return resp, nil
	}

	if err := scanner.Err(); err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("error reading SSE stream: %v", err),
			mcpErrTypeSSEStream,
			err,
		)
	}
	// Stream closed cleanly without our matching response. This could be a
	// truly transient server cut, but in practice it has also covered MCP
	// servers that never emit a reply for a given request — retrying burns
	// the activity budget without progress. Mark non-retryable; the workflow
	// layer can still choose to re-invoke the activity if it's a known flake.
	return nil, temporal.NewNonRetryableApplicationError(
		"response not found in SSE stream",
		mcpErrTypeBadResponse,
		nil,
	)
}

// MCPTask implements the Task interface for invoking MCP tools.
// Note: This implementation supports the "Streamable HTTP" transport for MCP,
// which uses standard HTTP POST requests for JSON-RPC messages and supports
// both immediate JSON responses and SSE streams. It relies on the
// `Mcp-Session-Id` header for session management across requests.
//
// TODO: Add OAuth 2.0 support to llm-server MCP tools as well
// (llm/llm-server/tools/core/tool_custom_mcp.go — encryption, redaction, provider caching).
type MCPTask struct{}

func (t *MCPTask) GetName() string {
	return "llm.mcp_call"
}

// coerceToMap normalizes a value that should be map[string]any into one.
//
// Workflow templates that reference an object-shaped source (e.g.
// `{{ Configs['dev-pg'] }}`) resolve to whatever type the source holds. Configs
// are stored as strings in the DB (model.Config.Value is string), so a template
// pointed at a Config that holds JSON resolves to a raw JSON string, not a map.
// A silent type assertion would drop the value and send an empty map to the MCP
// server — the bug from #28227. Parse the string form explicitly so the failure
// is either (a) a successful parse, or (b) a clear error surfaced to the user.
func coerceToMap(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("expected a JSON object, got string that failed to parse: %w", err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("expected object (map), got %T", v)
}

func (t *MCPTask) GetDescription() string {
	return "Call a tool on an external MCP-compatible server."
}

func (t *MCPTask) GetDisplayName() string {
	return "MCP Call"
}

// JSON-RPC types
type JsonRpcRequest struct {
	JsonRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      any    `json:"id,omitempty"`
}

type JsonRpcResponse struct {
	JsonRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
	ID      any             `json:"id,omitempty"`
}

type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCP types
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ClientInfo         `json:"clientInfo"`
}

type ClientCapabilities struct {
	Roots    *struct{} `json:"roots,omitempty"`
	Sampling *struct{} `json:"sampling,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type CallToolParams struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"` // Removed omitempty to ensure it's always present
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (t *MCPTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	// Create a copy of params for logging to avoid modifying the original map
	logParams := make(map[string]any)
	for k, v := range params {
		if k == "headers" {
			logParams[k] = "[REDACTED]"
		} else {
			logParams[k] = v
		}
	}
	taskCtx.GetLogger().Debug("Executing MCPTask", "params", logParams)

	// Backward compatibility: infer connection_mode from params when not explicitly set
	if _, hasMode := params["connection_mode"]; !hasMode {
		if urlVal, _ := params["url"].(string); urlVal != "" {
			params["connection_mode"] = "direct"
		} else {
			params["connection_mode"] = "integration"
		}
	}

	// Coerce object-typed params upfront so both Execute paths (direct URL below
	// and the integration relay in executeViaIntegration) see a real map. The
	// runtime template renderer can hand us a JSON-string when the field was
	// templated against a Configs entry — see coerceToMap's doc comment.
	if coerced, err := coerceToMap(params["arguments"]); err != nil {
		return nil, fmt.Errorf("invalid 'arguments': %w", err)
	} else if coerced != nil {
		params["arguments"] = coerced
	}
	if coerced, err := coerceToMap(params["headers"]); err != nil {
		return nil, fmt.Errorf("invalid 'headers': %w", err)
	} else if coerced != nil {
		params["headers"] = coerced
	}

	// If integration_id is provided, resolve config and execute via relay
	if integrationID, ok := params["integration_id"].(string); ok && integrationID != "" {
		resolvedID, err := integrationsService.ResolveIntegrationID(taskCtx.GetNewRequestContext(), integrationID, []string{"mcp"})
		if err != nil {
			return nil, err
		}
		return t.executeViaIntegration(taskCtx, resolvedID, params)
	}

	urlStr, ok := params["url"].(string)
	if !ok || urlStr == "" {
		return nil, fmt.Errorf("url is required (or provide integration_id)")
	}

	toolName, ok := params["tool_name"].(string)
	if !ok || toolName == "" {
		return nil, fmt.Errorf("tool_name is required")
	}

	// Pre-coerced above; type assertions here are safe and present only to bind
	// local variables to the values stored in params.
	toolArgs, _ := params["arguments"].(map[string]any)
	if toolArgs == nil {
		toolArgs = map[string]any{}
	}
	headers, _ := params["headers"].(map[string]any)

	// For OAuth 2.0: resolve token upfront and inject into headers
	if authType, _ := params["auth_type"].(string); authType == "oauth2" {
		tokenURL, _ := params["oauth_token_url"].(string)
		clientID, _ := params["oauth_client_id"].(string)
		clientSecret, _ := params["oauth_client_secret"].(string)
		scope, _ := params["oauth_scope"].(string)
		audience, _ := params["oauth_audience"].(string)

		if tokenURL == "" || clientID == "" || clientSecret == "" {
			return nil, fmt.Errorf("OAuth 2.0 requires oauth_token_url, oauth_client_id, and oauth_client_secret")
		}

		token, err := integrations.FetchOAuthToken(tokenURL, clientID, clientSecret, scope, audience)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch OAuth token: %w", err)
		}

		if headers == nil {
			headers = make(map[string]any)
		}
		headers["Authorization"] = "Bearer " + token
		taskCtx.GetLogger().Info("Applied OAuth 2.0 authentication for MCP call")
	}

	timeoutStr, _ := params["timeout"].(string)
	timeout := 60 * time.Second
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = d
		}
	} else if deadline, ok := taskCtx.GetContext().Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		timeout = remaining
	}

	client := &http.Client{
		Timeout: timeout,
	}

	// Session state
	var sessionId string

	// Helper to send JSON-RPC request
	sendRequest := func(method string, params any, isNotification bool) (*JsonRpcResponse, error) {
		reqId := uuid.New().String()
		reqBody := JsonRpcRequest{
			JsonRPC: "2.0",
			Method:  method,
			Params:  params,
		}
		if !isNotification {
			reqBody.ID = reqId
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("POST", urlStr, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionId != "" {
			req.Header.Set("Mcp-Session-Id", sessionId)
		}

		// Inject custom headers
		for k, v := range headers {
			if strVal, ok := v.(string); ok {
				req.Header.Set(k, strVal)
			} else {
				req.Header.Set(k, fmt.Sprintf("%v", v))
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		// Update session ID if provided/changed
		if newSid := resp.Header.Get("Mcp-Session-Id"); newSid != "" {
			sessionId = newSid
		}

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, mcpMaxErrorBodyBytes))
			return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		}

		if isNotification {
			return nil, nil
		}

		// Handle Response
		ct := resp.Header.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			var rpcResp JsonRpcResponse
			if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
				return nil, temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("failed to decode JSON response: %v", err),
					mcpErrTypeBadResponse, err,
				)
			}
			return &rpcResp, nil
		} else if strings.Contains(ct, "text/event-stream") {
			return parseMCPSSEResponse(resp.Body, reqId)
		} else {
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("unsupported content type: %s", ct),
				mcpErrTypeBadResponse, nil,
			)
		}
	}

	// 1. Initialize
	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05", // Using a recent version
		Capabilities: ClientCapabilities{
			Roots:    &struct{}{},
			Sampling: &struct{}{},
		},
		ClientInfo: ClientInfo{
			Name:    "runbook-server",
			Version: "1.0.0",
		},
	}

	initResp, err := sendRequest("initialize", initParams, false)
	if err != nil {
		return nil, fmt.Errorf("initialize failed: %w", err)
	}
	if initResp.Error != nil {
		return nil, fmt.Errorf("initialize error: %s", initResp.Error.Message)
	}

	// 2. Initialized Notification
	_, err = sendRequest("notifications/initialized", map[string]any{}, true)
	if err != nil {
		return nil, fmt.Errorf("initialized notification failed: %w", err)
	}

	// 3. Call Tool
	callParams := CallToolParams{
		Name:      toolName,
		Arguments: toolArgs,
	}
	callResp, err := sendRequest("tools/call", callParams, false)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}
	if callResp.Error != nil {
		return nil, fmt.Errorf("tool execution error: %s", callResp.Error.Message)
	}

	var result CallToolResult
	if err := json.Unmarshal(callResp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}

	// We return the result even if IsError is true, as per review feedback.
	// The workflow engine will handle the output schema validation.

	var out map[string]any
	b, _ := json.Marshal(result)
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return out, nil
}

// decryptIntegrationConfig extracts and decrypts integration config values into a map.
func decryptIntegrationConfig(values []integrationsService.IntegrationConfigValue) (map[string]string, error) {
	configMap := make(map[string]string, len(values))
	for _, v := range values {
		val := v.Value
		if v.IsEncrypted && val != "" {
			decrypted, err := common.Decrypt(val)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt integration config %q: %w", v.Name, err)
			}
			val = decrypted
		}
		configMap[v.Name] = val
	}
	return configMap, nil
}

// MCPToolInfo describes a tool exposed by an MCP server.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ListTools connects to an MCP server (directly or via integration) and returns
// the list of available tools by performing the initialize → tools/list handshake.
func (t *MCPTask) ListTools(taskCtx types.TaskContext, params map[string]any) ([]MCPToolInfo, error) {
	accountId := taskCtx.GetAccountID()
	requestContext := taskCtx.GetNewRequestContext()

	var mcpURL, authType, bearerToken string
	var headers map[string]any

	if integrationID, ok := params["integration_id"].(string); ok && integrationID != "" {
		resolvedID, err := integrationsService.ResolveIntegrationID(requestContext, integrationID, []string{"mcp"})
		if err != nil {
			return nil, err
		}
		integrationID = resolvedID

		// Resolve from integration config
		integrationConfig, err := integrationsService.GetIntegration(requestContext, accountId, "mcp", integrationID)
		if err != nil {
			return nil, fmt.Errorf("failed to get MCP integration: %w", err)
		}

		configMap, err := decryptIntegrationConfig(integrationConfig.Values)
		if err != nil {
			return nil, err
		}

		mcpURL = configMap["url"]
		authType = configMap["auth_type"]

		switch authType {
		case "bearer":
			bearerToken = configMap["bearer_token"]
		case "oauth2":
			tokenURL := configMap["oauth_token_url"]
			clientID := configMap["oauth_client_id"]
			clientSecret := configMap["oauth_client_secret"]
			if tokenURL == "" || clientID == "" || clientSecret == "" {
				return nil, fmt.Errorf("OAuth 2.0 requires oauth_token_url, oauth_client_id, and oauth_client_secret")
			}
			token, err := integrations.FetchOAuthToken(tokenURL, clientID, clientSecret, configMap["oauth_scope"], configMap["oauth_audience"])
			if err != nil {
				return nil, fmt.Errorf("failed to fetch OAuth token: %w", err)
			}
			bearerToken = token
		case "basic":
			// basic auth needs to be handled via headers
		case "api_key", "custom_header":
			headers = map[string]any{
				configMap["custom_header_name"]: configMap["custom_header_value"],
			}
		}
	} else {
		mcpURL, _ = params["url"].(string)
		headers, _ = params["headers"].(map[string]any)

		// Mirror Execute's direct-URL OAuth2 handling so the tool dropdown can
		// reach MCP servers that require auth (e.g. servers behind OAuth 2.0
		// client_credentials). Other auth schemes are expected to flow through
		// the `headers` field, matching the schema's auth_type options.
		if at, _ := params["auth_type"].(string); at == "oauth2" {
			tokenURL, _ := params["oauth_token_url"].(string)
			clientID, _ := params["oauth_client_id"].(string)
			clientSecret, _ := params["oauth_client_secret"].(string)
			if tokenURL == "" || clientID == "" || clientSecret == "" {
				return nil, fmt.Errorf("OAuth 2.0 requires oauth_token_url, oauth_client_id, and oauth_client_secret")
			}
			scope, _ := params["oauth_scope"].(string)
			audience, _ := params["oauth_audience"].(string)
			token, err := integrations.FetchOAuthToken(tokenURL, clientID, clientSecret, scope, audience)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch OAuth token: %w", err)
			}
			bearerToken = token
		}
	}

	if mcpURL == "" {
		return nil, fmt.Errorf("MCP server URL is required (provide url or integration_id)")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var sessionId string

	sendRequest := func(method string, reqParams any, isNotification bool) (*JsonRpcResponse, error) {
		reqId := uuid.New().String()
		reqBody := JsonRpcRequest{JsonRPC: "2.0", Method: method, Params: reqParams}
		if !isNotification {
			reqBody.ID = reqId
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("POST", mcpURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sessionId != "" {
			req.Header.Set("Mcp-Session-Id", sessionId)
		}
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
		}
		for k, v := range headers {
			if strVal, ok := v.(string); ok {
				req.Header.Set(k, strVal)
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		if newSid := resp.Header.Get("Mcp-Session-Id"); newSid != "" {
			sessionId = newSid
		}
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, mcpMaxErrorBodyBytes))
			return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		}
		if isNotification {
			return nil, nil
		}

		ct := resp.Header.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			var rpcResp JsonRpcResponse
			if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
				return nil, temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("failed to decode JSON response: %v", err),
					mcpErrTypeBadResponse, err,
				)
			}
			return &rpcResp, nil
		} else if strings.Contains(ct, "text/event-stream") {
			return parseMCPSSEResponse(resp.Body, reqId)
		}
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("unsupported content type: %s", ct),
			mcpErrTypeBadResponse, nil,
		)
	}

	// 1. Initialize
	initResp, err := sendRequest("initialize", InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    ClientCapabilities{Roots: &struct{}{}, Sampling: &struct{}{}},
		ClientInfo:      ClientInfo{Name: "runbook-server", Version: "1.0.0"},
	}, false)
	if err != nil {
		return nil, fmt.Errorf("initialize failed: %w", err)
	}
	if initResp.Error != nil {
		return nil, fmt.Errorf("initialize error: %s", initResp.Error.Message)
	}

	// 2. Initialized notification
	_, err = sendRequest("notifications/initialized", map[string]any{}, true)
	if err != nil {
		return nil, fmt.Errorf("initialized notification failed: %w", err)
	}

	// 3. List tools
	listResp, err := sendRequest("tools/list", map[string]any{}, false)
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}
	if listResp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", listResp.Error.Message)
	}

	// Parse result: { "tools": [{ "name": "...", "description": "...", ... }] }
	var listResult struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list result: %w", err)
	}

	return listResult.Tools, nil
}

// executeViaIntegration resolves MCP config from an integration and executes via relay.
func (t *MCPTask) executeViaIntegration(taskCtx types.TaskContext, integrationID string, params map[string]any) (any, error) {
	accountId := taskCtx.GetAccountID()
	requestContext := taskCtx.GetNewRequestContext()

	toolName, ok := params["tool_name"].(string)
	if !ok || toolName == "" {
		return nil, fmt.Errorf("tool_name is required")
	}

	// Fetch integration config
	integrationConfig, err := integrationsService.GetIntegration(requestContext, accountId, "mcp", integrationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP integration: %w", err)
	}

	// Extract config values
	configMap, err := decryptIntegrationConfig(integrationConfig.Values)
	if err != nil {
		return nil, err
	}

	connectionMode := configMap["connection_mode"]
	if connectionMode == "" {
		connectionMode = "direct"
	}

	// Build relay action params — default arguments to empty object if nil
	toolArgs, _ := params["arguments"].(map[string]any)
	if toolArgs == nil {
		toolArgs = map[string]any{}
	}

	argsJSON, _ := json.Marshal(toolArgs)
	taskCtx.GetLogger().Debug("mcp: executeViaIntegration arguments", "raw", params["arguments"], "resolved", string(argsJSON), "connectionMode", connectionMode)

	actionParams := map[string]any{
		"method": "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": toolArgs,
		},
	}

	var agentType string

	if connectionMode == "direct" {
		actionParams["connection_mode"] = "direct"
		actionParams["url"] = configMap["url"]
		actionParams["auth_type"] = configMap["auth_type"]

		authCreds := map[string]string{}
		authType := configMap["auth_type"]
		switch authType {
		case "bearer":
			authCreds["bearer_token"] = configMap["bearer_token"]
		case "basic":
			authCreds["username"] = configMap["username"]
			authCreds["password"] = configMap["password"]
		case "api_key", "custom_header":
			authCreds["custom_header_name"] = configMap["custom_header_name"]
			authCreds["custom_header_value"] = configMap["custom_header_value"]
		case "oauth2":
			// Exchange OAuth2 client credentials for a bearer token
			tokenURL := configMap["oauth_token_url"]
			clientID := configMap["oauth_client_id"]
			clientSecret := configMap["oauth_client_secret"]
			if tokenURL == "" || clientID == "" || clientSecret == "" {
				return nil, fmt.Errorf("OAuth 2.0 requires oauth_token_url, oauth_client_id, and oauth_client_secret")
			}
			token, err := integrations.FetchOAuthToken(tokenURL, clientID, clientSecret, configMap["oauth_scope"], configMap["oauth_audience"])
			if err != nil {
				return nil, fmt.Errorf("failed to fetch OAuth token: %w", err)
			}
			// Send as bearer to relay
			actionParams["auth_type"] = "bearer"
			authCreds["bearer_token"] = token
		}
		if len(authCreds) > 0 {
			actionParams["auth_credentials"] = authCreds
		}
	} else {
		// vm_agent mode
		actionParams["connection_mode"] = "vm_agent"
		actionParams["datasource_id"] = integrationID
		agentType = "proxy"
	}

	relayTimeout := 120 * time.Second
	if deadline, ok := taskCtx.GetContext().Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		relayTimeout = remaining
	}
	body := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "mcp_request",
		ActionParams: actionParams,
		AgentType:    agentType,
		Timeout:      relayTimeout,
	}

	response, err := relay.ExecuteRelay(body)
	if err != nil {
		return nil, fmt.Errorf("MCP relay request failed: %w", err)
	}

	// Parse response: {"status_code": N, "data": "<json-rpc-response>"}
	dataStr, ok := response["data"].(string)
	if !ok {
		return nil, fmt.Errorf("MCP response missing 'data' field")
	}

	statusCode, _ := response["status_code"].(float64)
	if statusCode >= 400 {
		return nil, fmt.Errorf("MCP server returned status %d: %s", int(statusCode), dataStr)
	}

	// Parse the JSON-RPC response to extract the tool call result
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(dataStr), &rpcResp); err != nil {
		slog.Error("mcp: failed to parse JSON-RPC response", "error", err)
		return nil, fmt.Errorf("failed to parse MCP response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("MCP tool error: %s", rpcResp.Error.Message)
	}

	var result CallToolResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}

	var out map[string]any
	b, _ := json.Marshal(result)
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %w", err)
	}

	return out, nil
}

func (t *MCPTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"connection_mode": {
				Type:        types.PropertyTypeString,
				Description: "How to connect to the MCP server.",
				Required:    true,
				Options:     []string{"integration", "direct"},
				Order:       0,
			},
			"integration_id": {
				Type:         types.PropertyTypeIntegration,
				Description:  "Select an MCP integration to use.",
				Required:     false,
				SubType:      "mcp",
				Order:        1,
				DependsOn:    []string{"connection_mode"},
				VisibleWhen:  &types.VisibleWhen{Field: "connection_mode", Value: []string{"integration"}},
				RequiredWhen: &types.RequiredWhen{Field: "connection_mode", Value: []string{"integration"}},
			},
			"url": {
				Type:         types.PropertyTypeString,
				Description:  "The URL of the MCP server. Tip: Save connection details as an MCP integration under Settings > Integrations for reuse.",
				Required:     false,
				Order:        2,
				DependsOn:    []string{"connection_mode"},
				VisibleWhen:  &types.VisibleWhen{Field: "connection_mode", Value: []string{"direct"}},
				RequiredWhen: &types.RequiredWhen{Field: "connection_mode", Value: []string{"direct"}},
			},
			"tool_name": {
				Type:        types.PropertyTypeString,
				Description: "The name of the tool to invoke.",
				Required:    true,
				Order:       3,
				DependsOn:   []string{"connection_mode", "integration_id", "url"},
				OptionsSource: &types.OptionsSource{
					Type: "mcp_tools",
					DependencyMapping: map[string]string{
						"integration_id":  "integration_id",
						"url":             "url",
						"connection_mode": "connection_mode",
					},
				},
			},
			"arguments": {
				Type:        types.PropertyTypeObject,
				Description: "The arguments for the tool.",
				Required:    false,
				Order:       4,
			},
			"headers": {
				Type:        types.PropertyTypeObject,
				Description: "Optional headers to include in the request (e.g., Authorization).",
				Required:    false,
				Order:       5,
				DependsOn:   []string{"connection_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "connection_mode", Value: []string{"direct"}},
			},
			"auth_type": {
				Type:        types.PropertyTypeString,
				Description: "Authentication type: 'oauth2' or empty for none. For bearer/basic/api_key auth, use the 'headers' parameter directly.",
				Required:    false,
				Options:     []string{"", "oauth2"},
				Order:       6,
				DependsOn:   []string{"connection_mode"},
				VisibleWhen: &types.VisibleWhen{Field: "connection_mode", Value: []string{"direct"}},
			},
			"oauth_token_url": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 token endpoint URL.",
				Required:    false,
				Order:       7,
				DependsOn:   []string{"connection_mode", "auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_client_id": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 client ID.",
				Required:    false,
				Order:       8,
				DependsOn:   []string{"connection_mode", "auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_client_secret": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 client secret.",
				Required:    false,
				IsEncrypted: true,
				Order:       9,
				DependsOn:   []string{"connection_mode", "auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_scope": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 scope, space-separated.",
				Required:    false,
				Order:       10,
				DependsOn:   []string{"connection_mode", "auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"oauth_audience": {
				Type:        types.PropertyTypeString,
				Description: "OAuth 2.0 audience / resource identifier. Required by some providers like Auth0.",
				Required:    false,
				Order:       11,
				DependsOn:   []string{"connection_mode", "auth_type"},
				VisibleWhen: &types.VisibleWhen{Field: "auth_type", Value: []string{"oauth2"}},
			},
			"timeout": {
				Type:        types.PropertyTypeString,
				Description: "Request timeout (e.g., '30s').",
				Required:    false,
				Default:     "60s",
				Order:       12,
			},
		},
	}
}

func (t *MCPTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"content": {
				Type:        "array",
				Description: "The content returned by the tool.",
				Required:    true,
			},
			"isError": {
				Type:        "boolean",
				Description: "Whether the tool execution resulted in an error.",
				Required:    false,
			},
		},
	}
}
