//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatadogContainersAgent_Execute(t *testing.T) {
	if os.Getenv("TEST_DATADOG_ACCOUNT") == "" {
		t.Skip("skipping: TEST_DATADOG_ACCOUNT not set")
	}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		// {
		// 	SessionId: "ut-datadog-containers-agent-1",
		// 	AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "Get me all pods in namespace 'default'.",
		// },
		// {
		// 	SessionId: "ut-datadog-containers-agent-2",
		// 	AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
		// 	UserId:    os.Getenv("TEST_USER"),
		// 	Query:     "List all running containers for deployment 'app-dev' in namespace 'nudgebee'",
		// },
		{
			SessionId: "ut-datadog-containers-agent-3",
			AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me all containers, grouped by image name.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			containersAgent := NewDatadogContainersAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, containersAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)

			assert.Equal(t, containersAgent.GetName(), resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			// The agent uses ReAct and should have 2 steps: query generation and execution.
			assert.Equal(t, 2, len(resp.AgentStepResponse))
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}
