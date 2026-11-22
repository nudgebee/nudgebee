package tools

import (
	"log/slog"
	"strconv"

	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// scmProxyEnabled reports whether gh/glab go through llm-server's SCM proxy
// instead of receiving the integration's token (#41852).
func scmProxyEnabled() bool {
	return config.Config.LlmServerWorkspaceScmEnabled
}

// scmProxyEnv marks a command's env so the workspace binds its execution token
// to these integrations (either may be nil) and points gh/glab at the SCM
// proxy. Over REST, the proxy lets the command change something only when the
// approval gate classified the call as a write and it passed the gate; GraphQL
// is passed through for now.
func scmProxyEnv(nbRequestContext core.NbToolContext, github, gitlab *core.ToolConfig) map[string]string {
	env := map[string]string{
		workspace.ENV_NB_SCM_WRITE: strconv.FormatBool(isApprovedWrite(nbRequestContext.RequestType)),
	}
	if github != nil {
		env[workspace.ENV_NB_SCM_GITHUB_TOOL] = ToolExecuteGithubCliCommand
		env[workspace.ENV_NB_SCM_GITHUB_CONFIG] = github.Name
	}
	if gitlab != nil {
		env[workspace.ENV_NB_SCM_GITLAB_TOOL] = ToolExecuteGitlabCliCommand
		env[workspace.ENV_NB_SCM_GITLAB_CONFIG] = gitlab.Name
	}
	return env
}

// scmProxyEnvForCommand binds every free-form workspace command (shell,
// remediation) to the account's GitHub and GitLab integrations, whatever the
// command looks like: a script that calls gh gets the same access as a direct
// gh call (#41077). Only the command's short-lived execution token enters the
// pod, so binding commands that never call gh hands out nothing new.
func scmProxyEnvForCommand(nbRequestContext core.NbToolContext, accountId string) map[string]string {
	github, err := resolveScmToolConfig(nbRequestContext, accountId, ToolExecuteGithubCliCommand)
	if err != nil {
		slog.Warn("scm proxy: github integration lookup failed", "account_id", accountId, "error", err)
	}
	gitlab, err := resolveScmToolConfig(nbRequestContext, accountId, ToolExecuteGitlabCliCommand)
	if err != nil {
		slog.Warn("scm proxy: gitlab integration lookup failed", "account_id", accountId, "error", err)
	}
	if github == nil && gitlab == nil {
		return nil
	}
	return scmProxyEnv(nbRequestContext, github, gitlab)
}

func isApprovedWrite(t core.ToolRequestType) bool {
	switch t {
	case core.ToolRequestTypeCreate, core.ToolRequestTypeUpdate, core.ToolRequestTypeDelete:
		return true
	}
	return false
}
