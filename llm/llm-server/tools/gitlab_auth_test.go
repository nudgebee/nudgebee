package tools

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitlabCfg(url, password string) core.ToolConfig {
	return core.ToolConfig{
		Name: "gitlab-prod",
		Values: []core.ToolConfigValue{
			{Name: "auth_type", Value: "token"},
			{Name: "username", Value: "someuser"},
			{Name: "url", Value: url},
			{Name: "password", Value: password},
		},
	}
}

func TestBuildGitlabAuth_SaaSOmitsHost(t *testing.T) {
	auth, err := BuildGitlabAuth(gitlabCfg("https://gitlab.com", "glpat-TestTokenAbc123"))
	require.NoError(t, err)
	require.NotNil(t, auth)
	assert.Equal(t, "glpat-TestTokenAbc123", auth.Env["GITLAB_TOKEN"])
	// glab already defaults to gitlab.com; setting GITLAB_HOST there is noise.
	assert.NotContains(t, auth.Env, "GITLAB_HOST")
	// Without a writable config dir glab aborts before making any request —
	// ExecuteCliCommand passes no HOME, so this must always be set.
	assert.NotEmpty(t, auth.Env["GLAB_CONFIG_DIR"], "glab needs a writable config dir or every command fails")
	assert.Empty(t, auth.CommandPrefix)
	assert.Empty(t, auth.CommandSuffix)
}

func TestBuildGitlabAuth_SelfHostedSetsHost(t *testing.T) {
	auth, err := BuildGitlabAuth(gitlabCfg("https://gitlab.example.com", "glpat-TestTokenAbc123"))
	require.NoError(t, err)
	require.NotNil(t, auth)
	assert.Equal(t, "gitlab.example.com", auth.Env["GITLAB_HOST"])
}

func TestBuildGitlabAuth_EmptyToken(t *testing.T) {
	_, err := BuildGitlabAuth(gitlabCfg("https://gitlab.com", ""))
	require.Error(t, err)
}

func TestGitlabHostEnv(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"saas", "https://gitlab.com", ""},
		{"saas with trailing slash", "https://gitlab.com/", ""},
		{"saas uppercase", "https://GitLab.com", ""},
		{"self-hosted", "https://gitlab.example.com", "gitlab.example.com"},
		{"self-hosted keeps port", "https://gitlab.example.com:8080", "gitlab.example.com:8080"},
		// The integration's url field is free text, so a scheme-less host is
		// plausible. Without the prefix fallback url.Parse would leave Host
		// empty and we would silently target gitlab.com with a self-hosted token.
		{"bare host without scheme", "gitlab.example.com", "gitlab.example.com"},
		{"bare saas host without scheme", "gitlab.com", ""},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, gitlabHostEnv(c.url), "url: %q", c.url)
		})
	}
}

func TestExtractGitlabConfigFields(t *testing.T) {
	values := []core.ToolConfigValue{
		{Name: "id", Value: "ignored"},
		{Name: "username", Value: "ignored"},
		{Name: "url", Value: "https://gitlab.example.com"},
		{Name: "password", Value: "glpat-Abc123"},
		{Name: "projects", Value: "ignored"},
	}
	apiUrl, password := extractGitlabConfigFields(values)
	assert.Equal(t, "https://gitlab.example.com", apiUrl)
	assert.Equal(t, "glpat-Abc123", password)
}

func TestDetectGitlabCLI(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"glab mr list", true},
		{"glab ci trace 123 --repo group/project", true},
		{"ls && glab auth status", true},
		{"glabber --version", false},
		{"gl list", false},
		{"gitlab-cli list", false},
		{"gh issue list", false},
		{"ls -la", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			assert.Equal(t, c.want, detectGitlabCLI(c.cmd), "cmd: %q", c.cmd)
		})
	}
}

// The two SCM detectors must not claim each other's commands — they run
// back-to-back over the same shell command in ShellTool.Call.
func TestScmDetectorsDoNotOverlap(t *testing.T) {
	assert.False(t, detectGithubCLI("glab mr list"), "glab must not trip the gh matcher")
	assert.False(t, detectGitlabCLI("gh pr list"), "gh must not trip the glab matcher")
}

// gitlabErrorHint must fire on the real strings glab emits (captured from the
// v1.112.0 binary) and stay silent otherwise, so an unrecognized error reaches
// the model verbatim instead of wrapped in a misleading hint.
func TestGitlabErrorHint(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantSubstr string
	}{
		{"unknown flag", "ERROR\n\nUnknown flag: --limit.\n\nTry --help for usage.", "--per-page"},
		{"404 from bad project", "ERROR\n\n404 Not Found.", "group/project"},
		{"unauthorized", "ERROR\n\n401 Unauthorized", "scope"},
		{"forbidden", "ERROR\n\n403 Forbidden", "scope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hint := gitlabErrorHint(c.raw)
			assert.NotEmpty(t, hint, "expected a hint for %q", c.raw)
			assert.Contains(t, hint, c.wantSubstr)
		})
	}

	// Unrecognized errors get no hint, matching githubErrorHint's contract.
	assert.Empty(t, gitlabErrorHint("connection reset by peer"))
	assert.Empty(t, gitlabErrorHint(""))
}

func TestScrubCredentials_GitlabToken(t *testing.T) {
	env := map[string]string{
		"GITLAB_TOKEN": "glpat-VeryLongSecretTokenExample1234",
	}
	out := ScrubCredentials("token=glpat-VeryLongSecretTokenExample1234 in env", env)
	assert.NotContains(t, out, "glpat-VeryLongSecretTokenExample1234")
	assert.Contains(t, out, "[REDACTED]")
}
