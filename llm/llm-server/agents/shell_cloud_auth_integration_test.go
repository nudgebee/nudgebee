package agents

import (
	"encoding/json"
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Integration tests: shell_execute with cloud auth injection
//
// These tests verify that cloud debug agents can use shell_execute to run
// cloud CLI commands. The shell tool auto-injects credentials based on the
// account's cloud_provider, so commands like "aws s3 ls" or "gcloud compute
// instances list" should succeed without routing through specialized tools.
//
// Prerequisites:
//   - TEST_TENANT, TEST_USER env vars set
//   - TEST_AWS_ACCOUNT / TEST_GCP_ACCOUNT / TEST_AZURE_ACCOUNT env vars set
//   - LLM provider configured
//   - Workspace + relay services running
//   - Cloud accounts configured with valid credentials in DB
// =============================================================================

func TestAwsAgent_ShellExecuteWithCloudAuth(t *testing.T) {
	// Enable shell tool for this test
	origShell := config.Config.LlmServerShellToolEnabled
	origWorkspace := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerShellToolEnabled = true
	config.Config.LlmServerWorkspaceEnabled = true
	t.Cleanup(func() {
		config.Config.LlmServerShellToolEnabled = origShell
		config.Config.LlmServerWorkspaceEnabled = origWorkspace
	})

	accountId := os.Getenv("TEST_AWS_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_AWS_ACCOUNT not set, skipping integration test")
	}

	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{accountId})
	testCases := []struct {
		SessionId string
		Query     string
	}{
		{
			SessionId: "test-shell-aws-auth-1",
			// Asking to use shell directly encourages the planner to use shell_execute
			// rather than the dedicated aws_execute tool.
			Query: "Using the shell_execute tool, run 'aws sts get-caller-identity' to verify AWS credentials are working. Just run the command and show the output.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			awsAgent := newAwsDebugAgent(accountId)

			err := core.DeleteConversationBySession(tc.SessionId, accountId, os.Getenv("TEST_USER"))
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, awsAgent, os.Getenv("TEST_USER"), accountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))

			// Verify shell_execute was used
			foundShell := false
			for _, step := range resp.AgentStepResponse {
				if step.Call.FunctionCall.Name == "shell_execute" {
					foundShell = true
					break
				}
			}
			assert.True(t, foundShell, "AWS agent should use shell_execute when explicitly asked to use shell")

			// Verify the response is not an auth error
			assert.NotContains(t, resp.Response, "Unable to locate credentials",
				"Shell command should have AWS credentials injected")
			assert.NotContains(t, resp.Response, "ExpiredTokenException",
				"AWS credentials should be valid")
		})
	}
}

func TestGcpAgent_ShellExecuteWithCloudAuth(t *testing.T) {
	origShell := config.Config.LlmServerShellToolEnabled
	origWorkspace := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerShellToolEnabled = true
	config.Config.LlmServerWorkspaceEnabled = true
	t.Cleanup(func() {
		config.Config.LlmServerShellToolEnabled = origShell
		config.Config.LlmServerWorkspaceEnabled = origWorkspace
	})

	accountId := os.Getenv("TEST_GCP_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_GCP_ACCOUNT not set, skipping integration test")
	}

	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{accountId})
	testCases := []struct {
		SessionId string
		Query     string
	}{
		{
			SessionId: "test-shell-gcp-auth-1",
			Query:     "Using the shell_execute, run 'gcloud auth list' to verify GCP credentials are working. Just run the command and show the output.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			gcpAgent := newGcpDebugAgent(accountId)

			err := core.DeleteConversationBySession(tc.SessionId, accountId, os.Getenv("TEST_USER"))
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, gcpAgent, os.Getenv("TEST_USER"), accountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))

			foundShell := false
			for _, step := range resp.AgentStepResponse {
				if step.Call.FunctionCall.Name == "shell_execute" {
					foundShell = true
					break
				}
			}
			assert.True(t, foundShell, "GCP agent should use shell_execute when explicitly asked to use shell")

			assert.NotContains(t, resp.Response, "could not find default credentials",
				"Shell command should have GCP credentials injected")
		})
	}
}

// =============================================================================
// Cross-account auth: K8s account running gcloud via shell
//
// This test verifies the exact scenario from the bug report: a conversation
// started on a K8s account uses shell_execute to run a gcloud command. The
// shell tool should detect the gcloud CLI, look up the GCP account from the
// tool_configs hint (or sole-account fallback), and inject auth.
//
// Prerequisites:
//   - TEST_ACCOUNT: a K8s (non-cloud) account ID
//   - TEST_GCP_ACCOUNT: a GCP account ID in the same tenant
//   - Both accounts must belong to the same tenant (TEST_TENANT)
// =============================================================================

func TestK8sAgent_ShellExecuteGcloudCrossAccount(t *testing.T) {
	origShell := config.Config.LlmServerShellToolEnabled
	origWorkspace := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerShellToolEnabled = true
	config.Config.LlmServerWorkspaceEnabled = true
	t.Cleanup(func() {
		config.Config.LlmServerShellToolEnabled = origShell
		config.Config.LlmServerWorkspaceEnabled = origWorkspace
	})

	k8sAccountId := os.Getenv("TEST_ACCOUNT")
	gcpAccountId := os.Getenv("TEST_GCP_ACCOUNT")
	if k8sAccountId == "" || gcpAccountId == "" {
		t.Skip("TEST_ACCOUNT and TEST_GCP_ACCOUNT required for cross-account test")
	}

	tenantId := os.Getenv("TEST_TENANT")
	userId := os.Getenv("TEST_USER")
	if tenantId == "" || userId == "" {
		t.Skip("TEST_TENANT and TEST_USER required for cross-account test")
	}

	sc := security.NewRequestContextForTenantAccountAdmin(
		tenantId, userId,
		[]string{k8sAccountId, gcpAccountId},
	)

	testCases := []struct {
		SessionId string
		Query     string
	}{
		{
			SessionId: "test-shell-cross-gcp-1",
			// Simulate a K8s debug session that needs GCP Cloud SQL data.
			// The tool_configs hint would normally be set by the planner; here
			// the agent should fall back to sole-account resolution.
			Query: "Using the shell_execute tool, run 'gcloud auth list' to check if GCP credentials are available. Just run the command and show the output.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			k8sAgent := newK8sDebugAgent(k8sAccountId)

			err := core.DeleteConversationBySession(tc.SessionId, k8sAccountId, os.Getenv("TEST_USER"))
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, os.Getenv("TEST_USER"), k8sAccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))

			// Verify shell_execute was used (not gcloud_execute)
			foundShell := false
			for _, step := range resp.AgentStepResponse {
				if step.Call.FunctionCall.Name == "shell_execute" {
					foundShell = true
					break
				}
			}
			assert.True(t, foundShell, "K8s agent should use shell_execute when explicitly asked")

			// The key assertion: gcloud should have credentials, not the
			// "no active account selected" error that triggered this fix.
			assert.NotContains(t, resp.Response, "You do not currently have an active account selected",
				"Cross-account GCP auth should be injected for gcloud commands on K8s accounts")
			assert.NotContains(t, resp.Response, "No credentialed accounts",
				"Cross-account GCP auth should provide credentials")
		})
	}
}

func TestAzureAgent_ShellExecuteWithCloudAuth(t *testing.T) {
	origShell := config.Config.LlmServerShellToolEnabled
	origWorkspace := config.Config.LlmServerWorkspaceEnabled
	config.Config.LlmServerShellToolEnabled = true
	config.Config.LlmServerWorkspaceEnabled = true
	t.Cleanup(func() {
		config.Config.LlmServerShellToolEnabled = origShell
		config.Config.LlmServerWorkspaceEnabled = origWorkspace
	})

	accountId := os.Getenv("TEST_AZURE_ACCOUNT")
	if accountId == "" {
		t.Skip("TEST_AZURE_ACCOUNT not set, skipping integration test")
	}

	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{accountId})
	testCases := []struct {
		SessionId string
		Query     string
	}{
		{
			SessionId: "test-shell-azure-auth-1",
			Query:     "Using the shell_execute, run 'az account show' to verify Azure credentials are working. Just run the command and show the output.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.SessionId, func(t *testing.T) {
			azureAgent := newAzureDebugAgent(accountId)

			err := core.DeleteConversationBySession(tc.SessionId, accountId, os.Getenv("TEST_USER"))
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, azureAgent, os.Getenv("TEST_USER"), accountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)

			fmt.Println("response - ", resp.Response)
			invocationLog, _ := json.Marshal(resp.AgentStepResponse)
			fmt.Println("tools - ", string(invocationLog))

			foundShell := false
			for _, step := range resp.AgentStepResponse {
				if step.Call.FunctionCall.Name == "shell_execute" {
					foundShell = true
					break
				}
			}
			assert.True(t, foundShell, "Azure agent should use shell_execute when explicitly asked to use shell")

			assert.NotContains(t, resp.Response, "Please run 'az login'",
				"Shell command should have Azure credentials injected")
		})
	}
}
