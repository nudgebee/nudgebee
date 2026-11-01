package agents

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// setupMockTenantTargets configures mock tenant account targets for testing.
func setupMockTenantTargets(t *testing.T) {
	configs := map[string][]toolcore.ToolConfig{
		tools.ToolExecuteKubectlCommand: {
			{Name: "k8s-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "k8s-prod-id"}}},
			{Name: "k8s-staging", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "k8s-staging-id"}}},
		},
		tools.ToolExecuteAwsCliCommand: {
			{Name: "aws-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "aws-prod-id"}, {Name: "account_number", Value: "111122223333"}}},
			{Name: "aws-staging", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "aws-staging-id"}, {Name: "account_number", Value: "444455556666"}}},
		},
		tools.ToolExecuteGcpCliCommand: {
			{Name: "gcp-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "gcp-prod-id"}}},
		},
		tools.ToolExecuteAzureCliCommand: {
			{Name: "azure-prod", Values: []toolcore.ToolConfigValue{{Name: "id", Value: "az-prod-id"}}},
		},
	}
	previous := tools.TenantAccountLister
	tools.TenantAccountLister = func(_ *security.RequestContext, tool toolcore.NBTool) ([]toolcore.ToolConfig, error) {
		return configs[tool.Name()], nil
	}
	t.Cleanup(func() { tools.TenantAccountLister = previous })
}

// TestE2E_MultiTargetSequentialExecution tests sequential calls across multiple targets
// in a single session without target state leaking across calls.
func TestE2E_MultiTargetSequentialExecution(t *testing.T) {
	setupMockTenantTargets(t)

	const originAccount = "origin-k8s-account"
	accessibleAccounts := []string{originAccount, "k8s-prod-id", "k8s-staging-id"}
	sc := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", accessibleAccounts)

	// Step 1: Execute against k8s-prod
	targetProd := &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-prod"}
	resolvedProd, err := tools.ResolveAgentHandoffTarget(sc, originAccount, targetProd)
	require.NoError(t, err)
	require.Equal(t, "k8s-prod-id", resolvedProd.AccountID)

	kubectlTool := tools.KubectlExecuteTool{}
	preparedProd, err := kubectlTool.PrepareDelegatedTargetInput(resolvedProd, `{"command":"kubectl get pods"}`)
	require.NoError(t, err)
	reqProd := toolcore.ParseNBToolCallRequestFromInput(preparedProd)
	assert.Equal(t, "k8s-prod", reqProd.Arguments["cluster"], "first call must target k8s-prod")

	// Step 2: Sequential execution against k8s-staging in the same conversation
	targetStaging := &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-staging"}
	resolvedStaging, err := tools.ResolveAgentHandoffTarget(sc, originAccount, targetStaging)
	require.NoError(t, err)
	require.Equal(t, "k8s-staging-id", resolvedStaging.AccountID)

	preparedStaging, err := kubectlTool.PrepareDelegatedTargetInput(resolvedStaging, `{"command":"kubectl get pods"}`)
	require.NoError(t, err)
	reqStaging := toolcore.ParseNBToolCallRequestFromInput(preparedStaging)
	assert.Equal(t, "k8s-staging", reqStaging.Arguments["cluster"], "second call must target k8s-staging")
	assert.NotEqual(t, reqProd.Arguments["cluster"], reqStaging.Arguments["cluster"], "target must not leak between sequential calls")
}

// TestE2E_CrossProviderTargetSwitching tests delegating from a Kubernetes-backed
// origin account to AWS and GCP targets.
func TestE2E_CrossProviderTargetSwitching(t *testing.T) {
	setupMockTenantTargets(t)

	const originAccount = "origin-k8s-account"
	accessibleAccounts := []string{originAccount, "aws-prod-id", "gcp-prod-id"}
	sc := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", accessibleAccounts)

	// Delegate to AWS
	targetAWS := &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod"}
	resolvedAWS, err := tools.ResolveAgentHandoffTarget(sc, originAccount, targetAWS)
	require.NoError(t, err)
	require.Equal(t, "aws-prod-id", resolvedAWS.AccountID)

	awsTool := tools.AwsCliTool{}
	preparedAWS, err := awsTool.PrepareDelegatedTargetInput(resolvedAWS, `{"command":"aws s3 ls"}`)
	require.NoError(t, err)
	reqAWS := toolcore.ParseNBToolCallRequestFromInput(preparedAWS)
	assert.Equal(t, "aws-prod", reqAWS.Arguments["account"], "delegated AWS tool input must receive aws-prod account")

	// Delegate to GCP
	targetGCP := &toolcore.ExecutionTarget{Provider: "gcp", Name: "gcp-prod"}
	resolvedGCP, err := tools.ResolveAgentHandoffTarget(sc, originAccount, targetGCP)
	require.NoError(t, err)
	require.Equal(t, "gcp-prod-id", resolvedGCP.AccountID)

	gcpTool := tools.GcpCliTool{}
	preparedGCP, err := gcpTool.PrepareDelegatedTargetInput(resolvedGCP, `{"command":"gcloud compute instances list"}`)
	require.NoError(t, err)
	reqGCP := toolcore.ParseNBToolCallRequestFromInput(preparedGCP)
	assert.Equal(t, "gcp-prod", reqGCP.Arguments["account"], "delegated GCP tool input must receive gcp-prod account")
}

// TestE2E_ObservabilityDualAccountIsolation tests that observability backends
// use target.AccountID for data querying while keeping conversation/workspace
// assets anchored to the origin account.
func TestE2E_ObservabilityDualAccountIsolation(t *testing.T) {
	const originAccount = "origin-cluster-1"
	const targetAccount = "remote-cluster-2"

	for _, tc := range []struct {
		name     string
		tool     toolcore.NBTool
		provider string
		target   string
	}{
		{"metrics", MetricsAgentTool{}, "kubernetes", "remote-cluster-2"},
		{"traces", TracesAgentTool{}, "kubernetes", "remote-cluster-2"},
		{"logs", &fetchLogsDirectTool{accountId: originAccount}, "kubernetes", "remote-cluster-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := toolcore.NbToolContext{
				Ctx:       security.NewRequestContextForSuperAdmin(),
				AccountId: originAccount,
				QueryConfig: toolcore.NBQueryConfig{
					DelegatedTarget: &toolcore.ExecutionTarget{
						Provider:  tc.provider,
						Name:      tc.target,
						AccountID: targetAccount,
					},
				},
			}

			// When target account lacks configured provider, the tool must reject
			// rather than falling back to originAccount.
			resp, err := tc.tool.Call(ctx, toolcore.NBToolCallRequest{Command: "query cpu or errors"})
			require.NoError(t, err)
			assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status, "must fail if target backend is unconfigured")
			assert.Contains(t, resp.Data, tc.target, "error message must name the target that failed to resolve")
			assert.NotContains(t, resp.Data, originAccount, "error message must never fall back to origin account")
		})
	}
}

// TestE2E_StrictNonFallbackRejection tests that unconfigured or unauthorized
// targets strictly reject and never fall back to the origin conversation's credentials.
func TestE2E_StrictNonFallbackRejection(t *testing.T) {
	setupMockTenantTargets(t)

	const originAccount = "origin-k8s-account"
	// User only has access to origin and k8s-prod, NOT to k8s-secret or aws-prod
	accessibleAccounts := []string{originAccount, "k8s-prod-id"}
	sc := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", accessibleAccounts)

	// Unauthorized target
	unauthorizedTarget := &toolcore.ExecutionTarget{Provider: "kubernetes", Name: "k8s-secret"}
	_, err := tools.ResolveAgentHandoffTarget(sc, originAccount, unauthorizedTarget)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected or you do not have access")
	assert.Contains(t, err.Error(), "k8s-prod")

	// Missing provider
	missingProviderTarget := &toolcore.ExecutionTarget{Provider: "aws", Name: "aws-prod"}
	_, err = tools.ResolveAgentHandoffTarget(sc, originAccount, missingProviderTarget)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected or you do not have access")
}

// TestE2E_TargetProvenanceInResponse tests that tool responses from delegated
// executions retain target metadata for UI display and subsequent LLM turns.
func TestE2E_TargetProvenanceInResponse(t *testing.T) {
	setupMockTenantTargets(t)

	const account = "test-account"
	sc := security.NewRequestContextForSuperAdmin()

	originalDao := core.GetConversationDao()
	core.SetConversationDao(&mockE2EConversationDao{})
	t.Cleanup(func() { core.SetConversationDao(originalDao) })

	// Register a mock tool that echoes its input
	mockName := "mock_target_probe_" + uuid.NewString()
	toolcore.RegisterNBToolFactory(mockName, func(acc string) (toolcore.NBTool, error) {
		return &mockTargetProbeTool{name: mockName}, nil
	})

	tool := &delegateAgentTool{accountId: account}
	ctx := toolcore.NbToolContext{
		Ctx:       sc,
		AccountId: account,
	}

	input := toolcore.NBToolCallRequest{
		Arguments: map[string]any{
			"prompt": "inspect cluster",
			"tools":  []any{mockName},
			"target": map[string]any{
				"provider": "kubernetes",
				"name":     "k8s-prod",
			},
		},
	}

	resp, err := tool.Call(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, resp.AdditionalDetails)

	targetVal, ok := resp.AdditionalDetails["target"].(map[string]any)
	require.True(t, ok, "additionalDetails must contain target map")
	assert.Equal(t, "kubernetes", targetVal["provider"])
	assert.Equal(t, "k8s-prod", targetVal["name"])
}

type mockE2EConversationDao struct {
	core.IConversationDao
}

func (m *mockE2EConversationDao) SaveConversationAgentCall(conversationId, messageId, accountId, userId, agentName, parentAgentId, query, originalQuery, sessionId, queryContext string, queryConfig toolcore.NBQueryConfig) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (m *mockE2EConversationDao) ListConversationAgents(messageId, status string) ([]core.ConversationAgent, error) {
	return nil, nil
}

func (m *mockE2EConversationDao) GetConversationAgentParentAgentIdAndPreviousState(agentId string) (string, string) {
	return "", ""
}

func (m *mockE2EConversationDao) UpdateConversationAgentResponse(agentId, response string, status core.AgentExecutionStatus, state, summary, agentStepResponse, references string) error {
	return nil
}

func (m *mockE2EConversationDao) SaveAgentReferences(accountId, conversationId, messageId, agentId string, references []core.AgentReference) error {
	return nil
}

func (m *mockE2EConversationDao) LoadConversationMessages(accountID, conversationID, userID string, requestType core.MessageType, k int) ([]map[string]string, error) {
	return nil, nil
}

// mockTargetProbeTool is a test probe for target responses
type mockTargetProbeTool struct {
	name string
}

func (m *mockTargetProbeTool) Name() string                     { return m.name }
func (m *mockTargetProbeTool) Description() string              { return "mock probe" }
func (m *mockTargetProbeTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (m *mockTargetProbeTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (m *mockTargetProbeTool) Call(_ toolcore.NbToolContext, req toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{
		Status: toolcore.NBToolResponseStatusSuccess,
		Data:   "probe ok",
	}, nil
}
