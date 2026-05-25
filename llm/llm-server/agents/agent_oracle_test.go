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

func TestOracleDebugAgent_Execute(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" {
		t.Skip("integration test requires TEST_ACCOUNT and TEST_USER")
	}

	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-oracle-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me active sessions",
			},
		}
	for _, tc := range testCases {
		agent := OracleDebugAgent{accountId: tc.AccountId}

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
