package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/security"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// mcpIntegrationToolCache caches MCP tools discovered from integrations (30min
// TTL). A list holding an integration's placeholder is kept only until that
// integration's next retry is due (mcpUnavailableTool.retryAfter), which comes
// from its stored health so every pod follows the same clock (#40994).
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

// mcpDiscoveryRetryMin is how soon a failed discovery is tried again. An MCP
// server restart takes about a minute, and before #40766 it could cost a pod
// that integration's tools for the full 30 minutes.
const mcpDiscoveryRetryMin = time.Minute

// mcpDiscoveryRetryAfter returns how long to keep a tool list in which an
// integration's discovery has been failing for failingFor. The wait equals the
// time the integration has already been down, between mcpDiscoveryRetryMin and
// defaultCacheTTL: retries come at about 1, 2, 4, 8, 16 and 32 minutes after
// the first failure, then every 30 minutes. A short outage is picked up within
// a minute or two, while an integration that is down for good (a deleted
// endpoint, expired credentials) costs no more discovery calls than it did
// before.
//
// It is derived from elapsed time, not from a failure count, because several
// requests can miss the cache at once and each runs its own discovery: a count
// would grow with every one of them and push the first retry out.
func mcpDiscoveryRetryAfter(failingFor time.Duration) time.Duration {
	return min(max(failingFor, mcpDiscoveryRetryMin), defaultCacheTTL)
}

// mcpImplType is the metric-label value emitted for MCP integration tools.
// Kept as a package-level const so call sites stay self-describing and
// future tool types can add their own constants alongside.
const mcpImplType = "mcp"

// ToolImplTypeMCP is ImplTypeFor's value for an MCP integration tool.
const ToolImplTypeMCP = mcpImplType

// mcpDiscoveryImplType is a circuit-breaker toolType distinct from
// mcpImplType, used only for discoverMCPTools' RecordSuccess/RecordFailure
// bookkeeping. Deliberately separate from the Call breaker: a successful
// discovery doesn't prove Call will succeed (auth can be scoped per-tool,
// an MCP shim can list tools it can't execute), and a failed discovery
// never registers a callable tool in the first place, so there's nothing
// for the Call breaker to protect. Recording discovery outcomes under this
// key keeps the two failure domains independent while reusing the same
// breaker and the same nb_llm_tool_healthy gauge (as its own type=
// mcp_discovery series). Nothing calls IsHealthy with this toolType — it's
// observability-only bookkeeping, not a fast-fail gate.
const mcpDiscoveryImplType = "mcp_discovery"

func init() {
	RegisterToolCacheInvalidator(func(accountId string) {
		mcpIntegrationToolCacheInstance.delete(accountId)
	})
	// Wire the cache into the nb_llm_tools_available observable gauge
	// (with type="mcp") so dashboards can see how many MCP tools each
	// (account, integration) is currently exposing. Common can't import
	// tools/core (cycle), so the snapshot fn flows through a registration
	// hook.
	common.RegisterToolsAvailableSnapshot(mcpToolsAvailableSnapshot)
}

// mcpToolsAvailableSnapshot walks the per-account MCP tool cache and
// reports tool counts grouped by (account, integration_id). Only entries
// whose TTL has not expired are reported. Used by the
// nb_llm_tools_available observable gauge.
func mcpToolsAvailableSnapshot() []common.ToolsAvailableEntry {
	mcpIntegrationToolCacheInstance.mutex.RLock()
	defer mcpIntegrationToolCacheInstance.mutex.RUnlock()

	now := time.Now()
	type counterKey struct{ account, integration string }
	counts := make(map[counterKey]int)
	for accountId, item := range mcpIntegrationToolCacheInstance.data {
		if !now.Before(item.expiry) {
			continue
		}
		for _, t := range item.tools {
			mcpTool, ok := t.(mcpIntegrationTool)
			if !ok {
				continue
			}
			counts[counterKey{accountId, mcpTool.config.IntegrationID}]++
		}
	}

	out := make([]common.ToolsAvailableEntry, 0, len(counts))
	for k, c := range counts {
		out = append(out, common.ToolsAvailableEntry{
			Type:          mcpImplType,
			AccountId:     k.account,
			IntegrationId: k.integration,
			ToolCount:     c,
		})
	}
	return out
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

// get returns a defensive copy of the cached slice so callers can never
// mutate the shared backing array underneath a concurrent reader/writer.
func (c *mcpIntegrationToolCache) get(accountId string) ([]NBTool, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	item, exists := c.data[accountId]
	if exists && time.Now().Before(item.expiry) {
		tools := make([]NBTool, len(item.tools))
		copy(tools, item.tools)
		return tools, true
	}
	return nil, false
}

// set stores the account's tool list and returns how long it will be kept:
// defaultCacheTTL, or less when the list holds an integration's placeholder, which
// is kept until that integration's retry is due.
func (c *mcpIntegrationToolCache) set(accountId string, tools []NBTool) time.Duration {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	ttl := defaultCacheTTL
	for _, t := range tools {
		unavailable, ok := t.(mcpUnavailableTool)
		if !ok {
			continue
		}
		retry := unavailable.retryAfter
		if retry <= 0 {
			retry = mcpDiscoveryRetryMin
		}
		ttl = min(ttl, retry)
	}
	c.data[accountId] = struct {
		tools  []NBTool
		expiry time.Time
	}{
		tools:  tools,
		expiry: time.Now().Add(ttl),
	}
	return ttl
}

// shorten makes the account's cached list expire within d, if it would
// otherwise be kept longer.
func (c *mcpIntegrationToolCache) shorten(accountId string, d time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	item, ok := c.data[accountId]
	if !ok {
		return
	}
	if limit := time.Now().Add(d); item.expiry.After(limit) {
		item.expiry = limit
		c.data[accountId] = item
	}
}

// deleteIntegration drops every cached list holding a tool or the placeholder of
// the integration, restricted to accountIds when that is not empty.
func (c *mcpIntegrationToolCache) deleteIntegration(integrationID string, accountIds []string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	only := make(map[string]bool, len(accountIds))
	for _, id := range accountIds {
		only[id] = true
	}
	for accountId, item := range c.data {
		if len(only) > 0 && !only[accountId] {
			continue
		}
		for _, t := range item.tools {
			if mcpToolIntegrationID(t) == integrationID {
				delete(c.data, accountId)
				break
			}
		}
	}
}

func mcpToolIntegrationID(t NBTool) string {
	switch v := t.(type) {
	case mcpIntegrationTool:
		return v.config.IntegrationID
	case mcpUnavailableTool:
		return v.integrationID
	}
	return ""
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
	// WriteTools are the server's own tool names whose calls change something,
	// so they ask for approval. Every other tool runs as before.
	WriteTools []string
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
	// readOnlyHint is the server's own annotations.readOnlyHint for the tool:
	// true only when the server says the tool does not change anything.
	readOnlyHint bool
}

// MCPReadOnlyHinter is an MCP tool that reports whether its server marks it
// read-only (annotations.readOnlyHint). It is a hint from the server, not a
// classification: core does not act on it; a run policy may.
type MCPReadOnlyHinter interface {
	MCPReadOnlyHint() bool
}

// MCPReadOnlyHint reports the server's readOnlyHint for the tool.
func (m mcpIntegrationTool) MCPReadOnlyHint() bool { return m.readOnlyHint }

// mcpReadOnlyHint reads a tool's annotations.readOnlyHint: true only for an
// object whose readOnlyHint is the boolean true. Any other shape (annotations
// not an object, readOnlyHint a string) counts as not read-only.
func mcpReadOnlyHint(annotations json.RawMessage) bool {
	var parsed struct {
		ReadOnlyHint any `json:"readOnlyHint"`
	}
	if len(annotations) == 0 || json.Unmarshal(annotations, &parsed) != nil {
		return false
	}
	hint, _ := parsed.ReadOnlyHint.(bool)
	return hint
}

func (m mcpIntegrationTool) Name() string {
	return m.toolName
}

// InferToolRequestType reports a write for the tools the integration lists
// in write_tools, so they ask for approval; others stay unclassified, as before.
func (m mcpIntegrationTool) InferToolRequestType(_ *security.RequestContext, _, _ string) (ToolRequestType, error) {
	if slices.Contains(m.config.WriteTools, m.mcpToolName) {
		return ToolRequestTypeUpdate, nil
	}
	return "", nil
}

// approvalMark: an MCP tool asks only when its integration lists it.
func (m mcpIntegrationTool) approvalMark() ToolApproval {
	if slices.Contains(m.config.WriteTools, m.mcpToolName) {
		return ToolApprovalAlways
	}
	return ToolApprovalNever
}

// IntegrationID is the MCP integration the tool belongs to.
func (m mcpIntegrationTool) IntegrationID() string {
	return m.config.IntegrationID
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

// ResultCanChange: an MCP server does not say whether a result is stable, and
// browser, ticket and deploy tools return something new on every call (#39635).
func (m mcpIntegrationTool) ResultCanChange() bool { return true }

// GetImplType reports the tool implementation class for metrics labeling.
// MCP integration tools share an HTTP/relay path and per-integration
// credentials, so dashboards group them under type="mcp".
func (m mcpIntegrationTool) GetImplType() string {
	return mcpImplType
}

// CircuitBreakerKey opts MCP tools into the generic circuit breaker
// keyed per (account, integration). Two integrations on the same
// account fail independently; the same integration ID across accounts
// also fails independently because the breaker namespaces by account.
//
// Deliberately per-integration, not per-tool: a broken integration server
// is far more common than one broken tool on an otherwise-healthy server,
// and keying per-tool would mean N separate 3-strike counters for one
// dead process. The cost is blast radius — a single chronically-failing
// tool trips the circuit for every other tool the same integration
// exposes on that account until cooldown elapses.
func (m mcpIntegrationTool) CircuitBreakerKey(ctx NbToolContext, _ NBToolCallRequest) (CircuitBreakerKey, bool) {
	if ctx.AccountId == "" || m.config.IntegrationID == "" {
		return CircuitBreakerKey{}, false
	}
	return CircuitBreakerKey{AccountId: ctx.AccountId, InstanceId: m.config.IntegrationID}, true
}

// IsInfrastructureFailure classifies an MCP Call outcome for the
// breaker. Every error path in Call comes from executeMCPViaRelay —
// relay transport failure, HTTP/JSON-RPC decode failure, upstream MCP
// server unreachable — which is exactly the "infra is broken" signal
// the breaker is designed to short-circuit on. A logical failure (the
// LLM picking a tool that's not registered with the server, or the tool
// returning result.isError) comes back with a nil error and an
// NBToolResponseStatusError response; it does not trip the breaker.
func (m mcpIntegrationTool) IsInfrastructureFailure(err error, _ NBToolResponse) bool {
	return err != nil
}

func (m mcpIntegrationTool) Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	log := ctx.Ctx.GetLogger().With("toolName", m.toolName, "integrationId", m.config.IntegrationID, "accountId", ctx.AccountId)

	log.Info("mcp-integration: executing tool")

	// Build tool call arguments. Per-call latency and operation count
	// are recorded by the planner's central tool.Call choke point; the
	// circuit-breaker check + recording is also handled there via the
	// CircuitBreakerKeyer / CircuitBreakerFailureClassifier interfaces
	// this type implements. This method just does the work.
	args := input.Arguments
	if args == nil {
		args = make(map[string]any)
	}

	callParams := map[string]any{
		"name":      m.mcpToolName,
		"arguments": args,
	}

	// Scope the upstream MCP session to this conversation so stateful servers
	// (browser, shell, DB session) don't share state across concurrent chats.
	// SessionId is inherited by sub-agents, so a delegate sees its parent's state.
	sessionKey := ctx.SessionId
	if sessionKey == "" {
		sessionKey = ctx.ConversationId
	}

	response, err := executeMCPViaRelay(ctx.GoContext(), ctx.AccountId, m.config, "tools/call", callParams, sessionKey, time.Duration(config.Config.LlmServerMCPExecutionTimeoutSeconds)*time.Second)
	// Record what the call says about the server (#40994). A call that timed
	// out or got a 5xx starts a probe rather than counting as a failure: a slow
	// or broken tool on a live server is not an outage.
	outcome := classifyMCPOutcome(err, false)
	if outcome.kind == mcpOutcomeSuspect {
		probeSuspectMCPIntegration(ctx.AccountId, m.config)
	} else {
		recordMCPHealth(ctx.AccountId, m.config, outcome, false)
	}
	if err != nil {
		// Debug: the integration's health logs once when it changes.
		log.Debug("mcp-integration: tool call failed", "error", err)
		// Emit the failure as JSON so the tool response is consistently JSON
		// whether the call succeeds (MCP server JSON-RPC) or fails. This is an
		// integration-layer error (not the MCP server's), so it is labelled as
		// such rather than forged into a JSON-RPC error object.
		errJSON, _ := json.Marshal(map[string]string{
			"error":  fmt.Sprintf("MCP tool call failed: %v", err),
			"source": "mcp_integration",
			"reason": classifyMCPError(err),
		})
		return NBToolResponse{
			Status: NBToolResponseStatusError,
			Data:   string(errJSON),
			Type:   NBToolResponseTypeText,
		}, err
	}

	// The MCP server answered, but the answer can still be a failure: a
	// tool-level error (result.isError, e.g. "connection refused" from the
	// server's own backend) or a JSON-RPC error object (unknown tool, bad
	// params). Report those as errors so the stored status, tool-health
	// metrics and the planner's evidence accounting see them. err stays nil:
	// the integration itself is reachable, so the circuit breaker must not trip.
	status := NBToolResponseStatusSuccess
	data := mcpToolResultText(response)
	if isMCPToolCallError(response) {
		log.Warn("mcp-integration: tool returned an error result")
		status = NBToolResponseStatusError
		// Once unwrapped, an isError result is bare text ("connect: connection
		// refused") and the planner does not tell the model a step failed, so
		// say so in the text. A JSON-RPC error object is not unwrapped and
		// already reads as an error.
		if data != response {
			data = "MCP tool returned an error: " + data
		}
	}

	return NBToolResponse{
		Status: status,
		Data:   data,
		Type:   NBToolResponseTypeText,
	}, nil
}

// mcpToolResultText returns what the model should read from a tools/call
// reply: the tool's text content, not the JSON-RPC envelope around it. The
// envelope (`{"result":{"content":[{"type":"text","text":"..."}]}}`) escapes
// every quote and newline of the real output and adds nothing the model can
// use. Text parts are joined with newlines; any other part (image, resource)
// is kept as its JSON, matching nbCustomMCPTool.Call. A result with no
// content returns its structuredContent JSON if it has one, else "" — the
// executor classifies "" as an empty result and gives the model its
// no-output message. A reply without a result (a JSON-RPC error object,
// non-JSON text) is returned unchanged.
func mcpToolResultText(response string) string {
	var rpcResp struct {
		Result *struct {
			Content           []json.RawMessage `json:"content"`
			StructuredContent json.RawMessage   `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(unwrapSSEIfPresent(response)), &rpcResp); err != nil || rpcResp.Result == nil {
		return response
	}
	if len(rpcResp.Result.Content) == 0 {
		if len(rpcResp.Result.StructuredContent) > 0 && string(rpcResp.Result.StructuredContent) != "null" {
			return string(rpcResp.Result.StructuredContent)
		}
		return ""
	}
	parts := make([]string, 0, len(rpcResp.Result.Content))
	for _, raw := range rpcResp.Result.Content {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &item); err == nil && item.Type == "text" {
			parts = append(parts, item.Text)
			continue
		}
		parts = append(parts, string(raw))
	}
	return strings.Join(parts, "\n")
}

// isMCPToolCallError reports whether a tools/call JSON-RPC reply carries a
// failure: either a JSON-RPC error object or a result with isError set. A reply
// that isn't parseable JSON-RPC is not classified as an error here.
func isMCPToolCallError(response string) bool {
	var rpcResp struct {
		Result *struct {
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(unwrapSSEIfPresent(response)), &rpcResp); err != nil {
		return false
	}
	if rpcResp.Error != nil {
		return true
	}
	return rpcResp.Result != nil && rpcResp.Result.IsError
}

// classifyMCPError buckets an MCP/relay failure into a short reason tag so the
// LLM consuming the error response can react appropriately (e.g. distinguish a
// timeout from an unreachable server). Best-effort string match — these
// transport failures have no sentinel errors to match on. Match case-insensitively
// so lowercase stdlib errors (e.g. "i/o timeout") classify correctly.
func classifyMCPError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timeout"):
		return "timeout"
	case strings.Contains(s, "unable to access relay"):
		return "relay_unreachable"
	case strings.Contains(s, "connection refused"):
		return "mcp_unreachable"
	default:
		return "mcp_error"
	}
}

// executeMCPViaRelay sends an MCP JSON-RPC request through the relay server.
// For direct mode: relay handles the HTTP call to the MCP server.
// For vm_agent mode: relay routes to forager via RabbitMQ.
// sessionKey, when set, gives the call its own upstream MCP session in the relay
// instead of the account-wide shared one.
func executeMCPViaRelay(ctx context.Context, accountID string, cfg mcpIntegrationConfig, method string, params map[string]any, sessionKey string, timeout time.Duration) (string, error) {
	actionParams := map[string]any{
		"method": method,
	}
	if params != nil {
		actionParams["params"] = params
	}
	if sessionKey != "" {
		actionParams["session_id"] = sessionKey
	}

	var agentType string
	noAgent := false

	if cfg.ConnectionMode == "direct" {
		actionParams["connection_mode"] = "direct"
		// Lets any relay pod reload credentials to end this session later
		// (docs/mcp-sessions.md).
		actionParams["integration_id"] = cfg.IntegrationID
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
		noAgent = true
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
		NoAgent:      noAgent,
		Timeout:      duration,
		// Failures are recorded as the integration's health, which logs once
		// when it changes; the relay client need not log every retry.
		Quiet: true,
	}

	response, err := relay.Execute(ctx, body)
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
		return "", &mcpStatusError{Code: int(statusCode), Body: dataStr}
	}

	return dataStr, nil
}

// ListMCPIntegrationTools returns MCP tools discovered from integrations for the account.
// Results are cached with 30min TTL, or until the next retry when an
// integration's discovery failed (mcpDiscoveryRetryAfter).
func ListMCPIntegrationTools(accountId string) []NBTool {
	if accountId == "" {
		return nil
	}

	if tools, ok := mcpIntegrationToolCacheInstance.get(accountId); ok {
		common.MetricsToolCacheLookup(mcpImplType, "hit")
		return tools
	}
	common.MetricsToolCacheLookup(mcpImplType, "miss")

	tools := loadMCPIntegrationToolsFromDB(accountId)
	// A nil list means the integrations could not be read. It is not cached,
	// so the next lookup tries again. A list in which one integration's
	// discovery failed is not nil: it holds that integration's placeholder next
	// to the other integrations' tools, so set keeps it only until the retry is
	// due.
	if tools != nil {
		if retryAfter := mcpIntegrationToolCacheInstance.set(accountId, tools); retryAfter < defaultCacheTTL {
			slog.Info("mcp-integration: tool discovery failed, tool list cached until the retry",
				"account_id", accountId, "retry_after", retryAfter.String())
		}
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
			Config:       map[string]any{"integration_id": mcpTool.config.IntegrationID, "mcp_tool_name": mcpTool.mcpToolName},
			Approval:     ApprovalMode(mcpTool),
			IsConfigured: true,
		})
	}
	return dtos
}

// loadMCPIntegrationToolsFromDB queries MCP integrations and discovers tools via relay.
//
// It returns nil only when the integrations could not be read. An account with
// no MCP integration gets an empty list that is not nil, so the caller caches
// it like any other answer; an integration change invalidates it.
func loadMCPIntegrationToolsFromDB(accountId string) []NBTool {
	configs, ok := loadMCPIntegrationConfigs(accountId)
	if !ok {
		return nil
	}

	allTools := []NBTool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	// An integration that is down is not discovered again until its retry is
	// due on the shared clock in its stored health (#40994). Without the
	// stored health every integration is discovered, as before.
	ids := make([]string, 0, len(configs))
	for _, cfg := range configs {
		ids = append(ids, cfg.IntegrationID)
	}
	stored, err := mcpHealthStoreInstance.load(ids)
	if err != nil {
		slog.Warn("mcp-integration: unable to read integration health", "error", err, "account_id", accountId)
	}
	now := time.Now()

	for _, cfg := range configs {
		if entry, ok := stored[cfg.IntegrationID][mcpHealthScope(cfg, accountId)]; ok {
			if wait, skip := mcpHealthSkip(entry, now); skip {
				placeholder := newMCPUnavailableTool(cfg, entry.Status)
				placeholder.retryAfter = wait
				mu.Lock()
				allTools = append(allTools, placeholder)
				mu.Unlock()
				continue
			}
		}
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
// The second result is false when they could not be read, which is not the same
// as the account having none.
func loadMCPIntegrationConfigs(accountId string) ([]mcpIntegrationConfig, bool) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("mcp-integration: failed to get database manager", "error", err)
		return nil, false
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
		return nil, false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("mcp-integration: failed to close rows", "error", err)
		}
	}()

	configs, err := scanMCPIntegrationConfigs(rows)
	if err != nil {
		slog.Error("mcp-integration: error iterating integration rows", "error", err, "account_id", accountId)
		return nil, false
	}
	return configs, true
}

// scanMCPIntegrationConfigs reads rows of (integration id, name, config name,
// config value, config encrypted) into one config per integration, in row order.
func scanMCPIntegrationConfigs(rows *sqlx.Rows) ([]mcpIntegrationConfig, error) {
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
		case "write_tools":
			cfg.WriteTools = ParseToolNameList(val)
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
		return nil, err
	}

	configs := make([]mcpIntegrationConfig, 0, len(configOrder))
	for _, id := range configOrder {
		configs = append(configs, *configMap[id])
	}
	return configs, nil
}

// mcpUnavailableTool is a sentinel returned in place of an enabled MCP
// integration's real tools when discovery fails. It keeps the integration
// VISIBLE to the planner — which otherwise improvises unpredictably when the
// tools silently vanish — and returns a consistent JSON "unavailable" error.
type mcpUnavailableTool struct {
	integrationID   string
	integrationName string
	reason          string
	// retryAfter is how long the account's list may keep this placeholder
	// before the integration is tried again; zero means mcpDiscoveryRetryMin.
	retryAfter time.Duration
}

func newMCPUnavailableTool(cfg mcpIntegrationConfig, reason string) mcpUnavailableTool {
	return mcpUnavailableTool{integrationID: cfg.IntegrationID, integrationName: cfg.IntegrationName, reason: reason}
}

// unavailableAfterFailure is the placeholder for a discovery that just failed,
// kept until the retry its stored health calls for. Without a stored verdict
// (the health could not be written, or the relay itself was unreachable) it is
// retried after mcpDiscoveryRetryMin.
func unavailableAfterFailure(cfg mcpIntegrationConfig, reason string, health mcpHealthEntry, known bool) mcpUnavailableTool {
	t := newMCPUnavailableTool(cfg, reason)
	if known && health.FailingSince != nil {
		t.retryAfter = mcpDiscoveryRetryAfter(time.Since(*health.FailingSince))
	}
	return t
}

func (t mcpUnavailableTool) Name() string {
	return sanitizeToolPrefix(t.integrationName) + "_unavailable"
}

func (t mcpUnavailableTool) Description() string {
	return fmt.Sprintf("The MCP integration %q is configured but currently UNREACHABLE (%s). "+
		"Its tools cannot be used right now — clearly tell the user this integration is unavailable "+
		"instead of attempting other tools.", t.integrationName, t.reason)
}

func (t mcpUnavailableTool) InputSchema() ToolSchema {
	return ToolSchema{Type: ToolSchemaTypeObject, Properties: map[string]ToolSchemaProperty{}}
}

func (t mcpUnavailableTool) GetType() NBToolType { return NBToolTypeTool }

func (t mcpUnavailableTool) Call(_ NbToolContext, _ NBToolCallRequest) (NBToolResponse, error) {
	j, _ := json.Marshal(map[string]any{
		"error":     fmt.Sprintf("MCP integration %q is currently unreachable", t.integrationName),
		"source":    "mcp_integration",
		"reason":    t.reason,
		"available": false,
	})
	return NBToolResponse{Status: NBToolResponseStatusError, Data: string(j), Type: NBToolResponseTypeText}, nil
}

// discoverMCPTools calls tools/list on an MCP server via relay and returns NBTools.
func discoverMCPTools(accountId string, cfg mcpIntegrationConfig) []NBTool {
	tools, _ := runMCPDiscovery(accountId, cfg, false)
	return tools
}

// runMCPDiscovery is discoverMCPTools that also returns the outcome it
// recorded. authoritative (Check now) skips the hold.
func runMCPDiscovery(accountId string, cfg mcpIntegrationConfig, authoritative bool) ([]NBTool, mcpOutcome) {
	log := slog.With("integration_id", cfg.IntegrationID, "integration_name", cfg.IntegrationName, "account_id", accountId)
	start := time.Now()
	defer func() {
		common.MetricsToolDiscoveryLatency(mcpImplType, accountId, cfg.IntegrationID, time.Since(start).Seconds())
	}()

	response, err := executeMCPViaRelay(context.Background(), accountId, cfg, "tools/list", nil, "", time.Duration(config.Config.LlmServerMCPDiscoveryTimeoutSeconds)*time.Second)
	if err != nil {
		toolCircuitBreakerInstance.RecordFailure(mcpDiscoveryImplType, CircuitBreakerKey{AccountId: accountId, InstanceId: cfg.IntegrationID})
		common.MetricsToolDiscovery(mcpImplType, accountId, cfg.IntegrationID, "failure")
		// Debug, as are the two failures below: the integration's health logs
		// once when it changes (#40994), not on every retry.
		log.Debug("mcp-integration: failed to discover tools", "error", err)
		o := classifyMCPOutcome(err, true)
		health, known := recordMCPHealth(accountId, cfg, o, authoritative)
		// Keep the integration visible (and consistently reported) instead of
		// silently vanishing, which makes the agent improvise. See A2 fix.
		return []NBTool{unavailableAfterFailure(cfg, classifyMCPError(err), health, known)}, o
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
				// Raw, and read by mcpReadOnlyHint: a server that sends it in
				// another shape must not fail the whole tool list.
				Annotations json.RawMessage `json:"annotations"`
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
		toolCircuitBreakerInstance.RecordFailure(mcpDiscoveryImplType, CircuitBreakerKey{AccountId: accountId, InstanceId: cfg.IntegrationID})
		common.MetricsToolDiscovery(mcpImplType, accountId, cfg.IntegrationID, "parse_error")
		log.Debug("mcp-integration: failed to parse tools/list response", "error", err, "response", truncate(response, 500))
		// The server answered, but with nothing usable: an error, not an outage.
		o := mcpFailure(mcpHealthError, "invalid tools/list response", true)
		health, known := recordMCPHealth(accountId, cfg, o, authoritative)
		return []NBTool{unavailableAfterFailure(cfg, "invalid_response", health, known)}, o
	}

	if rpcResp.Error != nil {
		toolCircuitBreakerInstance.RecordFailure(mcpDiscoveryImplType, CircuitBreakerKey{AccountId: accountId, InstanceId: cfg.IntegrationID})
		common.MetricsToolDiscovery(mcpImplType, accountId, cfg.IntegrationID, "json_rpc_error")
		log.Debug("mcp-integration: tools/list returned error", "code", rpcResp.Error.Code, "message", rpcResp.Error.Message)
		o := mcpFailure(mcpHealthError, fmt.Sprintf("tools/list returned error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message), true)
		health, known := recordMCPHealth(accountId, cfg, o, authoritative)
		return []NBTool{unavailableAfterFailure(cfg, "discovery_error", health, known)}, o
	}

	tools := make([]NBTool, 0, len(rpcResp.Result.Tools))

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
		toolName := MCPToolName(cfg.IntegrationName, t.Name)

		tools = append(tools, mcpIntegrationTool{
			config:       cfg,
			toolName:     toolName,
			mcpToolName:  t.Name,
			toolDesc:     t.Description,
			readOnlyHint: mcpReadOnlyHint(t.Annotations),
			toolSchema: ToolSchema{
				Type:       schemaType,
				Properties: props,
				Required:   t.InputSchema.Required,
			},
		})
	}

	discovered := make([]string, 0, len(rpcResp.Result.Tools))
	for _, t := range rpcResp.Result.Tools {
		discovered = append(discovered, t.Name)
	}
	warnMissingWriteTools(log, cfg.WriteTools, discovered)
	toolCircuitBreakerInstance.RecordSuccess(mcpDiscoveryImplType, CircuitBreakerKey{AccountId: accountId, InstanceId: cfg.IntegrationID})
	common.MetricsToolDiscovery(mcpImplType, accountId, cfg.IntegrationID, "success")
	o := mcpSuccess(true, len(tools))
	recordMCPHealth(accountId, cfg, o, authoritative)
	log.Info("mcp-integration: discovered tools", "count", len(tools))
	return tools, o
}

// MCPToolName is the name an MCP server's tool is offered under: the
// integration's name as a prefix, then the server's own tool name.
func MCPToolName(integrationName, serverTool string) string {
	return sanitizeToolPrefix(integrationName) + "_" + serverTool
}

// ParseToolNameList reads write_tools: tool names separated by commas, as the
// integration form saves list fields.
func ParseToolNameList(value string) []string {
	var names []string
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// warnMissingWriteTools logs write_tools entries the server no longer offers,
// e.g. after a rename, since the renamed tool would no longer ask.
func warnMissingWriteTools(log *slog.Logger, writeTools []string, discovered []string) {
	for _, name := range writeTools {
		if !slices.Contains(discovered, name) {
			log.Warn("mcp-integration: write tool not offered by the server; it will not ask for approval", "write_tool", name)
		}
	}
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
