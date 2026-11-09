package core

import (
	"strings"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The execution-targets lookup needs a database; planner tests run without
// one, so the package's tests use an empty block unless a test sets its own.
func init() {
	renderExecutionTargetsBlock = func(*security.RequestContext, NBAgentRequest, []toolcore.NBTool) string { return "" }
}

func TestExecutionTargetsBlockReachesHumanMessage(t *testing.T) {
	block := "<execution_targets>\nKubernetes clusters: current k8s-prod; others you can use: k8s-dev.\n</execution_targets>"
	for _, parent := range []string{"", "orch-1"} {
		planner := &NBReActPlanner4{
			request:               NBAgentRequest{AgentId: "agent-1", ParentAgentId: parent, AccountId: "acc-1"},
			executionTargetsBlock: block,
		}
		human := planner.humanText("list pods on k8s-dev")
		assert.Contains(t, human, block, "top-level and sub-agents both see the targets (parent=%q)", parent)
		assert.Less(t, strings.Index(human, block), strings.Index(human, "<question>"))
	}

	empty := (&NBReActPlanner4{request: NBAgentRequest{AgentId: "a", AccountId: "acc-1"}}).humanText("q")
	assert.NotContains(t, empty, "execution_targets")
}

func TestDelegatedTargetRendersWithoutOtherTargets(t *testing.T) {
	block := renderDelegatedTargetBlock(&toolcore.ExecutionTarget{Provider: "gcp", Name: "prod"})
	assert.Contains(t, block, `"provider":"gcp"`)
	assert.Contains(t, block, `"name":"prod"`)
	assert.Contains(t, block, "does not grant access")
	assert.Contains(t, block, `GCP account "prod"`)
	assert.Contains(t, block, "not a namespace, label, project", "the name must not be reused as a query filter")
}

func TestResolveAgentHandoffTarget(t *testing.T) {
	const account = "origin"
	configs := map[string][]toolcore.ToolConfig{
		tools.ToolExecuteKubectlCommand: {
			{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "k8s-id"}}},
			{Name: "k8s-secret", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "secret-id"}}},
		},
		tools.ToolExecuteAwsCliCommand: {
			{Name: "aws-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "aws-id"}, {Name: "account_number", Value: "123456789012"}}},
			{Name: "aws-secret", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "secret-id"}}},
		},
		tools.ToolExecuteGcpCliCommand: {
			{Name: "gcp-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "gcp-id"}}},
		},
		tools.ToolExecuteAzureCliCommand: {
			{Name: "azure-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "az-id"}}},
		},
	}
	previous := tools.TenantAccountLister
	tools.TenantAccountLister = func(_ *security.RequestContext, tool toolcore.NBTool) ([]toolcore.ToolConfig, error) {
		return configs[tool.Name()], nil
	}
	t.Cleanup(func() { tools.TenantAccountLister = previous })
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account, "k8s-id", "aws-id", "gcp-id", "az-id"})

	for _, tc := range []struct {
		provider, requested, canonical, id string
	}{
		{"kubernetes", "K8S-PROD", "k8s-prod", "k8s-id"},
		{"aws", "123456789012", "aws-prod", "aws-id"},
		{"gcp", "gcp-prod", "gcp-prod", "gcp-id"},
		{"azure", "azure-prod", "azure-prod", "az-id"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			got, err := tools.ResolveAgentHandoffTarget(ctx, account, &toolcore.ExecutionTarget{Provider: tc.provider, Name: tc.requested})
			require.NoError(t, err)
			require.Equal(t, &toolcore.ExecutionTarget{Provider: tc.provider, Name: tc.canonical, AccountID: tc.id}, got)
		})
	}

	for _, requested := range []string{"aws-missing", "aws-secret"} {
		_, err := tools.ResolveAgentHandoffTarget(ctx, account, &toolcore.ExecutionTarget{Provider: "aws", Name: requested})
		require.ErrorContains(t, err, "not connected or you do not have access")
		require.Contains(t, err.Error(), "aws-prod")
		require.NotContains(t, err.Error(), "secret-id")
	}
	_, err := tools.ResolveAgentHandoffTarget(ctx, account, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-secret"})
	require.ErrorContains(t, err, "not connected or you do not have access")
	require.ErrorContains(t, err, "available clusters: k8s-prod")
	got, err := tools.ResolveAgentHandoffTarget(nil, account, nil)
	require.NoError(t, err)
	require.Nil(t, got)
	_, err = tools.ResolveAgentHandoffTarget(nil, account, &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod"})
	require.ErrorContains(t, err, "security context")
	_, err = ExecuteAgentToolCall(toolcore.NbToolContext{Ctx: ctx, AccountId: account}, handoffTargetTestAgent{}, toolcore.NBToolCallRequest{
		Arguments: map[string]any{"target": map[string]any{"provider": "aws", "name": "aws-secret"}},
	})
	require.ErrorContains(t, err, "not connected or you do not have access")
}

type handoffTargetTestAgent struct{ NBAgent }

func (handoffTargetTestAgent) AcceptsExecutionTarget()          {}
func (handoffTargetTestAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeReAct }
func (handoffTargetTestAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool {
	return nil
}

type handoffCustomTestAgent struct{ NBAgent }

func (handoffCustomTestAgent) GetPlannerType() AgentPlannerType { return AgentPlannerTypeCustom }
func (handoffCustomTestAgent) GetName() string                  { return "custom_test" }
func (handoffCustomTestAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool {
	return nil
}

func TestNestedTargetAwareAgentInheritsDelegatedTarget(t *testing.T) {
	bound := &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "k8s-id"}
	aware := &nbAgentTool{agent: handoffTargetTestAgent{}, targetSupported: true}
	blind := &nbAgentTool{name: "events", agent: handoffCustomTestAgent{}}

	got, err := prepareDelegatedActionInput(aware, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "inspect pods"})
	require.NoError(t, err)
	target, err := toolcore.ParseExecutionTarget(toolcore.NBToolCallRequest{Command: got.ToolInput})
	require.NoError(t, err)
	require.Equal(t, &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod"}, target, "a targeted caller hands its target to a target-aware agent")

	explicit := `{"command":"inspect pods","target":{"provider":"kubernetes","name":"k8s-dev"}}`
	got, err = prepareDelegatedActionInput(aware, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: explicit})
	require.NoError(t, err)
	require.Equal(t, explicit, got.ToolInput, "an explicit target wins")

	_, err = prepareDelegatedActionInput(blind, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "inspect pods"})
	require.ErrorContains(t, err, `events cannot run against the Kubernetes cluster "k8s-prod"`, "a target-blind agent must not answer from the origin account")
	got, err = prepareDelegatedActionInput(blind, bound, "k8s-id", NBAgentPlannerToolAction{ToolInput: "inspect pods"})
	require.NoError(t, err)
	require.Equal(t, "inspect pods", got.ToolInput, "targeting the conversation's own account needs no target support")

	ctx := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id"}), AccountId: "origin-id", QueryConfig: toolcore.NBQueryConfig{DelegatedTarget: bound}}
	target, err = childAgentTarget(ctx, handoffTargetTestAgent{}, toolcore.NBToolCallRequest{Command: "inspect pods"})
	require.NoError(t, err)
	require.Nil(t, target, "without a planner-injected target the child uses the conversation default")
	_, err = childAgentTarget(ctx, handoffCustomTestAgent{}, toolcore.NBToolCallRequest{Arguments: map[string]any{"target": map[string]any{"provider": "kubernetes", "name": "k8s-prod"}}})
	require.ErrorContains(t, err, "does not support")
	require.NotContains(t, blind.InputSchema().Properties, "target")
	require.Contains(t, aware.InputSchema().Properties, "target")
}

func TestTargetBlindToolsRefusedUnlessNeutral(t *testing.T) {
	bound := &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod", AccountID: "aws-id"}
	toolcore.DeclareAccountScope(toolcore.AccountScopeIndependent, "neutral_test_tool")
	_, err := prepareDelegatedActionInput(namedTestTool{"recommendation_apply"}, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "{}"})
	require.ErrorContains(t, err, `recommendation_apply cannot run against the AWS account "aws-prod"`, "a write declared origin-bound is refused")
	_, err = prepareDelegatedActionInput(namedTestTool{"NEUTRAL_TEST_TOOL"}, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "{}"})
	require.NoError(t, err, "a tool that reads no account data stays usable")
	_, err = prepareDelegatedActionInput(namedTestTool{"recommendation_apply"}, nil, "origin-id", NBAgentPlannerToolAction{ToolInput: "{}"})
	require.NoError(t, err, "an untargeted child is unchanged")
	for _, tool := range []toolcore.NBTool{tools.PrometheusExecuteTool{}, &tools.NBLogToolV2{}, tools.TracesExecuteClickhouseTool{}, tools.MetricsListTool{}, tools.NBTraceToolV2{}} {
		_, err = prepareDelegatedActionInput(tool, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "{}"})
		require.NoError(t, err, "%T reads the target from the call context", tool)
	}
	_, err = prepareDelegatedActionInput(tools.ServerExecuteTool{}, bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "{}"})
	require.Error(t, err, "SSH servers are reached only from the conversation's own account")
	_, err = prepareDelegatedActionInput(toolcore.NewClientToolWrapper(toolcore.NBToolCommand{}), bound, "origin-id", NBAgentPlannerToolAction{ToolInput: "{}"})
	require.NoError(t, err, "client tools run on the user's side")
}

type namedTestTool struct{ name string }

func (n namedTestTool) Name() string                   { return n.name }
func (namedTestTool) Description() string              { return "" }
func (namedTestTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (namedTestTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (namedTestTool) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

func TestNestedHandoffKeepsTargetIdentity(t *testing.T) {
	previous := tools.TenantAccountLister
	tools.TenantAccountLister = func(_ *security.RequestContext, tool toolcore.NBTool) ([]toolcore.ToolConfig, error) {
		if tool.Name() != tools.ToolExecuteKubectlCommand {
			return nil, nil
		}
		return []toolcore.ToolConfig{{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "k8s-new-id"}}}}, nil
	}
	t.Cleanup(func() { tools.TenantAccountLister = previous })
	ctx := toolcore.NbToolContext{
		Ctx:         security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "k8s-new-id"}),
		AccountId:   "origin-id",
		QueryConfig: toolcore.NBQueryConfig{DelegatedTarget: &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "k8s-old-id"}},
	}
	request := toolcore.NBToolCallRequest{Arguments: map[string]any{"target": map[string]any{"provider": "kubernetes", "name": "k8s-prod"}}}
	_, err := childAgentTarget(ctx, handoffTargetTestAgent{}, request)
	require.ErrorContains(t, err, "changed since handoff")

	ctx.QueryConfig.DelegatedTarget.AccountID = "k8s-new-id"
	target, err := childAgentTarget(ctx, handoffTargetTestAgent{}, request)
	require.NoError(t, err)
	require.Equal(t, "k8s-new-id", target.AccountID)
}

// Stubs the tenant's connected clusters and AWS accounts for handoff tests.
func stubHandoffAccounts(t *testing.T) {
	t.Helper()
	previous := tools.TenantAccountLister
	tools.TenantAccountLister = func(_ *security.RequestContext, tool toolcore.NBTool) ([]toolcore.ToolConfig, error) {
		switch tool.Name() {
		case tools.ToolExecuteKubectlCommand:
			return []toolcore.ToolConfig{{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "k8s-id"}}}}, nil
		case tools.ToolExecuteAwsCliCommand:
			return []toolcore.ToolConfig{
				{Name: "aws-origin", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "origin-id"}}},
				{Name: "aws-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "aws-id"}}},
			}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { tools.TenantAccountLister = previous })
}

type awsBoundTestAgent struct{ handoffTargetTestAgent }

func (awsBoundTestAgent) GetName() string        { return "aws_logs_test" }
func (awsBoundTestAgent) TargetProvider() string { return "aws" }

func targetRequest(provider, name string) toolcore.NBToolCallRequest {
	return toolcore.NBToolCallRequest{Arguments: map[string]any{"target": map[string]any{"provider": provider, "name": name}}}
}

// Seen on dev: an investigation on gcp-dev asked logs about gcp-dev itself
// and was refused. Naming the conversation's own account is no handoff, even
// for a target-blind agent.
func TestTargetNamingTheConversationsOwnAccountIsNoHandoff(t *testing.T) {
	stubHandoffAccounts(t)
	ctx := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "aws-id", "k8s-id"}), AccountId: "origin-id"}

	target, err := childAgentTarget(ctx, handoffCustomTestAgent{}, targetRequest("aws", "aws-origin"))
	require.NoError(t, err)
	require.Nil(t, target, "a target-blind agent runs on its own account")
	target, err = childAgentTarget(ctx, handoffTargetTestAgent{}, targetRequest("aws", "aws-origin"))
	require.NoError(t, err)
	require.Nil(t, target, "a target-aware agent too")

	_, err = childAgentTarget(ctx, handoffCustomTestAgent{}, targetRequest("aws", "aws-prod"))
	require.ErrorContains(t, err, "does not support a delegated execution target", "another account still needs target support")
}

// A provider-bound agent (aws_logs runs the aws CLI) takes another account
// of its own kind, and refuses any other kind instead of answering from the
// conversation's own account.
func TestProviderBoundAgentTakesOnlyItsOwnKindOfTarget(t *testing.T) {
	stubHandoffAccounts(t)
	ctx := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "aws-id", "k8s-id"}), AccountId: "origin-id"}

	target, err := childAgentTarget(ctx, awsBoundTestAgent{}, targetRequest("aws", "aws-prod"))
	require.NoError(t, err)
	require.Equal(t, "aws-id", target.AccountID)

	_, err = childAgentTarget(ctx, awsBoundTestAgent{}, targetRequest("kubernetes", "k8s-prod"))
	require.ErrorContains(t, err, `aws_logs_test reads aws accounts only; Kubernetes cluster "k8s-prod" is not one`)
}

type accountTestAgent struct {
	handoffTargetTestAgent
	accountID string
}

func (a accountTestAgent) GetName() string { return "routed_test_" + a.accountID }

// Seen on dev: an AWS conversation asked logs about k8s-prod and got the
// AWS logs agent, built for the conversation's account. A tool routed by
// target gets the agent its factory builds for the target account.
func TestTargetRoutedToolBuildsTheTargetsAgent(t *testing.T) {
	stubHandoffAccounts(t)
	RegisterTargetRoutedNBAgentFactoryAndTool("routed_test", func(accountId string) (NBAgent, error) {
		return accountTestAgent{accountID: accountId}, nil
	}, "", "", "")
	tool := &nbAgentTool{name: "routed_test", accountId: "origin-id", targetSupported: true, routedByTarget: true}
	ctx := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "aws-id", "k8s-id"}), AccountId: "origin-id"}

	agent, err := tool.agentForCall(ctx, targetRequest("kubernetes", "k8s-prod"))
	require.NoError(t, err)
	require.Equal(t, "routed_test_k8s-id", agent.GetName(), "built for the target")

	agent, err = tool.agentForCall(ctx, toolcore.NBToolCallRequest{Command: "errors in the last hour"})
	require.NoError(t, err)
	require.Equal(t, "routed_test_origin-id", agent.GetName(), "no target: the conversation's account")

	ctx.QueryConfig.DelegatedTarget = &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod", AccountID: "aws-id"}
	agent, err = tool.agentForCall(ctx, toolcore.NBToolCallRequest{Command: "errors in the last hour"})
	require.NoError(t, err)
	require.Equal(t, "routed_test_aws-id", agent.GetName(), "a handed target routes too")

	plain := &nbAgentTool{name: "events", agent: accountTestAgent{accountID: "origin-id"}, targetSupported: true}
	agent, err = plain.agentForCall(ctx, targetRequest("kubernetes", "k8s-prod"))
	require.NoError(t, err)
	require.Equal(t, "routed_test_origin-id", agent.GetName(), "tools not routed by target are unchanged")
}
