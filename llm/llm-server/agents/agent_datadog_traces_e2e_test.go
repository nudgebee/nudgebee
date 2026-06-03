//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatadogTracesAgentExecute(t *testing.T) {
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
			SessionId: "ut-datadog-traces-agent-1",
			AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me error traces for deployment services-server in nudgebee namespace",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			tracesAgent := NewDatadogTracesAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, resp.AgentName, tracesAgent.GetName())
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

func TestDatadogTracesAgentExecute2(t *testing.T) {
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
			SessionId: "ut-datadog-traces-agent-2",
			AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     "Show me traces for service app in namespace nudgebee with high latency in the last hour",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			tracesAgent := NewDatadogTracesAgent(tc.AccountId)
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, tracesAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, resp.AgentName, tracesAgent.GetName())
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}
