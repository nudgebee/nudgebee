package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// decidingPolicy is test_ext's policy deciding every write at the call: it
// counts CheckTool and records each StartToolCall with the account it lands
// on, answering run or not.
type decidingPolicy struct {
	fakeRunPolicy
	checks int
	starts []decidedAtCall
	run    bool
}

func (p *decidingPolicy) CheckTool(_ *security.RequestContext, call ToolCall) ToolVerdict {
	p.checks++
	if call.RequestType == "" || call.RequestType == toolcore.ToolRequestTypeRead {
		return ToolVerdict{}
	}
	return ToolVerdict{Kind: ToolCallDecideAtCall}
}

func (p *decidingPolicy) StartToolCall(_ *security.RequestContext, call ToolCall, account string) ToolStart {
	p.starts = append(p.starts, decidedAtCall{call: call, account: account})
	if p.run {
		return ToolStart{Run: true}
	}
	return ToolStart{Observation: "held by the policy"}
}

func decidingExecutor(ctx *security.RequestContext, tool toolcore.NBTool) *plannerExecutor {
	agent := &governedAgent{MockAgent{SupportedTools: []toolcore.NBTool{tool}}}
	executor := newPlannerExecutor(governedRun(ctx), &mockNBAgentPlanner{}, agent, policyActionRequest(), 3)
	executor.toolCallbackHandler = nil
	return executor
}

// A write the policy decides at the call reaches StartToolCall exactly once,
// on the conversation's account, with no approval prompt, though CheckTool
// is asked twice; the policy's answer decides whether the tool runs.
func TestDecidedAtCallRunsTheHookOnce(t *testing.T) {
	policy := &decidingPolicy{}
	withFakeRunPolicy(t, policy)
	tool := &MockClassifiedTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool", ReturnOutput: "ran", ReturnStatus: toolcore.NBToolResponseStatusSuccess},
		RequestType: toolcore.ToolRequestTypeCreate}
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{policyTestAccount})
	action := NBAgentPlannerToolAction{ToolID: "w1", Tool: "write_tool", ToolInput: "set x 1"}

	executor := decidingExecutor(ctx, tool)
	step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), action, "ran")
	require.NoError(t, err)
	assert.Nil(t, finish, "no approval prompt")
	assert.Equal(t, "held by the policy", step.Observation)
	assert.Empty(t, tool.CapturedCtx, "not run")
	require.Len(t, policy.starts, 1, "decided once")
	assert.Equal(t, policyTestAccount, policy.starts[0].account)
	assert.Equal(t, toolcore.ToolRequestTypeCreate, policy.starts[0].call.RequestType)
	assert.GreaterOrEqual(t, policy.checks, 2, "CheckTool is asked more than once, so it must change nothing")

	policy.run = true
	action.ToolID = "w2"
	_, finish, err = decidingExecutor(ctx, tool).doAction(getNameToTool([]toolcore.NBTool{tool}), action, "ran")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, "ran", tool.CapturedCtx, "the policy let it run")
	assert.Len(t, policy.starts, 2)
}

// A write the access check refuses never reaches the hook: nothing is
// decided, so nothing is recorded, for a call that does not run.
func TestDecidedAtCallNotReachedForARefusedWrite(t *testing.T) {
	policy := &decidingPolicy{run: true}
	withFakeRunPolicy(t, policy)
	tool := &MockClassifiedTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool", ReturnOutput: "ran"}, RequestType: toolcore.ToolRequestTypeCreate}
	noWrite := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"ffffffff-ffff-ffff-ffff-ffffffffffff"})
	_, finish, err := decidingExecutor(noWrite, tool).doAction(getNameToTool([]toolcore.NBTool{tool}), NBAgentPlannerToolAction{ToolID: "w", Tool: "write_tool", ToolInput: "set x 1"}, "")
	require.NoError(t, err)
	require.NotNil(t, finish)
	assert.Contains(t, finish.Data, "doesnt have access")
	assert.Empty(t, policy.starts)
}

// A kubectl write that names another cluster with --context is decided on
// that cluster's account, not the conversation's.
func TestDecidedAtCallLandsOnTheNamedCluster(t *testing.T) {
	policy := &decidingPolicy{}
	withFakeRunPolicy(t, policy)
	account := policyTestAccount
	configs := []toolcore.ToolConfig{
		{Name: "k8s-home", Values: []toolcore.ToolConfigValue{{Name: "id", Value: account}}},
		{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "prod-id"}}},
	}
	data, err := common.MarshalJson(configs)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, fmt.Sprintf("list_tool_configs:%s:%s", account, tools.ToolExecuteKubectlCommand), data))
	tools.StubTenantClusters(t, "tenant", configs)
	kubectl, ok := toolcore.GetNBTool(account, tools.ToolExecuteKubectlCommand)
	require.True(t, ok)

	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "prod-id"})
	executor := decidingExecutor(ctx, kubectl)
	executor.agentRequest.QueryConfig.ToolConfigs = map[string]string{tools.ToolExecuteKubectlCommand: "k8s-home"}
	step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{kubectl}),
		NBAgentPlannerToolAction{ToolID: "k", Tool: tools.ToolExecuteKubectlCommand, ToolInput: `{"command":"kubectl --context k8s-prod delete pod api-1 -n shop"}`}, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, "held by the policy", step.Observation)
	require.Len(t, policy.starts, 1)
	assert.Equal(t, "prod-id", policy.starts[0].account, "the account the write lands on")
}
