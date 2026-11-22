package agents

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// NudgeBee's background planning turn (#40734) gets its own prompt, which asks for the plan with its cause key and
// goal, and the check's reads plus propose_fix, without schedule_recheck. The follow-up check is unchanged.
func TestRemediationPlanningTurn(t *testing.T) {
	const enabled = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	loopOnFor(t, enabled)
	providerIs(t, "K8s")
	check := core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin())
	agent := RemediationAgent{accountId: enabled}
	planning := core.NBAgentRequest{AccountId: enabled, SessionId: "event-fp-1",
		QueryConfig: toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true, SkipConversationHistory: true, RemediationPlanTurn: "auto"}}

	assert.ElementsMatch(t, []string{tools.ToolExecuteKubectlCommand, "get_remediation_plan", "propose_fix", "prometheus_execute", "get_event_by_id", "get_event_rules"},
		toolNames(agent.GetSupportedToolsForRequest(check, planning)))
	prompt := agent.GetSystemPrompt(check, planning)
	assert.Equal(t, remediationAlertPlanningPrompt().Role, prompt.Role)
	text := strings.Join(prompt.Instructions, " ")
	for _, want := range []string{"propose_fix", "cause_key", "goal", "empty actions list"} {
		assert.Contains(t, text, want)
	}
	_, _, err := core.IsAgentToolAuthorizedToProcessRequest(check, agent, planning, core.NBAgentPlannerToolAction{Tool: "run_step"})
	assert.Error(t, err, "the planning turn changes nothing")

	followup := planning
	followup.QueryConfig.RemediationPlanTurn = ""
	assert.Equal(t, remediationAlertCheckPrompt().Role, agent.GetSystemPrompt(check, followup).Role, "the follow-up check keeps its prompt")
	assert.Contains(t, toolNames(agent.GetSupportedToolsForRequest(check, followup)), "schedule_recheck")
}
