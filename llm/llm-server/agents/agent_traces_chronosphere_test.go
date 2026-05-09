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
func TestTracesAgentChronosphere_ExecutePartialResource(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	tracesAgent, err := getTracesAgent(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-chronosphere-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you get me recent traces of event-service service",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation))
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, tracesAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestTracesAgentChronosphere_Execute(t *testing.T) {
	tracesAgent := TracesChronosphereAgent{accountId: os.Getenv("TEST_ACCOUNT")}
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-chronosphere-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What are latest failure on services-server in nudgebee namespace?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, tracesAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestTracesAgentChronosphere_K8sDebug(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	// Get me recent traces where savings is more than 1$
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-traces-chronosphere-chain-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "can you analyse k8s-collector traces in nudgebee namespace for last 24 hours?",
			},
		}
	for _, tc := range testCases {
		debugAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, debugAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		toolNames := []string{}

		for _, toolInvocation := range resp.AgentStepResponse {
			toolNames = append(toolNames, toolInvocation.Call.FunctionCall.Name)
		}
		assert.Contains(t, toolNames, TracesAgentName)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, debugAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}
