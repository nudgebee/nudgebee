package api

import (
	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConversationChatSuggestions(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	request := core.ConversationSuggestionRequest{
		AccountId:      os.Getenv("TEST_ACCOUNT"),
		UserId:         sc.GetSecurityContext().GetUserId(),
		ConversationId: "89f966e1-e58c-4826-aba3-6fe0f6784ba6",
		MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5a6",
	}
	respons, err := core.HandleConversationSuggestionRequest(sc, request)

	assert.Nil(t, err)
	assert.NotEmpty(t, respons.Suggestions)
	assert.LessOrEqual(t, 3, len(respons.Suggestions))
	assert.Equal(t, os.Getenv("TEST_ACCOUNT"), respons.AccountId)
	assert.Equal(t, request.ConversationId, respons.ConversationId)
	assert.Equal(t, request.MessageId, respons.MessageId)
}

func TestConversationFlowWithContext(t *testing.T) {
	sessionId := "session-memory-context"
	conversationId := "89f966e1-e58c-4826-aba3-6fe0f6784ba7"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	testCases :=
		[]struct {
			SessionId      string
			Query          string
			AccountId      string
			UserId         string
			MessageId      string
			ConversationId string
		}{
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5a6",
				ConversationId: conversationId,
				Query:          "Get me deployment details of ml-k8s-server in nudgebee namespace",
			},
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5a7",
				ConversationId: conversationId,
				Query:          "Get me cpu usage for the same",
			},
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5a8",
				ConversationId: conversationId,
				Query:          "Get me memory usage for the same",
			},
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5a9",
				ConversationId: conversationId,
				Query:          "get me logs for the same from nudgebee-test namespace",
			},
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5aa",
				ConversationId: conversationId,
				Query:          "what's the status of the deployment", // should use nudgebee-test namespace
			},
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5ab",
				ConversationId: conversationId,
				Query:          "do the same for nudgebee namespace",
			},
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5ac",
				ConversationId: conversationId,
				Query:          "get me latest logs",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	for _, tc := range testCases {
		agent, err := agents.InferAgentOrHelp(sc, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceUserInvestigation))
		assert.Nil(t, err)
		assert.NotNil(t, agent)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestPromptRefinement(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prompt-refinement-chain-5",
				AccountId: "a2a30b02-0f67-42e5-a2ab-c658230fd798",
				UserId:    "404d5721-dd4b-40ee-b054-78d58d11fd35",
				Query: `Refine this prompt: 'Context:
You are a Kubernetes assistant responsible for monitoring and diagnosing the health of applications running in the 'nudgebee' namespace. These applications depend on PostgreSQL and RabbitMQ. Metrics are available via Prometheus. Tracing data (if available) is exposed via OpenTelemetry-compatible backends.

Task:
Evaluate the health of the applications in the 'nudgebee' namespace by performing the following steps:

Instruction (Conditional Execution Flow):

1. **Check Application Status**:
   - List all pods in 'nudgebee' namespace.
   - For each pod, check if the pod status is not 'Running' or 'Completed'.
   - If any pod is in a CrashLoopBackOff or OOMKilled state:
     - Fetch the last 50 lines of logs for the failing container.
     - Report the reason and error message.

2. **Auto-discover Dependencies**:
   - Identify if 'postgres' and 'rabbitmq' deployments or statefulsets are present in the 'nudgebee' namespace or referenced via service names.
   - If found, tag them as dependencies and evaluate their health using the relevant metrics.

3. **Check Prometheus Metrics**:

   3.1 **General App Health**:
   - 'up{namespace="nudgebee"}' — Ensure all targets are up.
   - 'container_cpu_usage_seconds_total', 'container_memory_usage_bytes' for resource usage over time.

   3.2 **PostgreSQL Health**:
   - 'pg_stat_activity_count' or 'pg_stat_activity{datname="*"}'
   - 'pg_database_size_bytes', 'pg_locks_count', 'pg_stat_database_blks_hit / (pg_stat_database_blks_hit + pg_stat_database_blks_read)' (cache hit rate)
   - Alert if cache hit ratio < 0.99 or if connections exceed configured limits.

   3.3 **RabbitMQ Health**:
   - 'rabbitmq_queue_messages', 'rabbitmq_queue_messages_ready', 'rabbitmq_queue_messages_unacked'
   - 'rabbitmq_channel_messages_published_total', 'rabbitmq_channel_messages_delivered_total'
   - 'rabbitmq_disk_space_available_bytes', 'rabbitmq_build_info'

   3.4 **Error Rate and Latency**:
   - 'container_http_requests_total{destination_workload_namespace="nudgebee", status=~"5.."}'
   - 'rate(container_http_requests_total{destination_workload_namespace="nudgebee",status=~"5.."}[5m])' > threshold (e.g., 1)
   - 'histogram_quantile(0.95, rate(container_http_requests_duration_seconds_total_bucket{destination_workload_namespace="nudgebee"}[5m]))' for high latency

4. **Check for Spikes and Anomalies in Metrics**:
   - Identify any recent spike in error rate, memory, CPU, or request latency over the past 1 hour.
   - Highlight deltas compared to a 1-day baseline.

5. **Traces Analysis (if enabled)**:
   - Check traces for high-latency spans or spans with errors in the last 30 minutes.
   - Identify top slow operations or repeated failed DB queries.
   - Suggest code-level optimizations if bottlenecks are found.

6. **Final Output**:
   - Summary: Healthy / Degraded / Failing
   - Key Issues:
     - Pod-level failures
     - Postgres/RabbitMQ issues
     - Latency/Error anomalies
     - Optimization insights from traces
   - Recommendation:
     - e.g., “Postgres cache hit ratio is below optimal. Add indexes or increase memory.”
     - “RabbitMQ unacked messages are piling up — check consumer throughput.”

Scenario:
The assistant should adapt the depth of analysis depending on the presence or absence of metrics/logs. If traces are not available, skip step 5. If no anomalies are found, return a healthy status with usage summaries.

History:
Past reports have flagged RabbitMQ memory pressure and high latency in API endpoints during peak traffic hours.'
`,
			},
		}
	for _, tc := range testCases {
		promptRefinementAgent, _ := core.GetNBAgent(sc, agents.PromptRefinementAgentName, tc.AccountId, core.AgentStatusEnabled)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, promptRefinementAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		assert.Equal(t, resp.AgentName, promptRefinementAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogDisconnectInvestigate(t *testing.T) {
	sessionId := "log-disconnect-bug-context"
	conversationId := "89f966e1-e58c-4826-aba3-6fe0f6784ba9"
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	testCases :=
		[]struct {
			SessionId      string
			Query          string
			AccountId      string
			UserId         string
			MessageId      string
			ConversationId string
		}{
			{
				SessionId:      sessionId,
				AccountId:      accountId,
				UserId:         userId,
				MessageId:      "cf2d7d3d-ca2e-49af-8788-d9c9229fd5ad",
				ConversationId: conversationId,
				Query:          "why logs are disconnected? Investigate",
			},
		}
	err := core.DeleteConversationBySession(sessionId, accountId, userId)
	assert.Nil(t, err)

	for _, tc := range testCases {
		agent, err := agents.InferAgentOrHelp(sc, tc.UserId, tc.AccountId, tc.SessionId, tc.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceUserInvestigation))
		assert.Nil(t, err)
		assert.NotNil(t, agent)

		resp, err := core.HandleConversationSessionRequest(sc, agent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestConversationUsageMetrics(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	request := core.ConversationUsageMetricsRequest{
		AccountId:      os.Getenv("TEST_ACCOUNT"),
		UserId:         sc.GetSecurityContext().GetUserId(),
		ConversationId: "be2bf96c-0027-4322-8a02-80267187a6bc",
	}
	response, err := core.HandleConversationUsageMetricsApi(sc, request)

	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestPromqlQuery(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testcase := struct {
		Query     string
		AccountId string
		UserId    string
		SessionID string
	}{
		Query:     "@prometheus Get me cpu usage for nudgebee namespace",
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    sc.GetSecurityContext().GetUserId(),
		SessionID: "promql-query-session",
	}

	// Test with enhanced query agents enabled
	agent, err := agents.InferAgentOrHelp(sc, testcase.UserId, testcase.AccountId, testcase.SessionID, testcase.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceUserInvestigation))
	assert.Nil(t, err)
	assert.NotNil(t, agent)
	resp, err := core.HandleConversationSessionRequest(sc, agent, testcase.UserId, testcase.AccountId, testcase.SessionID, testcase.Query)
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, resp.AgentName, agent.GetName())
	assert.NotEmpty(t, resp.Query)
	assert.NotNil(t, resp.AgentStepResponse)
	assert.Greater(t, len(resp.Response), 0)
}

func TestLogqlQuery(t *testing.T) {
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testcase := struct {
		Query     string
		AccountId string
		UserId    string
		SessionID string
	}{
		Query:     "@logs Get me error logs for rag-server in nudgebee namespace",
		AccountId: os.Getenv("TEST_ACCOUNT"),
		UserId:    sc.GetSecurityContext().GetUserId(),
		SessionID: "logql-query-session",
	}

	// Test with enhanced query agents enabled
	agent, err := agents.InferAgentOrHelp(sc, testcase.UserId, testcase.AccountId, testcase.SessionID, testcase.Query, core.ConversationSessionRequestWithSource(core.ConversationSourceUserInvestigation))
	assert.Nil(t, err)
	assert.NotNil(t, agent)
	resp, err := core.HandleConversationSessionRequest(sc, agent, testcase.UserId, testcase.AccountId, testcase.SessionID, testcase.Query)
	assert.Nil(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, resp.AgentName, agent.GetName())
	assert.NotEmpty(t, resp.Query)
	assert.NotNil(t, resp.AgentStepResponse)
	assert.Greater(t, len(resp.Response), 0)
}
