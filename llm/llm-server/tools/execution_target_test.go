package tools

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

func TestDelegatedTargetIdentityCannotMoveAfterApproval(t *testing.T) {
	ctx := core.NbToolContext{QueryConfig: core.NBQueryConfig{DelegatedTarget: &core.ExecutionTarget{Provider: "aws", Name: "aws-prod", AccountID: "original-id"}}}
	original := core.ToolConfig{Name: "aws-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "original-id"}}}
	replaced := core.ToolConfig{Name: "aws-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "replacement-id"}}}
	other := core.ToolConfig{Name: "explicit-other", Values: []core.ToolConfigValue{{Name: "id", Value: "other-id"}}}
	require.NoError(t, VerifyDelegatedTargetIdentity(ctx, "aws", original, "aws-prod"))
	require.ErrorContains(t, VerifyDelegatedTargetIdentity(ctx, "aws", replaced, "aws-prod"), "changed since handoff")
	require.NoError(t, VerifyDelegatedTargetIdentity(ctx, "aws", original, ""))
	require.ErrorContains(t, VerifyDelegatedTargetIdentity(ctx, "aws", replaced, ""), "changed since handoff", "implicit defaults must still be identity-bound")
	require.NoError(t, VerifyDelegatedTargetIdentity(ctx, "aws", other, "explicit-other"), "an explicit override is a separate approval target")
	require.ErrorContains(t, VerifyDelegatedTargetIdentity(ctx, "aws", replaced, "explicit-other"), "changed since handoff", "a mismatched selector must not bypass the bound identity")
}

func stubTargets(t *testing.T) {
	previous := TenantAccountLister
	TenantAccountLister = func(_ *security.RequestContext, tool core.NBTool) ([]core.ToolConfig, error) {
		if tool.Name() != ToolExecuteKubectlCommand {
			return nil, nil
		}
		return []core.ToolConfig{
			{Name: "k8s-prod", Values: []core.ToolConfigValue{{Name: "id", Value: "prod-id"}}},
			{Name: "k8s-stage", Values: []core.ToolConfigValue{{Name: "id", Value: "stage-id"}}},
		}, nil
	}
	t.Cleanup(func() { TenantAccountLister = previous })
}

// ResolveCallTarget is the one place a call's data account is decided.
func TestResolveCallTarget(t *testing.T) {
	stubTargets(t)
	sc := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "prod-id", "stage-id"})
	ctx := core.NbToolContext{Ctx: sc, AccountId: "origin-id"}
	prodTarget := map[string]any{"target": map[string]any{"provider": "kubernetes", "name": "k8s-prod"}}

	got, err := ResolveCallTarget(ctx, core.NBToolCallRequest{})
	require.NoError(t, err)
	require.Equal(t, "origin-id", got.DataAccountID(), "no target: the conversation's own account")

	got, err = ResolveCallTarget(ctx, core.NBToolCallRequest{Arguments: prodTarget})
	require.NoError(t, err)
	require.Equal(t, "prod-id", got.DataAccountID(), "a top-level agent can name the cluster per call")
	require.Equal(t, "origin-id", got.AccountId, "the conversation's account is unchanged")

	ctx.QueryConfig.DelegatedTarget = &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-stage", AccountID: "stage-id"}
	got, err = ResolveCallTarget(ctx, core.NBToolCallRequest{})
	require.NoError(t, err)
	require.Equal(t, "stage-id", got.DataAccountID(), "a handed target applies by default")
	got, err = ResolveCallTarget(ctx, core.NBToolCallRequest{Arguments: prodTarget})
	require.NoError(t, err)
	require.Equal(t, "prod-id", got.DataAccountID(), "an explicit target wins over the handed one")

	ctx.QueryConfig.DelegatedTarget = &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "replaced-id"}
	_, err = ResolveCallTarget(ctx, core.NBToolCallRequest{})
	require.ErrorContains(t, err, "changed since handoff")

	ctx = core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id"}), AccountId: "origin-id"}
	_, err = ResolveCallTarget(ctx, core.NBToolCallRequest{Arguments: prodTarget})
	require.Error(t, err, "a caller without access to the target must not read its data")
}

func TestForTargetKeepsTheOriginalContextForTheConversation(t *testing.T) {
	ctx := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", QueryConfig: core.NBQueryConfig{DelegatedTarget: &core.ExecutionTarget{Name: "k8s-prod"}}}
	data := ctx.ForTarget()
	require.Equal(t, "prod-id", data.AccountId)
	require.Nil(t, data.QueryConfig.DelegatedTarget)
	require.Equal(t, "origin-id", ctx.AccountId, "the caller's context, and so its workspace, is unchanged")
}

func TestModelInputSchemaAddsTargetOnce(t *testing.T) {
	require.Contains(t, core.ModelInputSchema(RecommendationExecuteTool{}).Properties, "target", "follows-target tools get the input")
	require.NotContains(t, RecommendationExecuteTool{}.InputSchema().Properties, "target", "tools do not declare it themselves")
	require.NotContains(t, core.ModelInputSchema(KubectlExecuteTool{}).Properties, "target", "own-selector tools keep their own argument")
	require.NotContains(t, core.ModelInputSchema(ServerExecuteTool{}).Properties, "target", "origin-bound tools cannot take one")
}

type ownTargetTool struct{ ServerExecuteTool }

func (ownTargetTool) Name() string { return "custom_with_own_target" }
func (ownTargetTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Properties: map[string]core.ToolSchemaProperty{"target": {Type: core.ToolSchemaTypeString}}}
}

// Every dispatch path (executor, custom planners, watch polls) decides the
// call's account through ResolveToolCallTarget.
func TestResolveToolCallTarget(t *testing.T) {
	stubTargets(t)
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "prod-id"}), AccountId: "origin-id"}
	prod := core.NBToolCallRequest{Arguments: map[string]any{"target": map[string]any{"provider": "kubernetes", "name": "k8s-prod"}}}

	got, err := ResolveToolCallTarget(ctx, RecommendationExecuteTool{}, prod)
	require.NoError(t, err)
	require.Equal(t, "prod-id", got.DataAccountID(), "a follows-target tool reads the named account")

	_, err = ResolveToolCallTarget(ctx, ServerExecuteTool{}, prod)
	require.ErrorContains(t, err, "cannot run on another cluster or account", "an origin-bound tool refuses a target")
	_, err = ResolveToolCallTarget(ctx, ServerExecuteTool{}, core.NBToolCallRequest{Arguments: map[string]any{"target": "k8s-prod"}})
	require.Error(t, err, "a malformed target is refused too, not ignored")

	got, err = ResolveToolCallTarget(ctx, KubectlExecuteTool{}, prod)
	require.NoError(t, err)
	require.Equal(t, "origin-id", got.DataAccountID(), "own-selector tools route the target themselves")

	_, err = ResolveToolCallTarget(ctx, ownTargetTool{}, core.NBToolCallRequest{Arguments: map[string]any{"target": "anything"}})
	require.NoError(t, err, "a tool that defines its own target argument is left alone")

	got, err = ResolveToolCallTarget(ctx, ServerExecuteTool{}, core.NBToolCallRequest{Command: `{"target":{"provider":"aws","name":"x"}}`})
	require.NoError(t, err, "a target key inside the command payload is not a target argument")
	require.Equal(t, "origin-id", got.DataAccountID())
}

// The conversation view shows where each step ran; ExecutedTargetOf decides it.
func TestExecutedTargetOf(t *testing.T) {
	stubTargets(t)
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"origin-id", "prod-id"}), AccountId: "origin-id"}

	resolved, err := ResolveCallTarget(ctx, core.NBToolCallRequest{Arguments: map[string]any{"target": map[string]any{"provider": "kubernetes", "name": "k8s-prod"}}})
	require.NoError(t, err)
	require.Equal(t, &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}, ExecutedTargetOf(RecommendationExecuteTool{}, resolved, ""))

	resolved, err = ResolveCallTarget(ctx, core.NBToolCallRequest{})
	require.NoError(t, err)
	require.Nil(t, ExecutedTargetOf(RecommendationExecuteTool{}, resolved, ""), "nothing is recorded for the conversation's own account")

	require.Equal(t, &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod"},
		ExecutedTargetOf(KubectlExecuteTool{}, ctx, `{"command":"kubectl get pods","cluster":"k8s-prod"}`))
	require.Equal(t, &core.ExecutionTarget{Provider: "aws", Name: "aws-prod"},
		ExecutedTargetOf(AwsCliTool{}, ctx, `{"command":"aws s3 ls","account":"aws-prod"}`))
	require.Equal(t, &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod"},
		ExecutedTargetOf(ShellTool{}, ctx, `{"command":"kubectl --context k8s-prod get pods"}`))
	require.Nil(t, ExecutedTargetOf(KubectlExecuteTool{}, ctx, `{"command":"kubectl get pods"}`), "no named cluster: the conversation's default")

	details := core.WithExecutedTarget(nil, &core.ExecutionTarget{Provider: "aws", Name: "aws-prod", AccountID: "aws-id"})
	require.Equal(t, map[string]any{"provider": "aws", "name": "aws-prod", "account_id": "aws-id"}, details[core.MetadataExecutedTarget])
	require.Nil(t, core.WithExecutedTarget(nil, nil))
}

// stubStampAccounts stands in for the conversation and account stores: every
// call's conversation belongs to origin-id, and prod-id is k8s-prod.
func stubStampAccounts(t *testing.T) {
	t.Helper()
	conv, describe := conversationAccountOf, describeAccount
	t.Cleanup(func() { conversationAccountOf, describeAccount = conv, describe })
	conversationAccountOf = func(core.NbToolContext) string { return "origin-id" }
	describeAccount = func(id string) *core.ExecutionTarget {
		if id != "prod-id" {
			return nil
		}
		return &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: id}
	}
}

func ranOnAccount(resp core.NBToolResponse) string {
	target, _ := resp.AdditionalDetails[core.MetadataExecutedTarget].(map[string]any)
	id, _ := target["account_id"].(string)
	return id
}

// A call that ran somewhere other than the conversation's own account is
// stamped with that account, whether it succeeded or failed; the UI would
// otherwise say "ran on" for a typo, so a name that is not connected is not.
func TestStampExecutedTarget(t *testing.T) {
	stubStampAccounts(t)
	ctx := core.NbToolContext{AccountId: "origin-id", ToolConfig: core.ToolConfig{Name: "k8s-dev"}}
	prod := `{"command":"kubectl get pods","cluster":"k8s-prod"}`
	ok := core.NBToolResponse{Status: core.NBToolResponseStatusSuccess}
	accounts := map[string]string{"k8s-prod": "prod-id", "k8s-dev": "origin-id"}
	restore := resolveStampTarget
	t.Cleanup(func() { resolveStampTarget = restore })
	resolveStampTarget = func(_ *security.RequestContext, _ string, target *core.ExecutionTarget) (*core.ExecutionTarget, error) {
		id, found := accounts[strings.ToLower(target.Name)]
		if !found {
			return nil, errors.New("not connected")
		}
		return &core.ExecutionTarget{Provider: target.Provider, Name: target.Name, AccountID: id}, nil
	}

	got := StampExecutedTarget(ok, nil, KubectlExecuteTool{}, ctx, prod)
	require.Equal(t, "prod-id", ranOnAccount(got))
	require.NotContains(t, got.AdditionalDetails, core.MetadataResolvedTarget, "k8s-dev's config is not what ran on k8s-prod")

	unnamed := ctx
	unnamed.ToolConfig.Name = ""
	got = StampExecutedTarget(ok, nil, KubectlExecuteTool{}, unnamed, `{"command":"kubectl get pods","cluster":"k8s-dev"}`)
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget, "the own cluster by name, with no tool-config name, is still the own account")

	got = StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusError}, nil, KubectlExecuteTool{}, ctx, `{"command":"kubectl get pods","cluster":"typo"}`)
	require.Empty(t, got.AdditionalDetails, "a name that is not connected ran nowhere, with no integration either")
	got = StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusError}, nil, KubectlExecuteTool{}, ctx, prod)
	require.Equal(t, "prod-id", ranOnAccount(got), "a call that failed on k8s-prod is about k8s-prod")
	got = StampExecutedTarget(ok, errors.New("boom"), KubectlExecuteTool{}, ctx, prod)
	require.Equal(t, "prod-id", ranOnAccount(got))

	got = StampExecutedTarget(ok, nil, KubectlExecuteTool{}, ctx, `{"command":"kubectl get pods","cluster":"K8S-DEV"}`)
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget, "naming the conversation's own cluster is not another account")
	require.Equal(t, "k8s-dev", got.AdditionalDetails[core.MetadataResolvedTarget], "the conversation's own config ran")

	same := ctx
	same.ExecutedTarget = &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-dev", AccountID: "origin-id"}
	got = StampExecutedTarget(ok, nil, RecommendationExecuteTool{}, same, "")
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget)
}

// Sub-agents started as another account (metrics, traces) and tools their
// agent points at it (logs) carry no target of their own; their calls still
// say where they ran, relative to the conversation's account.
func TestStampExecutedTarget_CallUnderAnotherAccount(t *testing.T) {
	stubStampAccounts(t)
	asProd := core.NbToolContext{AccountId: "prod-id", ConversationId: "conv", ToolConfig: core.ToolConfig{Name: "prometheus-prod"}}
	for _, resp := range []core.NBToolResponse{{Status: core.NBToolResponseStatusSuccess}, {Status: core.NBToolResponseStatusError}} {
		got := StampExecutedTarget(resp, nil, RecommendationExecuteTool{}, asProd, `{"command":"up"}`)
		require.Equal(t, map[string]any{"provider": "kubernetes", "name": "k8s-prod", "account_id": "prod-id"}, got.AdditionalDetails[core.MetadataExecutedTarget], resp.Status)
		require.Equal(t, "prometheus-prod", got.AdditionalDetails[core.MetadataResolvedTarget], "its config was listed on k8s-prod, where it ran")
	}
	waiting := StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusWaiting}, nil, RecommendationExecuteTool{}, asProd, "")
	require.Empty(t, waiting.AdditionalDetails, "a call waiting on the user has not run")

	// In a child handed k8s-prod, a workspace tool still runs on the
	// conversation's account.
	child := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", ConversationId: "conv"}
	got := StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusSuccess}, nil, RecommendationExecuteTool{}, child, "")
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget)
}

// The integration field names only a real integration the call ran with.
func TestStampExecutedTarget_Integration(t *testing.T) {
	stubStampAccounts(t)
	ok := core.NBToolResponse{Status: core.NBToolResponseStatusSuccess}
	for _, placeholder := range []string{core.ToolConfigNameNone, core.ToolConfigNameAccountAgent} {
		got := StampExecutedTarget(ok, nil, RecommendationExecuteTool{}, core.NbToolContext{AccountId: "origin-id", ToolConfig: core.ToolConfig{Name: placeholder}}, "")
		require.NotContains(t, got.AdditionalDetails, core.MetadataResolvedTarget, placeholder)
	}
	// A follows-target tool keeps the conversation's config when it reads
	// another account; that config is not what it ran with.
	resolved := core.NbToolContext{AccountId: "origin-id", TargetAccountId: "prod-id", ToolConfig: core.ToolConfig{Name: "k8s-dev"},
		ExecutedTarget: &core.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}}
	got := StampExecutedTarget(ok, nil, RecommendationExecuteTool{}, resolved, "")
	require.Equal(t, "prod-id", ranOnAccount(got))
	require.NotContains(t, got.AdditionalDetails, core.MetadataResolvedTarget)
	got = StampExecutedTarget(ok, nil, RecommendationExecuteTool{}, core.NbToolContext{AccountId: "origin-id", ToolConfig: core.ToolConfig{Name: "dev-pg"}}, "")
	require.Equal(t, "dev-pg", got.AdditionalDetails[core.MetadataResolvedTarget])
}

// An agent called with a target (metrics, prometheus, delegate) is stamped
// from its input, whether it succeeded or failed, but not while it is waiting
// on the user.
func TestStampExecutedTarget_AgentInput(t *testing.T) {
	stubStampAccounts(t)
	restore := resolveStampTarget
	t.Cleanup(func() { resolveStampTarget = restore })
	resolveStampTarget = func(_ *security.RequestContext, _ string, target *core.ExecutionTarget) (*core.ExecutionTarget, error) {
		return &core.ExecutionTarget{Provider: target.Provider, Name: target.Name, AccountID: map[string]string{"k8s-prod": "prod-id", "k8s-dev": "origin-id"}[target.Name]}, nil
	}
	ctx := core.NbToolContext{AccountId: "origin-id"}
	agent := RecommendationExecuteTool{} // any tool without a cluster argument of its own
	prod := `{"command":"cpu trend","target":{"provider":"kubernetes","name":"k8s-prod"}}`

	for name, tc := range map[string]struct {
		resp core.NBToolResponse
		err  error
	}{
		"succeeded": {core.NBToolResponse{Status: core.NBToolResponseStatusSuccess}, nil},
		"failed":    {core.NBToolResponse{Status: core.NBToolResponseStatusError}, nil},
		"errored":   {core.NBToolResponse{}, errors.New("boom")},
	} {
		got := StampExecutedTarget(tc.resp, tc.err, agent, ctx, prod)
		require.Equal(t, "prod-id", ranOnAccount(got), name)
	}
	got := StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusWaiting}, nil, agent, ctx, prod)
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget, "waiting")
	own := `{"command":"cpu trend","target":{"provider":"kubernetes","name":"k8s-dev"}}`
	got = StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusSuccess}, nil, agent, ctx, own)
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget, "the conversation's own cluster")
	got = StampExecutedTarget(core.NBToolResponse{Status: core.NBToolResponseStatusSuccess}, nil, agent, ctx, `{"command":"cpu trend"}`)
	require.NotContains(t, got.AdditionalDetails, core.MetadataExecutedTarget, "no target")
}
