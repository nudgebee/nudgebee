package knowledge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchGitHubSkillSource(t *testing.T) {
	tarball := buildRepoTarball(t, map[string]string{"skills/a/SKILL.md": "---\nname: a\ndescription: d\n---\nb"}, nil).Bytes()
	var gotAuth, gotIfNoneMatch []string
	tarballs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/repos/acme/skills/commits/main":
			assert.Equal(t, "application/vnd.github.sha", r.Header.Get("Accept"))
			gotIfNoneMatch = append(gotIfNoneMatch, r.Header.Get("If-None-Match"))
			if r.Header.Get("If-None-Match") == `"e1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"e1"`)
			_, _ = w.Write([]byte("0123456789abcdef"))
		case "/repos/acme/skills/commits/HEAD":
			_, _ = w.Write([]byte("fedcba9876543210"))
		case "/repos/acme/skills/tarball/0123456789abcdef", "/repos/acme/skills/tarball/fedcba9876543210":
			tarballs++
			_, _ = w.Write(tarball)
		case "/repos/acme/skills/commits/nope":
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := GitHubTokenResolver
	t.Cleanup(func() { GitHubTokenResolver = old; githubCommitETags.Clear() })
	GitHubTokenResolver = func(context.Context, string) (string, string, error) { return srv.URL, "tok", nil }

	read := func(rc io.ReadCloser) []byte {
		t.Helper()
		require.NotNil(t, rc)
		defer func() { _ = rc.Close() }()
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		return b
	}

	commit, body, err := FetchGitHubSkillSource(context.Background(), "t1", "acme/skills", "main", "")
	require.NoError(t, err)
	assert.Equal(t, "0123456789abcdef", commit)
	assert.Equal(t, tarball, read(body))
	assert.Equal(t, []string{"Bearer tok", "Bearer tok"}, gotAuth)

	// Same ref again: asked with the ETag, answered 304, and with the commit
	// already synced nothing is downloaded.
	commit, body, err = FetchGitHubSkillSource(context.Background(), "t1", "acme/skills", "main", "0123456789abcdef")
	require.NoError(t, err)
	assert.Equal(t, "0123456789abcdef", commit)
	assert.Nil(t, body)
	assert.Equal(t, []string{"", `"e1"`}, gotIfNoneMatch)
	assert.Equal(t, 1, tarballs)

	// A 304 with a different synced commit (Sync now passes none) still downloads.
	_, body, err = FetchGitHubSkillSource(context.Background(), "t1", "acme/skills", "main", "")
	require.NoError(t, err)
	assert.Equal(t, tarball, read(body))
	assert.Equal(t, 2, tarballs)

	commit, body, err = FetchGitHubSkillSource(context.Background(), "t1", "acme/skills", "", "")
	require.NoError(t, err)
	assert.Equal(t, "fedcba9876543210", commit, "no branch = default branch")
	read(body)

	_, _, err = FetchGitHubSkillSource(context.Background(), "t1", "acme/skills", "nope", "")
	assert.ErrorContains(t, err, "branch not found")
	_, _, err = FetchGitHubSkillSource(context.Background(), "t1", "acme/missing", "main", "")
	assert.ErrorContains(t, err, "repository or branch not found")
}

func TestNormalizeGitHubRepo(t *testing.T) {
	for in, want := range map[string]string{
		"grafana/skills":                        "grafana/skills",
		"https://github.com/grafana/skills":     "grafana/skills",
		"https://github.com/grafana/skills.git": "grafana/skills",
		" https://github.com/grafana/skills/ ":  "grafana/skills",
		"datadog-labs/agent-skills":             "datadog-labs/agent-skills",
	} {
		got, err := NormalizeGitHubRepo(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "grafana", "https://gitlab.com/a/b", "a/b/c", "../x/y", "../x", "acme/.."} {
		_, err := NormalizeGitHubRepo(bad)
		assert.Error(t, err, bad)
	}
}

// A broken GitHub integration doesn't block a public repository: the fetch
// goes anonymous, and a failure then names the integration too.
func TestFetchGitHubSkillSourceFallsBackWhenIntegrationFails(t *testing.T) {
	tarball := buildRepoTarball(t, map[string]string{"s/SKILL.md": "---\nname: s\ndescription: d\n---\nb"}, nil).Bytes()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/repos/acme/public/commits/HEAD":
			_, _ = w.Write([]byte("0123456789abcdef"))
		case "/repos/acme/public/tarball/0123456789abcdef":
			_, _ = w.Write(tarball)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	oldResolver, oldBase := GitHubTokenResolver, githubAnonymousAPIBase
	t.Cleanup(func() { GitHubTokenResolver, githubAnonymousAPIBase = oldResolver, oldBase; githubCommitETags.Clear() })
	githubAnonymousAPIBase = srv.URL
	GitHubTokenResolver = func(context.Context, string) (string, string, error) {
		return "", "", errors.New("GITHUB_APP_ID environment variable is not set")
	}

	commit, body, err := FetchGitHubSkillSource(context.Background(), "t1", "acme/public", "", "")
	require.NoError(t, err)
	require.NotNil(t, body)
	_ = body.Close()
	assert.Equal(t, "0123456789abcdef", commit)
	assert.Equal(t, []string{"", ""}, auth, "anonymous")

	_, _, err = FetchGitHubSkillSource(context.Background(), "t1", "acme/private", "", "")
	assert.ErrorContains(t, err, "repository or branch not found")
	assert.ErrorContains(t, err, "the GitHub integration could not be used: GITHUB_APP_ID")
}
