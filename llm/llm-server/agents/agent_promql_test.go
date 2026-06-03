package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPromqlParsing(t *testing.T) {
	testCases := []struct {
		Input    string
		Expected []string
	}{
		{
			Input: `
				Here is a PromQL query: sum(rate(http_requests_total[5m])) by (job)
				Another: avg_over_time(cpu_usage[1h])
				max(memory_usage_bytes{instance="foo"})
				count(node_cpu_seconds_total{mode='idle'})
				foo
				just some text
			`,
			Expected: []string{
				"sum(rate(http_requests_total[5m])) by (job)",
				"avg_over_time(cpu_usage[1h])",
				"max(memory_usage_bytes{instance=\"foo\"})",
				"count(node_cpu_seconds_total{mode='idle'})",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Input, func(t *testing.T) {
			queries := ExtractPromQLExprs(tc.Input)
			assert.ElementsMatch(t, tc.Expected, queries)
		})
	}
}

// TestPromqlAgent_CustomMetricDiscoveryWithSearchMetrics tests that the promql agent
// uses search_metrics for custom metrics not in ground truth, and resolves them
// efficiently without excessive steps.

func TestGetSystemPrompt_PrintInstructions(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	l := &PromqlAgent{
		externalHostsCached: true,
	}

	query := core.NBAgentRequest{
		AccountId: "test-account",
	}

	systemPrompt := l.GetSystemPrompt(sc, query)

	b, err := json.MarshalIndent(systemPrompt, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal system prompt: %v", err)
	}

	fmt.Println("========== Generated System Prompt (JSON) ==========")
	fmt.Println(string(b))
	fmt.Println("========== End of System Prompt ==========")

	s := string(b)
	assert.NotEmpty(t, s, "system prompt JSON should not be empty")
	assert.True(t, strings.Contains(s, "Input Analysis"), "should contain 'Input Analysis' section")
	assert.True(t, strings.Contains(s, "OUTPUT CONTRACT"), "should contain 'OUTPUT CONTRACT' section")
}

func TestPromqlAgent_UpdateExecutorLlmResponse_InvalidFunction(t *testing.T) {
	agent := &PromqlAgent{}

	makeFinished := func(data string) *core.NBAgentPlannerFinishAction {
		return &core.NBAgentPlannerFinishAction{Data: data}
	}

	t.Run("non-standard function is rejected", func(t *testing.T) {
		query := `{"promql": "ts_of_max_over_time(rate(container_network_receive_bytes_total{namespace=\"nudgebee\"}[5m])[24h:])"}`
		actions, finished, err := agent.UpdateExecutorLlmResponse(nil, makeFinished(query), nil)
		assert.Nil(t, actions)
		assert.Nil(t, err)
		assert.NotNil(t, finished)
		assert.Contains(t, finished.Data, "unsupported function or syntax", "should surface a parse error")
	})

	t.Run("valid standard query passes through unchanged", func(t *testing.T) {
		query := `{"promql": "rate(container_cpu_usage_seconds_total{namespace=\"default\"}[5m])"}`
		_, finished, err := agent.UpdateExecutorLlmResponse(nil, makeFinished(query), nil)
		assert.Nil(t, err)
		assert.Equal(t, query, finished.Data, "valid query should not be modified")
	})

	t.Run("valid query with __CLUSTER__ placeholder passes", func(t *testing.T) {
		query := `{"promql": "rate(container_cpu_usage_seconds_total{__CLUSTER__ namespace=\"default\"}[5m])"}`
		_, finished, err := agent.UpdateExecutorLlmResponse(nil, makeFinished(query), nil)
		assert.Nil(t, err)
		assert.Equal(t, query, finished.Data, "query with __CLUSTER__ should not be modified")
	})

	t.Run("multiple queries — one invalid is caught", func(t *testing.T) {
		query := `{"promql": ["rate(container_cpu_usage_seconds_total[5m])", "ts_of_max_over_time(some_metric[1h])"]}`
		actions, finished, err := agent.UpdateExecutorLlmResponse(nil, makeFinished(query), nil)
		assert.Nil(t, actions)
		assert.Nil(t, err)
		assert.Contains(t, finished.Data, "unsupported function or syntax")
	})

	t.Run("nil finished passes through", func(t *testing.T) {
		actions, finished, err := agent.UpdateExecutorLlmResponse(nil, nil, nil)
		assert.Nil(t, actions)
		assert.Nil(t, finished)
		assert.Nil(t, err)
	})

	t.Run("non-JSON data passes through unchanged", func(t *testing.T) {
		data := "some plain text response"
		_, finished, err := agent.UpdateExecutorLlmResponse(nil, makeFinished(data), nil)
		assert.Nil(t, err)
		assert.Equal(t, data, finished.Data)
	})
}
