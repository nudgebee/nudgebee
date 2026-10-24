package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

func TestScmProxyEnvGrantsWritesOnlyToApprovedWrites(t *testing.T) {
	gh := &core.ToolConfig{Name: "acme-github"}
	for requestType, want := range map[core.ToolRequestType]string{
		core.ToolRequestTypeCreate: "true",
		core.ToolRequestTypeUpdate: "true",
		core.ToolRequestTypeDelete: "true",
		core.ToolRequestTypeRead:   "false",
		"":                         "false", // called outside the planner's approval gate
		"unknown":                  "false",
	} {
		env := scmProxyEnv(core.NbToolContext{RequestType: requestType}, gh, nil)
		assert.Equal(t, map[string]string{
			workspace.ENV_NB_SCM_GITHUB_TOOL:   ToolExecuteGithubCliCommand,
			workspace.ENV_NB_SCM_GITHUB_CONFIG: "acme-github",
			workspace.ENV_NB_SCM_WRITE:         want,
		}, env, "no token is ever in the env (%s)", requestType)
	}
}

func withScmProxy(t *testing.T, enabled bool) {
	t.Helper()
	prev := config.Config.LlmServerWorkspaceScmEnabled
	config.Config.LlmServerWorkspaceScmEnabled = enabled
	t.Cleanup(func() { config.Config.LlmServerWorkspaceScmEnabled = prev })
}

// #41077: scripts that call gh (Python subprocess, f-strings, heredocs with an
// apostrophe) were missed by the command matcher and ran with no credentials.
// Through the proxy every shell command is bound, whatever it looks like, and
// only the command's own execution token ever enters the pod.
func TestShellBindsEveryCommandToTheScmProxy(t *testing.T) {
	const account = "scm-shell-every-command"
	cacheShellTargets(t, account, ToolExecuteGithubCliCommand, []core.ToolConfig{{Name: "acme-github", Values: []core.ToolConfigValue{{Name: "password", Value: "ghp_example"}}}})
	cacheShellTargets(t, account, ToolExecuteGitlabCliCommand, []core.ToolConfig{{Name: "acme-gitlab", Values: []core.ToolConfigValue{{Name: "password", Value: "glpat-example"}}}})
	commands := map[string]string{
		"direct gh":            "gh pr list --repo o/r",
		"python subprocess":    "python3 - <<'EOF'\nimport subprocess\nsubprocess.run([\"gh\", \"api\", \"repos/o/r/pulls\"])\nEOF",
		"python f-string":      "python3 -c \"import os; os.system(f'gh api repos/o/r/pulls?page={1}')\"",
		"heredoc + apostrophe": "cat > /tmp/x.sh <<EOF\n# Let's page through PRs\ngh api repos/o/r/pulls\nEOF\nsh /tmp/x.sh",
		"gh and glab":          "gh pr list --repo o/r && glab mr list",
		"no scm at all":        "ls -la",
	}
	sc := security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{account})

	withScmProxy(t, true)
	for name, command := range commands {
		capture := &targetWorkspaceCapture{}
		tool := ShellTool{AccountId: account, workspaceManager: capture}
		ctx := core.NewNbToolContext(sc, tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{}, "call")
		_, err := tool.Call(ctx, core.NBToolCallRequest{Command: command})
		require.NoError(t, err, name)
		assert.Equal(t, "acme-github", capture.env[workspace.ENV_NB_SCM_GITHUB_CONFIG], name)
		assert.Equal(t, "acme-gitlab", capture.env[workspace.ENV_NB_SCM_GITLAB_CONFIG], name)
		assert.Equal(t, "false", capture.env[workspace.ENV_NB_SCM_WRITE], "%s: not an approved write", name)
		for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN"} {
			assert.NotContains(t, capture.env, k, "%s: no real token in the env", name)
		}
	}

	withScmProxy(t, false)
	capture := &targetWorkspaceCapture{}
	tool := ShellTool{AccountId: account, workspaceManager: capture}
	ctx := core.NewNbToolContext(sc, tool, account, "user", "conversation", "message", "agent", "", nil, "", core.NBQueryConfig{}, "call")
	_, err := tool.Call(ctx, core.NBToolCallRequest{Command: "ls -la"})
	require.NoError(t, err)
	assert.NotContains(t, capture.env, workspace.ENV_NB_SCM_GITHUB_CONFIG, "with the proxy off nothing changes: commands without gh get nothing")
	assert.NotContains(t, capture.env, "GITHUB_TOKEN")
}
