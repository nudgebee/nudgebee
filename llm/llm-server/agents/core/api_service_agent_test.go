package core

import (
	"log/slog"
	"os"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

func TestListCustomAgents(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	assert.NotEmpty(t, accountId, "TEST_ACCOUNT environment variable must be set")

	slog.Info("Testing ListCustomAgents", "accountId", accountId)

	// This should NOT crash with UUID parsing error even if there are system agent extensions
	agents := ListCustomAgents(sc, accountId, false)

	assert.NotNil(t, agents, "ListCustomAgents should return non-nil slice")
	slog.Info("ListCustomAgents completed successfully", "count", len(agents))

	for _, agent := range agents {
		assert.NotEmpty(t, agent.Id, "Agent ID should not be empty")
		assert.NotEmpty(t, agent.Name, "Agent Name should not be empty")
		assert.Equal(t, AgentTypeCustom, agent.Type, "Agent type should be custom")
		slog.Info("Found custom agent", "name", agent.Name, "id", agent.Id, "status", agent.Status)
	}
}

func TestAgentAdditionalInstructionsAndToolsAndConfigs_WithSystemAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	assert.NotEmpty(t, accountId, "TEST_ACCOUNT environment variable must be set")

	systemAgentName := "promql_query"
	slog.Info("Testing AgentAdditionalInstructionsAndToolsAndConfigs with system agent", "accountId", accountId, "agentName", systemAgentName)

	// This should NOT crash with UUID parsing error when querying system agent
	prompt, tools, config := AgentAdditionalInstructionsAndToolsAndConfigs(sc, accountId, systemAgentName)

	// Assert function executed without panic
	slog.Info("AgentAdditionalInstructionsAndToolsAndConfigs completed successfully",
		"agentName", systemAgentName,
		"hasPrompt", prompt != "",
		"toolCount", len(tools),
		"hasConfig", config != nil)

	if prompt != "" {
		slog.Info("Found additional instructions", "prompt", prompt)
	}
	if len(tools) > 0 {
		slog.Info("Found custom tools", "tools", tools)
	}
}

func TestAgentAdditionalInstructionsAndToolsAndConfigs_WithCustomAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	assert.NotEmpty(t, accountId, "TEST_ACCOUNT environment variable must be set")

	slog.Info("Testing AgentAdditionalInstructionsAndToolsAndConfigs with custom agent", "accountId", accountId)

	// Get all custom agents
	agents := ListCustomAgents(sc, accountId, false)
	if len(agents) == 0 {
		t.Skip("No custom agents found for testing")
		return
	}

	// Test with first custom agent name
	customAgentName := agents[0].Name
	customAgentId := agents[0].Id

	slog.Info("Testing with custom agent by name", "agentName", customAgentName)
	prompt, tools, config := AgentAdditionalInstructionsAndToolsAndConfigs(sc, accountId, customAgentName)
	slog.Info("Query by name completed",
		"agentName", customAgentName,
		"hasPrompt", prompt != "",
		"toolCount", len(tools),
		"hasConfig", config != nil)

	// Test with agent UUID
	slog.Info("Testing with custom agent by UUID", "agentId", customAgentId)
	prompt2, tools2, config2 := AgentAdditionalInstructionsAndToolsAndConfigs(sc, accountId, customAgentId)
	slog.Info("Query by UUID completed",
		"agentId", customAgentId,
		"hasPrompt", prompt2 != "",
		"toolCount", len(tools2),
		"hasConfig", config2 != nil)

	// Assert both queries return the same results
	assert.Equal(t, prompt, prompt2, "Should return same prompt when queried by name vs UUID")
	assert.Equal(t, tools, tools2, "Should return same tools when queried by name vs UUID")
}

func TestGetCustomNbAgent_WithSystemAgentExtension(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	assert.NotEmpty(t, accountId, "TEST_ACCOUNT environment variable must be set")

	systemAgentName := "promql_query"
	slog.Info("Testing GetCustomNbAgent with system agent name", "accountId", accountId, "agentName", systemAgentName)

	// This should NOT crash with UUID parsing error when querying system agent name
	agent, found := GetCustomNbAgent(sc, accountId, systemAgentName, "")

	// Should not find a custom agent because promql_query is a system agent
	assert.False(t, found, "Should not find custom agent for system agent name 'promql_query'")
	assert.Nil(t, agent, "Agent should be nil when not found")
	slog.Info("GetCustomNbAgent completed successfully", "agentName", systemAgentName, "found", found)
}

func TestListCustomNbAgent_WithMixedAgents(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	assert.NotEmpty(t, accountId, "TEST_ACCOUNT environment variable must be set")

	slog.Info("Testing ListCustomNbAgent", "accountId", accountId)

	// This should NOT crash even with mixed agent types (system agent extensions + custom agents)
	agents := ListCustomNbAgent(sc, accountId, "")

	assert.NotNil(t, agents, "ListCustomNbAgent should return non-nil slice")
	slog.Info("ListCustomNbAgent completed successfully", "count", len(agents))

	for _, agent := range agents {
		assert.NotNil(t, agent, "Agent in list should not be nil")
		assert.NotEmpty(t, agent.GetName(), "Agent name should not be empty")
		slog.Info("Found custom nb agent",
			"name", agent.GetName(),
			"plannerType", agent.GetPlannerType())
	}
}

func TestCreateAgentExtension_ForSystemAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	assert.NotEmpty(t, accountId, "TEST_ACCOUNT environment variable must be set")
	user := os.Getenv("TEST_USER")

	testAgentName := "promql_query_test"
	slog.Info("Testing CreateAgentExtension for system agent", "accountId", accountId, "agentName", testAgentName)

	// Create extension for system agent
	extension := AgentExtension{
		AgentName: testAgentName,
		Prompt:    "Custom instructions for promql test",
		Tools:     []string{"tool1", "tool2"},
	}

	result, err := CreateAgentExtension(sc, accountId, extension, user)
	assert.NoError(t, err, "Should create agent extension without error")
	assert.Equal(t, testAgentName, result.AgentName, "Returned agent name should match")
	slog.Info("CreateAgentExtension completed successfully", "agentName", result.AgentName)

	// Verify it was inserted correctly with agent_id as TEXT (not UUID)
	dbms, _ := common.GetDatabaseManager(common.Metastore)
	var agentId string
	err = dbms.Db.Get(&agentId, "SELECT agent_id FROM llm_agents_installation WHERE account_id = $1 AND agent_id = $2", accountId, testAgentName)
	assert.NoError(t, err, "Should find the inserted extension")
	assert.Equal(t, testAgentName, agentId, "agent_id column should contain the agent name (not UUID)")
	slog.Info("Verified agent_id is stored as text", "agent_id", agentId)

	// Now test that all queries handle this correctly and don't crash with UUID parsing error
	slog.Info("Testing AgentAdditionalInstructionsAndToolsAndConfigs after extension creation")
	prompt, tools, config := AgentAdditionalInstructionsAndToolsAndConfigs(sc, accountId, testAgentName)
	assert.Equal(t, "Custom instructions for promql test", prompt, "Should return the custom prompt")
	assert.Equal(t, 2, len(tools), "Should return 2 tools")
	assert.Contains(t, tools, "tool1", "Should contain tool1")
	assert.Contains(t, tools, "tool2", "Should contain tool2")
	slog.Info("Extension queried successfully", "prompt", prompt, "tools", tools, "hasConfig", config != nil)

	// Test ListCustomAgents doesn't crash with system agent extension present
	slog.Info("Testing ListCustomAgents with system agent extension present")
	agents := ListCustomAgents(sc, accountId, false)
	assert.NotNil(t, agents, "ListCustomAgents should not crash with system agent extension")
	slog.Info("ListCustomAgents completed successfully after creating extension", "count", len(agents))

	// Test ListCustomNbAgent doesn't crash
	slog.Info("Testing ListCustomNbAgent with system agent extension present")
	nbAgents := ListCustomNbAgent(sc, accountId, "")
	assert.NotNil(t, nbAgents, "ListCustomNbAgent should not crash with system agent extension")
	slog.Info("ListCustomNbAgent completed successfully", "count", len(nbAgents))

	// Cleanup
	slog.Info("Cleaning up test extension", "agentName", testAgentName)
	err = DeleteAgentExtension(sc, accountId, testAgentName)
	if err != nil {
		slog.Warn("Failed to cleanup test extension", "error", err)
	} else {
		slog.Info("Test extension cleaned up successfully")
	}
}
