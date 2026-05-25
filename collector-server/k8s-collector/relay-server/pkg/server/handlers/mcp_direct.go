package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/relay-server/pkg/server/metrics"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/attribute"
)

var mcpHTTPClient = &http.Client{
	Timeout: 120 * time.Second,
}

const mcpSessionTTL = 30 * time.Minute

type mcpSessionEntry struct {
	sessionID string
	expiry    time.Time
}

// mcpSessionCache caches MCP session IDs per (URL, account, caller session) tuple.
// Entries expire after mcpSessionTTL. Expired entries are evicted lazily on read
// and periodically by a background sweeper to prevent unbounded growth.
var mcpSessionCache = struct {
	sync.RWMutex
	sessions map[string]mcpSessionEntry // "URL|accountID[|session_id]" -> MCP session entry
}{sessions: make(map[string]mcpSessionEntry)}

func init() {
	go mcpSessionCacheSweeper()
}

// mcpSessionCacheSweeper periodically removes expired entries to prevent unbounded growth.
func mcpSessionCacheSweeper() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		mcpSessionCache.Lock()
		for key, entry := range mcpSessionCache.sessions {
			if now.After(entry.expiry) {
				delete(mcpSessionCache.sessions, key)
			}
		}
		mcpSessionCache.Unlock()
	}
}

// handleDirectMCP handles MCP requests in direct mode (no agent needed).
// Returns true if the request was handled, false if it should proceed to normal RPC flow.
func handleDirectMCP(c *gin.Context, rawBody []byte, logger *slog.Logger, start time.Time, accountID string) bool {
	actionName := gjson.GetBytes(rawBody, "body.action_name").String()
	if actionName == "" {
		actionName = gjson.GetBytes(rawBody, "action").String()
	}
	if actionName != "mcp_request" {
		return false
	}

	connectionMode := gjson.GetBytes(rawBody, "body.action_params.connection_mode").String()
	if connectionMode != "direct" {
		return false
	}

	// Record metrics for direct MCP requests (same as normal RPC path)
	if metrics.AsyncMetricsInstance != nil {
		metrics.AsyncMetricsInstance.IncInFlight(metrics.AttrAccount(accountID))
		defer metrics.AsyncMetricsInstance.DecInFlight(metrics.AttrAccount(accountID))
		defer func() {
			metrics.AsyncMetricsInstance.RecordAgentRTT(time.Since(start).Seconds(), metrics.AttrAccount(accountID))
			metrics.AsyncMetricsInstance.RecordRequestLatency(time.Since(start).Seconds(),
				metrics.AttrAccount(accountID), attribute.String("handler", "request"), attribute.String("action", "mcp_request"))
		}()
	}

	mcpURL := gjson.GetBytes(rawBody, "body.action_params.url").String()
	if mcpURL == "" {
		c.JSON(400, gin.H{"error": "url is required for direct MCP request"})
		return true
	}

	// Build MCP JSON-RPC request body from action_params (validate before session init
	// to avoid creating upstream sessions for invalid requests)
	mcpBody, err := buildMCPRequestBody(rawBody)
	if err != nil {
		logger.Error("failed to build MCP request body", "error", err)
		c.JSON(400, gin.H{"error": fmt.Sprintf("invalid MCP request: %v", err)})
		return true
	}

	// Cache key always includes accountID to prevent cross-tenant session sharing.
	// Optional session_id provides per-conversation isolation within an account.
	callerSessionID := gjson.GetBytes(rawBody, "body.action_params.session_id").String()
	cacheKey := mcpURL + "|" + accountID
	if callerSessionID != "" {
		cacheKey += "|" + callerSessionID
	}

	// Ensure we have a session for Streamable HTTP servers
	sessionID, err := ensureMCPSession(c.Request.Context(), cacheKey, mcpURL, rawBody, logger)
	if err != nil {
		logger.Warn("MCP session init failed, proceeding without session", "url", mcpURL, "error", err)
	}

	// Execute the actual MCP request
	responseData, statusCode, err := doMCPRequest(c.Request.Context(), cacheKey, mcpURL, mcpBody, sessionID, rawBody, logger)
	if err != nil {
		logger.Error("direct MCP request failed", "url", mcpURL, "error", err)
		c.JSON(502, gin.H{"error": fmt.Sprintf("MCP server request failed: %v", err)})
		return true
	}

	// Return in the same format as forager would: {"status_code": N, "data": "..."}
	result := map[string]any{
		"status_code": statusCode,
		"data":        responseData,
	}
	c.JSON(200, result)
	return true
}

// ensureMCPSession returns a cached session ID or performs an initialize handshake.
// Uses double-checked locking to prevent redundant handshakes from concurrent goroutines.
// cacheKey isolates sessions per (URL, caller) pair; mcpURL is the actual MCP server endpoint.
func ensureMCPSession(ctx context.Context, cacheKey string, mcpURL string, rawBody []byte, logger *slog.Logger) (string, error) {
	// Fast path: check cache with read lock
	mcpSessionCache.RLock()
	if entry, ok := mcpSessionCache.sessions[cacheKey]; ok && time.Now().Before(entry.expiry) {
		mcpSessionCache.RUnlock()
		return entry.sessionID, nil
	}
	mcpSessionCache.RUnlock()

	// Slow path: acquire write lock and re-check before performing handshake
	mcpSessionCache.Lock()
	if entry, ok := mcpSessionCache.sessions[cacheKey]; ok && time.Now().Before(entry.expiry) {
		mcpSessionCache.Unlock()
		return entry.sessionID, nil
	}
	// Evict expired entry if present
	delete(mcpSessionCache.sessions, cacheKey)
	mcpSessionCache.Unlock()

	// Perform initialize handshake (outside lock to avoid blocking other keys)
	sessionID, err := performMCPInitialize(ctx, mcpURL, rawBody)
	if err != nil {
		return "", err
	}
	if sessionID == "" {
		// Server doesn't require sessions — that's fine
		return "", nil
	}

	logger.Info("MCP session initialized", "url", mcpURL)

	mcpSessionCache.Lock()
	mcpSessionCache.sessions[cacheKey] = mcpSessionEntry{
		sessionID: sessionID,
		expiry:    time.Now().Add(mcpSessionTTL),
	}
	mcpSessionCache.Unlock()

	return sessionID, nil
}

// performMCPInitialize sends the MCP initialize handshake and returns the session ID.
func performMCPInitialize(ctx context.Context, mcpURL string, rawBody []byte) (string, error) {
	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "nudgebee-relay",
				"version": "1.0.0",
			},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", mcpURL, bytes.NewReader(initBody))
	if err != nil {
		return "", fmt.Errorf("creating init request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	if err := injectMCPAuth(req, rawBody); err != nil {
		return "", fmt.Errorf("auth setup failed: %w", err)
	}

	resp, err := mcpHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("init request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("init returned HTTP %d: %s", resp.StatusCode, truncateBytes(body, 256))
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain remaining body

	return resp.Header.Get("Mcp-Session-Id"), nil
}

// doMCPRequest sends the actual MCP JSON-RPC request with session and SSE handling.
// If the server returns 400 with "session" in the error, it retries with a fresh session.
func doMCPRequest(ctx context.Context, cacheKey string, mcpURL string, mcpBody []byte, sessionID string, rawBody []byte, logger *slog.Logger) (string, int, error) {
	responseData, statusCode, err := executeMCPHTTP(ctx, mcpURL, mcpBody, sessionID, rawBody)
	if err != nil {
		return "", 0, err
	}

	// If we got a 400 with session-related error, invalidate and retry once
	if statusCode == 400 && strings.Contains(strings.ToLower(responseData), "session") {
		logger.Info("MCP session expired, re-initializing", "url", mcpURL)
		mcpSessionCache.Lock()
		delete(mcpSessionCache.sessions, cacheKey)
		mcpSessionCache.Unlock()

		newSessionID, err := ensureMCPSession(ctx, cacheKey, mcpURL, rawBody, logger)
		if err != nil {
			return "", 0, fmt.Errorf("session re-init failed: %w", err)
		}
		return executeMCPHTTP(ctx, mcpURL, mcpBody, newSessionID, rawBody)
	}

	return responseData, statusCode, nil
}

// executeMCPHTTP performs a single MCP HTTP POST with optional session ID and SSE parsing.
func executeMCPHTTP(ctx context.Context, mcpURL string, mcpBody []byte, sessionID string, rawBody []byte) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", mcpURL, bytes.NewReader(mcpBody))
	if err != nil {
		return "", 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if err := injectMCPAuth(req, rawBody); err != nil {
		return "", 0, fmt.Errorf("auth setup failed: %w", err)
	}

	resp, err := mcpHTTPClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("reading response: %w", err)
	}

	// Auto-detect SSE: if the server returned text/event-stream, parse data lines
	responseData := string(respBody)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		responseData = parseSSEResponse(respBody)
	}

	return responseData, resp.StatusCode, nil
}

// parseSSEResponse extracts JSON data from an SSE response body.
// Collects all "data: " lines and concatenates them, stopping at "[DONE]".
// Falls back to the raw body if no SSE data lines are found or on scan error.
func parseSSEResponse(body []byte) string {
	var result strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			result.WriteString(data)
		}
	}
	if scanner.Err() != nil || result.Len() == 0 {
		return string(body)
	}
	return result.String()
}

// buildMCPRequestBody constructs the JSON-RPC body for the MCP server.
// Looks for action_params.body (raw string) or action_params.params (JSON object).
func buildMCPRequestBody(rawBody []byte) ([]byte, error) {
	// If a raw body string is provided, use it directly
	bodyStr := gjson.GetBytes(rawBody, "body.action_params.body").String()
	if bodyStr != "" {
		return []byte(bodyStr), nil
	}

	// Otherwise build from method + params (JSON-RPC format)
	method := gjson.GetBytes(rawBody, "body.action_params.method").String()
	params := gjson.GetBytes(rawBody, "body.action_params.params")

	if method == "" {
		return nil, fmt.Errorf("method is required in action_params")
	}

	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params.Exists() && params.Type != gjson.Null {
		var p any
		if err := json.Unmarshal([]byte(params.Raw), &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		rpcReq["params"] = p
	}

	return json.Marshal(rpcReq)
}

func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// injectMCPAuth adds authentication headers to the MCP HTTP request.
// Returns an error if authentication setup fails (e.g. OAuth token exchange).
func injectMCPAuth(req *http.Request, rawBody []byte) error {
	authType := gjson.GetBytes(rawBody, "body.action_params.auth_type").String()
	creds := gjson.GetBytes(rawBody, "body.action_params.auth_credentials")

	switch authType {
	case "basic":
		username := creds.Get("username").String()
		password := creds.Get("password").String()
		req.SetBasicAuth(username, password)
	case "bearer":
		token := creds.Get("bearer_token").String()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "custom_header", "api_key":
		name := creds.Get("custom_header_name").String()
		value := creds.Get("custom_header_value").String()
		if authType == "api_key" && name == "" {
			name = "X-API-Key"
		}
		if name != "" {
			req.Header.Set(name, value)
		}
	case "oauth2":
		token, err := fetchOAuthToken(
			req.Context(),
			creds.Get("oauth_token_url").String(),
			creds.Get("oauth_client_id").String(),
			creds.Get("oauth_client_secret").String(),
			creds.Get("oauth_scope").String(),
			creds.Get("oauth_audience").String(),
		)
		if err != nil {
			return fmt.Errorf("OAuth token exchange failed: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

// fetchOAuthToken performs an OAuth 2.0 client_credentials grant and returns the access token.
func fetchOAuthToken(ctx context.Context, tokenURL, clientID, clientSecret, scope, audience string) (string, error) {
	if tokenURL == "" || clientID == "" || clientSecret == "" {
		return "", fmt.Errorf("oauth2: token_url, client_id, and client_secret are required")
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	if scope != "" {
		form.Set("scope", scope)
	}
	if audience != "" {
		form.Set("audience", audience)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth2: failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := mcpHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth2: token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("oauth2: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth2: token endpoint returned %d: %s", resp.StatusCode, truncateBytes(body, 256))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("oauth2: failed to parse response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("oauth2: empty access token in response")
	}

	return tokenResp.AccessToken, nil
}
