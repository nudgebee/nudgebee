package core

import (
	"errors"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// policyLookupCounter replaces the metastore with a hook that fails every
// connection and counts the ones made by the approval-rule lookup, so a policy
// check is observable without a database. A failed hook is never cached, so
// every lookup reaches it.
func policyLookupCounter(t *testing.T) *atomic.Int32 {
	t.Helper()
	var lookups atomic.Int32
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		if strings.Contains(string(debug.Stack()), "security/toolapproval.FindMatch") {
			lookups.Add(1)
		}
		return nil, errors.New("test: no database")
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })
	previous := config.Config.LlmServerToolApprovalPolicyEnabled
	config.Config.LlmServerToolApprovalPolicyEnabled = true
	t.Cleanup(func() { config.Config.LlmServerToolApprovalPolicyEnabled = previous })
	return &lookups
}

func policyWriteExecutor(readOnly bool) (*plannerExecutor, map[string]toolcore.NBTool, NBAgentPlannerToolAction) {
	tool := &MockClassifiedTool{
		MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool"},
		RequestType:              toolcore.ToolRequestTypeCreate,
	}
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", AgentId: uuid.NewString(), ConversationId: "conversation", MessageId: "message"}
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", security.GetSystemUserId(), []string{request.AccountId})
	if readOnly {
		ctx = WithReadOnlyRun(ctx)
	}
	executor := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{tool}}, request, 3)
	action := NBAgentPlannerToolAction{ToolID: "probe", Tool: "write_tool", ToolInput: "redis-cli set test 1"}
	return executor, getNameToTool([]toolcore.NBTool{tool}), action
}

// A rule only removes the approval prompt; it must never let a write through
// in a read-only run, so the read-only refusal happens before any rule lookup.
func TestReadOnlyRunRefusesWriteBeforeApprovalRuleLookup(t *testing.T) {
	lookups := policyLookupCounter(t)
	executor, nameToTool, action := policyWriteExecutor(true)

	step, finish, err := executor.doAction(nameToTool, action, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "read-only investigation")
	assert.Zero(t, lookups.Load(), "no approval rule is consulted in a read-only run")
}

// Control for the test above: outside a read-only run the same write does
// consult the rules, and a failed lookup falls back to the approval prompt.
func TestWriteOutsideReadOnlyRunConsultsApprovalRules(t *testing.T) {
	previousDao := GetConversationDao()
	SetConversationDao(readOnlyLoopDao{})
	defer SetConversationDao(previousDao)
	lookups := policyLookupCounter(t)
	executor, nameToTool, action := policyWriteExecutor(false)

	_, finish, err := executor.doAction(nameToTool, action, "")
	require.NoError(t, err)
	require.NotNil(t, finish, "the lookup failed, so the user is asked")
	assert.Positive(t, lookups.Load(), "the approval rules were consulted")
}

// A waived call always names who made the request: the person, or the system
// user for a run with nobody signed in.
func TestDecisionRequesterNeverEmpty(t *testing.T) {
	assert.Equal(t, "user-1", decisionRequester(" user-1 ", true))
	assert.Equal(t, security.GetSystemUserId(), decisionRequester("", false))
	assert.Equal(t, security.GetSystemUserId(), decisionRequester("", true))
	assert.Equal(t, security.GetSystemUserId(), decisionRequester("user-1", false), "an unattended run is recorded as the system user")
}
