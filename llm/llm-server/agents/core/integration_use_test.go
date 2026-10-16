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
func TestCallNbToolChecksIntegrationUseInAGrantScopedRun(t *testing.T) {
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

	// Anyone else keeps today's rules.
	person := grantedContext(t, nil)
	tool = &mcpCapturingTool{}
	callAs(t, person, tool, "")
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

	// A custom-role person keeps the built-in role check.
	person := grantedContext(t, map[string]bool{"k8s:Execute": true})
	assert.False(t, mayWrite(person, kubectl, "acct-a"))

	// No security context: nothing may be written.
	assert.False(t, mayWrite(nil, kubectl, "acct-a"))
	assert.False(t, mayWrite(security.NewRequestContext(t.Context(), nil, nil, nil, nil), kubectl, "acct-a"))
}
