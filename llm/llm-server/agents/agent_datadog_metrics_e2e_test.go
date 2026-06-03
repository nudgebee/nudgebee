//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDatadogMetricsAgent_Execute0(t *testing.T) {
	if os.Getenv("TEST_DATADOG_ACCOUNT") == "" {
		t.Skip("skipping: TEST_DATADOG_ACCOUNT not set")
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
				SessionId: "ut-datadog-metrics-chain-0",
				AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get me memory usage of kube-system namespace in last 10 days",
			},
		}
	for _, tc := range testCases {
		agent := NewDatadogMetricsAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.GreaterOrEqual(t, len(resp.AgentStepResponse), 1)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestDatadogMetricsAgent_Execute1(t *testing.T) {
	if os.Getenv("TEST_DATADOG_ACCOUNT") == "" {
		t.Skip("skipping: TEST_DATADOG_ACCOUNT not set")
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
				SessionId: "ut-datadog-metrics-chain-1",
				AccountId: os.Getenv("TEST_DATADOG_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get CPU and Memory utilization metrics for app deployment in nudgebee namespace over the last hour",
			},
		}
	for _, tc := range testCases {
		agent := NewDatadogMetricsAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.GreaterOrEqual(t, len(resp.AgentStepResponse), 1)
		assert.Greater(t, len(resp.Response), 0)
	}

}
