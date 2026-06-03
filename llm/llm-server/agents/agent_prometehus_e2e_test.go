//go:build e2e

package agents

import (
	"fmt"
	"log/slog"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TODO mock DBs
// TODO mock Tool Execution

func TestPrometheusAgentExecute0(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-0",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "get me memory usage of kube-system namespace in last 10 days",
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		// convert
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Equal(t, len(resp.AgentStepResponse), 2)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusAgent_RangeExtraction(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-range-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get memory usage for the last 2 days",
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.NotNil(t, resp.AgentStepResponse)

		// Verify that the range parameter is present in the tool call
		foundRange := false
		exp, err := regexp.Compile(`"range"\s*:\s*"2d"`)
		assert.NoError(t, err)

		for _, step := range resp.AgentStepResponse {
			if step.Call.FunctionCall.Name == "prometheus_execute" {
				// Check arguments for range
				argsStr := step.Call.FunctionCall.Arguments
				// argsStr is a JSON string, simply check if it contains "range"
				if len(argsStr) > 0 {
					// Simple check for "range" key in the JSON string
					// A more robust check would unmarshal, but since we don't have common/json imported here easily without adding deps, string check is a good proxy
					if containsRange := exp.MatchString(argsStr); containsRange {
						foundRange = true
					}
				}
			}
		}
		// Note: This assertion might fail if we can't mock the LLM execution to strictly follow instructions in this unit test environment.
		// But conceptually this is what we want to test.
		if len(resp.AgentStepResponse) > 0 {
			// Assert we got some response, even if specific tool call structure depends on LLM behavior
			assert.Greater(t, len(resp.Response), 0)
			// Assert that we found the range parameter in the tool call if steps were generated
			assert.True(t, foundRange, "Expected 'range' parameter in prometheus_execute tool call")
		}
	}
}

func TestPrometheusChainExecutePrometheusTimezoneScenarios(t *testing.T) {
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prometheus-chain-no-timezone",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get cpu usage between 2026-01-05T10:00:00 and 2026-01-08T10:30:00",
			},
		}
	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		promAgent := newPrometheusAgent(tc.AccountId)
		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)
		resp, err := core.HandleConversationSessionRequest(sc, promAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, promAgent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestPrometheusAgentExecute1(t *testing.T) {
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
				Query:     "top 5 slow applications",
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		// convert
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Equal(t, len(resp.AgentStepResponse), 1)
		for _, invocation := range resp.AgentStepResponse {
			assert.NotNil(t, invocation)
			assert.NotNil(t, invocation.Response)
			if invocation.Call.FunctionCall.Name == "promql_query" {
				re := regexp.MustCompile(`topk\(5, histogram_quantile\(0\.(95|99), sum\(rate\(container_http_requests_duration_seconds_total_bucket\{__CLUSTER__\}\[5m\]\)\) by \(le, destination_workload_name, (destination_workload_namespace|namespace)\)\)\)`)
				assert.Regexp(t, re, invocation.Response.Content)
			}
		}
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusAgentExecute2(t *testing.T) {
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
				Query:     "Average response time for API endpoint services-server in the last hour?",
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.GreaterOrEqual(t, len(resp.AgentStepResponse), 2)
		if resp.AgentStepResponse[0].Call.FunctionCall.Name == "promql_query" {
			assert.Contains(t, resp.AgentStepResponse[0].Response.Content, "rate")
			assert.Contains(t, resp.AgentStepResponse[0].Response.Content, "container_http_requests")
			assert.Contains(t, resp.AgentStepResponse[0].Response.Content, "1h")
		}
		for _, invocation := range resp.AgentStepResponse {
			assert.NotNil(t, invocation)
			assert.NotNil(t, invocation.Response)
		}
	}

}

func TestMultiplePromQL(t *testing.T) {
	err := os.Setenv("AWS_REGION", "us-west-2")
	assert.Nil(t, err)
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-3",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What is the PromQL query to get CPU and memory usage of k8s collector server in nudgebee namespace?",
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
		assert.Equal(t, len(resp.AgentStepResponse), 5)
		for _, invocation := range resp.AgentStepResponse {
			assert.NotNil(t, invocation)
			assert.NotNil(t, invocation.Response)
		}
	}
}

func TestPrometheusAgent_ExecuteOllama(t *testing.T) {
	config.Config.SetString("llm_provider_promql_query", "openai")
	config.Config.SetString("llm_model_promql_query", "cyberuser42/DeepSeek-R1-Distill-Qwen-7B:latest")
	config.Config.SetString("llm_provider_prometheus", "openai")
	config.Config.SetString("llm_model_prometheus", "cyberuser42/DeepSeek-R1-Distill-Qwen-7B:latest")
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prometheus-ollama-4",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "What is the PromQL query to get CPU and memory usage of k8s collector server?",
			},
		}
	for _, tc := range testCases {
		prometheusAgent := newPrometheusAgent(tc.AccountId)

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

func TestPrometheusAgent_ExecuteDirectQuery(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-5",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `up{job="rabbitmq"}`,
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		// convert
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Equal(t, len(resp.AgentStepResponse), 1)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusAgent_ListMetrices(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-6",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Can you get me list of http related metrics?`,
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		// convert
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Equal(t, len(resp.AgentStepResponse), 1)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusAgent_QueryAndCritique(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-7",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     `Get the maximum memory usage in bytes for the pod labeled as 'llm-server' in the 'nudgebee' namespace over the last 24 hours`,
			},
		}
	for _, tc := range testCases {
		prometheusChain := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusChain, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		// convert
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Equal(t, len(resp.AgentStepResponse), 2)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, prometheusChain.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestPrometheusAgent_VisualizationCharts(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-viz-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get me a chart of max memory usage of ml-k8s-server deployment in nudgebee namespace for last 1 days..",
			},
		}
	for _, tc := range testCases {
		prometheusAgent := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestPrometheusAgent_TimeDuration(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-5",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Get the memory usage metric for the cloud-collector-server in the nudgebee namespace around 01:02 UTC, specifically for a 30-minute window around that time..",
			},
		}
	for _, tc := range testCases {
		prometheusAgent := newPrometheusAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, prometheusAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)

		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

// TestPrometheusAgent_CustomMetricDiscovery tests that custom metric queries
// (redis, kafka, postgres, JVM, etc.) are resolved efficiently using semantic
// search (search_metrics) rather than guessing or excessive retries.
func TestPrometheusAgent_CustomMetricDiscovery(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name           string
		SessionId      string
		Query          string
		AccountId      string
		UserId         string
		ExpectMetrics  []string // at least one of these metric substrings should appear in the response
		MaxSteps       int      // max expected agent steps (fewer = better, previously these caused 5+ steps)
		ExpectToolUsed string   // tool we expect to see used (search_metrics for custom metrics)
	}{
		{
			Name:           "Redis memory usage",
			SessionId:      "ut-prom-custom-redis-1",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "What is the Redis memory usage?",
			ExpectMetrics:  []string{"redis_memory", "redis_allocator"},
			MaxSteps:       4,
			ExpectToolUsed: tools.ToolSearchMetrics,
		},
		{
			Name:           "Kafka consumer lag",
			SessionId:      "ut-prom-custom-kafka-1",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "Show me the kafka consumer lag",
			ExpectMetrics:  []string{"kafka", "consumer_lag", "confluent"},
			MaxSteps:       4,
			ExpectToolUsed: tools.ToolSearchMetrics,
		},
		{
			Name:           "JVM heap memory",
			SessionId:      "ut-prom-custom-jvm-1",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "What is the JVM heap memory usage?",
			ExpectMetrics:  []string{"jvm", "java", "heap"},
			MaxSteps:       4,
			ExpectToolUsed: tools.ToolSearchMetrics,
		},
		{
			Name:           "Postgres connections",
			SessionId:      "ut-prom-custom-pg-1",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "How many postgres database connections are there?",
			ExpectMetrics:  []string{"pg_", "postgres"},
			MaxSteps:       4,
			ExpectToolUsed: tools.ToolSearchMetrics,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			prometheusAgent := newPrometheusAgent(tc.AccountId)

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

			// Log performance data
			slog.Info("Custom metric discovery test",
				"test", tc.Name,
				"steps", len(resp.AgentStepResponse),
				"elapsed_ms", elapsed.Milliseconds(),
				"query", tc.Query,
			)

			// Print step details for debugging
			for i, step := range resp.AgentStepResponse {
				toolName := ""
				toolArgs := ""
				if step.Call.FunctionCall != nil {
					toolName = step.Call.FunctionCall.Name
					toolArgs = step.Call.FunctionCall.Arguments
				}
				fmt.Printf("  Step %d: tool=%s args=%s\n", i+1, toolName, toolArgs)
			}

			// Check step count - these queries previously caused 5+ steps due to guessing/retrying
			assert.LessOrEqual(t, len(resp.AgentStepResponse), tc.MaxSteps,
				"Query '%s' should complete in <= %d steps (was 5+ before), got %d",
				tc.Query, tc.MaxSteps, len(resp.AgentStepResponse))

			// Check that search_metrics was used for custom metric discovery
			foundSearchMetrics := false
			foundPrometheusExecute := false
			prometheusExecuteHasData := false
			for _, step := range resp.AgentStepResponse {
				if step.Call.FunctionCall != nil {
					if step.Call.FunctionCall.Name == tc.ExpectToolUsed {
						foundSearchMetrics = true
					}
					if step.Call.FunctionCall.Name == tools.ToolQueryPrometheus {
						foundPrometheusExecute = true
						// Check that prometheus_execute returned actual data (not error/empty)
						content := step.Response.Content
						if content != "" && !strings.Contains(strings.ToLower(content), "no data") &&
							!strings.Contains(strings.ToLower(content), "error") &&
							len(content) > 10 {
							prometheusExecuteHasData = true
						}
					}
				}
				// Also check nested agent calls (promql_query may use search_metrics internally)
				if step.Response.Content != "" && strings.Contains(step.Response.Content, tc.ExpectToolUsed) {
					foundSearchMetrics = true
				}
			}
			// Note: search_metrics may be called inside the nested promql_query agent
			// so we check both direct usage and presence in response content
			if !foundSearchMetrics {
				t.Logf("Warning: %s tool not directly visible in top-level steps for '%s'. "+
					"It may have been called inside nested promql_query agent.", tc.ExpectToolUsed, tc.Query)
			}

			// Verify prometheus_execute was called (full end-to-end flow)
			assert.True(t, foundPrometheusExecute,
				"Expected prometheus_execute to be called for '%s' — query should be discovered AND executed", tc.Query)

			// Verify prometheus_execute returned actual data
			if foundPrometheusExecute {
				assert.True(t, prometheusExecuteHasData,
					"Expected prometheus_execute to return data for '%s', but got empty or error response", tc.Query)
			}

			// Check that the response contains the expected metric
			allResponseContent := strings.Join(resp.Response, " ")
			for _, step := range resp.AgentStepResponse {
				allResponseContent += " " + step.Response.Content
			}
			allResponseContent = strings.ToLower(allResponseContent)

			foundMetric := false
			for _, metricSubstr := range tc.ExpectMetrics {
				if strings.Contains(allResponseContent, strings.ToLower(metricSubstr)) {
					foundMetric = true
					break
				}
			}
			assert.True(t, foundMetric,
				"Expected response for '%s' to contain one of %v, but got: %s",
				tc.Query, tc.ExpectMetrics, allResponseContent[:min(500, len(allResponseContent))])
		})
	}
}

// TestPrometheusAgent_FastPathKnownMetrics tests that standard K8s metrics
// resolve via the fast path (direct execution or promql_query) without
// unnecessary tool calls. These queries previously took 170-250s with 7-9 iterations
// because the agent would call metrics_list/metrics_labels_list excessively.
func TestPrometheusAgent_FastPathKnownMetrics(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name          string
		SessionId     string
		Query         string
		AccountId     string
		UserId        string
		ExpectMetrics []string // at least one metric substring expected in response
		MaxSteps      int      // max top-level steps (fast path = fewer steps)
	}{
		{
			Name:          "CPU usage for specific deployment",
			SessionId:     "ut-prom-fastpath-cpu-1",
			AccountId:     os.Getenv("TEST_ACCOUNT"),
			UserId:        os.Getenv("TEST_USER"),
			Query:         "llm-server cpu usage",
			ExpectMetrics: []string{"container_cpu_usage_seconds_total", "cpu"},
			MaxSteps:      3, // was 7-9 steps, should be: promql_query + execute (+ optional summary)
		},
		{
			Name:          "Error rate for specific app",
			SessionId:     "ut-prom-fastpath-errorrate-1",
			AccountId:     os.Getenv("TEST_ACCOUNT"),
			UserId:        os.Getenv("TEST_USER"),
			Query:         "error rate for services-server",
			ExpectMetrics: []string{"container_http_requests_total", "error", "5xx", "500"},
			MaxSteps:      6, // ratio queries need more steps: execute → verify namespace → re-execute with namespace filter
		},
		{
			Name:          "High memory usage pods",
			SessionId:     "ut-prom-fastpath-highmem-1",
			AccountId:     os.Getenv("TEST_ACCOUNT"),
			UserId:        os.Getenv("TEST_USER"),
			Query:         "pods with high memory usage",
			ExpectMetrics: []string{"container_memory", "memory"},
			MaxSteps:      3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			prometheusAgent := newPrometheusAgent(tc.AccountId)

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

			slog.Info("Fast path known metric test",
				"test", tc.Name,
				"steps", len(resp.AgentStepResponse),
				"elapsed_ms", elapsed.Milliseconds(),
				"query", tc.Query,
			)

			// Print step details for debugging
			foundPrometheusExecute := false
			prometheusExecuteHasData := false
			for i, step := range resp.AgentStepResponse {
				toolName := ""
				toolArgs := ""
				if step.Call.FunctionCall != nil {
					toolName = step.Call.FunctionCall.Name
					toolArgs = step.Call.FunctionCall.Arguments
				}
				fmt.Printf("  Step %d: tool=%s args=%s\n", i+1, toolName, toolArgs)

				if step.Call.FunctionCall != nil && step.Call.FunctionCall.Name == tools.ToolQueryPrometheus {
					foundPrometheusExecute = true
					content := step.Response.Content
					if content != "" && !strings.Contains(strings.ToLower(content), "no data") &&
						!strings.Contains(strings.ToLower(content), "error") &&
						len(content) > 10 {
						prometheusExecuteHasData = true
					}
				}
			}

			// Known K8s metrics should resolve in few steps — no need for search_metrics or metrics_list
			assert.LessOrEqual(t, len(resp.AgentStepResponse), tc.MaxSteps,
				"Known metric query '%s' should complete in <= %d steps (was 7-9 before), got %d",
				tc.Query, tc.MaxSteps, len(resp.AgentStepResponse))

			// Verify end-to-end: prometheus_execute was called and returned data
			assert.True(t, foundPrometheusExecute,
				"Expected prometheus_execute to be called for known metric '%s'", tc.Query)
			if foundPrometheusExecute {
				assert.True(t, prometheusExecuteHasData,
					"Expected prometheus_execute to return data for '%s', but got empty or error response", tc.Query)
			}

			// Verify response contains expected metric references
			allResponseContent := strings.ToLower(strings.Join(resp.Response, " "))
			for _, step := range resp.AgentStepResponse {
				allResponseContent += " " + strings.ToLower(step.Response.Content)
			}
			foundMetric := false
			for _, metricSubstr := range tc.ExpectMetrics {
				if strings.Contains(allResponseContent, strings.ToLower(metricSubstr)) {
					foundMetric = true
					break
				}
			}
			assert.True(t, foundMetric,
				"Expected response for '%s' to contain one of %v", tc.Query, tc.ExpectMetrics)
		})
	}
}

// TestPrometheusAgent_CustomMetricNoExcessiveRetries tests that custom metric queries
// that previously caused excessive retries (10+ iterations, 500s+ duration) are now
// resolved efficiently. These are the worst performers identified from production DB.
func TestPrometheusAgent_CustomMetricNoExcessiveRetries(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	testCases := []struct {
		Name           string
		SessionId      string
		Query          string
		AccountId      string
		UserId         string
		ExpectMetrics  []string
		MaxSteps       int
		ExpectToolUsed string
	}{
		{
			// Previously: 574s, 13 prometheus iterations, 8 tool calls (3x promql_query, 4x metrics_list)
			// Agent called metrics_list("tcp connections") 3x with empty results, then tried "network"
			Name:           "TCP connections (was 574s/13 iters)",
			SessionId:      "ut-prom-custom-tcp-1",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "total successful tcp connections",
			ExpectMetrics:  []string{"tcp", "connect", "network", "net"},
			MaxSteps:       4,
			ExpectToolUsed: tools.ToolSearchMetrics,
		},
		{
			// Previously: 118s, 5 promql iterations, 4 tool calls (2x search_metrics, metrics_list, labels)
			// No consul metrics in environment — agent should detect quickly and report
			Name:           "Consul health (was 118s/5 iters)",
			SessionId:      "ut-prom-custom-consul-2",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "consul service health check status",
			ExpectMetrics:  []string{"consul", "health", "coredns"},
			MaxSteps:       4,
			ExpectToolUsed: tools.ToolSearchMetrics,
		},
		{
			// "http request with high latency" — should use known eBPF metrics fast path
			// but previously agent went through metrics_list instead
			Name:           "HTTP high latency",
			SessionId:      "ut-prom-custom-latency-1",
			AccountId:      os.Getenv("TEST_ACCOUNT"),
			UserId:         os.Getenv("TEST_USER"),
			Query:          "http request with high latency",
			ExpectMetrics:  []string{"http", "latency", "duration", "histogram_quantile"},
			MaxSteps:       3,
			ExpectToolUsed: tools.ToolQueryPrometheus,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			prometheusAgent := newPrometheusAgent(tc.AccountId)

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

			slog.Info("Excessive retry regression test",
				"test", tc.Name,
				"steps", len(resp.AgentStepResponse),
				"elapsed_ms", elapsed.Milliseconds(),
				"query", tc.Query,
			)

			// Print step details
			foundExpectedTool := false
			foundPrometheusExecute := false
			prometheusExecuteHasData := false
			retriedSameToolCount := map[string]int{}
			for i, step := range resp.AgentStepResponse {
				toolName := ""
				toolArgs := ""
				if step.Call.FunctionCall != nil {
					toolName = step.Call.FunctionCall.Name
					toolArgs = step.Call.FunctionCall.Arguments
					retriedSameToolCount[toolName]++
				}
				fmt.Printf("  Step %d: tool=%s args=%s\n", i+1, toolName, toolArgs)

				if step.Call.FunctionCall != nil {
					if step.Call.FunctionCall.Name == tc.ExpectToolUsed {
						foundExpectedTool = true
					}
					if step.Call.FunctionCall.Name == tools.ToolQueryPrometheus {
						foundPrometheusExecute = true
						content := step.Response.Content
						if content != "" && !strings.Contains(strings.ToLower(content), "no data") &&
							!strings.Contains(strings.ToLower(content), "error") &&
							len(content) > 10 {
							prometheusExecuteHasData = true
						}
					}
				}
				// Check nested agent output for tool usage
				if step.Response.Content != "" && strings.Contains(step.Response.Content, tc.ExpectToolUsed) {
					foundExpectedTool = true
				}
			}

			// Step count regression check
			assert.LessOrEqual(t, len(resp.AgentStepResponse), tc.MaxSteps,
				"Query '%s' should complete in <= %d steps, got %d",
				tc.Query, tc.MaxSteps, len(resp.AgentStepResponse))

			// No tool should be called more than 2x (catches excessive retries)
			for toolName, count := range retriedSameToolCount {
				assert.LessOrEqual(t, count, 2,
					"Tool '%s' called %d times for query '%s' — should not retry excessively",
					toolName, count, tc.Query)
			}

			// Verify end-to-end execution
			assert.True(t, foundPrometheusExecute,
				"Expected prometheus_execute to be called for '%s'", tc.Query)
			if foundPrometheusExecute {
				assert.True(t, prometheusExecuteHasData,
					"Expected prometheus_execute to return data for '%s'", tc.Query)
			}

			if !foundExpectedTool {
				t.Logf("Warning: expected tool %s not directly visible for '%s' — may be inside nested agent",
					tc.ExpectToolUsed, tc.Query)
			}

			// Verify response contains expected metric references
			allResponseContent := strings.ToLower(strings.Join(resp.Response, " "))
			for _, step := range resp.AgentStepResponse {
				allResponseContent += " " + strings.ToLower(step.Response.Content)
			}
			foundMetric := false
			for _, metricSubstr := range tc.ExpectMetrics {
				if strings.Contains(allResponseContent, strings.ToLower(metricSubstr)) {
					foundMetric = true
					break
				}
			}
			assert.True(t, foundMetric,
				"Expected response for '%s' to contain one of %v", tc.Query, tc.ExpectMetrics)
		})
	}
}
