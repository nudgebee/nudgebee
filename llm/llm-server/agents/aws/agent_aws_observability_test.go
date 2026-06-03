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

func TestAwsObservabilityAgent_ExampleCount(t *testing.T) {
	agent := &AwsObservabilityAgent{accountId: "test-account"}
	prompt := agent.GetSystemPrompt(nil, core.NBAgentRequest{})
	assert.LessOrEqual(t, len(prompt.Examples), 12,
		"examples should be consolidated to reduce prompt size and latency")
	assert.GreaterOrEqual(t, len(prompt.Examples), 8,
		"should retain enough examples to cover distinct AWS observability patterns")
}

func TestAwsObservabilityAgent_Metadata(t *testing.T) {
	agent := &AwsObservabilityAgent{accountId: "test-account"}
	assert.Equal(t, AgentAwsObservabilityName, agent.GetName())
	assert.NotEmpty(t, agent.GetDescription())
	assert.NotEmpty(t, agent.GetNameAliases())
}
