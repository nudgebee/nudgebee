//go:build e2e

package signoz

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSignozAgentExecute(t *testing.T) {
	accountID := os.Getenv("TEST_SIGNOZ_ACCOUNT")
	if accountID == "" {
		t.Skip("skipping: TEST_SIGNOZ_ACCOUNT not set (configure a Signoz-integration account UUID in .env to exercise this test)")
	}
	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-signoz-agent-1",
			AccountId: accountID,
			UserId:    os.Getenv("TEST_USER"),
			Query:     "get me recent logs of llm-server in nudgebee namespace",
		},
	}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		logAgent := NewSignozLogAgent(tc.AccountId)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestSignozAgentExecute2(t *testing.T) {
	accountID := os.Getenv("TEST_SIGNOZ_ACCOUNT")
	if accountID == "" {
		t.Skip("skipping: TEST_SIGNOZ_ACCOUNT not set")
	}
	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "ut-signoz-agent-2",
			AccountId: accountID,
			UserId:    os.Getenv("TEST_USER"),
			Query:     "get me recent logs",
		},
	}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})

		logAgent := NewSignozLogAgent(tc.AccountId)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}
