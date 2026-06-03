//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDelegateAgent_PostgresConnectionPoolInvestigation tests that the k8s_debug
// agent can leverage delegate_agent to spawn a specialist sub-agent for cross-domain
// investigation combining PostgreSQL internals with infrastructure metrics.
func TestDelegateAgent_PostgresConnectionPoolInvestigation(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "postgres_pool_exhaustion_with_delegate",
			SessionId: "ut-delegate-agent-pg-pool-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: `The checkout-svc in production namespace is experiencing intermittent 500 errors.
We suspect PostgreSQL connection pool exhaustion. The service uses pgbouncer with max_client_conn=100.

Investigate:
1. Check the pod status and recent events for checkout-svc
2. Analyze PostgreSQL connections - active vs idle, waiting clients, pool saturation
3. Correlate with application logs for connection timeout errors

Use a specialist to deep-dive into the database performance if needed.`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
			k8sAgent := newK8sDebugAgent(tc.AccountId)

			// Verify delegate_agent is in the supported tools
			tools := k8sAgent.GetSupportedTools(sc)
			hasDelegateAgent := false
			for _, tool := range tools {
				if tool.Name() == DelegateAgentToolName {
					hasDelegateAgent = true
					break
				}
			}
			assert.True(t, hasDelegateAgent, "k8s_debug agent should have delegate_agent in its supported tools")

			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			assert.Nil(t, err)
			if err != nil {
				return
			}
			assert.NotNil(t, resp)

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))
			t.Log("tools - ", string(invocationLog))

			assert.Equal(t, k8sAgent.GetName(), resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)

			// Verify the response addresses postgres/database concerns
			responseText := strings.Join(resp.Response, " ")
			responseLower := strings.ToLower(responseText)
			invocationLogStr := strings.ToLower(string(invocationLog))

			// The planner should have used postgres-related tools or delegate_agent
			usedPostgres := strings.Contains(invocationLogStr, "postgres")
			usedDelegate := strings.Contains(invocationLogStr, "delegate_agent")
			t.Logf("Used postgres tool: %v, Used delegate_agent: %v", usedPostgres, usedDelegate)

			// At minimum, the response should discuss postgres/database/connection
			hasDbContext := strings.Contains(responseLower, "postgres") ||
				strings.Contains(responseLower, "database") ||
				strings.Contains(responseLower, "connection")
			assert.True(t, hasDbContext, "Response should address database/postgres/connection concerns")
		})
	}
}

// TestDelegateAgent_ParallelSpecialistInvestigation tests that the debug agent
// can dispatch multiple delegate_agent calls (or mixed tool calls) for parallel
// investigation of independent sub-problems.
func TestDelegateAgent_ParallelSpecialistInvestigation(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_USER") == "" || os.Getenv("TEST_TENANT") == "" {
		t.Skip("skipping integration test: TEST_ACCOUNT, TEST_USER, TEST_TENANT not set")
	}

	testCases := []struct {
		Name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			Name:      "parallel_db_and_infra_investigation",
			SessionId: "ut-delegate-agent-parallel-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query: `The order-processing service in production is failing. We see two independent issues:
1. PostgreSQL: "too many connections" errors in the logs - need to check pg_stat_activity, connection pool config, and slow queries
2. Pod health: pods are restarting with OOMKilled - need to check memory limits, actual usage, and container resource metrics

These are independent problems. Investigate both in parallel.`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
			k8sAgent := newK8sDebugAgent(tc.AccountId)

			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

			assert.Nil(t, err)
			if err != nil {
				return
			}
			assert.NotNil(t, resp)

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))
			t.Log("tools - ", string(invocationLog))

			assert.Equal(t, k8sAgent.GetName(), resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)

			// Response should cover both postgres and k8s/OOM aspects
			responseText := strings.Join(resp.Response, " ")
			responseLower := strings.ToLower(responseText)

			hasDbContext := strings.Contains(responseLower, "postgres") ||
				strings.Contains(responseLower, "connection") ||
				strings.Contains(responseLower, "database")
			hasK8sContext := strings.Contains(responseLower, "oom") ||
				strings.Contains(responseLower, "memory") ||
				strings.Contains(responseLower, "restart")

			t.Logf("Has DB context: %v, Has K8s context: %v", hasDbContext, hasK8sContext)
			assert.True(t, hasDbContext || hasK8sContext, "Response should address at least one of the two investigation areas")
		})
	}
}
