package agents

import (
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemAgentCatalogListsEachAgentOnce: the Admin → Agents table and the
// Nubi picker show one row per system agent. A legacy name registered as its
// own factory key (instead of as an alias) lists the agent twice, and the
// "All accounts" view listed every alias as its own row (#39310).
func TestSystemAgentCatalogListsEachAgentOnce(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	seen := map[string]string{}
	for _, a := range core.ListAgentsForTenant(ctx, false) {
		if a.Type != core.AgentTypeSystem {
			continue
		}
		assert.False(t, core.IsSystemAgentAlias(a.Name), "alias %q listed as its own agent", a.Name)
		displayName := a.Name
		if len(a.Aliases) > 0 {
			displayName = a.Aliases[0]
		}
		if prev, dup := seen[displayName]; dup {
			t.Errorf("%q listed twice: as %q and %q", displayName, prev, a.Name)
		}
		seen[displayName] = a.Name
	}
}

func TestDatadogOrchestratorLegacyNameIsAlias(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent, ok := core.GetNBAgent(ctx, "datadog_debug", "account-1", "")
	require.True(t, ok, "legacy name must still resolve for stored history")
	assert.IsType(t, &DatadogOrchestratorAgent{}, agent)
	assert.True(t, core.IsSystemAgentAlias("datadog_debug"))
	assert.False(t, core.IsSystemAgentAlias(AgentDatadogOrchestratorName))
}
