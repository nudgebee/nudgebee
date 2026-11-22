package adapter

import (
	"encoding/base64"
	"os"
	"strings"
)

// gitTokenUsername is the HTTP Basic user name sent alongside the token. Both
// GitHub and GitLab accept "oauth2" with an access token as the password; it
// is what the clone URL carried before the token moved out of it.
const gitTokenUsername = "oauth2"

// gitHTTPAuthEnv returns the environment for a git command that talks to the
// remote under baseURL (e.g. "https://github.com/"). The token travels as an
// Authorization header set through ephemeral GIT_CONFIG_* variables, so it is
// never on the command line (readable by any process via ps or
// /proc/<pid>/cmdline) and never written into the clone's .git/config. The
// header is scoped to baseURL so a redirect to another host does not carry it.
// GIT_TERMINAL_PROMPT=0 makes a missing or rejected token fail fast instead of
// waiting on a prompt nobody can answer.
func gitHTTPAuthEnv(baseURL, token string) []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if token == "" {
		return env
	}
	basic := base64.StdEncoding.EncodeToString([]byte(gitTokenUsername + ":" + token))
	return append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http."+baseURL+".extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
	)
}

// githubGitEnv is gitHTTPAuthEnv for github.com.
func githubGitEnv(token string) []string {
	return gitHTTPAuthEnv("https://github.com/", token)
}

// redactGitOutput removes credentials from git output before it is logged or
// returned in an error: any user:password@ embedded in a URL, and the token
// itself wherever it appears.
func redactGitOutput(s, token string) string {
	s = redactGitCredentials(s)
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}
