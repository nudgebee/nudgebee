//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestAzureAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-azure-agent-1",
			AccountId: os.Getenv("TEST_AZURE_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "get me all resource groups",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			azureAgent := newAzureAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.NoError(t, err, "Cleanup failed for session %s", tc.SessionId)

			resp, err := core.HandleConversationSessionRequest(sc, azureAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.NoError(t, err, "Failed to handle conversation session")
			assert.NotNil(t, resp, "Response should not be nil")

			messageId, err := uuid.Parse(resp.MessageId)
			assert.Nil(t, err)

			agentId, err := uuid.Parse(resp.AgentId)
			assert.Nil(t, err)
			if resp.Status == core.ConversationStatusWaiting {
				resp, err = core.HandleConversationSessionRequest(sc, azureAgent, tc.UserId, tc.AccountId, tc.SessionId, "azure-dev", core.ConversationSessionRequestWithMessageId(uuid.NullUUID{
					UUID:  messageId,
					Valid: true,
				}), core.ConversationSessionRequestWithAgentId(uuid.NullUUID{
					UUID:  agentId,
					Valid: true,
				}))
				assert.Nil(t, err)
				assert.NotNil(t, resp)
			}

			assert.Equal(t, resp.AgentName, azureAgent.GetName(), "Agent name mismatch")

			assert.NotEmpty(t, resp.Query, "Query should not be empty")

			assert.NotNil(t, resp.AgentStepResponse, "Agent step response should not be nil")

			assert.Greater(t, len(resp.Response), 0, "Response should not be empty")
		})
	}
}
