package agents

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// The remediation agent lists the alert's tools for a tenant with the loop on (the plan's, or the follow-up
// check's in a read-only run), and each request's mode narrows them at dispatch. Anywhere else it is as before.
func TestRemediationAlertPlanMode(t *testing.T) {
	const enabled, other = "cccccccc-cccc-cccc-cccc-cccccccccccc", "dddddddd-dddd-dddd-dddd-dddddddddddd"
	loopOnFor(t, enabled)
	providerIs(t, "K8s")
	ctx, check := security.NewRequestContextForSuperAdmin(), core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin())
	agent := RemediationAgent{accountId: enabled}
	read := tools.ToolExecuteKubectlCommand
	base := []string{"remediation_generate", "remediation_execute"}
	planTools := []string{read, "get_remediation_plan", "propose_fix", "run_step"}
	checkTools := []string{read, "get_remediation_plan", "propose_fix", "schedule_recheck", "prometheus_execute", "get_event_by_id", "get_event_rules"}

	assert.ElementsMatch(t, append(base, planTools...), toolNames(agent.GetSupportedTools(ctx)), "listed explicitly")
	assert.ElementsMatch(t, append(base, checkTools...), toolNames(agent.GetSupportedTools(check)), "the check's in a read-only run")
	assert.ElementsMatch(t, base, toolNames(RemediationAgent{accountId: other}.GetSupportedTools(ctx)), "loop off: as on main")

	alertChat := core.NBAgentRequest{AccountId: enabled, SessionId: "event-fp-1"}
	assert.Equal(t, planTools, toolNames(agent.GetSupportedToolsForRequest(ctx, alertChat)))
	_, _, err := core.IsAgentToolAuthorizedToProcessRequest(ctx, agent, alertChat, core.NBAgentPlannerToolAction{Tool: "remediation_execute"})
	assert.Error(t, err, "a step runs through run_step, recorded on the plan")

	// NudgeBee's follow-up check: reads, may propose or ask to look again; no run_step, and nothing but its own
	// tools (StrictReadOnly). Its CLI tool runs only strict reads there (tools.readOnlyCheckRefusal).
	followup := alertChat
	followup.QueryConfig = toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true}
	assert.ElementsMatch(t, checkTools, toolNames(agent.GetSupportedToolsForRequest(check, followup)))
	assert.Contains(t, agent.GetSystemPrompt(check, followup).Role, "reading only")
	for _, name := range []string{"run_step", "remediation_execute", toolcore.ToolExecuteShellCommand, "watch_resource", "aws_execute"} {
		_, _, err := core.IsAgentToolAuthorizedToProcessRequest(check, agent, followup, core.NBAgentPlannerToolAction{Tool: name})
		assert.Error(t, err, "refused in the follow-up check: "+name)
	}
	// Another read-only turn there (not the follow-up check) gets no CLI tool: nothing would vet its reads.
	assert.NotContains(t, toolNames(agent.GetSupportedToolsForRequest(check, alertChat)), read)

	// Review 3 bug 7: an RCA session (event-rca-<fp>) is not the alert's conversation.
	assert.Equal(t, remediationModeInvestigation, resolveRemediationMode(core.NBAgentRequest{AccountId: enabled, SessionId: "event-rca-fp-1"}))
	// Outside an alert's conversation the agent keeps main's tools; with the loop on it also lists the plan's,
	// which refuse there (api's tests).
	for _, account := range []string{other, enabled} {
		got := toolNames(RemediationAgent{accountId: account}.GetSupportedToolsForRequest(ctx, core.NBAgentRequest{AccountId: account, SessionId: "C1-1700000000.1"}))
		assert.Contains(t, got, "remediation_execute", account)
		assert.Equal(t, account == enabled, slices.Contains(got, "run_step"), account)
	}
	recommendation := core.NBAgentRequest{AccountId: enabled, SessionId: "event-fp-1", QueryConfig: toolcore.NBQueryConfig{RecommendationId: "r1"}}
	assert.Equal(t, remediationModeRecommendation, resolveRemediationMode(recommendation), "a recommendation keeps its own mode")
	assert.Contains(t, agent.GetSystemPrompt(ctx, alertChat).Instructions[1], "get_remediation_plan")
}

// The orchestrators list the plan tools for a tenant with the loop on, so a fix asked for in an alert's chat
// can run as the plan's step; each tool refuses outside an alert's conversation. Not in the automatic
// investigation, which only reads, and not for other tenants.
func TestOrchestratorsGetAlertPlanTools(t *testing.T) {
	const enabled, other = "cccccccc-cccc-cccc-cccc-cccccccccccc", "dddddddd-dddd-dddd-dddd-dddddddddddd"
	loopOnFor(t, enabled)
	ctx := security.NewRequestContextForSuperAdmin()
	for _, account := range []string{enabled, other} {
		for _, orchestrator := range []core.NBAgent{&K8sLeanAgent{accountId: account}, &AwsOrchestratorAgent{accountId: account},
			&GcpOrchestratorAgent{accountId: account}, &AzureOrchestratorAgent{accountId: account}} {
			listed := toolNames(orchestrator.GetSupportedTools(ctx))
			investigation := toolNames(orchestrator.GetSupportedTools(core.WithReadOnlyRun(ctx)))
			for _, name := range remediationAlertPlanTools {
				assert.NotContains(t, investigation, name)
				if account == enabled {
					assert.Contains(t, listed, name)
				} else {
					assert.NotContains(t, listed, name)
				}
			}
		}
	}
}

// The follow-up check runs as NudgeBee with no person to approve anything, so what keeps it from changing
// the account is that it dispatches only its own tools (above), and that each of them is a read by its own
// static rules: nothing reaches "no classifier decided, running as read", and no model decides.
func TestAlertCheckToolsAreStaticReads(t *testing.T) {
	const enabled = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	loopOnFor(t, enabled)
	providerIs(t, "K8s")
	check := core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin())
	request := core.NBAgentRequest{AccountId: enabled, SessionId: "event-fp-1", QueryConfig: toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true}}
	supported := (RemediationAgent{accountId: enabled}).GetSupportedToolsForRequest(check, request)
	require.Len(t, supported, 1+len(remediationAlertCheckTools), "every check tool is registered")
	for _, tool := range supported {
		if _, standIn := tool.(alertToolStandIn); standIn {
			continue // api's TestAlertToolsClassifyThemselves
		}
		inference, ok := tool.(toolcore.ToolRequestInference)
		require.True(t, ok, "%s classifies its calls itself", tool.Name())
		requestType, err := inference.InferToolRequestType(check, tool.Name(), `{"query":"up","command":"kubectl get pods -n shop"}`)
		require.NoError(t, err, tool.Name())
		assert.Equal(t, toolcore.ToolRequestTypeRead, requestType, tool.Name())
	}
}

// Live run, 20 Oct: the investigation critique rejected the follow-up's status report until only "..." was
// left. With the loop on the remediation agent's answers are not investigations; with it off, as on main.
func TestRemediationAgentSkipsTheInvestigationCritique(t *testing.T) {
	loopOnFor(t, "acc-on")
	for account, want := range map[string]bool{"acc-on": false, "acc-off": true} {
		var agent core.NBAgent = RemediationAgent{accountId: account}
		critique, ok := agent.(core.NBAgentReActPlannerCritiqueSupport)
		require.True(t, ok)
		assert.Equal(t, want, critique.CritiqueEnabled(), account)
	}
}

// Review 4: with the loop off the session is never read (it can be a database query), for every orchestrator turn.
func TestRemediationModeReadsNoSessionWithTheLoopOff(t *testing.T) {
	loopOnFor(t)
	prevSession := requestSession
	requestSession = func(core.NBAgentRequest) string { t.Fatal("the session was read with the loop off"); return "" }
	t.Cleanup(func() { requestSession = prevSession })
	assert.Equal(t, remediationModeInvestigation, resolveRemediationMode(core.NBAgentRequest{AccountId: "a", ConversationId: "c"}))
}

// The alert's tools are defined and registered by the api package, which this test binary does not link:
// stand-ins under their names let the agents' lists be checked here. api's tests check the tools themselves.
type alertToolStandIn struct{ name string }

func (s alertToolStandIn) Name() string               { return s.name }
func (alertToolStandIn) Description() string          { return "" }
func (alertToolStandIn) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (s alertToolStandIn) InferToolRequestType(*security.RequestContext, string, string) (toolcore.ToolRequestType, error) {
	if s.name == toolcore.ToolRunStep {
		return toolcore.ToolRequestTypeCreate, nil
	}
	return toolcore.ToolRequestTypeRead, nil
}
func (alertToolStandIn) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (alertToolStandIn) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

func init() {
	for _, name := range []string{toolcore.ToolGetRemediationPlan, toolcore.ToolProposeFix, toolcore.ToolRunStep, toolcore.ToolScheduleRecheck} {
		toolcore.RegisterNBToolFactory(name, func(string) (toolcore.NBTool, error) { return alertToolStandIn{name: name}, nil })
	}
}

// providerIs stubs every account's cloud as provider.
func providerIs(t *testing.T, provider string) {
	prev := alertAccountProvider
	alertAccountProvider = func(string) string { return provider }
	t.Cleanup(func() { alertAccountProvider = prev })
}

// The account's own CLI tool: kubectl_execute on Kubernetes, its cloud's CLI elsewhere, none when unknown.
func TestAlertCliTool(t *testing.T) {
	for provider, want := range map[string]string{"K8s": "kubectl_execute", "AWS": "aws_execute", "Azure": "azure_execute", "GCP": "gcloud_execute", "": ""} {
		providerIs(t, provider)
		tool, ok := alertCliTool("account-1")
		assert.Equal(t, want != "", ok, provider)
		if ok {
			assert.Equal(t, want, tool.Name(), provider)
		}
	}
}

// loopOnFor turns the remediation loop on for accounts' tenants only, for the test.
func loopOnFor(t *testing.T, accounts ...string) {
	prev := events.RemediationLoopOn
	events.RemediationLoopOn = func(accountId string) bool { return slices.Contains(accounts, accountId) }
	t.Cleanup(func() { events.RemediationLoopOn = prev })
}

// Review of #41596: on a cloud account the follow-up check gets no CLI (no deny-list covers every secret read);
// a person's alert chat keeps it.
func TestAlertCheckGetsNoCloudCli(t *testing.T) {
	const enabled = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	loopOnFor(t, enabled)
	providerIs(t, "AWS")
	agent, chat := RemediationAgent{accountId: enabled}, core.NBAgentRequest{AccountId: enabled, SessionId: "event-fp-1"}
	assert.Contains(t, toolNames(agent.GetSupportedToolsForRequest(security.NewRequestContextForSuperAdmin(), chat)), tools.ToolExecuteAwsCliCommand)
	followup := chat
	followup.QueryConfig = toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true}
	assert.NotContains(t, toolNames(agent.GetSupportedToolsForRequest(core.WithReadOnlyRun(security.NewRequestContextForSuperAdmin()), followup)), tools.ToolExecuteAwsCliCommand)
	// Nor does its prompt offer one (review of #41596).
	prompt := strings.Join(remediationAlertCheckPrompt().Instructions, " ")
	for _, cli := range []string{tools.ToolExecuteAwsCliCommand, tools.ToolExecuteAzureCliCommand, tools.ToolExecuteGcpCliCommand} {
		assert.NotContains(t, prompt, cli)
	}
}
