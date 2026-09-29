package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security/toolapproval"
	toolcore "nudgebee/llm/tools/core"
)

func TestToolApprovalConfigOptionsShellCommandState(t *testing.T) {
	t.Run("tool-wide rule does not wait for a command target", func(t *testing.T) {
		required, pending, options, err := toolApprovalConfigOptions(nil, "account", "shell_execute", string(toolapproval.MatchTool), "")
		require.NoError(t, err)
		require.False(t, required)
		require.False(t, pending)
		require.Empty(t, options)
	})

	t.Run("exact command waits until a command identifies its target", func(t *testing.T) {
		required, pending, options, err := toolApprovalConfigOptions(nil, "account", "shell_execute", string(toolapproval.MatchExactCommand), "")
		require.NoError(t, err)
		require.False(t, required)
		require.True(t, pending)
		require.Empty(t, options)
	})

	t.Run("local command has no configured target", func(t *testing.T) {
		required, pending, options, err := toolApprovalConfigOptions(nil, "account", "shell", string(toolapproval.MatchExactCommand), "ls -la")
		require.NoError(t, err)
		require.False(t, required)
		require.False(t, pending)
		require.Empty(t, options)
	})
}

func TestCommonToolApprovalTools(t *testing.T) {
	accounts := [][]toolcore.ToolDto{
		{
			{Name: "shell_execute", Status: toolcore.ToolStatusEnabled, NBToolType: toolcore.NBToolTypeTool},
			{Name: "kubectl_execute", Status: toolcore.ToolStatusEnabled, NBToolType: toolcore.NBToolTypeTool, NeedsConfig: true},
			{Name: "only_first", Status: toolcore.ToolStatusEnabled, NBToolType: toolcore.NBToolTypeTool},
			{Name: "agent", Status: toolcore.ToolStatusEnabled, NBToolType: toolcore.NBToolTypeAgent},
		},
		{
			{Name: "shell_execute", Status: toolcore.ToolStatusEnabled, NBToolType: toolcore.NBToolTypeTool},
			{Name: "kubectl_execute", Status: toolcore.ToolStatusEnabled, NBToolType: toolcore.NBToolTypeTool, NeedsConfig: true},
			{Name: "disabled", Status: toolcore.ToolStatusDisabled, NBToolType: toolcore.NBToolTypeTool},
		},
	}

	require.Equal(t, []toolApprovalToolOption{
		{Name: "kubectl_execute", ConfigurationMode: "required"},
		{Name: "shell_execute", ConfigurationMode: "command"},
	}, commonToolApprovalTools(accounts))
}

func TestToolApprovalOmittedConditionsStoreEmptyList(t *testing.T) {
	rule, err := ruleFromRequest(toolApprovalUpsertRequest{AccountID: uuid.NewString(), Name: "r", ToolName: "kubectl_execute", MatchKind: toolapproval.MatchTool}, uuid.New())
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(rule.Conditions))
}

func TestToolApprovalAllAccountsMustBeExplicit(t *testing.T) {
	base := toolApprovalUpsertRequest{Name: "r", ToolName: "kubectl_execute", MatchKind: toolapproval.MatchTool}

	_, err := ruleFromRequest(base, uuid.New())
	require.ErrorContains(t, err, "account_id is required unless all_accounts", "a missing account is never read as every account")

	all := base
	all.AllAccounts = true
	rule, err := ruleFromRequest(all, uuid.New())
	require.NoError(t, err)
	require.True(t, rule.AllAccounts())

	both := all
	both.AccountID = uuid.NewString()
	_, err = ruleFromRequest(both, uuid.New())
	require.ErrorContains(t, err, "either all accounts or one account")
}

func TestToolApprovalFieldsMarkCommandFields(t *testing.T) {
	shell, ok := toolcore.GetNBTool("", toolcore.ToolExecuteShellCommand)
	require.True(t, ok)
	byName := map[string]toolApprovalToolField{}
	for _, field := range toolApprovalFields(shell) {
		byName[field.Name] = field
	}
	require.Contains(t, byName, "work_dir")
	require.False(t, byName["work_dir"].Command)
	require.True(t, byName["command"].Command, "the command field is offered and marked as a CLI command")
	kind, _ := toolApprovalTargetKind(nil, shell)
	require.Equal(t, toolApprovalTargetCommand, kind)
}

func TestToolApprovalDisabledRuleSkipsTargetChecks(t *testing.T) {
	enabled := false
	rule, err := buildToolApprovalRule(nil, toolApprovalUpsertRequest{Name: "stale", ToolName: "no_such_tool", MatchKind: toolapproval.MatchTool, AllAccounts: true, Enabled: &enabled}, uuid.New())
	require.NoError(t, err, "switching a rule off must work even when its tool or integration is gone")
	require.False(t, rule.Enabled)

	enabled = true
	_, err = buildToolApprovalRule(nil, toolApprovalUpsertRequest{Name: "stale", ToolName: "no_such_tool", MatchKind: toolapproval.MatchTool, AllAccounts: true, Enabled: &enabled}, uuid.New())
	require.Error(t, err, "an enabled rule still needs an available tool")
}

func TestToolApprovalAllowUnattendedIsOptIn(t *testing.T) {
	base := toolApprovalUpsertRequest{AccountID: uuid.NewString(), Name: "r", ToolName: "kubectl_execute", MatchKind: toolapproval.MatchTool}
	rule, err := ruleFromRequest(base, uuid.New())
	require.NoError(t, err)
	require.False(t, rule.AllowUnattended, "rules apply only to signed-in requesters unless opted in")
	base.AllowUnattended = true
	rule, err = ruleFromRequest(base, uuid.New())
	require.NoError(t, err)
	require.True(t, rule.AllowUnattended)
}
