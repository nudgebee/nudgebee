package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

func TestDelegatedTargetPreparationBindsDirectWriteConfirmation(t *testing.T) {
	tool := tools.AwsCliTool{}
	action := NBAgentPlannerToolAction{Tool: tool.Name(), ToolInput: `{"command":"aws ec2 terminate-instances --instance-ids i-1"}`}
	require.Equal(t, tool.Name(), confirmationKeyForAction(tool, action))

	prepared, err := prepareDelegatedActionInput(tool, &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod"}, "origin-id", action)
	require.NoError(t, err)
	require.Equal(t, "aws_execute@aws-prod", confirmationKeyForAction(tool, prepared))
	requested, err := tool.RequestedTarget(prepared.ToolInput)
	require.NoError(t, err)
	require.Equal(t, "aws-prod", requested)

	preparedAgain, err := prepareDelegatedActionInput(tool, &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod"}, "origin-id", prepared)
	require.NoError(t, err)
	require.Equal(t, prepared.ToolInput, preparedAgain.ToolInput, "resume must use the same approval target")
}

func TestDelegatedConcreteTargetSkipsConversationConfigSelection(t *testing.T) {
	const account = "delegated-config-selection-test"
	configs := []toolcore.ToolConfig{{Name: "aws-dev"}, {Name: "aws-prod"}}
	data, err := common.MarshalJson(configs)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, "list_tool_configs:"+account+":aws_execute", data))
	e := plannerExecutor{ctx: security.NewRequestContextForSuperAdmin(), agentRequest: NBAgentRequest{
		AccountId: account,
		QueryConfig: toolcore.NBQueryConfig{
			OriginalUserQuery: "check aws-dev",
			DelegatedTarget:   &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod", AccountID: "target-id"},
		},
	}}
	for _, tc := range []struct {
		name   string
		tool   toolcore.NBTool
		action NBAgentPlannerToolAction
	}{
		{"direct", tools.AwsCliTool{}, NBAgentPlannerToolAction{Tool: tools.ToolExecuteAwsCliCommand, ToolInput: `{"command":"aws s3 ls"}`}},
		{"shell", tools.AwsCliTool{}, NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: `{"command":"aws s3 ls"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps, finish, err := e.followupForMultipleToolConfigs(tc.tool, tc.action)
			require.NoError(t, err)
			require.Empty(t, steps)
			require.Nil(t, finish)
			require.Empty(t, e.agentRequest.QueryConfig.ToolConfigs, "target choice must not be persisted as a conversation default")
		})
	}
}

// An aws write whose remote target cannot be tied to one account must be
// refused before approval; doAction turns this into a tool failure step.
func TestShellWriteTargetRefusesAmbiguousAwsWrite(t *testing.T) {
	e := plannerExecutor{ctx: security.NewRequestContextForSuperAdmin(), agentRequest: NBAgentRequest{AccountId: "ambiguous-aws-write"}}
	for _, input := range []string{
		`{"command":"for b in x y; do aws s3 rm s3://$b/key; done"}`,
		`{"command":"cd /tmp && aws s3 rm s3://bucket/key"}`,
		`{"account":"aws-prod","command":"aws --profile aws-dev s3 rm s3://bucket/key"}`,
	} {
		_, err := e.shellWriteTarget(tools.AwsCliTool{}, NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: input})
		require.True(t, errors.Is(err, tools.ErrAmbiguousShellTarget), input)
	}
}

// Regression (fresh-context review of #39373): for choosing a connection, an
// ambiguous aws READ script must behave exactly like a plain aws command that
// names no target, with or without a delegated target of another provider.
// Only writes are refused (TestShellWriteTargetRefusesAmbiguousAwsWrite).
func TestAmbiguousAwsReadScriptPassesConfigSelection(t *testing.T) {
	const account = "ambiguous-aws-read"
	configs := []toolcore.ToolConfig{{Name: "aws-dev", Values: []toolcore.ToolConfigValue{{Name: "id", Value: account}}}}
	data, err := common.MarshalJson(configs)
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, "list_tool_configs:"+account+":aws_execute", data))
	previous := tools.TenantAccountLister
	tools.TenantAccountLister = func(_ *security.RequestContext, _ toolcore.NBTool) ([]toolcore.ToolConfig, error) {
		return configs, nil
	}
	t.Cleanup(func() { tools.TenantAccountLister = previous })

	run := func(target *toolcore.ExecutionTarget, command string) ([]NBAgentPlannerToolActionStep, *NBAgentPlannerFinishAction, error) {
		e := plannerExecutor{ctx: security.NewRequestContextForSuperAdmin(), agentRequest: NBAgentRequest{
			AccountId: account,
			// A conversation that already saved its AWS selection, as most do.
			QueryConfig: toolcore.NBQueryConfig{DelegatedTarget: target, ToolConfigs: map[string]string{tools.ToolExecuteAwsCliCommand: "aws-dev"}},
		}}
		input, err := common.MarshalJson(map[string]string{"command": command})
		require.NoError(t, err)
		return e.followupForMultipleToolConfigs(tools.AwsCliTool{}, NBAgentPlannerToolAction{Tool: toolcore.ToolExecuteShellCommand, ToolInput: string(input)})
	}
	for _, target := range []*toolcore.ExecutionTarget{nil, {Provider: "kubernetes", Name: "k8s-prod", AccountID: "k8s-id"}} {
		wantSteps, wantFinish, wantErr := run(target, "aws s3 ls")
		for _, command := range []string{
			`for r in us-east-1 eu-west-1; do aws ec2 describe-instances --region $r; done`,
			`cd /tmp && aws s3 ls`,
		} {
			steps, finish, err := run(target, command)
			require.Equal(t, wantErr, err, command)
			require.Equal(t, wantFinish == nil, finish == nil, command)
			require.Empty(t, wantSteps, "baseline: a plain aws command passes config selection")
			require.Equal(t, len(wantSteps), len(steps), "%s: must match a plain untargeted aws command, got %+v", command, steps)
			for _, step := range steps {
				require.NotContains(t, step.Observation, "aws-led", command)
			}
		}
	}
}
