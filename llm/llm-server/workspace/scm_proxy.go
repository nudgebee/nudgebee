package workspace

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"nudgebee/llm/config"
)

// gh and glab in a workspace command run against llm-server's SCM proxy
// instead of holding the integration's token (#41852). The tool marks its env
// with the integrations the command may use; commandEnv consumes the markers,
// binds the command's execution token to them and points gh/glab at the proxy
// with that token. The markers never reach the pod.
const (
	ENV_NB_SCM_GITHUB_TOOL   = "NB_SCM_GITHUB_TOOL"
	ENV_NB_SCM_GITHUB_CONFIG = "NB_SCM_GITHUB_CONFIG"
	ENV_NB_SCM_GITLAB_TOOL   = "NB_SCM_GITLAB_TOOL"
	ENV_NB_SCM_GITLAB_CONFIG = "NB_SCM_GITLAB_CONFIG"
	ENV_NB_SCM_WRITE         = "NB_SCM_WRITE"

	ScmProviderGithub = "github"
	ScmProviderGitlab = "gitlab"

	// workspaceProxyCADir is where workspace pods mount the proxy's CA certificate.
	// gh and glab are Go programs: they read every file in SSL_CERT_DIR, while
	// OpenSSL tools (curl) read only hashed names there, so adding this
	// directory changes nothing for them.
	workspaceProxyCADir = "/etc/nudgebee/workspace-proxy"
	workspaceProxyCAKey = "ca.crt"
)

// ScmBinding names one integration: the tool whose configs hold it, and the
// config's name.
type ScmBinding struct {
	Tool   string `json:"tool"`
	Config string `json:"config"`
}

// ScmClaims bind an execution token to the GitHub and/or GitLab integration a
// command may use. Write is true only for a call that passed the write
// approval gate.
type ScmClaims struct {
	Github *ScmBinding `json:"github,omitempty"`
	Gitlab *ScmBinding `json:"gitlab,omitempty"`
	Write  bool        `json:"write,omitempty"`
}

// Binding returns the integration bound for provider, or nil.
func (c *ScmClaims) Binding(provider string) *ScmBinding {
	if c == nil {
		return nil
	}
	switch provider {
	case ScmProviderGithub:
		return c.Github
	case ScmProviderGitlab:
		return c.Gitlab
	}
	return nil
}

// WorkspaceProxyTLSHost is the host[:port] gh and glab are pointed at: the configured
// host, else llm-server's own host with the proxy port. The proxy's
// certificate is issued for this name.
func WorkspaceProxyTLSHost() string {
	if h := strings.TrimSpace(config.Config.LlmServerWorkspaceProxyTLSHost); h != "" {
		return h
	}
	host := "llm-server"
	if u, err := url.Parse(config.Config.LlmServerUrl); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return net.JoinHostPort(host, strconv.Itoa(config.Config.LlmServerWorkspaceProxyTLSPort))
}

// takeScmClaims removes the SCM markers from env and returns the claims they
// describe, or nil when the command is not bound to any integration.
func takeScmClaims(env map[string]string) (*ScmClaims, error) {
	binding := func(toolKey, configKey string) (*ScmBinding, error) {
		tool, hasTool := env[toolKey]
		cfg, hasConfig := env[configKey]
		delete(env, toolKey)
		delete(env, configKey)
		if !hasTool && !hasConfig {
			return nil, nil
		}
		if tool == "" || cfg == "" {
			return nil, fmt.Errorf("workspace: incomplete scm proxy binding")
		}
		return &ScmBinding{Tool: tool, Config: cfg}, nil
	}
	github, err := binding(ENV_NB_SCM_GITHUB_TOOL, ENV_NB_SCM_GITHUB_CONFIG)
	if err != nil {
		return nil, err
	}
	gitlab, err := binding(ENV_NB_SCM_GITLAB_TOOL, ENV_NB_SCM_GITLAB_CONFIG)
	if err != nil {
		return nil, err
	}
	write := env[ENV_NB_SCM_WRITE] == "true"
	delete(env, ENV_NB_SCM_WRITE)
	if github == nil && gitlab == nil {
		return nil, nil
	}
	return &ScmClaims{Github: github, Gitlab: gitlab, Write: write}, nil
}

// addScmEnv points gh and/or glab at the proxy, authenticated with the
// command's execution token. The integration's own token is never set; a stray
// GH_TOKEN/GITHUB_TOKEN/GITLAB_TOKEN from the caller is removed so nothing else
// gets a credential.
func addScmEnv(env map[string]string, claims *ScmClaims, token string) {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GITLAB_TOKEN", "GITLAB_HOST"} {
		delete(env, k)
	}
	host := WorkspaceProxyTLSHost()
	certDirs := "/etc/ssl/certs"
	if existing := env["SSL_CERT_DIR"]; existing != "" {
		certDirs = existing
	}
	env["SSL_CERT_DIR"] = certDirs + ":" + workspaceProxyCADir
	if claims.Github != nil {
		env["GH_HOST"] = host
		env["GH_ENTERPRISE_TOKEN"] = token
		env["GH_NO_UPDATE_NOTIFIER"] = "1"
		env["GH_PROMPT_DISABLED"] = "1"
	}
	if claims.Gitlab != nil {
		env["GITLAB_HOST"] = host
		env["GITLAB_TOKEN"] = token
		env["GLAB_SEND_TELEMETRY"] = "false"
		env["GLAB_CHECK_UPDATE"] = "false"
	}
}

// scrubToken masks the command's execution token in its output: the token is
// in the command's env, so `env` or `printenv` would otherwise print it.
func scrubToken(output, token string) string {
	if len(token) < 16 {
		return output
	}
	return strings.ReplaceAll(output, token, "[REDACTED]")
}
