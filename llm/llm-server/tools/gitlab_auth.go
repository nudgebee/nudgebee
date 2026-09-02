package tools

import (
	"fmt"
	"net/url"
	"nudgebee/llm/tools/core"
	"os"
	"path/filepath"
	"strings"
)

// extractGitlabConfigFields pulls url and password from a gitlab integration's
// ToolConfig values. Password is already decrypted by ListToolConfigs for
// ToolConfigSourceTicket integrations.
//
// There is deliberately no auth_type to branch on, unlike github: api-server's
// gitlab ConfigSchema (services/integrations/gitlab.go) declares only
// url/username/password/group, so GitLab is personal-access-token only and
// `password` is always the raw token.
func extractGitlabConfigFields(values []core.ToolConfigValue) (apiUrl, password string) {
	for _, v := range values {
		switch v.Name {
		case "url":
			apiUrl = v.Value
		case "password":
			password = v.Value
		}
	}
	return
}

// gitlabHostEnv returns the GITLAB_HOST value glab needs to target a self-hosted
// instance, or "" for gitlab.com (where glab's own default is already correct).
//
// The stored `url` has a different shape from the github integration's: github
// stores an API host ("api.github.com"), gitlab stores the base web URL
// ("https://gitlab.com" — the config modal's default). A bare host with no
// scheme is tolerated because the field is free text; without the prefix,
// url.Parse would put it in Path, leave Host empty, and we would silently target
// gitlab.com with a self-hosted token.
func gitlabHostEnv(apiUrl string) string {
	apiUrl = strings.TrimSpace(apiUrl)
	if apiUrl == "" {
		return ""
	}
	if !strings.Contains(apiUrl, "://") {
		apiUrl = "https://" + apiUrl
	}
	parsed, err := url.Parse(apiUrl)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if strings.EqualFold(parsed.Hostname(), "gitlab.com") {
		return ""
	}
	// Host rather than Hostname so a non-default port survives.
	return parsed.Host
}

// BuildGitlabAuth builds the env needed to authenticate `glab` CLI commands in a
// workspace. Returns a CloudAuthResult (the generic auth-carrier type, despite
// the "Cloud" name) so the shell tool can merge it alongside cloud auth.
func BuildGitlabAuth(cfg core.ToolConfig) (*CloudAuthResult, error) {
	apiUrl, token := extractGitlabConfigFields(cfg.Values)
	if token == "" {
		return nil, fmt.Errorf("gitlab: empty personal access token in integration config")
	}

	env := map[string]string{
		"GITLAB_TOKEN": token,
		// glab refuses to run at all if it cannot create its config directory,
		// which it locates from HOME. The local-exec path (ExecuteCliCommand)
		// passes only the auth vars plus PATH — no HOME — so glab resolves the
		// directory to a system location it cannot write and every command dies
		// before reaching the API ("failed to create config directory ...
		// permission denied"). Pointing it at a guaranteed-writable path makes
		// the tool behave identically in both execution modes. Nothing sensitive
		// lands there: auth is supplied through GITLAB_TOKEN on every call, so
		// the directory only ever holds glab's own defaults.
		"GLAB_CONFIG_DIR": filepath.Join(os.TempDir(), "glab-cli"),
	}
	// glab reads GITLAB_HOST (not GLAB_HOST) to target non-gitlab.com instances.
	if host := gitlabHostEnv(apiUrl); host != "" {
		env["GITLAB_HOST"] = host
	}

	return &CloudAuthResult{Env: env}, nil
}
