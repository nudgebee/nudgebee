package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestConversationalAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-conversational-chain-1",
				AccountId: accountId,
				UserId:    userId,
				Query:     "what is my current cluster mem usage?",
			},
			{
				SessionId: "ut-conversational-chain-1",
				AccountId: accountId,
				UserId:    userId,
				Query:     "Provide the ans in human readable format",
			},
		}
	for _, tc := range testCases {
		k8sDebugAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sDebugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sDebugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestConversationalAgentMultiMessages(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	sessionId := "ut-conversational-chain-2"
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    userId,
				Query:     "Can you show pods in Nudgebee namespace",
			},
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    "5b195690-b393-4937-ab07-690a751f40c5",
				Query:     "Can you show recent restarted pods in nudgebee namespace",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(accountId)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestConversationalAgentMultiMessagesWithPreviousContext(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_CONVERSATIONAL_AGENT_ACCOUNT")
	userId := os.Getenv("TEST_CONVERSATIONAL_AGENT_USER")
	sessionId := "ut-conversational-chain-3"
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    userId,
				Query:     "Can you show memory usage of app-dev in nudgebee namespace",
			},
			{
				SessionId: sessionId,
				AccountId: accountId,
				UserId:    "5b195690-b393-4937-ab07-690a751f40c5",
				Query:     "Can you show logs as well",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	for _, tc := range testCases {
		k8sDebug := newK8sDebugAgent(tc.AccountId)

		resp, err := core.HandleConversationSessionRequest(sc, k8sDebug, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, k8sDebug.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
