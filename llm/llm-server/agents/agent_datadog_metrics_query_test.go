package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidDatadogMetric(t *testing.T) {
	testCases := []struct {
		metricName  string
		shouldExist bool
	}{
		{
			metricName:  "kubernetes.cpu.usage.total",
			shouldExist: true,
		},
		{
			metricName:  "kubernetes.memory.usage",
			shouldExist: true,
		},
		{
			metricName:  "system.net.bytes_rcvd",
			shouldExist: true,
		},
		{
			metricName:  "container.cpu.usage",
			shouldExist: true,
		},
		{
			metricName:  "kubernetes_state.deployment.replicas",
			shouldExist: true,
		},
		{
			metricName:  "system.uptime",
			shouldExist: true,
		},
		{
			metricName:  "kubernetes.containers.restarts",
			shouldExist: true,
		},
		{
			metricName:  "invalid.metric.name",
			shouldExist: false,
		},
		{
			metricName:  "http.request.count",
			shouldExist: false,
		},
		{
			metricName:  "trace.http.request.hits",
			shouldExist: false,
		},
		{
			metricName:  "custom.application.metric",
			shouldExist: false,
		},
		{
			metricName:  "",
			shouldExist: false,
		},
	}

	for _, tc := range testCases {
		result := isValidDatadogMetric(tc.metricName)
		assert.Equal(t, tc.shouldExist, result)
	}
}

func TestDatadogMetricsQueryAgent_UpdateExecutorLlmResponse_ValidQueries(t *testing.T) {
	agent := DatadogMetricsQueryAgent{}

	testCases := []struct {
		query string
	}{
		{query: "avg:kubernetes.cpu.usage.total{pod_name:my-pod} by {pod_name}"},
		{query: "sum:system.net.bytes_rcvd{host:my-host} by {host}"},
		{query: "avg:container.memory.usage{container_name:app}"},
		{query: "sum:kubernetes_state.deployment.replicas{kube_namespace:default}"},
		{query: "avg:kubernetes.memory.usage{service IN (svc1,svc2,svc3)} by {service}"},
		{query: "avg:kubernetes.memory.usage{kube_namespace:production AND service IN (svc1,svc2)} by {service}"},
	}

	for _, tc := range testCases {
		finished := &core.NBAgentPlannerFinishAction{
			Data: tc.query,
		}

		_, resultFinished, err := agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, finished, nil)

		assert.NoError(t, err)
		assert.NotNil(t, resultFinished)
		assert.Equal(t, tc.query, resultFinished.Data)
	}
}

func TestDatadogMetricsQueryAgent_UpdateExecutorLlmResponse_InvalidQueries(t *testing.T) {
	agent := DatadogMetricsQueryAgent{}

	testCases := []struct {
		query         string
		errorContains string
	}{
		{
			query:         "avg:invalid.metric.name{tag:value}",
			errorContains: "invalid metric 'invalid.metric.name'",
		},
		{
			query:         "sum:http.request.count{service:my-service}",
			errorContains: "invalid metric 'http.request.count'",
		},
		{
			query:         "sum:trace.http.request.hits{status_code:5*}",
			errorContains: "invalid metric 'trace.http.request.hits'",
		},
		{
			query:         "avg:custom.application.metric{env:prod}",
			errorContains: "invalid metric 'custom.application.metric'",
		},
	}

	for _, tc := range testCases {
		finished := &core.NBAgentPlannerFinishAction{
			Data: tc.query,
		}

		_, resultFinished, err := agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, finished, nil)

		assert.Error(t, err)
		assert.Nil(t, resultFinished)
		assert.Contains(t, err.Error(), tc.errorContains)
	}
}

func TestDatadogMetricsQueryAgent_UpdateExecutorLlmResponse_EdgeCases(t *testing.T) {
	agent := DatadogMetricsQueryAgent{}

	// Empty query
	finished := &core.NBAgentPlannerFinishAction{Data: ""}
	_, resultFinished, err := agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, finished, nil)
	assert.NoError(t, err)
	assert.NotNil(t, resultFinished)
	assert.Equal(t, "", resultFinished.Data)

	// Nil finished
	_, resultFinished, err = agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, resultFinished)

	// Preserve original error
	originalError := fmt.Errorf("original error")
	finished = &core.NBAgentPlannerFinishAction{Data: "avg:kubernetes.cpu.usage.total{pod_name:my-pod}"}
	_, _, err = agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, finished, originalError)
	assert.Error(t, err)
	assert.Equal(t, originalError, err)

	// Invalid query format
	finished = &core.NBAgentPlannerFinishAction{Data: "invalid query format"}
	_, resultFinished, err = agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, finished, nil)
	assert.NoError(t, err)
	assert.NotNil(t, resultFinished)
}

func TestDatadogMetricsQueryAgent_UpdateExecutorLlmResponse_ErrorMessage(t *testing.T) {
	agent := DatadogMetricsQueryAgent{}

	finished := &core.NBAgentPlannerFinishAction{
		Data: "sum:http.request.count{service:test}",
	}

	_, resultFinished, err := agent.UpdateExecutorLlmResponse([]core.NBAgentPlannerToolAction{}, finished, nil)

	assert.Error(t, err)
	assert.Nil(t, resultFinished)

	expectedError := fmt.Errorf("invalid metric 'http.request.count' used in query. This metric is not in the available metrics list. Please use the metrics_list tool to find a valid metric")
	assert.Equal(t, expectedError.Error(), err.Error())
}
