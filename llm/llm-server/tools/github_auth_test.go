package tools

import (
	"context"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGithubAuth_PAT(t *testing.T) {
	cfg := core.ToolConfig{
		Name: "github-prod",
		Values: []core.ToolConfigValue{
			{Name: "auth_type", Value: "personal"},
			{Name: "url", Value: "https://api.github.com"},
			{Name: "password", Value: "ghp_TestPersonalAccessTokenAbc123"},
		},
	}
	auth, err := BuildGithubAuth(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, auth)
	assert.Equal(t, "ghp_TestPersonalAccessTokenAbc123", auth.Env["GITHUB_TOKEN"])
	assert.Empty(t, auth.CommandPrefix)
	assert.Empty(t, auth.CommandSuffix)
}

func TestBuildGithubAuth_EmptyToken(t *testing.T) {
	cfg := core.ToolConfig{
		Name: "github-broken",
		Values: []core.ToolConfigValue{
			{Name: "auth_type", Value: "personal"},
			{Name: "password", Value: ""},
		},
	}
	_, err := BuildGithubAuth(context.Background(), cfg)
	require.Error(t, err)
}

func TestBuildGithubAuth_AppInvalidInstallationID(t *testing.T) {
	cfg := core.ToolConfig{
		Name: "github-app",
		Values: []core.ToolConfigValue{
			{Name: "auth_type", Value: "application"},
			{Name: "url", Value: "https://api.github.com"},
			{Name: "password", Value: "not-a-number"},
		},
	}
	_, err := BuildGithubAuth(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid installation_id")
}

func TestExtractGithubConfigFields(t *testing.T) {
	values := []core.ToolConfigValue{
		{Name: "id", Value: "ignored"},
		{Name: "auth_type", Value: "application"},
		{Name: "url", Value: "https://gh.example.com"},
		{Name: "password", Value: "12345"},
		{Name: "projects", Value: "ignored"},
	}
	authType, apiUrl, password := extractGithubConfigFields(values)
	assert.Equal(t, "application", authType)
	assert.Equal(t, "https://gh.example.com", apiUrl)
	assert.Equal(t, "12345", password)
}

func TestDetectGithubCLI(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"gh issue list", true},
		{"gh pr view 123 --repo foo/bar", true},
		{"ls && gh auth status", true},
		{"echo gh", true}, // shlex token "gh" exact match — accept; auth is best-effort.
		{"ghost --version", false},
		{"ghi list", false},
		{"github-cli list", false},
		{"ls -la", false},
		{"aws s3 ls", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			assert.Equal(t, c.want, detectGithubCLI(c.cmd), "cmd: %q", c.cmd)
		})
	}
}

func TestScrubCredentials_GithubToken(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN": "ghp_VeryLongSecretTokenExample1234567890",
	}
	out := ScrubCredentials("token=ghp_VeryLongSecretTokenExample1234567890 in env", env)
	assert.NotContains(t, out, "ghp_VeryLongSecretTokenExample1234567890")
	assert.Contains(t, out, "[REDACTED]")
}
