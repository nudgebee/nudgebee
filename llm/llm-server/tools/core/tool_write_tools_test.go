package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An MCP tool asks for approval only when its integration lists it in
// write_tools, matched by the server's own tool name (#40399).
func TestMCPWriteToolsClassifyListedToolsAsWrites(t *testing.T) {
	cfg := mcpIntegrationConfig{IntegrationID: "integration-1", IntegrationName: "Jira Prod", WriteTools: ParseToolNameList(" create_issue, ,delete_issue,create_issue")}
	require.Equal(t, []string{"create_issue", "delete_issue"}, cfg.WriteTools)

	write := mcpIntegrationTool{config: cfg, toolName: MCPToolName(cfg.IntegrationName, "create_issue"), mcpToolName: "create_issue"}
	requestType, err := write.InferToolRequestType(nil, write.Name(), "{}")
	require.NoError(t, err)
	assert.Equal(t, ToolRequestTypeUpdate, requestType)
	assert.Equal(t, "jira_prod_create_issue", write.Name())
	assert.Equal(t, "integration-1", write.IntegrationID())

	read := mcpIntegrationTool{config: cfg, toolName: MCPToolName(cfg.IntegrationName, "search_issues"), mcpToolName: "search_issues"}
	requestType, err = read.InferToolRequestType(nil, read.Name(), "{}")
	require.NoError(t, err)
	assert.Empty(t, requestType, "an unlisted tool stays unclassified and runs as before")
}

// A custom tool is a write only when its config says "read_only": false;
// absent or true keeps today's behaviour.
func TestCustomToolWritesOnlyWhenMarkedNotReadOnly(t *testing.T) {
	assert.True(t, CustomToolWrites(map[string]any{"read_only": false}))
	assert.False(t, CustomToolWrites(map[string]any{"read_only": true}))
	assert.False(t, CustomToolWrites(map[string]any{}))
	assert.False(t, CustomToolWrites(nil))
	assert.False(t, CustomToolWrites(map[string]any{"read_only": "false"}), "only a boolean counts")

	tool := nbCustomContainerTool{tool: ToolDto{Name: "deploy", Config: map[string]any{"image": "x", "read_only": false}}}
	requestType, err := tool.InferToolRequestType(nil, "deploy", "{}")
	require.NoError(t, err)
	assert.Equal(t, ToolRequestTypeUpdate, requestType)
}

type readOnlyProbe struct {
	ReadOnlyTool
	nbCustomContainerTool
}

type classifiedProbe struct{ NBTool }

func (classifiedProbe) GetType() NBToolType { return NBToolTypeTool }

func (classifiedProbe) InferToolRequestType(_ *security.RequestContext, _, _ string) (ToolRequestType, error) {
	return "", nil
}

type unclassifiedProbe struct{ NBTool }

func (unclassifiedProbe) GetType() NBToolType { return NBToolTypeTool }

// Tool lists show when a tool's calls ask for approval (#40399).
func TestApprovalModeForToolLists(t *testing.T) {
	marked := mcpIntegrationConfig{IntegrationID: "i", IntegrationName: "srv", WriteTools: []string{"create"}}
	assert.Equal(t, ToolApprovalAlways, ApprovalMode(mcpIntegrationTool{config: marked, mcpToolName: "create"}))
	assert.Equal(t, ToolApprovalNever, ApprovalMode(mcpIntegrationTool{config: marked, mcpToolName: "search"}))
	assert.Equal(t, ToolApprovalAlways, ApprovalMode(&mcpIntegrationTool{config: marked, mcpToolName: "create"}), "a pointer is read the same way")
	assert.Equal(t, ToolApprovalAlways, ApprovalMode(&nbCustomContainerTool{tool: ToolDto{Config: map[string]any{"read_only": false}}}))
	assert.Equal(t, ToolApprovalAlways, ApprovalMode(nbCustomContainerTool{tool: ToolDto{Config: map[string]any{"read_only": false}}}))
	assert.Equal(t, ToolApprovalNever, ApprovalMode(nbCustomContainerTool{tool: ToolDto{Config: map[string]any{}}}))
	assert.Equal(t, ToolApprovalNever, ApprovalMode(readOnlyProbe{}), "a read-only built-in never asks")
	assert.Equal(t, ToolApprovalAuto, ApprovalMode(classifiedProbe{}), "a classified built-in decides per call")
	assert.Equal(t, ToolApprovalNever, ApprovalMode(unclassifiedProbe{}), "unclassified calls run as reads")
	assert.Empty(t, ApprovalMode(nil))
}

// A custom tool can't take a built-in tool's or agent's name: the Tools list no
// longer carries agents, so the form can't catch it (#40399).
func TestIsSystemToolNameCoversToolsAgentsAndAliases(t *testing.T) {
	const name = "approval_test_builtin_agent"
	RegisterNBToolFactory(name, func(string) (NBTool, error) { return nil, nil })
	RegisterNBToolAlias("approval_test_alias", name)
	t.Cleanup(func() {
		delete(nbSystemTools, name)
		delete(toolAliases, "approval_test_alias")
		delete(reverseToolAliases, name)
	})

	assert.True(t, isSystemToolName("Approval_Test_Builtin_Agent"))
	assert.True(t, isSystemToolName("approval_test_alias"))
	assert.False(t, isSystemToolName("my_custom_tool"))
}
