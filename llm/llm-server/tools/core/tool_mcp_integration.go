package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"sort"
	"strings"
	"sync"
	"time"
)

// mcpIntegrationToolCache caches MCP tools discovered from integrations (30min TTL).
type mcpIntegrationToolCache struct {
	mutex sync.RWMutex
	data  map[string]struct {
		tools  []NBTool
		expiry time.Time
	}
}

var mcpIntegrationToolCacheInstance = &mcpIntegrationToolCache{
	data: make(map[string]struct {
		tools  []NBTool
		expiry time.Time
	}),
}

func init() {
	RegisterToolCacheInvalidator(func(accountId string) {
		mcpIntegrationToolCacheInstance.delete(accountId)
	})
}

// InvalidateAccountIntegrationCache busts every llm-server cache that
// depends on integration state for the given account, in this process and
// across all replicas (via the existing Redis pub/sub channel).
//
// Callers (api-server) invoke this after any successful integration
// mutation — create / update status / delete — regardless of integration
// type. The broad scope is deliberate: MCP integrations alter the agent
// tool list, LLM provider integrations alter cached provider credentials,
// and future integration types may add their own caches via
// RegisterToolCacheInvalidator. Treating "an integration changed" as a
// single coherent event avoids silent staleness when a new integration
// adds a cache that nobody remembered to wire into the per-type rules.
//
// Delegates to InvalidateToolCache so the work goes through the existing,
// already-tested invalidation chain (custom tools, DTO views, MCP tools,
// LLM provider config, account config summary) and broadcast.
func InvalidateAccountIntegrationCache(accountId string) {
	InvalidateToolCache(accountId)
}

func (c *mcpIntegrationToolCache) get(accountId string) ([]NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *mcpIntegrationToolCache) set(accountId string, tools []NBTool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.data[accountId] = struct {
		tools  []NBTool
		expiry time.Time
	}{
		tools:  tools,
		expiry: time.Now().Add(defaultCacheTTL),
	}
}

func (c *mcpIntegrationToolCache) delete(accountId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.data, accountId)
}

// mcpIntegrationConfig holds resolved config for an MCP integration.
type mcpIntegrationConfig struct {
	IntegrationID   string
	IntegrationName string
	ConnectionMode  string
	DatasourceKey   string // agent-side datasource ID (e.g. "local:my-mcp-server")
	URL             string
	AuthType        string
	LLMInstructions string
	// Auth credentials (decrypted)
	BearerToken       string
	Username          string
	Password          string
	CustomHeaderName  string
	CustomHeaderValue string
	// OAuth 2.0 client_credentials
	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScope        string
	OAuthAudience     string
}

// mcpIntegrationTool is an NBTool backed by a discovered MCP server tool via integrations.
type mcpIntegrationTool struct {
	config      mcpIntegrationConfig
	toolName    string // Prefixed name for uniqueness (e.g. "myserver_list_users")
	mcpToolName string // Original name as known by the MCP server (e.g. "list_users")
	toolDesc    string
	toolSchema  ToolSchema
}

func (m mcpIntegrationTool) Name() string {
	return m.toolName
}

func (m mcpIntegrationTool) Description() string {
	desc := m.toolDesc
	if m.config.LLMInstructions != "" {
		desc = m.config.LLMInstructions + "\n\n" + desc
	}
	return desc
}

func (m mcpIntegrationTool) InputSchema() ToolSchema {
	return m.toolSchema
}

func (m mcpIntegrationTool) GetType() NBToolType {
	return NBToolTypeTool
}

func (m mcpIntegrationTool) Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	log := ctx.Ctx.GetLogger().With("toolName", m.toolName, "integrationId", m.config.IntegrationID, "accountId", ctx.AccountId)
	log.Info("mcp-integration: executing tool")

	// Build tool call arguments
	args := input.Arguments
	if args == nil {
		args = make(map[string]any)
	}

	callParams := map[string]any{
		"name":      m.mcpToolName,
		"arguments": args,
	}

	response, err := executeMCPViaRelay(ctx.AccountId, m.config, "tools/call", callParams, time.Duration(config.Config.LlmServerMCPExecutionTimeoutSeconds)*time.Second)
	if err != nil {
		log.Error("mcp-integration: tool call failed", "error", err)
		return NBToolResponse{
			Status: NBToolResponseStatusError,
			Data:   fmt.Sprintf("MCP tool call failed: %v", err),
			Type:   NBToolResponseTypeText,
		}, nil
	}

	return NBToolResponse{
		Status: NBToolResponseStatusSuccess,
		Data:   response,
		Type:   NBToolResponseTypeText,
	}, nil
}

// executeMCPViaRelay sends an MCP JSON-RPC request through the relay server.
// For direct mode: relay handles the HTTP call to the MCP server.
// For vm_agent mode: relay routes to forager via RabbitMQ.
func executeMCPViaRelay(accountID string, cfg mcpIntegrationConfig, method string, params map[string]any, timeout time.Duration) (string, error) {
	actionParams := map[string]any{
		"method": method,
	}
	if params != nil {
		actionParams["params"] = params
	}

	var agentType string

	if cfg.ConnectionMode == "direct" {
		actionParams["connection_mode"] = "direct"
		actionParams["url"] = cfg.URL
		actionParams["auth_type"] = cfg.AuthType
		if cfg.AuthType == "none" {
			actionParams["auth_type"] = ""
		}

		authCreds := map[string]string{}
		switch cfg.AuthType {
		case "bearer":
			authCreds["bearer_token"] = cfg.BearerToken
		case "basic":
			authCreds["username"] = cfg.Username
			authCreds["password"] = cfg.Password
		case "api_key", "custom_header":
			authCreds["custom_header_name"] = cfg.CustomHeaderName
			authCreds["custom_header_value"] = cfg.CustomHeaderValue
		case "oauth2":
			authCreds["oauth_token_url"] = cfg.OAuthTokenURL
			authCreds["oauth_client_id"] = cfg.OAuthClientID
			authCreds["oauth_client_secret"] = cfg.OAuthClientSecret
			authCreds["oauth_scope"] = cfg.OAuthScope
			authCreds["oauth_audience"] = cfg.OAuthAudience
		}
		if len(authCreds) > 0 {
			actionParams["auth_credentials"] = authCreds
		}
		// No agent type needed for direct mode — relay handles it
	} else {
		// vm_agent mode — route to forager
		actionParams["connection_mode"] = "vm_agent"
		dsKey := cfg.DatasourceKey
		if dsKey == "" {
			dsKey = cfg.IntegrationID
		}
		actionParams["datasource_id"] = dsKey
		agentType = "proxy"
	}

	duration := timeout
	if duration == 0 {
		duration = time.Duration(config.Config.LlmServerRelayCommandExecutionTimeoutSeconds) * time.Second
	}

	body := relay.ActionExecuteBody{
		AccountID:    accountID,
		ActionName:   "mcp_request",
		ActionParams: actionParams,
		AgentType:    agentType,
		Timeout:      duration,
	}

	response, err := relay.Execute(body)
	if err != nil {
		return "", fmt.Errorf("relay MCP request failed: %w", err)
	}

	return parseMCPRelayResponse(response)
}

// parseMCPRelayResponse extracts the MCP response data from the relay response.
// Response format: {"status_code": N, "data": "<json-string>"}
func parseMCPRelayResponse(response map[string]any) (string, error) {
	dataStr, ok := response["data"].(string)
	if !ok {
		// Try nested data from normal relay response format
		if dataMap, ok := response["data"].(map[string]any); ok {
			b, _ := json.Marshal(dataMap)
			return string(b), nil
		}
		return "", fmt.Errorf("MCP response missing 'data' field")
	}

	statusCode, _ := response["status_code"].(float64)
	if statusCode >= 400 {
		return "", fmt.Errorf("MCP server returned status %d: %s", int(statusCode), dataStr)
	}

	return dataStr, nil
}

// ListMCPIntegrationTools returns MCP tools discovered from integrations for the account.
// Results are cached with 30min TTL.
func ListMCPIntegrationTools(accountId string) []NBTool {
	if accountId == "" {
		return nil
	}

	if tools, ok := mcpIntegrationToolCacheInstance.get(accountId); ok {
		return tools
	}

	tools := loadMCPIntegrationToolsFromDB(accountId)
	// Only cache successful discoveries — don't cache nil/empty on failure
	if tools != nil {
		mcpIntegrationToolCacheInstance.set(accountId, tools)
	}
	return tools
}

// ListMCPIntegrationToolDtos returns ToolDto representations for the MCP integration tools.
func ListMCPIntegrationToolDtos(accountId string) []ToolDto {
	tools := ListMCPIntegrationTools(accountId)
	dtos := make([]ToolDto, 0, len(tools))
	for _, t := range tools {
		mcpTool, ok := t.(mcpIntegrationTool)
		if !ok {
			continue
		}
		dtos = append(dtos, ToolDto{
			Id:           mcpTool.config.IntegrationID + ":" + mcpTool.toolName,
			Name:         mcpTool.toolName,
			Description:  mcpTool.Description(),
			Type:         ToolTypeCustom,
			Status:       ToolStatusEnabled,
			ExecutorType: ToolExecutorTypeMCP,
			NBToolType:   NBToolTypeTool,
			InputSchema:  mcpTool.toolSchema,
			Config:       map[string]any{"integration_id": mcpTool.config.IntegrationID},
			IsConfigured: true,
		})
	}
	return dtos
}

// loadMCPIntegrationToolsFromDB queries MCP integrations and discovers tools via relay.
func loadMCPIntegrationToolsFromDB(accountId string) []NBTool {
	configs := loadMCPIntegrationConfigs(accountId)
	if len(configs) == 0 {
		return nil
	}

	var allTools []NBTool
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, cfg := range configs {
		wg.Add(1)
		go func(c mcpIntegrationConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tools := discoverMCPTools(accountId, c)
			if len(tools) > 0 {
				mu.Lock()
				allTools = append(allTools, tools...)
				mu.Unlock()
			}
		}(cfg)
	}
	wg.Wait()

	// Discovery runs in parallel goroutines, so the append order above reflects
	// goroutine completion order (i.e. relay/network latency), not config order.
	// Sort by tool name so the resulting tool_descriptions block is byte-stable
	// across cache refills — otherwise every 30-min refill rotates the block and
	// invalidates the account-scope LLM cache.
	sort.SliceStable(allTools, func(i, j int) bool {
		return allTools[i].Name() < allTools[j].Name()
	})

	return allTools
}

// loadMCPIntegrationConfigs queries the integrations table for enabled MCP integrations.
func loadMCPIntegrationConfigs(accountId string) []mcpIntegrationConfig {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("mcp-integration: failed to get database manager", "error", err)
		return nil
	}

	rows, err := dbms.Query(`
		SELECT
			i.id::text,
			i.name::text,
			icv.name as config_name,
			icv.value as config_value,
			icv.is_encrypted as config_encrypted
		FROM integrations i
		JOIN integrations_cloud_accounts ia ON i.id = ia.integration_id
		LEFT JOIN integration_config_values icv ON i.id = icv.integration_id
		WHERE ia.cloud_account_id = $1
		AND i.type = 'mcp' AND i.status = 'enabled'
	`, accountId)
	if err != nil {
		slog.Error("mcp-integration: failed to query integrations", "error", err, "account_id", accountId)
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("mcp-integration: failed to close rows", "error", err)
		}
	}()

	configMap := make(map[string]*mcpIntegrationConfig)
	configOrder := []string{}

	for rows.Next() {
		var integrationID, integrationName string
		var configKey, configValue *string
		var configEncrypted *bool

		if err := rows.Scan(&integrationID, &integrationName, &configKey, &configValue, &configEncrypted); err != nil {
			slog.Error("mcp-integration: failed to scan row", "error", err)
			continue
		}

		if _, exists := configMap[integrationID]; !exists {
			configMap[integrationID] = &mcpIntegrationConfig{
				IntegrationID:   integrationID,
				IntegrationName: integrationName,
				ConnectionMode:  "direct", // default
			}
			configOrder = append(configOrder, integrationID)
		}

		if configKey == nil || configValue == nil {
			continue
		}

		val := *configValue
		if configEncrypted != nil && *configEncrypted {
			decrypted, err := common.Decrypt(val)
			if err != nil {
				slog.Error("mcp-integration: failed to decrypt config", "error", err, "key", *configKey)
			} else {
				val = decrypted
			}
		}

		cfg := configMap[integrationID]
		switch *configKey {
		case "connection_mode":
			cfg.ConnectionMode = val
		case "datasource_key":
			cfg.DatasourceKey = val
		case "url":
			cfg.URL = val
		case "auth_type":
			cfg.AuthType = val
		case "bearer_token":
			cfg.BearerToken = val
		case "username":
			cfg.Username = val
		case "password":
			cfg.Password = val
		case "custom_header_name":
			cfg.CustomHeaderName = val
		case "custom_header_value":
			cfg.CustomHeaderValue = val
		case "llm_instructions":
			cfg.LLMInstructions = val
		case "oauth_token_url":
			cfg.OAuthTokenURL = val
		case "oauth_client_id":
			cfg.OAuthClientID = val
		case "oauth_client_secret":
			cfg.OAuthClientSecret = val
		case "oauth_scope":
			cfg.OAuthScope = val
		case "oauth_audience":
			cfg.OAuthAudience = val
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("mcp-integration: error iterating integration rows", "error", err, "account_id", accountId)
		return nil
	}

	configs := make([]mcpIntegrationConfig, 0, len(configOrder))
	for _, id := range configOrder {
		configs = append(configs, *configMap[id])
	}
	return configs
}

// discoverMCPTools calls tools/list on an MCP server via relay and returns NBTools.
func discoverMCPTools(accountId string, cfg mcpIntegrationConfig) []NBTool {
	log := slog.With("integration_id", cfg.IntegrationID, "integration_name", cfg.IntegrationName, "account_id", accountId)

	response, err := executeMCPViaRelay(accountId, cfg, "tools/list", nil, time.Duration(config.Config.LlmServerMCPDiscoveryTimeoutSeconds)*time.Second)
	if err != nil {
		log.Error("mcp-integration: failed to discover tools", "error", err)
		return nil
	}

	// Parse JSON-RPC response
	var rpcResp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Type       string                            `json:"type"`
					Properties map[string]map[string]interface{} `json:"properties"`
					Required   []string                          `json:"required"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	// Some MCP servers (notably GitHub's remote MCP) reply over Server-Sent
	// Events transport: the JSON-RPC payload is wrapped in "event:" / "data:"
	// lines instead of being the bare body. Unwrap before parsing so those
	// integrations work without relay-side changes.
	response = unwrapSSEIfPresent(response)

	if err := json.Unmarshal([]byte(response), &rpcResp); err != nil {
		log.Error("mcp-integration: failed to parse tools/list response", "error", err, "response", truncate(response, 500))
		return nil
	}

	if rpcResp.Error != nil {
		log.Error("mcp-integration: tools/list returned error", "code", rpcResp.Error.Code, "message", rpcResp.Error.Message)
		return nil
	}

	tools := make([]NBTool, 0, len(rpcResp.Result.Tools))
	prefix := sanitizeToolPrefix(cfg.IntegrationName)

	for _, t := range rpcResp.Result.Tools {
		if t.Name == "" {
			continue
		}

		// Convert properties to ToolSchemaProperty
		props := make(map[string]ToolSchemaProperty, len(t.InputSchema.Properties))
		for k, v := range t.InputSchema.Properties {
			description := ""
			if d, ok := v["description"].(string); ok {
				description = d
			}
			schemaType := ToolSchemaTypeString
			if typ, ok := v["type"].(string); ok && typ != "" {
				schemaType = ToolSchemaType(typ)
			}
			prop := ToolSchemaProperty{
				Type:        schemaType,
				Description: description,
			}
			if enum, ok := v["enum"].([]interface{}); ok {
				prop.Enum = enum
			}
			if def, ok := v["default"]; ok {
				prop.Default = def
			}
			props[k] = prop
		}

		schemaType := ToolSchemaType(t.InputSchema.Type)
		if schemaType == "" {
			schemaType = ToolSchemaTypeObject
		}

		// Prefix tool name with integration name for uniqueness
		toolName := prefix + "_" + t.Name

		tools = append(tools, mcpIntegrationTool{
			config:      cfg,
			toolName:    toolName,
			mcpToolName: t.Name,
			toolDesc:    t.Description,
			toolSchema: ToolSchema{
				Type:       schemaType,
				Properties: props,
				Required:   t.InputSchema.Required,
			},
		})
	}

	log.Info("mcp-integration: discovered tools", "count", len(tools))
	return tools
}

// sanitizeToolPrefix creates a valid tool name prefix from an integration name.
func sanitizeToolPrefix(name string) string {
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		if r == ' ' || r == '-' {
			return '_'
		}
		return -1
	}, name)
	// Remove consecutive underscores
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	name = strings.Trim(name, "_")
	if name == "" {
		name = "mcp"
	}
	return name
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// unwrapSSEIfPresent extracts the JSON-RPC payload from a Server-Sent
// Events stream when the body uses SSE framing. The MCP "Streamable HTTP"
// transport allows servers to reply with one or more SSE events of the
// form:
//
//	event: message
//	data: {"jsonrpc":"2.0","id":1,"result":{...}}
//
// GitHub's remote MCP server uses this transport. Plain-JSON responses
// (which start with '{' or '[') are returned unchanged.
//
// When multiple data: lines are present we keep the LAST one — for our
// request/response RPC pattern (tools/list, tools/call) the last event
// carries the final result; earlier events, if any, would be progress
// notifications we don't act on at this layer.
func unwrapSSEIfPresent(body string) string {
	trimmed := strings.TrimLeft(body, " \t\r\n")
	if trimmed == "" || trimmed[0] == '{' || trimmed[0] == '[' {
		return body
	}

	var lastData string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data:") {
			lastData = strings.TrimSpace(line[len("data:"):])
		}
	}
	if lastData != "" {
		return lastData
	}
	return body
}
