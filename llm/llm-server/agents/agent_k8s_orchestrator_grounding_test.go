package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
)

func TestK8sGroundingNudgeScope(t *testing.T) {
	original := config.Config.OrchestratorGroundingEnabled
	t.Cleanup(func() { config.Config.OrchestratorGroundingEnabled = original })

	config.Config.OrchestratorGroundingEnabled = true
	assert.NotEmpty(t, k8sGroundingIfEnabled(core.NBAgentRequest{AgentId: "top"}))
	assert.Empty(t, k8sGroundingIfEnabled(core.NBAgentRequest{AgentId: "child", ParentAgentId: "top"}))

	config.Config.OrchestratorGroundingEnabled = false
	assert.Empty(t, k8sGroundingIfEnabled(core.NBAgentRequest{AgentId: "top"}))
}
