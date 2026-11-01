//go:build e2e

package core

import (
	"fmt"
	"os"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// #39826: a k8s cluster conversation (no AWS or GCP config of its own) runs a
// cloud command that names a connected account. It must run there through
// the planner's tool call path, not be refused as unconfigured.
func TestCallNbTool_NamedCloudAccountFromClusterConversation(t *testing.T) {
	RequireEnv(t, "TEST_ACCOUNT", "TEST_USER", "TEST_TENANT")
	accountId := os.Getenv("TEST_ACCOUNT")
	sc := security.NewRequestContextForTenantAdminWithUser(t.Context(), os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"))
	request := func() NBAgentRequest {
		return NBAgentRequest{AccountId: accountId, UserId: os.Getenv("TEST_USER"), ConversationId: uuid.NewString(), MessageId: uuid.NewString(), AgentId: uuid.NewString()}
	}
	call := func(t *testing.T, toolName, input string) toolcore.NBToolResponse {
		tool, ok := toolcore.GetNBTool(accountId, toolName)
		require.True(t, ok)
		configs, err := toolcore.ListToolConfigs(sc, accountId, tool)
		require.NoError(t, err)
		require.Empty(t, configs, "the conversation's account must have no %s config of its own, or this does not reproduce #39826", toolName)
		resp, err := callNbTool(sc, request(), "named-target-e2e", tool, input, nil, "", uuid.NewString(), "")
		t.Logf("%s: status=%s err=%v data=%.300q", toolName, resp.Status, err, fmt.Sprint(resp.Data))
		require.NoError(t, err)
		return resp
	}

	t.Run("aws", func(t *testing.T) {
		target := envOr("TEST_DELEGATE_AWS_TARGET", "aws-prod")
		selected, err := tools.AccountTargetByName(sc, accountId, tools.ToolExecuteAwsCliCommand, target)
		require.NoError(t, err, "%s must be a connected AWS account in this tenant", target)
		expected := ""
		for _, v := range selected.Values {
			if v.Name == "account_number" {
				expected = v.Value
			}
		}
		require.NotEmpty(t, expected)

		resp := call(t, tools.ToolExecuteAwsCliCommand, fmt.Sprintf(`{"account":%q,"command":"aws sts get-caller-identity"}`, target))
		assert.NotEqual(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, fmt.Sprint(resp.Data), expected, "the identity must be %s's", target)

		// Naming nothing still has nothing to run against.
		resp = call(t, tools.ToolExecuteAwsCliCommand, `{"command":"aws sts get-caller-identity"}`)
		assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, fmt.Sprint(resp.Data), "requires configuration")
	})

	// A delegated child handed aws-prod: the planner fills the handed account
	// into the call's input, which must then pass the same gate.
	t.Run("delegated aws", func(t *testing.T) {
		target := envOr("TEST_DELEGATE_AWS_TARGET", "aws-prod")
		handed, err := tools.ResolveAgentHandoffTarget(sc, accountId, &toolcore.ExecutionTarget{Provider: "aws", Name: target})
		require.NoError(t, err)
		tool, ok := toolcore.GetNBTool(accountId, tools.ToolExecuteAwsCliCommand)
		require.True(t, ok)
		prepared, err := prepareDelegatedActionInput(tool, handed, accountId, NBAgentPlannerToolAction{Tool: tool.Name(), ToolInput: `{"command":"aws sts get-caller-identity"}`})
		require.NoError(t, err)
		t.Logf("prepared input: %s", prepared.ToolInput)
		req := request()
		req.QueryConfig.DelegatedTarget = handed
		resp, err := callNbTool(sc, req, "named-target-e2e", tool, prepared.ToolInput, nil, "", uuid.NewString(), "")
		t.Logf("delegated aws_execute: status=%s err=%v data=%.300q", resp.Status, err, fmt.Sprint(resp.Data))
		require.NoError(t, err)
		assert.NotEqual(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.NotContains(t, fmt.Sprint(resp.Data), "requires configuration")
	})

	t.Run("gcp", func(t *testing.T) {
		target, project := os.Getenv("TEST_DELEGATE_GCP_TARGET"), os.Getenv("TEST_DELEGATE_GCP_PROJECT")
		if target == "" || project == "" {
			t.Skip("TEST_DELEGATE_GCP_TARGET and TEST_DELEGATE_GCP_PROJECT are not set")
		}
		resp := call(t, tools.ToolExecuteGcpCliCommand, fmt.Sprintf(`{"account":%q,"command":"gcloud projects describe %s --format='value(projectId)'"}`, target, project))
		assert.NotEqual(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.Contains(t, fmt.Sprint(resp.Data), project)
	})

	// Azure's dev credentials may be invalid. Whatever Azure says, the command
	// must reach the named subscription: not be refused as unconfigured, not
	// answered from the conversation's own account, and labelled with where
	// it ran.
	t.Run("azure", func(t *testing.T) {
		target := envOr("TEST_DELEGATE_AZURE_TARGET", "nudgebee-azure")
		resp := call(t, tools.ToolExecuteAzureCliCommand, fmt.Sprintf(`{"account":%q,"command":"az account show --query id -o tsv"}`, target))
		assert.NotContains(t, fmt.Sprint(resp.Data), "requires configuration")
		assert.NotContains(t, fmt.Sprint(resp.Data), "not connected or you do not have access")
		ranOn, _ := resp.AdditionalDetails[toolcore.MetadataExecutedTarget].(map[string]any)
		assert.Equal(t, "azure", ranOn["provider"], "the step must say it ran on the Azure account")
		assert.Equal(t, target, ranOn["name"])
	})
}
