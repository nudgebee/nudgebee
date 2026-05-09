package agents

import (
	"context"
	"testing"

	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBasicShellScript(t *testing.T) {
	cfg, err := config.LoadConfig()
	require.NoError(t, err)

	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		assert.NotNil(t, err)
		return
	}
	orchestratorAgent := NewOrchestratorAgent(cfg, llmClient, nil, nil)
	assert.NotNil(t, orchestratorAgent)
	assert.Equal(t, "orchestrator_agent", orchestratorAgent.GetName())

	request := NBAgentRequest{
		Query:          "can you list files in my current directory?",
		AccountId:      "test-account",
		AgentId:        "orchestrator_agent",
		ConversationId: "test-conversation",
		ParentAgentId:  "",
		MessageId:      "test-message",
		UserId:         "test-user",
	}
	response, err := orchestratorAgent.Execute(context.Background(), request)
	assert.Nil(t, err)
	assert.NotNil(t, response)

}
