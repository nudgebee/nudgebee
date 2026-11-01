package core

import (
	"testing"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// Regression for #39719. doAction used to substitute the agent's first
// configurable tool ("borrow") when the called tool declared no config of its
// own, then resolve an integration for that borrowed tool. The result was
// unusable — FollowupRequestForMultipleToolConfigs matches on action.Tool, so a
// borrowed check can never produce a question — and on the FinOps agent it cost
// a reasoning-tier model call per iteration that answered UNCERTAIN 92% of the
// time. The borrow had already broken shell target resolution once (#38175).
//
// Only the called tool's NBToolConfig decides now. toolResolvesOwnConfig takes
// no agent, so no caller can reintroduce the substitution by accident.
func TestToolResolvesOwnConfigIgnoresOtherTools(t *testing.T) {
	configurable := &MockConfigurableTool{MockContextCapturingTool{NameVal: "ticket_master_v2"}}
	plain := &MockContextCapturingTool{NameVal: "recommendation_execute"}

	require.True(t, toolResolvesOwnConfig(configurable),
		"a tool declaring ConfigSchema must resolve its own integration")
	require.False(t, toolResolvesOwnConfig(plain),
		"a tool declaring no ConfigSchema must not trigger integration resolution")
	require.False(t, toolResolvesOwnConfig(nil), "nil tool must not panic or resolve")

	// The agent owning both tools is irrelevant: the decision is per-tool.
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{configurable, plain}}
	require.Len(t, agent.GetSupportedTools(security.NewRequestContextForSuperAdmin()), 2)
	require.False(t, toolResolvesOwnConfig(plain),
		"the agent's other tools must not make a non-configurable tool resolve")
}

// A configurable tool wrapped as a non-leaf (agent/sub-agent) type resolves its
// config through its own execution, not through the caller's per-action check.
func TestToolResolvesOwnConfigLeafToolsOnly(t *testing.T) {
	nonLeaf := &MockAgentTypeTool{MockContextCapturingTool{NameVal: "sub_agent"}}
	require.NotEqual(t, toolcore.NBToolTypeTool, nonLeaf.GetType(), "fixture must be non-leaf")
	require.False(t, toolResolvesOwnConfig(nonLeaf))
}

// The shell tool keeps reaching config resolution through configOwnerForAction,
// which maps a shell command to the cloud tool that owns the credential. That
// owner's name differs from action.Tool by design, so "borrowed" can never be
// inferred from name inequality — this is what #38175 had to restore.
func TestShellCommandStillResolvesThroughConfigOwner(t *testing.T) {
	shell, ok := toolcore.GetNBTool("shell-owner-test", toolcore.ToolExecuteShellCommand)
	if !ok || shell == nil {
		t.Skip("shell tool not registered in this build")
	}
	owner := configOwnerForAction(shell, "shell-owner-test", `{"command":"aws s3 ls"}`)
	require.NotEqual(t, shell.Name(), owner.Name(),
		"a shell aws command must resolve to the aws tool, not the shell tool")
	require.True(t, toolResolvesOwnConfig(owner),
		"the resolved owner must still be asked for its integration")
	require.Equal(t, tools.ToolExecuteAwsCliCommand, owner.Name())
}

// The case the borrow was added for (#25869): the TicketV2 agent's first action
// is often ask_clarification, and the intent was to get "which ticketing
// system?" answered before the ticket tool runs.
//
// Both halves are asserted against the builder that actually decides the
// question, with two integrations configured:
//
//   - ask_clarification (what the borrow fired on) yields NO question, because
//     the builder matches on action.Tool. This is why the borrow never achieved
//     what it was written for, and why removing it loses nothing.
//   - the ticket tool itself still yields the question, with both integrations
//     offered. The ask is not lost; it happens when the tool is really called.
func TestTicketFlowStillAsksWhenTheTicketToolIsCalled(t *testing.T) {
	const account = "ticket-early-ask-test"
	const ticketTool = "ticket_master_v2"

	seedConfigs(t, account, ticketTool, "jira-primary", "servicenow-eu")

	ticket := &MockConfigurableTool{MockContextCapturingTool{NameVal: ticketTool}}
	askClarification := &MockContextCapturingTool{NameVal: "ask_clarification"}
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{askClarification, ticket}}

	ctx := security.NewRequestContextForSuperAdmin()
	query := NBAgentRequest{AccountId: account, AgentId: uuid.NewString()}

	borrowed, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent,
		NBAgentPlannerToolAction{Tool: askClarification.Name(), ToolID: "call-1"})
	require.NoError(t, err)
	require.Empty(t, borrowed.Question,
		"a check on a non-configurable tool can never ask: the borrow was dead code")

	direct, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent,
		NBAgentPlannerToolAction{Tool: ticketTool, ToolID: "call-2"})
	require.NoError(t, err)
	require.NotEmpty(t, direct.Question, "the ticket tool itself must still ask")
	require.Equal(t, ticketTool, direct.ToolName)
	require.ElementsMatch(t, []string{"jira-primary", "servicenow-eu"}, direct.FollowupOptions)
}

// seedConfigs registers tool configs for an account so resolution reads them
// from cache instead of the database, and removes the key when the test ends so
// the shared cache does not leak between tests.
func seedConfigs(t *testing.T, account, toolName string, names ...string) {
	t.Helper()
	configs := make([]toolcore.ToolConfig, 0, len(names))
	for _, n := range names {
		configs = append(configs, toolcore.ToolConfig{Name: n})
	}
	data, err := common.MarshalJson(configs)
	require.NoError(t, err)
	key := "list_tool_configs:" + account + ":" + toolName
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, key, data))
	t.Cleanup(func() {
		_ = common.CacheDelete(toolcore.CacheNamespaceLlmToolConfig, key)
	})
}

// Resolution outcomes for a configurable tool called directly, which is the only
// path left after #39719. Each case previously also ran against a borrowed tool
// and is now reached once, at the right moment.
func TestConfiguredToolResolutionOutcomes(t *testing.T) {
	const toolName = "ticket_master_v2"
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("sole config auto-selects without asking", func(t *testing.T) {
		const account = "resolve-sole-config"
		seedConfigs(t, account, toolName, "jira-only")
		tool := &MockConfigurableTool{MockContextCapturingTool{NameVal: toolName}}
		e := plannerExecutor{ctx: ctx, agent: &MockAgent{SupportedTools: []toolcore.NBTool{tool}},
			agentRequest: NBAgentRequest{AccountId: account, AgentId: uuid.NewString()}}
		steps, finish, err := e.followupForMultipleToolConfigs(tool,
			NBAgentPlannerToolAction{Tool: toolName, ToolID: "c1"})
		require.NoError(t, err)
		require.Empty(t, steps)
		require.Nil(t, finish, "one integration must be chosen automatically, never asked")
		require.Equal(t, "jira-only", e.agentRequest.QueryConfig.ToolConfigs[toolName])
	})

	t.Run("already chosen config is not asked again", func(t *testing.T) {
		const account = "resolve-already-chosen"
		seedConfigs(t, account, toolName, "jira-primary", "servicenow-eu")
		tool := &MockConfigurableTool{MockContextCapturingTool{NameVal: toolName}}
		agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
		action := NBAgentPlannerToolAction{Tool: toolName, ToolID: "c2"}

		// Control: the same account and configs DO produce a question when nothing
		// has been chosen yet, so the empty result below is caused by the existing
		// choice and not by a mis-seeded fixture.
		control, err := FollowupRequestForMultipleToolConfigs(ctx,
			NBAgentRequest{AccountId: account, AgentId: uuid.NewString()}, agent, action)
		require.NoError(t, err)
		require.NotEmpty(t, control.Question, "control: an unchosen conversation must be asked")

		query := NBAgentRequest{AccountId: account, AgentId: uuid.NewString(),
			QueryConfig: toolcore.NBQueryConfig{ToolConfigs: map[string]string{toolName: "jira-primary"}}}
		req, err := FollowupRequestForMultipleToolConfigs(ctx, query, agent, action)
		require.NoError(t, err)
		require.Empty(t, req.Question, "a conversation that already chose must not be re-asked")
	})

	t.Run("no configured integrations does not ask", func(t *testing.T) {
		const account = "resolve-zero-configs"
		seedConfigs(t, account, toolName)
		tool := &MockConfigurableTool{MockContextCapturingTool{NameVal: toolName}}
		agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
		req, err := FollowupRequestForMultipleToolConfigs(ctx,
			NBAgentRequest{AccountId: account, AgentId: uuid.NewString()}, agent,
			NBAgentPlannerToolAction{Tool: toolName, ToolID: "c3"})
		require.NoError(t, err)
		require.Empty(t, req.Question, "nothing to choose from is a tool failure, not a question")
	})

	t.Run("action tool casing still matches", func(t *testing.T) {
		const account = "resolve-casing"
		seedConfigs(t, account, toolName, "jira-primary", "servicenow-eu")
		tool := &MockConfigurableTool{MockContextCapturingTool{NameVal: toolName}}
		agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
		req, err := FollowupRequestForMultipleToolConfigs(ctx,
			NBAgentRequest{AccountId: account, AgentId: uuid.NewString()}, agent,
			NBAgentPlannerToolAction{Tool: "TICKET_MASTER_V2", ToolID: "c4"})
		require.NoError(t, err)
		require.NotEmpty(t, req.Question, "doAction dispatches case-insensitively; resolution must too")
		require.Equal(t, toolName, req.ToolName)
	})
}

// Every aliased cloud tool must still reach its own integration through
// configOwnerForAction, not just aws (the #38175 path).
func TestShellOwnersForEveryAliasedCloudTool(t *testing.T) {
	shell, ok := toolcore.GetNBTool("shell-owner-matrix", toolcore.ToolExecuteShellCommand)
	if !ok || shell == nil {
		t.Skip("shell tool not registered in this build")
	}
	for _, tc := range []struct{ command, wantTool string }{
		{`{"command":"aws s3 ls"}`, tools.ToolExecuteAwsCliCommand},
		{`{"command":"kubectl get pods"}`, tools.ToolExecuteKubectlCommand},
	} {
		owner := configOwnerForAction(shell, "shell-owner-matrix", tc.command)
		require.Equal(t, tc.wantTool, owner.Name(), tc.command)
		require.True(t, toolResolvesOwnConfig(owner),
			"%s must still resolve its own integration", tc.wantTool)
	}
}

// #38596: the first pick for an integration tool takes the connection the call
// names with `instance` — exactly, so a database or host name is not a pick —
// for tools with no matching of their own (MySQL, MSSQL, ArgoCD, Datadog, New
// Relic), as Postgres already did through its own.
func TestFirstPickTakesTheInstanceTheCallNames(t *testing.T) {
	const account = "first-pick-instance"
	ctx := security.NewRequestContextForSuperAdmin()
	tool := tools.MySQLExecuteTool{}
	seedConfigs(t, account, tool.Name(), "mysql-1", "mysql-2")
	e := plannerExecutor{ctx: ctx, agent: &MockAgent{SupportedTools: []toolcore.NBTool{tool}},
		agentRequest: NBAgentRequest{AccountId: account, AgentId: uuid.NewString()}}
	steps, finish, err := e.followupForMultipleToolConfigs(tool,
		NBAgentPlannerToolAction{Tool: tool.Name(), ToolID: "c1", ToolInput: `{"query":"select 1","instance":"MYSQL-2"}`})
	require.NoError(t, err)
	require.Empty(t, steps)
	require.Nil(t, finish, "the named connection is the pick; nothing to ask")
	require.Equal(t, "mysql-2", e.agentRequest.QueryConfig.ToolConfigs[tool.Name()])

	configs := []toolcore.ToolConfig{{Name: "mysql-1"}, {Name: "mysql-2"}}
	_, ok := tools.IntegrationNamedIn(configs, toolcore.ParseNBToolCallRequestFromInput(`{"query":"select 1","instance":"orders"}`))
	require.False(t, ok, "a database name is no pick; the planner's other ways apply as before")
	_, ok = tools.IntegrationNamedIn(configs, toolcore.ParseNBToolCallRequestFromInput(`{"query":"select 1"}`))
	require.False(t, ok, "a call naming nothing picks as before")
}
