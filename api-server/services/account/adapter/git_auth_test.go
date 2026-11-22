package adapter

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeGitToken = "ghp_FAKEtoken0123456789"

// fakeGitScript stands in for git on PATH: it records each invocation's argv
// and auth config, and fails the subcommand named in FAKE_GIT_FAIL with
// FAKE_GIT_OUTPUT on stderr — the way git reports an unreachable remote.
const fakeGitScript = `#!/bin/sh
echo "ARGV $*" >> "$FAKE_GIT_LOG"
echo "KEY $GIT_CONFIG_KEY_0" >> "$FAKE_GIT_LOG"
echo "VALUE $GIT_CONFIG_VALUE_0" >> "$FAKE_GIT_LOG"
if [ -n "$FAKE_GIT_FAIL" ]; then
  case " $* " in *" $FAKE_GIT_FAIL "*) echo "$FAKE_GIT_OUTPUT" >&2; exit 128;; esac
fi
exit 0
`

type fakeGit struct{ log string }

func installFakeGit(t *testing.T, failSubcommand, output string) *fakeGit {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(fakeGitScript), 0o755))
	f := &fakeGit{log: filepath.Join(t.TempDir(), "git.log")}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GIT_LOG", f.log)
	t.Setenv("FAKE_GIT_FAIL", failSubcommand)
	t.Setenv("FAKE_GIT_OUTPUT", output)
	return f
}

func (f *fakeGit) read(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	require.NoError(t, err)
	return string(b)
}

// cloneDir is the target directory of the recorded `git clone`.
func (f *fakeGit) cloneDir(t *testing.T) string {
	t.Helper()
	for _, line := range strings.Split(f.read(t), "\n") {
		if strings.HasPrefix(line, "ARGV clone ") {
			fields := strings.Fields(line)
			return fields[len(fields)-1]
		}
	}
	t.Fatal("no git clone recorded")
	return ""
}

func argvLines(log string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "ARGV ") {
			out = append(out, line)
		}
	}
	return out
}

func wantBasicHeader(token string) string {
	return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("oauth2:"+token))
}

func bufferedAdapterContext(buf *bytes.Buffer) *testAccountAdapterContext {
	return &testAccountAdapterContext{logger: slog.New(slog.NewTextHandler(buf, nil))}
}

func TestCheckoutCodeRepoKeepsTokenOutOfArgv(t *testing.T) {
	fg := installFakeGit(t, "", "")
	details := gitDetailFromDeployment{Org: "acme", Repo: "widgets", BaseBranch: "main", Sha1: "abc1234", Token: fakeGitToken}

	var logs bytes.Buffer
	dir, err := checkoutCodeRepo(bufferedAdapterContext(&logs), ApplyRecommendationRequest{}, details)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	log := fg.read(t)
	argv := argvLines(log)
	require.Len(t, argv, 3, "clone, fetch, checkout")
	for _, line := range argv {
		assert.NotContains(t, line, fakeGitToken)
	}
	assert.Contains(t, argv[0], "https://github.com/acme/widgets.git")
	assert.Contains(t, log, "KEY http.https://github.com/.extraHeader")
	assert.Contains(t, log, "VALUE "+wantBasicHeader(fakeGitToken))
}

func TestCheckoutCodeRepoGitLabKeepsTokenOutOfArgv(t *testing.T) {
	fg := installFakeGit(t, "", "")
	details := gitLabDetailFromDeployment{ProjectPath: "grp/proj", BaseBranch: "main", Token: fakeGitToken, GitLabURL: "https://gitlab.example.com"}

	var logs bytes.Buffer
	dir, err := checkoutCodeRepoGitLab(bufferedAdapterContext(&logs), ApplyRecommendationRequest{}, details)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	log := fg.read(t)
	argv := argvLines(log)
	require.Len(t, argv, 1)
	assert.NotContains(t, argv[0], fakeGitToken)
	assert.Contains(t, argv[0], "https://gitlab.example.com/grp/proj.git")
	assert.Contains(t, log, "KEY http.https://gitlab.example.com/.extraHeader")
	assert.Contains(t, log, "VALUE "+wantBasicHeader(fakeGitToken))
}

func TestCheckoutCodeRepoWithoutTokenSendsNoHeader(t *testing.T) {
	fg := installFakeGit(t, "", "")
	details := gitDetailFromDeployment{Org: "acme", Repo: "widgets", BaseBranch: "main"}

	var logs bytes.Buffer
	dir, err := checkoutCodeRepo(bufferedAdapterContext(&logs), ApplyRecommendationRequest{}, details)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	log := fg.read(t)
	assert.Contains(t, log, "KEY \n")
	assert.NotContains(t, log, "Authorization")
}

// A failed clone or fetch must leave no directory behind and must not put the
// token into the log or the returned error, even when git's output carries it.
func TestCheckoutCodeRepoFailureRedactsAndCleansUp(t *testing.T) {
	tokenOutput := "fatal: unable to access 'https://oauth2:" + fakeGitToken + "@github.com/acme/widgets.git/': 403 (token " + fakeGitToken + ")"
	for _, failing := range []string{"clone", "fetch"} {
		t.Run(failing, func(t *testing.T) {
			fg := installFakeGit(t, failing, tokenOutput)
			details := gitDetailFromDeployment{Org: "acme", Repo: "widgets", BaseBranch: "main", Sha1: "abc1234", Token: fakeGitToken}

			var logs bytes.Buffer
			dir, err := checkoutCodeRepo(bufferedAdapterContext(&logs), ApplyRecommendationRequest{}, details)
			require.Error(t, err)
			assert.Empty(t, dir)
			assert.NotContains(t, err.Error(), fakeGitToken)
			assert.NotContains(t, logs.String(), fakeGitToken)

			_, statErr := os.Stat(fg.cloneDir(t))
			assert.True(t, os.IsNotExist(statErr), "temp checkout dir must be removed on failure")
		})
	}
}

func TestRedactGitOutput(t *testing.T) {
	in := "fatal: unable to access 'https://oauth2:" + fakeGitToken + "@github.com/a/b.git/': denied for " + fakeGitToken
	got := redactGitOutput(in, fakeGitToken)
	assert.NotContains(t, got, fakeGitToken)
	assert.Contains(t, got, "https://***:***@github.com/a/b.git/")
	assert.Equal(t, "plain output", redactGitOutput("plain output", ""))
}

// TestGitHTTPAuthEnvAuthenticatesRealGit proves real git honours the
// GIT_CONFIG_* header: it clones over HTTP from git http-backend behind a
// server that refuses any request without the expected Authorization header,
// and checks the token is not written into the clone's config.
func TestGitHTTPAuthEnvAuthenticatesRealGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	execPath, err := exec.Command(gitBin, "--exec-path").Output()
	if err != nil {
		t.Skip("git --exec-path failed")
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend not available")
	}

	root := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	run(src, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o644))
	run(src, "add", ".")
	run(src, "commit", "-q", "-m", "init")
	run(root, "clone", "-q", "--bare", src, filepath.Join(root, "repo.git"))

	want := wantBasicHeader(fakeGitToken)[len("Authorization: "):]
	cgiHandler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		cgiHandler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	clone := func(env []string) (string, error) {
		dest := filepath.Join(t.TempDir(), "clone")
		cmd := exec.Command(gitBin, "clone", "-q", "--depth", "1", "-b", "main", srv.URL+"/repo.git", dest)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), err
		}
		cfg, readErr := os.ReadFile(filepath.Join(dest, ".git", "config"))
		require.NoError(t, readErr)
		return string(cfg), nil
	}

	cfg, err := clone(gitHTTPAuthEnv(srv.URL+"/", fakeGitToken))
	require.NoError(t, err, cfg)
	assert.NotContains(t, cfg, fakeGitToken)
	assert.Contains(t, cfg, srv.URL+"/repo.git")

	// Without the token the server refuses, and GIT_TERMINAL_PROMPT=0 makes git
	// fail instead of waiting on a prompt.
	_, err = clone(gitHTTPAuthEnv(srv.URL+"/", ""))
	assert.Error(t, err)
}
