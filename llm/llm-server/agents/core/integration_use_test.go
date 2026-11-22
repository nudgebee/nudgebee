package core

import (
	"encoding/json"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mcpCapturingTool is an MCP integration tool that records whether it ran.
type mcpCapturingTool struct {
	capturingTool
	readOnly bool
	ran      bool
}

func (*mcpCapturingTool) GetImplType() string     { return toolcore.ToolImplTypeMCP }
func (m *mcpCapturingTool) MCPReadOnlyHint() bool { return m.readOnly }
func (m *mcpCapturingTool) Call(ctx toolcore.NbToolContext, req toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	m.ran = true
	return m.capturingTool.Call(ctx, req)
}

// kubectlLike is a cluster tool named like kubectl.
type kubectlLike struct{ capturingTool }

func (kubectlLike) Name() string { return "kubectl_execute" }

// grantedContext is a context in tenant t (accounts acct-a, acct-b) whose
// principal holds only grants on acct-a.
func grantedContext(t *testing.T, grants map[string]bool) *security.RequestContext {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"TenantId": "t", "UserId": "u", "AccountIds": []string{"acct-a", "acct-b"},
		"ScopedCustomPermissions": map[string]any{"acct-a": grants},
	})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	return security.NewRequestContext(t.Context(), &sc, nil, nil, nil)
}

func callAs(t *testing.T, ctx *security.RequestContext, tool toolcore.NBTool, requestType toolcore.ToolRequestType) toolcore.NBToolResponse {
	t.Helper()
	resp, err := callNbTool(ctx, NBAgentRequest{AccountId: "acct-a", UserId: "u", ConversationId: "conversation", MessageId: "message", AgentId: "agent"}, "bee", tool, `{"command":"search"}`, nil, "", "tool-call-1", "", requestType)
	require.NoError(t, err)
	return resp
}

// In a run that acts only through its grants, an integration is used only
// with a grant for its type, on the integration the call resolved.
func TestCallNbToolChecksIntegrationUseInAGrantCheckedRun(t *testing.T) {
	bee := security.WithGrantScopedRun(grantedContext(t, map[string]bool{"k8s:Read": true}))
	tool := &mcpCapturingTool{}
	resp := callAs(t, bee, tool, toolcore.ToolRequestTypeRead)
	assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
	assert.Contains(t, resp.Data, "integrations.mcp:Read on account acct-a")
	assert.False(t, tool.ran, "refused before the call")

	bee = security.WithGrantScopedRun(grantedContext(t, map[string]bool{"integrations.mcp:Read": true}))
	tool = &mcpCapturingTool{}
	callAs(t, bee, tool, toolcore.ToolRequestTypeRead)
	assert.True(t, tool.ran)

	// A call nothing classified is a change, unless the server marks the tool read-only.
	tool = &mcpCapturingTool{}
	resp = callAs(t, bee, tool, "")
	assert.Contains(t, resp.Data, "integrations.mcp:Execute")
	assert.False(t, tool.ran)
	tool = &mcpCapturingTool{readOnly: true}
	callAs(t, bee, tool, "")
	assert.True(t, tool.ran)

	// A person without a built-in role on the account is checked the same way.
	person := grantedContext(t, nil)
	tool = &mcpCapturingTool{}
	resp = callAs(t, person, tool, toolcore.ToolRequestTypeRead)
	assert.Contains(t, resp.Data, "integrations.mcp:Read on account acct-a")
	assert.False(t, tool.ran, "a person needs the integration's use grant too")
	tool = &mcpCapturingTool{}
	callAs(t, grantedContext(t, map[string]bool{"integrations.mcp:Read": true}), tool, toolcore.ToolRequestTypeRead)
	assert.True(t, tool.ran)

	// A built-in role on the account keeps today's rules.
	tool = &mcpCapturingTool{}
	callAs(t, builtInContext(t, "account_admin"), tool, "")
	assert.True(t, tool.ran)
}

func TestMayWrite(t *testing.T) {
	kubectl := &kubectlLike{}
	mcp := &mcpCapturingTool{}
	other := &capturingTool{}

	bee := security.WithGrantScopedRun(grantedContext(t, map[string]bool{"k8s:Read": true}))
	assert.False(t, mayWrite(bee, kubectl, "acct-a"), "k8s:Read does not change a cluster")
	assert.True(t, mayWrite(bee, mcp, "acct-a"), "an integration is checked where the call resolves it")
	assert.False(t, mayWrite(bee, other, "acct-a"), "a tool with no module is refused")

	bee = security.WithGrantScopedRun(grantedContext(t, map[string]bool{"k8s:Execute": true}))
	assert.True(t, mayWrite(bee, kubectl, "acct-a"))
	assert.False(t, mayWrite(bee, kubectl, "acct-b"))

	// A person whose access comes from custom roles writes through the same grant.
	person := grantedContext(t, map[string]bool{"k8s:Execute": true})
	assert.True(t, mayWrite(person, kubectl, "acct-a"))
	assert.False(t, mayWrite(person, kubectl, "acct-b"), "no grant on acct-b")
	assert.False(t, mayWrite(person, other, "acct-a"), "a tool with no module is refused")
	assert.True(t, mayWrite(person, mcp, "acct-a"), "left to the integration check where the call resolves it")
	assert.False(t, mayWrite(grantedContext(t, map[string]bool{"k8s:Write": true}), kubectl, "acct-a"), "Write does not run changes")

	// A built-in role keeps the built-in check.
	admin := builtInContext(t, "account_admin")
	assert.True(t, mayWrite(admin, kubectl, "acct-a"))
	assert.True(t, mayWrite(admin, other, "acct-a"))
	readOnly := builtInContext(t, "account_admin_readonly")
	assert.False(t, mayWrite(readOnly, kubectl, "acct-a"))
	assert.False(t, mayWrite(readOnly, mcp, "acct-a"), "the integration check does not run for a built-in role, so it may not be deferred to")

	// No security context: nothing may be written.
	assert.False(t, mayWrite(nil, kubectl, "acct-a"))
	assert.False(t, mayWrite(security.NewRequestContext(t.Context(), nil, nil, nil, nil), kubectl, "acct-a"))
}

// builtInContext is a request context whose principal holds role scoped to acct-a.
func builtInContext(t *testing.T, role string) *security.RequestContext {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"TenantId": "t", "UserId": "u", "AccountIds": []string{"acct-a", "acct-b"},
		"Roles": []string{role}, "ScopedEntityIds": map[string]any{role: []string{"acct-a"}},
	})
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal(raw, &sc))
	return security.NewRequestContext(t.Context(), &sc, nil, nil, nil)
}

// namedTool is capturingTool under a chosen registered name.
type namedTool struct {
	capturingTool
	name string
}

func (n *namedTool) Name() string { return n.name }

// A person whose access to the account comes from custom roles reads through a
// tool only with Read on every module of its scope; the refusal names the grant.
func TestReadGrantMissing(t *testing.T) {
	listEvents := &namedTool{name: "list_events"}
	datadogLogs := &namedTool{name: "datadog_log_execute"}
	think := &namedTool{name: "think"}
	unknown := &namedTool{name: "custom_container_tool"}

	person := grantedContext(t, map[string]bool{"k8s:Read": true})
	assert.Equal(t, "events:Read", readGrantMissing(person, listEvents, "acct-a"))
	assert.Equal(t, "", readGrantMissing(person, &kubectlLike{}, "acct-a"), "kubectl reads through k8s:Read")
	assert.Equal(t, "", readGrantMissing(person, think, "acct-a"), "unscoped tools read no account data")
	assert.Equal(t, "a built-in role", readGrantMissing(person, unknown, "acct-a"), "a tool with no scope needs a built-in role")
	assert.Equal(t, "", readGrantMissing(person, &mcpCapturingTool{}, "acct-a"), "an integration is checked where the call resolves it")
	assert.Equal(t, "logs:Read", readGrantMissing(person, datadogLogs, "acct-a"), "a provider's logs also need logs:Read")
	assert.Equal(t, "events:Read", readGrantMissing(person, listEvents, "acct-b"), "no grant on acct-b")

	withEvents := grantedContext(t, map[string]bool{"k8s:Read": true, "events:Read": true})
	assert.Equal(t, "", readGrantMissing(withEvents, listEvents, "acct-a"))
	writer := grantedContext(t, map[string]bool{"events:Write": true})
	assert.Equal(t, "", readGrantMissing(writer, listEvents, "acct-a"), "Write implies Read")

	// A built-in role on the account and a bee's run keep their own checks.
	assert.Equal(t, "", readGrantMissing(builtInContext(t, "account_admin_readonly"), unknown, "acct-a"))
	assert.Equal(t, "", readGrantMissing(security.WithGrantScopedRun(person), listEvents, "acct-a"))
}

// The model is shown only the tools a grants-only person may read through; the
// input list (the account's cached list) is never changed, and a built-in role
// sees every tool.
func TestToolsForPrompt(t *testing.T) {
	listEvents := &namedTool{name: "list_events"}
	think := &namedTool{name: "think"}
	kubectl := &kubectlLike{}
	all := []toolcore.NBTool{kubectl, listEvents, think}
	before := append([]toolcore.NBTool(nil), all...)

	person := grantedContext(t, map[string]bool{"k8s:Read": true})
	got := ToolsForPrompt(person, "acct-a", all)
	assert.Equal(t, []toolcore.NBTool{kubectl, think}, got)
	assert.Equal(t, before, all, "the cached list is not filtered in place")

	assert.Equal(t, all, ToolsForPrompt(builtInContext(t, "account_admin"), "acct-a", all))
	assert.Equal(t, all, ToolsForPrompt(security.WithGrantScopedRun(person), "acct-a", all), "a bee's run keeps its own gates")
}

// Review follow-ups: the shared workspace, integration use grants, and reads
// that resolve to another account.
func TestReadGrantMissingEdges(t *testing.T) {
	// Workspace readers see files other people's conversations saved there.
	shell := &namedTool{name: "shell_execute"}
	person := grantedContext(t, map[string]bool{"metrics:Read": true})
	assert.Equal(t, "a built-in role", readGrantMissing(person, shell, "acct-a"))
	assert.Equal(t, "", readGrantMissing(builtInContext(t, "account_admin_readonly"), shell, "acct-a"))

	// An integration module follows the use-grant rule: Write manages it, it does not use it.
	vm := &namedTool{name: "vm_execute"}
	manager := grantedContext(t, map[string]bool{"cloud:Read": true, "integrations.ssh:Write": true})
	assert.Equal(t, "integrations.ssh:Read", readGrantMissing(manager, vm, "acct-a"))
	user := grantedContext(t, map[string]bool{"cloud:Read": true, "integrations.ssh:Execute": true})
	assert.Equal(t, "", readGrantMissing(user, vm, "acct-a"))

	// A read that resolves to another account needs the grant there too.
	prom := &namedTool{name: "prometheus_execute"}
	onlyA := grantedContext(t, map[string]bool{"metrics:Read": true})
	assert.Equal(t, "", TargetReadGrantMissing(onlyA, prom, "acct-a", ""), "no target: the dispatch check covered it")
	assert.Equal(t, "", TargetReadGrantMissing(onlyA, prom, "acct-a", "acct-a"))
	assert.Equal(t, "metrics:Read", TargetReadGrantMissing(onlyA, prom, "acct-a", "acct-b"))
}
