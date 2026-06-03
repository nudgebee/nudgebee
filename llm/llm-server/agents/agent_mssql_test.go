package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMSSQLDebugAgent_GetName(t *testing.T) {
	agent := MSSQLDebugAgent{accountId: "test-account"}
	assert.Equal(t, MSSQLAgentName, agent.GetName())
}

func TestMSSQLDebugAgent_GetNameAliases(t *testing.T) {
	agent := MSSQLDebugAgent{accountId: "test-account"}
	aliases := agent.GetNameAliases()
	assert.Contains(t, aliases, "MsSql")
	assert.Contains(t, aliases, "SqlServer")
}

func TestMSSQLDebugAgent_GetDescription(t *testing.T) {
	agent := MSSQLDebugAgent{accountId: "test-account"}
	desc := agent.GetDescription()
	assert.Contains(t, desc, "Microsoft SQL Server")
	assert.Contains(t, desc, "T-SQL")
}

func TestMSSQLDebugAgent_GetSupportedTools(t *testing.T) {
	agent := MSSQLDebugAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	supportedTools := agent.GetSupportedTools(sc)
	assert.Len(t, supportedTools, 1)
	assert.IsType(t, tools.MSSQLExecuteTool{}, supportedTools[0])
	assert.Equal(t, tools.ToolExecuteMSSQLQuery, supportedTools[0].Name())
}

func TestMSSQLDebugAgent_GetPlannerType(t *testing.T) {
	agent := MSSQLDebugAgent{accountId: "test-account"}
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
}

func TestMSSQLDebugAgent_GetSystemPrompt(t *testing.T) {
	agent := MSSQLDebugAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{
		Query:     "show me active connections",
		AccountId: "test-account",
	})

	assert.Equal(t, "a Microsoft SQL Server database expert and troubleshooter", prompt.Role)
	assert.NotEmpty(t, prompt.Instructions)
	assert.NotEmpty(t, prompt.Constraints)
	assert.NotEmpty(t, prompt.ToolUsage)
	assert.Contains(t, prompt.ToolUsage, tools.ToolExecuteMSSQLQuery)
	assert.NotEmpty(t, prompt.Examples)
	assert.Equal(t, "mssql", prompt.Rag.Module)
}
