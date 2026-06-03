//go:build e2e

package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logAnalysisGitData2 = `
{
  "errors": ["{\"time\":\"2026-03-10T02:10:09.135937654Z\",\"level\":\"ERROR\",\"msg\":\"Failed to process rule, continuing with next rule\",\"cron_job\":\"Insight refresh\",\"cron_id\":\"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa\",\"trace_id\":\"00000000000000000000000000000000\",\"ruleId\":\"17\",\"error\":{\"message\":\"invalid aggregate column: invalid SQL identifier: \\\"max(workload_namespace_count)\\\"\",\"type\":\"*fmt.wrapError\",\"stacktrace\":\"nudgebee/services/insight.Process(0xc02da91620, {0x0, 0x0, 0x0})\\n\\t/app/insight/service.go:198 +0x7cd\\nnudgebee/services/api.handleCrons.func1.2()\\n\\t/app/api/cron.go:106 +0x8a\\ncreated by nudgebee/services/api.handleCrons.func1 in goroutine 1064\\n\\t/app/api/cron.go:102 +0x42b\\n\"}}"],
  "files": [{"file_name":"service.go", "file_path":"api-server/services/insight/service.go"}],
  "git_repo": "https://github.com/nudgebee/nudgebee",
  "query": "Analyze the failures in the logs and suggest how to fix them."
}
`

func TestLogAnalysisCode2_ExecuteLogAnalysis(t *testing.T) {
	codeAnalysis := CodeAgent2{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	// Use workspace mode with local code-analysis server
	config.Config.LlmServerWorkspaceEnabled = true
	config.Config.LlmServerWorkspaceLocalUrl = "http://localhost:8080"
	defer func() {
		config.Config.LlmServerWorkspaceEnabled = false
		config.Config.LlmServerWorkspaceLocalUrl = ""
	}()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-code-chain-1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     logAnalysisGitData2,
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, codeAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, codeAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestLogAnalysisCode2_ExecuteShellCommand(t *testing.T) {
	codeAnalysis := CodeAgent2{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	config.Config.LlmServerCodeAgentMode = "remote-cli"

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-code-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you show files in temp directory",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, codeAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, codeAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

func TestCodeAgent2_UsingCli(t *testing.T) {

	codeAnalysis := CodeAgent2{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-code-chain-1.1",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     logAnalysisGitData2,
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, codeAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, codeAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}

func TestLogAnalysisCode1_ExecuteShellCommand(t *testing.T) {
	codeAnalysis := CodeAgent2{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})
	config.Config.LlmServerCodeAgentMode = "remote-cli"

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-code-chain-2",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     "Can you list last 5 pr from nudgebee/nudgebee",
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, codeAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, codeAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

// TestCodeAgent2_UsingWorkspace is an integration test that runs code analysis
// via the workspace /analyze endpoint instead of launching a pod.
// Requires: K8s access, workspace pod running, DB access, git credentials configured.
// Run with: go test -v -run TestCodeAgent2_UsingWorkspace ./agents/...

func TestCodeAgent2_UsingWorkspace(t *testing.T) {
	codeAnalysis := CodeAgent2{}
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{})

	// Enable workspace mode
	config.Config.LlmServerWorkspaceEnabled = true
	defer func() {
		config.Config.LlmServerWorkspaceEnabled = false
	}()

	testCases := []struct {
		name      string
		SessionId string
		Query     string
		AccountId string
		UserId    string
	}{
		{
			name:      "workspace_code_analysis",
			SessionId: "ut-code-chain-ws-1",
			AccountId: os.Getenv("TEST_ACCOUNT"),
			UserId:    os.Getenv("TEST_USER"),
			Query:     logAnalysisGitData2,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
			assert.Nil(t, err)

			resp, err := core.HandleConversationSessionRequest(sc, codeAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, codeAnalysis.GetName(), resp.AgentName)
			assert.NotEmpty(t, resp.Query)
			assert.Greater(t, len(resp.Response), 0)
		})
	}
}

// TestCodeAgent2_WorkspaceResponseParsing tests that the workspace response
// from /analyze is correctly parsed into the expected output format.
