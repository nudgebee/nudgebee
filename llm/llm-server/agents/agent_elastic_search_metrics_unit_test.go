package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestElasticSearchMetricsAgent_Properties(t *testing.T) {
	agent := ElasticSearchMetricsAgent{accountId: "test-account"}

	assert.Equal(t, ElasticSearchMetricsAgentName, agent.GetName())
	assert.Contains(t, agent.GetNameAliases(), "Elastic Search Metrics")
	assert.NotEmpty(t, agent.GetDescription())
	assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
	assert.Equal(t, 10, agent.GetMaxIterations())
}

func TestElasticSearchMetricsAgent_SystemPrompt(t *testing.T) {
	agent := ElasticSearchMetricsAgent{accountId: "test-account"}
	sc := security.NewRequestContextForSuperAdmin()
	query := core.NBAgentRequest{Query: "average cpu usage"}

	prompt := agent.GetSystemPrompt(sc, query)

	assert.NotEmpty(t, prompt.Role)
	assert.NotEmpty(t, prompt.Instructions)
	assert.NotEmpty(t, prompt.Constraints)
	assert.NotEmpty(t, prompt.OutputFormat)

	// Verify that Elasticsearch instructions are present
	foundES := false
	for _, inst := range prompt.Instructions {
		if containsIgnoreCase(inst, "Elasticsearch") || containsIgnoreCase(inst, "Opensearch") {
			foundES = true
			break
		}
	}
	assert.True(t, foundES, "System prompt should contain Elasticsearch/Opensearch instructions")
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
