package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedSearchLegacyReferences(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for _, name := range []string{WebSearchAgentName, "websearch_old"} {
		agent, ok := core.GetNBAgent(ctx, name, "account-1", "")
		require.True(t, ok, "%s must resolve for saved conversations", name)
		assert.IsType(t, UnifiedSearchAgent{}, agent)
		assert.Equal(t, WebSearchAgentName, agent.GetName())
	}
	tool, ok := toolcore.GetNBTool("account-1", WebSearchAgentName)
	require.True(t, ok)
	assert.Equal(t, WebSearchAgentName, tool.Name())
	assert.True(t, core.IsSystemAgentAlias("websearch_old"))
	assert.False(t, core.IsSystemAgentAlias(WebSearchAgentName))
	assert.NotContains(t, toolcore.ListRegisteredSystemToolNames(), "websearch_old")
}
