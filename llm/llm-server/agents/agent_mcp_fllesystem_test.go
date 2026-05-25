package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMCPAgent(t *testing.T) {
	// --- Test Setup ---
	testAccountId := os.Getenv("TEST_ACCOUNT")
	if testAccountId == "" {
		t.Skip("TEST_ACCOUNT environment variable not set, skipping test")
	}
	testTenantId := os.Getenv("TEST_TENANT")
	if testTenantId == "" {
		t.Skip("TEST_TENANT environment variable not set, skipping test")
	}
	testUserId := os.Getenv("TEST_USER")
	if testUserId == "" {
		t.Skip("TEST_USER environment variable not set, skipping test")
	}

	sc := security.NewRequestContextForTenantAccountAdmin(testTenantId, testUserId, []string{testAccountId})
	agentName := "mcp_test_agent"
	mcpToolName := "mcp_test_file_system_tool"

	// --- Create the Custom MCP Tool ---
	// (Ensure this tool is cleaned up afterwards if necessary)
	mcpToolDto := toolcore.ToolDto{
		Name:         mcpToolName,
		Description:  "Test MCP tool for filesystem operations",
		Type:         toolcore.ToolTypeCustom,
		NBToolType:   toolcore.NBToolTypeTool, // Mark as a multi-command tool implicitly via MCP
		ExecutorType: toolcore.ToolExecutorTypeMCP,
		Config: map[string]any{
			toolcore.ToolCustomMcpServerType:       toolcore.ToolCustomMcpServerTypeCli,
			toolcore.ToolCustomMcpServerCliCommand: "npx",
			toolcore.ToolCustomMcpServerCliArgs:    []string{"-y", "@modelcontextprotocol/server-filesystem@latest", "./"},
		},
		Status:    toolcore.ToolStatusEnabled,
		CreatedBy: testUserId,
	}
	//delete if tool already exists
	err := toolcore.DeleteCustomTool(sc, testAccountId, mcpToolName)
	if err != nil {
		t.Logf("Manual cleanup needed for tool: %s", mcpToolName)
		t.SkipNow()
	}

	createdTool, err := toolcore.CreateCustomTool(sc, testAccountId, mcpToolDto)
	assert.NoError(t, err, "Failed to create custom MCP tool")
	if err != nil {
		t.FailNow()
	}
	t.Logf("Created custom MCP tool: %s (ID: %s)", createdTool.Name, createdTool.Id)

	// Cleanup the tool after the test
	defer func() {
		delErr := toolcore.DeleteCustomTool(sc, testAccountId, mcpToolName)
		assert.NoError(t, delErr, "Failed to delete custom MCP tool")
		if delErr != nil {
			t.Logf("Manual cleanup needed for tool: %s", mcpToolName)
		} else {
			t.Logf("Cleaned up custom MCP tool: %s", mcpToolName)
		}
	}()

	agentDto := core.AgentDto{
		Name:         agentName,
		Description:  "Test agent using the file_system MCP tool",
		Type:         core.AgentTypeCustom,
		Status:       core.AgentStatusEnabled,
		SystemPrompt: `{"role": "You are an agent that uses the file_system tool."}`,
		ExecutorType: core.AgentPlannerTypeReAct,
		Tools:        []string{mcpToolName},
		CreatedBy:    testUserId,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// cleanup existing agent
	err = core.DeleteCustomAgent(sc, testAccountId, agentName)
	if err != nil {
		t.Logf("Manual cleanup needed for agent: %s", agentName)
		t.SkipNow()
	}

	createdAgent, err := core.CreateCustomAgent(sc, testAccountId, agentDto, nil, false)
	assert.NoError(t, err, "Failed to create custom agent")
	if err != nil {
		t.FailNow()
	}
	t.Logf("Created custom agent: %s (ID: %s)", createdAgent.Name, createdAgent.Id)

	// Cleanup the agent after the test
	defer func() {
		delErr := core.DeleteCustomAgent(sc, testAccountId, agentName)
		assert.NoError(t, delErr, "Failed to delete custom agent")
		if delErr != nil {
			t.Logf("Manual cleanup needed for agent: %s", agentName)
		} else {
			t.Logf("Cleaned up custom agent: %s", agentName)
		}
	}()

	// --- Get the Agent Instance ---
	// Allow some time for potential registration/propagation if needed
	time.Sleep(1 * time.Second)

	nbAgent, found := core.GetNBAgent(sc, agentName, testAccountId, core.AgentStatusEnabled)
	assert.True(t, found, "Could not find the created custom agent")
	assert.NotNil(t, nbAgent, "Retrieved agent instance is nil")
	if !found {
		t.FailNow()
	}

	// --- Execute the Agent ---
	sessionId := "test-mcp-agent-1"
	query := "list the files in the current directory"

	// Ensure conversation is clean before starting
	err = core.DeleteConversationBySession(sessionId, testAccountId, testUserId)
	assert.NoError(t, err, "Failed to delete previous conversation session")

	t.Logf("Executing agent '%s' with query: '%s'", agentName, query)
	resp, err := core.HandleConversationSessionRequest(sc, nbAgent, testUserId, testAccountId, sessionId, query)

	// --- Assert Results ---
	assert.NoError(t, err, "Agent execution failed")
	assert.NotNil(t, resp, "Agent response is nil")

	t.Logf("Agent Response Status: %s", resp.Status)
	t.Logf("Agent Response Content: %s", resp.Response)
	t.Logf("Agent Steps: %+v", resp.AgentStepResponse)

	assert.Equal(t, agentName, resp.AgentName, "Response agent name mismatch")
	assert.NotEmpty(t, resp.Response, "Agent response content is empty")

	assert.Contains(t, []core.ConversationStatus{core.ConversationStatusCompleted, core.ConversationStatusWaiting}, resp.Status, "Agent conversation status is not Completed or Waiting")

	// Verify that the MCP tool was called
	toolCalled := false
	for _, step := range resp.AgentStepResponse {
		if step.Call.FunctionCall != nil && step.Call.FunctionCall.Name == mcpToolName {
			toolCalled = true
			assert.Contains(t, step.Call.FunctionCall.Arguments, `"command":"list_directory"`, "Expected list_directory command in tool arguments")
			assert.NotEmpty(t, step.Response.Content, "Tool response content is empty")
			t.Logf("MCP Tool '%s' called with args: %s", mcpToolName, step.Call.FunctionCall.Arguments)
			t.Logf("MCP Tool '%s' response: %s", mcpToolName, step.Response.Content)
			break
		}
	}
	assert.True(t, toolCalled, "The custom MCP tool '%s' was not called by the agent", mcpToolName)

}
