package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"os"
	"testing"

	"github.com/google/uuid"
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

func TestMSSQLDebugAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-mssql-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me connection status",
			},
		}
	for _, tc := range testCases {
		agent := MSSQLDebugAgent{accountId: tc.AccountId}

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}
