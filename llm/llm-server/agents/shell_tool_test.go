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

func TestK8sAgent_ShellToolUsage(t *testing.T) {
	// Ensure shell tool is enabled
	originalVal := config.Config.LlmServerShellToolEnabled
	config.Config.LlmServerShellToolEnabled = true
	t.Cleanup(func() { config.Config.LlmServerShellToolEnabled = originalVal })

	testCases := []struct {
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			SessionId: "test-shell-usage-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			// This query is designed to be impossible for kubectl but easy for shell
			Query: `Check if the file /tmp/test_report.txt exists in the workspace. If it does, read its first 5 lines. If not, create it with "Hello World" and then verify it.`,
		},
	}

	for _, tc := range testCases {
		sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), tc.UserId, []string{tc.AccountId})
		k8sAgent := newK8sDebugAgent(tc.AccountId)

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, k8sAgent, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)

		fmt.Println("response - ", resp.Response)
		invocationLog, _ := json.Marshal(resp.AgentStepResponse)
		fmt.Println("tools - ", string(invocationLog))

		// Verify if shell_execute was among the tool calls
		foundShell := false
		for _, step := range resp.AgentStepResponse {
			if step.Call.FunctionCall.Name == "shell_execute" {
				foundShell = true
				break
			}
		}

		assert.True(t, foundShell, "Agent should have used shell_execute for a workspace file operation")
	}
}
