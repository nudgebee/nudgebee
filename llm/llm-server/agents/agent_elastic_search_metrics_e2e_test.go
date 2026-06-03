//go:build e2e

package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestElasticSearchMetricsAgent_Execute(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ES_METRICS_ACCOUNT")
	userId := os.Getenv("TEST_USER")

	if accountId == "" || userId == "" {
		t.Skip("skipping: TEST_ES_METRICS_ACCOUNT and TEST_USER must be set")
	}

	testCases := []struct {
		Query string
	}{
		// {
		// 	Query: "What metrics are available in the demo namespace over the last hour? Summarize the metric names and their latest values",
		// },
		{
			Query: "What is the average CPU usage of all pods in the nudgebee namespace over the last 7 days ?",
		},
		// {
		// 	Query: "Show me the memory usage trend for the llm-server in nudgebee namespace over the last 7 days",
		// },
	}

	for i, tc := range testCases {
		t.Run(tc.Query, func(t *testing.T) {
			sessionId := fmt.Sprintf("ut-es-metrics-%d", i+1)
			_ = core.DeleteConversationBySession(sessionId, accountId, userId)

			agent, ok := core.GetNBAgent(sc, ElasticSearchMetricsAgentName, accountId, "")
			assert.True(t, ok)
			assert.NotNil(t, agent)

			resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, sessionId, tc.Query)
			if err != nil {
				t.Skipf("Skipping due to missing connectivity: %v", err)
			}

			assert.NotNil(t, resp)
			assert.Equal(t, ElasticSearchMetricsAgentName, resp.AgentName)
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}
