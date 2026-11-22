package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTicketV2Agent_AgentMetadata(t *testing.T) {
	agent := TicketMasterV2Agent{}

	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, "tickets_v2", agent.GetName())
	})

	t.Run("Aliases", func(t *testing.T) {
		aliases := agent.GetNameAliases()
		assert.Contains(t, aliases, "TicketsV2")
	})

	t.Run("PlannerType", func(t *testing.T) {
		assert.Equal(t, core.AgentPlannerTypeReAct, agent.GetPlannerType())
	})

	t.Run("Description", func(t *testing.T) {
		desc := agent.GetDescription()
		assert.Contains(t, desc, "Jira")
		assert.Contains(t, desc, "GitHub")
		assert.Contains(t, desc, "GitLab")
		assert.Contains(t, desc, "ServiceNow")
		assert.Contains(t, desc, "PagerDuty")
		assert.Contains(t, desc, "ZenDuty")
		assert.Contains(t, desc, "Lists tickets")
	})

	t.Run("SupportedTools", func(t *testing.T) {
		sc := security.NewRequestContextForSuperAdmin()
		supportedTools := agent.GetSupportedTools(sc)
		assert.GreaterOrEqual(t, len(supportedTools), 1)

		toolNames := make([]string, len(supportedTools))
		for i, tool := range supportedTools {
			toolNames[i] = tool.Name()
		}
		assert.Contains(t, toolNames, "ticket_master_v2")
	})

	t.Run("SystemPrompt", func(t *testing.T) {
		sc := security.NewRequestContextForSuperAdmin()
		prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{})
		assert.Equal(t, "An intelligent multi-platform ticketing assistant", prompt.Role)
		assert.NotEmpty(t, prompt.Instructions)
		assert.NotEmpty(t, prompt.Constraints)
		assert.NotEmpty(t, prompt.Examples)
		assert.Equal(t, "Markdown", prompt.OutputFormat)

		// Verify key instructions are present (includes list_tickets instruction)
		assert.GreaterOrEqual(t, len(prompt.Instructions), 8)
		assert.GreaterOrEqual(t, len(prompt.Constraints), 5)

		// Verify tool usage includes ticket_master_v2
		assert.Contains(t, prompt.ToolUsage, "ticket_master_v2")
	})
}

// The retired v1 names keep resolving to v2: the router's keywords, the agent
// name "tickets" (stored history, @tickets) and both old tool names.
func TestRetiredTicketsV1NamesResolveToV2(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for _, name := range []string{"tickets", "ticket", "jira", TicketsV2AgentName} {
		agent, ok := getAgent(ctx, name, "account-1")
		assert.True(t, ok, name)
		if ok {
			assert.Equal(t, TicketsV2AgentName, agent.GetName(), name)
		}
	}
	agent, ok := core.GetNBAgent(ctx, "tickets", "account-1", core.AgentStatusEnabled)
	assert.True(t, ok)
	if ok {
		assert.Equal(t, TicketsV2AgentName, agent.GetName())
	}
	assert.Equal(t, TicketsV2AgentName, toolcore.ResolveNBToolAlias("tickets"))
	assert.Equal(t, tools.TicketMasterToolNameV2, toolcore.ResolveNBToolAlias("ticket_master"))
}
