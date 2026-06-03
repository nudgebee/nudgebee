//go:build e2e

package agents

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution
func TestPrometheusQueryAgentExecute(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Average response time for API endpoint /generate in the last hour",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecute2(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get 99th quantile of memory usage of deployment notifications in nudgebee namesapce",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteHF(t *testing.T) {
	config.Config.SetString("llm_provider_promql_query", "huggingface")
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-hf-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Average response time for API endpoint /generate in the last hour?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		startTime := time.Now()
		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		endTime := time.Now()
		slog.Info("Response Time", "time", endTime.Sub(startTime).Milliseconds())
		slog.Info("Response", "resp", resp)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteOllama(t *testing.T) {
	config.Config.SetString("llm_provider_promql_query", "openai")
	config.Config.SetString("llm_model_promql_query", "deepseek-coder-v2:16b")
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-ollama-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Average response time for API endpoint /health in the last hour?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func BenchmarkPrometheusQueryAgentAccuracy(t *testing.B) {
	prometheusChain := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	// read benchmark file from data/benchmark/promtheus.json
	jsonData, err := os.ReadFile("../data/benchmark/prometheus.json")
	if err != nil {
		panic(err)
	}

	type promqlQueries struct {
		Question  string `json:"question"`
		Answer    string `json:"answer"`
		SessionId string `json:"conversation_id"`
		AccountId string `json:"account_id"`
		UserId    string `json:"user_id"`
	}

	var testcases []promqlQueries
	err = json.Unmarshal(jsonData, &testcases)
	if err != nil {
		panic(err)
	}

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	for i, q := range testcases {
		if q.AccountId == "" {
			testcases[i].AccountId = accountId
		}
		if q.UserId == "" {
			testcases[i].UserId = userId
		}
		if q.SessionId == "" {
			testcases[i].SessionId = fmt.Sprintf("ut-prom-bench-%d", i)
		}
	}

	passedTests := 0
	for _, tc := range testcases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, _ := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Question)
		if len(resp.Response) > 0 {
			similarityValue := GetPromQlSimilarity(tc.Question, resp.Response[0], resp.AgentId, tc.AccountId, resp.ConversationId, "", tc.UserId)
			t.Log("Similarity - 0 if the answer is not correct")
			t.Log("Similarity - 1 if the answer is correct")
			t.Log("Query - ", tc.Question, "\nExpected: ", tc.Answer, "\nGot: ", resp.Response, "\nSimilarity", similarityValue, "\n")
			if len(resp.Response) > 0 && similarityValue > 0 {
				passedTests = passedTests + 1
			}
		}
	}

	t.Error("Passed", passedTests, "Total", len(testcases), "Accuracy", float64(passedTests)/float64(len(testcases))*100)

}

func TestFailedToolCalls(t *testing.T) {
	err := os.Setenv("AWS_REGION", config.Config.LlmProviderRegion)
	assert.Nil(t, err)
	prometheusChain := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("unable to connect", "error", err)
		return
	}

	rows, err := dbms.Db.Queryx(`select distinct query from llm_conversation_agent la where la.agent_name = 'promql' and status = 'fail'`)
	if err != nil {
		slog.Error("unable to query", "error", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("failed to close rows", "err", err)
		}
	}()
	queries := []string{}

	for rows.Next() {
		var session string
		err := rows.Scan(&session)
		if err != nil {
			slog.Error("unable to scan", "error", err)
		}
		queries = append(queries, session)
	}
	slog.Info("migrating data for", "queries", queries)

	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	if accountId == "" || userId == "" {
		t.Skip("skipping: TEST_ACCOUNT / TEST_USER not set")
	}

	passedTests := 0

	for i, query := range queries {
		sessionId := fmt.Sprintf("ut-prom-bench-%d", i)
		err := core.DeleteConversationBySession(sessionId, accountId, userId)
		assert.Nil(t, err)

		resp, _ := core.HandleConversationSessionRequest(sc, prometheusChain, userId, accountId, sessionId, query)

		if len(resp.Response) > 0 {
			passedTests = passedTests + 1
		} else {
			t.Log("Failed Query - ", query, "Expected", query, "Got", resp.Response)
		}
	}
	t.Error("Passed", passedTests, "Total", len(queries), "Accuracy", float64(passedTests)/float64(len(queries))*100)
}

func TestPrometheusQueryAgentExecuteMemoryMetrics(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-memory-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get the memory usage of ml-k8s-server in the nudgebee namespace",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteKubernetesMetrics(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-k8s-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get the CPU and memory usage metrics for the pods managed by the 'ad' deployment in the 'otel-demo' namespace to check for resource exhaustion or OOMKilled errors that might be causing the restarts.",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteKubernetesNodeMetrics(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-k8s-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What is the CPU utilization for node k3s-cluster-node-pool-1-xet14?",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteCorrectPromQLQueries(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-k8s-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "For each Kubernetes node, show the correlation between memory usage and CPU usage over the past day.",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteKubernetesPodMetrics(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-k8s-4",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Show me the average CPU usage across all Kubernetes pods in the last 2 hours, but only for pods that had memory usage above 500MiB at any point.",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteKubernetesPodMetricsWithDirectQuery(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-k8s-5",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Show me the average CPU usage in namespace regex like nudgebee and pod like app-dev",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusQueryAgentExecuteDirectQueryInput(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-k8s-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "{\"command\":\"rate(node_disk_io_time_seconds_total{namespace=\"my-agent\", pod=~\"my-agent-clickhouse.*\"}[5m])\",\"args\":null,\"context\":\"\"}",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPromqlAgent_CustomMetricDiscoveryWithSearchMetrics(t *testing.T) {
	prometheusAgent := &PromqlAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name          string
		SessionId     string
		Query         string
		AccountId     string
		UserId        string
		ExpectMetrics []string // at least one should appear in response
	}{
		{
			Name:          "Redis memory via search_metrics",
			SessionId:     "ut-promql-custom-redis-1",
			AccountId:     os.Getenv("TEST_ACCOUNT"),
			UserId:        os.Getenv("TEST_USER"),
			Query:         "redis memory usage",
			ExpectMetrics: []string{"redis_memory", "redis_allocator"},
		},
		{
			Name:          "Kafka consumer lag via search_metrics",
			SessionId:     "ut-promql-custom-kafka-1",
			AccountId:     os.Getenv("TEST_ACCOUNT"),
			UserId:        os.Getenv("TEST_USER"),
			Query:         "kafka consumer lag",
			ExpectMetrics: []string{"kafka", "consumer_lag", "confluent"},
		},
		{
			Name:          "Consul service health via search_metrics",
			SessionId:     "ut-promql-custom-consul-1",
			AccountId:     os.Getenv("TEST_ACCOUNT"),
			UserId:        os.Getenv("TEST_USER"),
			Query:         "consul service health check status",
			ExpectMetrics: []string{"consul"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			startTime := time.Now()
			resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			elapsed := time.Since(startTime)

			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, prometheusAgent.GetName(), resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			assert.NotNil(t, resp.AgentStepResponse)
			assert.Greater(t, len(resp.Response), 0)

			// Log performance
			slog.Info("Promql custom metric test",
				"test", tc.Name,
				"steps", len(resp.AgentStepResponse),
				"elapsed_ms", elapsed.Milliseconds(),
			)

			// Print step-by-step tool usage
			usedSearchMetrics := false
			for i, step := range resp.AgentStepResponse {
				toolName := ""
				toolInput := ""
				if step.Call.FunctionCall != nil {
					toolName = step.Call.FunctionCall.Name
					toolInput = step.Call.FunctionCall.Arguments
				}
				fmt.Printf("  Step %d: tool=%s input=%s\n", i+1, toolName, toolInput)
				if toolName == "search_metrics" {
					usedSearchMetrics = true
				}
			}

			// Verify search_metrics was used (since these are custom metrics not in ground truth)
			assert.True(t, usedSearchMetrics,
				"Expected search_metrics to be used for custom metric query '%s'", tc.Query)

			// Verify the response contains the expected metric
			allContent := strings.ToLower(strings.Join(resp.Response, " "))
			foundMetric := false
			for _, metricSubstr := range tc.ExpectMetrics {
				if strings.Contains(allContent, strings.ToLower(metricSubstr)) {
					foundMetric = true
					break
				}
			}
			assert.True(t, foundMetric,
				"Expected promql response for '%s' to contain one of %v", tc.Query, tc.ExpectMetrics)

			// PromQL agent should resolve in few steps (search_metrics + possibly metrics_labels_list + fallbacks)
			// With ground truth filter fix, expect <=3 steps; allow up to 5 as safety margin
			assert.LessOrEqual(t, len(resp.AgentStepResponse), 5,
				"Promql agent should resolve custom metric in <= 5 steps, got %d", len(resp.AgentStepResponse))
		})
	}
}
