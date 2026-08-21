package agents

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGithubAgent_HasCodeAgentTool(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	githubAgent := newGithubAgent(accountId)

	supportedTools := githubAgent.GetSupportedTools(sc)
	toolNames := make([]string, len(supportedTools))
	for i, tool := range supportedTools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "github_execute", "should have github_execute tool")
	assert.Contains(t, toolNames, AgentCodeAnalyzer, "should have code_analyzer tool for code fixes and PR creation")
}
