package core

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
)

// A k8s-dev conversation (account dev-id) whose tenant also has k8s-prod.
func writeTargetExecutor(t *testing.T, prodID string) *plannerExecutor {
	t.Helper()
	tools.StubTenantClusters(t, "tenant", []toolcore.ToolConfig{
		{Name: "k8s-dev", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "dev-id"}}},
		{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: prodID}}},
	})
	tools.StubIntegrationConfigs(t, map[string][]toolcore.ToolConfig{
		"dev-id": {{Name: "dev-pg"}, {Name: "test-pg"}},
		prodID:   {{Name: "prod"}, {Name: "prod-pg"}},
	})
	return &plannerExecutor{
		ctx:          security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"dev-id", prodID}),
		agentRequest: NBAgentRequest{AccountId: "dev-id", UserId: "user"},
	}
}

func approvalKey(e *plannerExecutor, tool toolcore.NBTool, input string) string {
	action := NBAgentPlannerToolAction{Tool: tool.Name(), ToolInput: input}
	return confirmationKeyWithTarget(confirmationKeyForAction(tool, action), e.writeTargetOf(tool, action))
}

// A write is approved for the account it lands on: a "yes" for one account
// does not cover another, and the conversation's own account keeps today's
// key.
func TestWriteApprovalKeyNamesTheAccount(t *testing.T) {
	e := writeTargetExecutor(t, "prod-id")
	kubectl := tools.KubectlExecuteTool{}
	postgres := tools.PostgresExecuteTool{}
	prod := `"target":{"provider":"kubernetes","name":"k8s-prod"}`

	require.Equal(t, "kubectl_execute@k8s-prod@prod-id", approvalKey(e, kubectl, `{"command":"kubectl scale deploy x --replicas=2","cluster":"k8s-prod"}`))
	require.Equal(t, "kubectl_execute", approvalKey(e, kubectl, `{"command":"kubectl scale deploy x --replicas=2"}`), "the conversation's own cluster keeps its key")

	onProd := approvalKey(e, postgres, `{"query":"delete from t","instance":"prod",`+prod+`}`)
	require.Equal(t, "postgres_query_execute@prod-id", onProd)
	require.Equal(t, onProd, approvalKey(e, postgres, `{"query":"delete from t",`+prod+`}`), "the same account before its integration is picked: one approval, not two")
	require.Equal(t, "postgres_query_execute", approvalKey(e, postgres, `{"query":"delete from t","instance":"dev-pg"}`), "the conversation's own database keeps its key")
}

// The key is by account id: a k8s-prod connection replaced between approval
// and resume is a new key, so the write is asked for again.
func TestWriteApprovalKeyChangesWhenTheConnectionIsReplaced(t *testing.T) {
	input := `{"command":"kubectl scale deploy x --replicas=2","cluster":"k8s-prod"}`
	approved := approvalKey(writeTargetExecutor(t, "prod-id"), tools.KubectlExecuteTool{}, input)
	resumed := approvalKey(writeTargetExecutor(t, "prod-id-2"), tools.KubectlExecuteTool{}, input)
	require.NotEqual(t, approved, resumed)
}

// The approval card says where the write lands; the conversation's own
// account keeps today's text.
func TestWriteApprovalCardNamesTheAccount(t *testing.T) {
	e := writeTargetExecutor(t, "prod-id")
	action := NBAgentPlannerToolAction{Tool: "postgres_query_execute", ToolInput: `{"query":"delete from t","instance":"prod","target":{"provider":"kubernetes","name":"k8s-prod"}}`}
	question := confirmationQuestionWithTarget("", action, toolcore.ToolRequestTypeDelete, e.writeTargetOf(tools.PostgresExecuteTool{}, action))
	require.Contains(t, question, "on Kubernetes cluster **k8s-prod** · prod")
	require.Contains(t, question, "Command - delete from t")

	require.Equal(t, "On Kubernetes cluster **k8s-prod**:\nRun uptime on vm-1?",
		confirmationQuestionWithTarget("Run uptime on vm-1?", action, toolcore.ToolRequestTypeUpdate, &writeTarget{Target: toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}}))
	require.Equal(t, "", confirmationQuestionWithTarget("", action, toolcore.ToolRequestTypeDelete, nil), "own account: the default card text")
}

// A write needs write access where it lands: on the target for a write on
// another account (the conversation's account is only read), on the
// conversation's account otherwise.
func TestWriteNeedsAccessWhereItLands(t *testing.T) {
	security.WorkspaceTenantAccounts = func(string) ([]string, error) { return []string{"dev-id", "prod-id"}, nil }
	t.Cleanup(func() { security.WorkspaceTenantAccounts = security.GetAccountIdsForTenant })
	sc, err := security.NewSecurityContextFromWorkspaceGrants("tenant", security.WorkspaceGrants{
		UserId:     "user",
		Roles:      []string{security.AUTH_ACCOUNT_ADMIN_ROLE, security.AUTH_ACCOUNT_READ_ADMIN_ROLE},
		AccountIds: []string{"dev-id", "prod-id"},
		ScopedEntityIds: map[string][]string{
			security.AUTH_ACCOUNT_ADMIN_ROLE:      {"prod-id"},
			security.AUTH_ACCOUNT_READ_ADMIN_ROLE: {"dev-id"},
		},
	})
	require.NoError(t, err)
	ctx := security.NewRequestContext(context.Background(), sc, slog.Default(), otel.GetTracerProvider().Tracer("test"), nil)

	write := mockInferenceTool{mockTool: mockTool{name: "mock_write"}, inferredType: toolcore.ToolRequestTypeUpdate}
	agent := mockAgentWithTools{mockPlainAgent: mockPlainAgent{name: "test_agent"}, tools: []toolcore.NBTool{write}}
	request := NBAgentRequest{AccountId: "dev-id", ConversationId: "conv"}
	action := NBAgentPlannerToolAction{Tool: "mock_write", ToolInput: "scale"}
	onProd := &writeTarget{Target: toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}}
	authorize := func(target *writeTarget, rule bool) *NBAgentPlannerFinishAction {
		finish, _, err := authorizeToolRequest(ctx, agent, request, action, func() []string { return writeAccountOf("dev-id", target, rule) })
		require.NoError(t, err)
		return finish
	}

	require.Nil(t, authorize(onProd, true), "write on k8s-prod, where the user can write")
	require.NotNil(t, authorize(nil, true), "write on k8s-dev, which the user can only read")
	require.NotNil(t, authorize(onProd, false), "rule off: the conversation's account must be writable too")
	readOnlyProd := &writeTarget{Target: toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "dev-id"}}
	require.NotNil(t, authorize(readOnlyProd, true), "a target the user can only read is refused")
	blank := &writeTarget{Target: toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: " "}}
	require.NotNil(t, authorize(blank, true), "a blank account is refused")
}

// Resume looks the approval up under the key the gate recorded it under, so a
// "yes" to a write on k8s-prod resumes the write instead of becoming its input.
func TestResumeFindsTheTargetApproval(t *testing.T) {
	e := writeTargetExecutor(t, "prod-id")
	kubectl := tools.KubectlExecuteTool{}
	action := NBAgentPlannerToolAction{Tool: kubectl.Name(), ToolInput: `{"command":"kubectl scale deploy x --replicas=2","cluster":"k8s-prod"}`}
	nameToTool := getNameToTool([]toolcore.NBTool{kubectl})
	gate := approvalKey(e, kubectl, action.ToolInput)
	require.Equal(t, gate, e.confirmationKeyOf(nameToTool, action))

	confirmations := map[string]string{gate: "yes"}
	require.True(t, isToolConfirmationApproved(confirmations, e.confirmationKeyOf(nameToTool, action)))
	require.False(t, isToolConfirmationApproved(map[string]string{"kubectl_execute@k8s-prod": "yes"}, e.confirmationKeyOf(nameToTool, action)), "an approval recorded before account ids were in the key is asked again")
	require.True(t, confirmationRejected(map[string]string{gate: "no"}, e.confirmationKeyOf(nameToTool, action)))
}

// Every write's card says where it lands, the conversation's own account
// included: its name, and the integration when already chosen there.
func TestWriteApprovalCardNamesTheConversationsOwnAccount(t *testing.T) {
	e := writeTargetExecutor(t, "prod-id")
	tools.StubAccountTargets(t, map[string]*toolcore.ExecutionTarget{
		"dev-id": {Provider: "kubernetes", Name: "k8s-dev", AccountID: "dev-id"},
	})
	postgres := tools.PostgresExecuteTool{}
	action := NBAgentPlannerToolAction{Tool: postgres.Name(), ToolInput: `{"query":"delete from orders"}`}
	card := func(tool toolcore.NBTool) string {
		return confirmationQuestionWithTarget("", action, toolcore.ToolRequestTypeDelete, e.destinationOf(tool, action, e.writeTargetOf(tool, action)))
	}

	e.agentRequest.QueryConfig.ToolConfigs = map[string]string{postgres.Name(): "dev-pg"}
	require.Contains(t, card(postgres), "on Kubernetes cluster **k8s-dev** · dev-pg", "the picked database")
	require.Contains(t, card(postgres), "Command - delete from orders")

	data, err := common.MarshalJson([]toolcore.ToolConfig{{Name: "only-pg"}})
	require.NoError(t, err)
	require.NoError(t, common.CacheSet(toolcore.CacheNamespaceLlmToolConfig, fmt.Sprintf("list_tool_configs:%s:%s", "dev-id", postgres.Name()), data))
	e.agentRequest.QueryConfig.ToolConfigs = nil
	require.Contains(t, card(postgres), "on Kubernetes cluster **k8s-dev** · only-pg", "the only database")

	kubectl := tools.KubectlExecuteTool{}
	e.agentRequest.QueryConfig.ToolConfigs = map[string]string{kubectl.Name(): "k8s-dev"}
	got := confirmationQuestionWithTarget("", NBAgentPlannerToolAction{Tool: kubectl.Name(), ToolInput: `{"command":"kubectl delete pod x"}`}, toolcore.ToolRequestTypeDelete, e.destinationOf(kubectl, NBAgentPlannerToolAction{Tool: kubectl.Name()}, nil))
	require.Contains(t, got, "on Kubernetes cluster **k8s-dev**.", "a cluster is not named twice")

	e.agentRequest.QueryConfig.ToolConfigs = map[string]string{postgres.Name(): "dev-pg"}
	switched := NBAgentPlannerToolAction{Tool: postgres.Name(), ToolInput: `{"query":"delete from orders","instance":"test-pg"}`}
	require.Contains(t, confirmationQuestionWithTarget("", switched, toolcore.ToolRequestTypeDelete, e.destinationOf(postgres, switched, nil)),
		"on Kubernetes cluster **k8s-dev** · test-pg", "a call naming another of the account's databases is approved for that one")

	require.Nil(t, (&plannerExecutor{ctx: e.ctx, agentRequest: NBAgentRequest{AccountId: "unknown-id"}}).destinationOf(postgres, action, nil), "an account that cannot be named keeps the generic card")
}

// The approval's stored config carries where the write lands, for the UI.
func TestApprovalConfigCarriesTheTarget(t *testing.T) {
	onProd := &writeTarget{Target: toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod", AccountID: "prod-id"}, Integration: "prod"}
	cfg := followupMessageConfig(FollowupRequest{FollowupType: FollowupTypeToolConfirmation, ToolName: "postgres_query_execute", Target: onProd.followupTarget(), CrossAccount: true})
	require.Equal(t, &FollowupTarget{Provider: "kubernetes", Kind: "Kubernetes cluster", Name: "k8s-prod", Integration: "prod"}, cfg["target"])
	require.Equal(t, true, cfg["crossAccount"])

	own := followupMessageConfig(FollowupRequest{FollowupType: FollowupTypeToolConfirmation, Target: (&writeTarget{Target: toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-dev"}}).followupTarget()})
	require.NotContains(t, own, "crossAccount")
	require.NotContains(t, followupMessageConfig(FollowupRequest{FollowupType: FollowupTypeToolConfirmation}), "target")
}

// On the conversation's own account, approving a write on its database does
// not approve a write the call aims at another of its databases.
func TestOwnDatabaseSwitchIsApprovedSeparately(t *testing.T) {
	e := writeTargetExecutor(t, "prod-id")
	postgres := tools.PostgresExecuteTool{}
	e.agentRequest.QueryConfig.ToolConfigs = map[string]string{postgres.Name(): "dev-pg"}
	key := func(input string) string {
		action := NBAgentPlannerToolAction{Tool: postgres.Name(), ToolInput: input}
		return e.approvalKey(postgres, action, e.writeTargetOf(postgres, action))
	}
	picked := key(`{"query":"delete from t"}`)
	require.Equal(t, "postgres_query_execute", picked, "the conversation's pick keeps today's key")
	require.Equal(t, picked, key(`{"query":"delete from t","instance":"dev-pg"}`), "naming the pick is no switch")
	switched := key(`{"query":"delete from t","instance":"test-pg"}`)
	require.Equal(t, "postgres_query_execute#test-pg", switched)
	require.False(t, isToolConfirmationApproved(map[string]string{picked: "yes"}, switched), "a yes for dev-pg does not run a write on test-pg")

	nameToTool := getNameToTool([]toolcore.NBTool{postgres})
	require.Equal(t, switched, e.confirmationKeyOf(nameToTool, NBAgentPlannerToolAction{Tool: postgres.Name(), ToolInput: `{"query":"delete from t","instance":"test-pg"}`}), "resume looks the approval up under the gate's key")
}

// Naming the conversation's own cluster is the same write as naming none: one
// approval, not two (seen live: the model retried a create without `cluster`
// and the user was asked again for k8s-dev).
func TestOwnClusterNamedOrNotIsOneApproval(t *testing.T) {
	e := writeTargetExecutor(t, "prod-id")
	e.agentRequest.QueryConfig.ToolConfigs = map[string]string{"kubectl_execute": "k8s-dev"}
	kubectl := tools.KubectlExecuteTool{}
	key := func(input string) string {
		action := NBAgentPlannerToolAction{Tool: kubectl.Name(), ToolInput: input}
		return e.approvalKey(kubectl, action, e.writeTargetOf(kubectl, action))
	}
	unnamed := key(`{"command":"kubectl create configmap x"}`)
	require.Equal(t, "kubectl_execute", unnamed)
	require.Equal(t, unnamed, key(`{"command":"kubectl create configmap x","cluster":"k8s-dev"}`))
	require.Equal(t, unnamed, key(`{"command":"kubectl create configmap x --context k8s-dev"}`))
	require.Equal(t, "kubectl_execute@k8s-prod@prod-id", key(`{"command":"kubectl create configmap x","cluster":"k8s-prod"}`), "another cluster keeps its own approval")
}
