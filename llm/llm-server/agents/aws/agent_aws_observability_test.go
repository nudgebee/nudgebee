package aws

import (
	"testing"
	"time"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

func TestAwsObservabilityAgent_Interfaces(t *testing.T) {
	agent := newAwsObservabilityAgent("test-account")

	t.Run("implements NBAgentIterationProvider", func(t *testing.T) {
		iterProvider, ok := agent.(core.NBAgentIterationProvider)
		assert.True(t, ok, "agent should implement NBAgentIterationProvider")
		assert.Equal(t, 7, iterProvider.GetMaxIterations())
	})

	t.Run("implements NBAgentTimeoutProvider", func(t *testing.T) {
		timeoutProvider, ok := agent.(core.NBAgentTimeoutProvider)
		assert.True(t, ok, "agent should implement NBAgentTimeoutProvider")
		assert.Equal(t, 3*time.Minute, timeoutProvider.GetTimeout())
	})
}

func TestAwsObservabilityAgent_PlannerType(t *testing.T) {
	agent := &AwsObservabilityAgent{accountId: "test-account"}
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
}

func TestAwsObservabilityAgent_Metadata(t *testing.T) {
	agent := &AwsObservabilityAgent{accountId: "test-account"}
	assert.Equal(t, AgentAwsObservabilityName, agent.GetName())
	assert.NotEmpty(t, agent.GetDescription())
	assert.NotEmpty(t, agent.GetNameAliases())
}

func TestAwsSubAgents_Metadata(t *testing.T) {
	t.Run("aws_metrics", func(t *testing.T) {
		a := NewAwsMetricsAgent("test-account")
		assert.Equal(t, AwsMetricsAgentName, a.GetName())
		assert.NotEmpty(t, a.GetDescription())
		assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())
		assert.Contains(t, a.GetNameAliases(), "AWSMetrics")
	})

	t.Run("aws_logs", func(t *testing.T) {
		a := NewAwsLogsAgent("test-account")
		assert.Equal(t, AwsLogsAgentName, a.GetName())
		assert.NotEmpty(t, a.GetDescription())
		assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())
		assert.Contains(t, a.GetNameAliases(), "AWSLogs")
	})

	t.Run("aws_traces", func(t *testing.T) {
		a := NewAwsTracesAgent("test-account")
		assert.Equal(t, AwsTracesAgentName, a.GetName())
		assert.NotEmpty(t, a.GetDescription())
		assert.Equal(t, core.AgentPlannerTypeReAct, a.GetPlannerType())
		assert.Contains(t, a.GetNameAliases(), "AWSTraces")
	})
}
