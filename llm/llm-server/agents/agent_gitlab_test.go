package agents

import (
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGitlabAgent_HasCodeAgentTool(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	accountId := os.Getenv("TEST_ACCOUNT")
	gitlabAgent := newGitlabAgent(accountId)

	supportedTools := gitlabAgent.GetSupportedTools(sc)
	toolNames := make([]string, len(supportedTools))
	for i, tool := range supportedTools {
		toolNames[i] = tool.Name()
	}

	assert.Contains(t, toolNames, "gitlab_execute", "should have gitlab_execute tool")
	assert.Contains(t, toolNames, AgentCodeAnalyzer, "should have code_analyzer tool for code fixes and MR creation")
}

// The agent is watch-capable, and watch sources are rejected unless the tool
// name ends in _execute (watch/source.go). Guard the naming contract so a
// future rename can't silently disable background pipeline watches.
func TestGitlabTool_NameIsWatchCompatible(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	gitlabAgent := newGitlabAgent(os.Getenv("TEST_ACCOUNT"))

	assert.True(t, gitlabAgent.(GitlabAgent).IsWatchCapable(), "gitlab triggers pipelines that complete later")

	for _, tool := range gitlabAgent.GetSupportedTools(sc) {
		if tool.Name() == "gitlab_execute" {
			return
		}
	}
	t.Fatal("gitlab_execute not found in supported tools")
}
