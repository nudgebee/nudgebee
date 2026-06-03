//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatadogEventsAgent_Execute(t *testing.T) {
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
		{
			SessionId: "ut-datadog-events-agent-1",
			AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me all kubernetes events with normal priority in the last hour.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			eventsAgent := NewDatadogEventsAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, eventsAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, resp.AgentName, eventsAgent.GetName())
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}
