package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOracleDebugAgent_GetName(t *testing.T) {
	agent := OracleDebugAgent{accountId: "test-account"}
	assert.Equal(t, OracleAgentName, agent.GetName())
}

func TestOracleDebugAgent_GetNameAliases(t *testing.T) {
	agent := OracleDebugAgent{accountId: "test-account"}
	aliases := agent.GetNameAliases()
	assert.Contains(t, aliases, "Oracle")
	assert.Contains(t, aliases, "OracleDb")
	assert.Contains(t, aliases, "OracleDatabase")
}

func TestOracleDebugAgent_GetDescription(t *testing.T) {
	agent := OracleDebugAgent{accountId: "test-account"}
	desc := agent.GetDescription()
	assert.Contains(t, desc, "Oracle Database")
	assert.Contains(t, desc, "Oracle SQL")
}

func TestOracleDebugAgent_GetSupportedTools(t *testing.T) {
	agent := OracleDebugAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	supportedTools := agent.GetSupportedTools(sc)
	assert.Len(t, supportedTools, 1)
	assert.IsType(t, tools.OracleExecuteTool{}, supportedTools[0])
	assert.Equal(t, tools.ToolExecuteOracleQuery, supportedTools[0].Name())
}

func TestOracleDebugAgent_GetPlannerType(t *testing.T) {
	agent := OracleDebugAgent{accountId: "test-account"}
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
}

func TestOracleDebugAgent_GetSystemPrompt(t *testing.T) {
	agent := OracleDebugAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "show me active sessions",
		AccountId: "test-account",
	})

	assert.Equal(t, "an Oracle Database expert and troubleshooter", prompt.Role)
	assert.NotEmpty(t, prompt.Instructions)
	assert.NotEmpty(t, prompt.Constraints)
	assert.NotEmpty(t, prompt.ToolUsage)
	assert.Contains(t, prompt.ToolUsage, tools.ToolExecuteOracleQuery)
	assert.NotEmpty(t, prompt.Examples)
}
