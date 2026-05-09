package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogChain_ExecuteKubectl(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-logs-kubectl-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me recent logs of deployment llm-server in nudgebee namespace",
			},
		}
	for _, tc := range testCases {
		logAgent := newKubectlLogAgent(tc.AccountId)

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

func TestLogChain_ExecuteKubectl2(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-logs-kubectl-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me recent logs of nginx",
			},
		}
	for _, tc := range testCases {
		logAgent := newKubectlLogAgent(tc.AccountId)

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
