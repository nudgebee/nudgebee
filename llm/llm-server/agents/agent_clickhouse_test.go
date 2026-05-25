package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestClickhouseDebugAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-clickhouse-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "show me connection status",
			},
		}
	for _, tc := range testCases {
		postgresChain := newClickhouseAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, postgresChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		messageId, err := uuid.Parse(resp.MessageId)
		assert.Nil(t, err)

		agentId, err := uuid.Parse(resp.AgentId)
		assert.Nil(t, err)
		if resp.Status == core.ConversationStatusWaiting {
			resp, err = core.HandleConversationSessionRequest(sc, postgresChain, tc.UserId, tc.AccountId, tc.SessionId, "dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
				UUID:  messageId,
				Valid: true,
			}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
				UUID:  agentId,
				Valid: true,
			}))
			assert.Nil(t, err)
			assert.NotNil(t, resp)
		}

		assert.Equal(t, resp.AgentName, postgresChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
