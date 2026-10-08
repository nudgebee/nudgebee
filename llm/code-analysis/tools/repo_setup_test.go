package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"nudgebee/code-analysis-agent/internal/git"
	"nudgebee/code-analysis-agent/internal/workspacegc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed:\n%s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// setupOrigin returns the file:// URL of a repository whose default branch
// (main) carries mainScript as its setup script ("" for none), plus a `feature`
// branch that carries featureScript ("" for none). The URL form matters: a
// clone from a plain local path hard-links the whole object store, so a clone
// of `feature` would already hold main's tip and nothing would be fetched.
func setupOrigin(t *testing.T, mainScript, featureScript string) string {
	t.Helper()
	url, _ := setupOriginDir(t, mainScript, featureScript)
	return url
}

func setupOriginDir(t *testing.T, mainScript, featureScript string) (url, dir string) {
	t.Helper()
	dir = t.TempDir()
	setupGit(t, dir, "init", "-q", "-b", "main")
	setupGit(t, dir, "config", "user.email", "test@example.com")
	setupGit(t, dir, "config", "user.name", "test")
	setupGit(t, dir, "config", "commit.gpgsign", "false")
	commit := func(relPath, content, message string) {
		full := filepath.Join(dir, relPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
		setupGit(t, dir, "add", ".")
		setupGit(t, dir, "commit", "-q", "-m", message)
	}
	commit("README.md", "base\n", "base")
	setupGit(t, dir, "checkout", "-q", "-b", "feature")
	if featureScript != "" {
		commit(RepoSetupScriptPath, featureScript, "feature setup script")
	}
	setupGit(t, dir, "checkout", "-q", "main")
	commit("main.txt", "main moved on\n", "main moves ahead")
	if mainScript != "" {
		commit(RepoSetupScriptPath, mainScript, "main setup script")
	}
	return "file://" + dir, dir
}

func setupClone(t *testing.T, origin, branch string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	setupGit(t, t.TempDir(), "clone", "-q", "--single-branch", "--branch", branch, origin, dir)
	return dir
}

// installScript is a setup script that counts its own runs in counterPath and
// installs a `hello-tool` command.
func installScript(counterPath string) string {
	return `echo run >> "` + counterPath + `"
printf '#!/bin/sh\necho hello-from-tool\n' > "$NB_TOOLS_BIN/hello-tool"
chmod +x "$NB_TOOLS_BIN/hello-tool"
`
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(raw), "\n")
}

func runCLI(t *testing.T, dir, command string) *CLIOutput {
	t.Helper()
	resp := NewCLITool(dir).Execute(context.Background(), map[string]any{"command": command})
	data, ok := resp.Data.(map[string]any)
	require.Truef(t, ok, "unexpected response data %T (error: %s)", resp.Data, resp.Error)
	out, ok := data["result"].(*CLIOutput)
	require.Truef(t, ok, "unexpected result %T", data["result"])
	return out
}

func TestRunRepoSetup_InstallsOnceAndPutsToolsOnPath(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, installScript(counter), "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")

	first := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, first)
	assert.Equal(t, RepoSetupRan, first.Status, first.Output)
	assert.Contains(t, first.Note(), "are on PATH")

	// A second checkout of the same repository reuses the install.
	second := RunRepoSetup(context.Background(), root, setupClone(t, origin, "main"), "")
	require.NotNil(t, second)
	assert.Equal(t, RepoSetupCached, second.Status)
	assert.Equal(t, 1, lineCount(t, counter), "the script ran once")

	binDir := repoToolsBin(repo)
	require.NotEmpty(t, binDir)
	assert.True(t, strings.HasPrefix(binDir, filepath.Join(root, "shared-cache", "tools")+string(filepath.Separator)), binDir)

	// Without a shell (bare name resolved by os/exec) and through one.
	assert.Contains(t, runCLI(t, repo, "hello-tool").Stdout, "hello-from-tool")
	assert.Contains(t, runCLI(t, repo, "hello-tool | cat").Stdout, "hello-from-tool")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0755))
	assert.Contains(t, runCLI(t, filepath.Join(repo, "sub", "dir"), "hello-tool").Stdout, "hello-from-tool", "from a subdirectory of the checkout")

	assert.Empty(t, setupGit(t, repo, "status", "--porcelain"), "setup leaves the working tree clean")
	assert.NotContains(t, runCLI(t, t.TempDir(), "hello-tool").Stdout, "hello-from-tool", "a directory outside the checkout does not get the tools")
}

func TestRunRepoSetup_ScriptRunsFromTheDefaultBranchOnly(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")

	t.Run("a script only on the branch under analysis is not run", func(t *testing.T) {
		origin := setupOrigin(t, "", installScript(counter))
		repo := setupClone(t, origin, "feature")

		assert.Nil(t, RunRepoSetup(context.Background(), t.TempDir(), repo, ""))
		assert.Equal(t, 0, lineCount(t, counter))
		assert.Empty(t, repoToolsBin(repo))
	})
	t.Run("a branch gets the default branch's script, not its own", func(t *testing.T) {
		branchCounter := filepath.Join(t.TempDir(), "branch-runs")
		origin := setupOrigin(t, installScript(counter), installScript(branchCounter))
		repo := setupClone(t, origin, "feature")

		result := RunRepoSetup(context.Background(), t.TempDir(), repo, "")
		require.NotNil(t, result)
		assert.Equal(t, RepoSetupRan, result.Status, result.Output)
		assert.Equal(t, 1, lineCount(t, counter))
		assert.Equal(t, 0, lineCount(t, branchCounter))
	})
}

func TestRunRepoSetup_ImageToolIsNotShadowed(t *testing.T) {
	origin := setupOrigin(t, `printf '#!/bin/sh\necho fake-git\n' > "$NB_TOOLS_BIN/git"
chmod +x "$NB_TOOLS_BIN/git"
`, "")
	repo := setupClone(t, origin, "main")

	result := RunRepoSetup(context.Background(), t.TempDir(), repo, "")
	require.NotNil(t, result)
	require.Equal(t, RepoSetupRan, result.Status, result.Output)

	for _, command := range []string{"git --version", "git --version | cat"} {
		out := runCLI(t, repo, command).Stdout
		assert.Contains(t, out, "git version", command)
		assert.NotContains(t, out, "fake-git", command)
	}
}

func TestRunRepoSetup_FailureIsRememberedThenRetried(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, `echo run >> "`+counter+`"
printf '#!/bin/sh\necho half-installed\n' > "$NB_TOOLS_BIN/hello-tool"
chmod +x "$NB_TOOLS_BIN/hello-tool"
echo "download failed: connection refused"
exit 3
`, "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")

	first := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, first)
	assert.Equal(t, RepoSetupFailed, first.Status)
	assert.Equal(t, 3, first.ExitCode)
	assert.Contains(t, first.Note(), "failed (exit code 3)")
	assert.Contains(t, first.Note(), "download failed: connection refused")
	assert.Empty(t, repoToolsBin(repo), "a failed setup puts nothing on PATH")

	second := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, second)
	assert.Equal(t, RepoSetupFailed, second.Status)
	assert.Equal(t, 3, second.ExitCode)
	assert.False(t, second.RetryAt.IsZero())
	assert.Equal(t, 1, lineCount(t, counter), "a recent failure is not re-run")

	prev := repoSetupRetryAfter
	repoSetupRetryAfter = 0
	t.Cleanup(func() { repoSetupRetryAfter = prev })
	third := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, third)
	assert.Equal(t, RepoSetupFailed, third.Status)
	assert.Equal(t, 2, lineCount(t, counter), "after the retry window the script runs again")
}

func TestRunRepoSetup_TimeoutKillsTheScriptAndItsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	origin := setupOrigin(t, `sleep 60 &
echo $! > "`+pidFile+`"
wait
`, "")
	repo := setupClone(t, origin, "main")
	prev := repoSetupTimeout
	repoSetupTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { repoSetupTimeout = prev })

	started := time.Now()
	result := RunRepoSetup(context.Background(), t.TempDir(), repo, "")
	require.NotNil(t, result)
	assert.Equal(t, RepoSetupFailed, result.Status)
	assert.Equal(t, -1, result.ExitCode)
	assert.Contains(t, result.Output, "timed out")
	assert.Less(t, time.Since(started), 20*time.Second)

	pid := strings.TrimSpace(string(mustRead(t, pidFile)))
	assert.Eventually(t, func() bool {
		return exec.Command("kill", "-0", pid).Run() != nil
	}, 5*time.Second, 50*time.Millisecond, "the script's background child was killed too")
}

func TestRunRepoSetup_CancelledRunIsNotRememberedAsAFailure(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	started := filepath.Join(t.TempDir(), "started")
	origin := setupOrigin(t, `echo run >> "`+counter+`"
if [ ! -e "`+started+`" ]; then
  touch "`+started+`"
  sleep 60
fi
`, "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if _, err := os.Stat(started); err == nil {
				cancel()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	cancelled := RunRepoSetup(ctx, root, repo, "")
	require.NotNil(t, cancelled)
	assert.Equal(t, RepoSetupFailed, cancelled.Status)

	again := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, again)
	assert.Equal(t, RepoSetupRan, again.Status, "the next clone runs the script straight away")
	assert.Equal(t, 2, lineCount(t, counter))
}

func TestRunRepoSetup_ScriptEnvironment(t *testing.T) {
	report := filepath.Join(t.TempDir(), "report")
	t.Setenv("NB_SETUP_TEST_SERVICE_SECRET", "must-not-reach-the-script")
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	origin := setupOrigin(t, `{
  echo "pwd=$(pwd)"
  echo "files=$(ls -A | wc -l | tr -d ' ')"
  echo "NB_TOOLS_DIR=$NB_TOOLS_DIR"
  echo "NB_TOOLS_BIN=$NB_TOOLS_BIN"
  echo "GOBIN=$GOBIN"
  echo "--- env"
  env
} > "`+report+`"
printf '#!/bin/sh\necho just-installed-ran\n' > "$NB_TOOLS_BIN/just-installed"
chmod +x "$NB_TOOLS_BIN/just-installed"
just-installed >> "`+report+`"
git --version >> "`+report+`"
`, "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")

	result := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, result)
	require.Equal(t, RepoSetupRan, result.Status, result.Output)

	binDir := repoToolsBin(repo)
	require.NotEmpty(t, binDir)
	got := string(mustRead(t, report))
	assert.Contains(t, got, "files=0\n", "the script starts in an empty directory, not the checkout")
	assert.NotContains(t, got, "pwd="+repo)
	assert.Contains(t, got, "NB_TOOLS_BIN="+binDir+"\n")
	assert.Contains(t, got, "GOBIN="+binDir+"\n")
	assert.Contains(t, got, "NB_TOOLS_DIR="+filepath.Dir(binDir)+"\n")
	assert.NotContains(t, got, "must-not-reach-the-script", "the service's environment is not handed to the script")
	assert.Contains(t, got, "HTTPS_PROXY=http://proxy.example.com:3128\n", "what it needs to reach the network is")
	assert.Contains(t, got, "HOME="+os.Getenv("HOME")+"\n")
	assert.Contains(t, got, "just-installed-ran\n", "a tool the script has just installed is on its PATH")
	assert.Contains(t, got, "git version", "the image's own tools still are")
}

func TestRunRepoSetup_ChangedScriptInstallsAfresh(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin, originDir := setupOriginDir(t, installScript(counter), "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")
	require.Equal(t, RepoSetupRan, RunRepoSetup(context.Background(), root, repo, "").Status)
	firstBin := repoToolsBin(repo)

	require.NoError(t, os.WriteFile(filepath.Join(originDir, RepoSetupScriptPath), []byte(installScript(counter)+"# v2\n"), 0644))
	setupGit(t, originDir, "commit", "-q", "-am", "change setup script")

	result := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, result)
	assert.Equal(t, RepoSetupRan, result.Status, result.Output)
	assert.Equal(t, 2, lineCount(t, counter))
	assert.NotEqual(t, firstBin, repoToolsBin(repo), "a changed script gets its own install directory")

	// Removing the script from the default branch takes the tools away again.
	setupGit(t, originDir, "rm", "-q", RepoSetupScriptPath)
	setupGit(t, originDir, "commit", "-q", "-m", "drop setup script")
	assert.Nil(t, RunRepoSetup(context.Background(), root, repo, ""))
	assert.Empty(t, repoToolsBin(repo))
}

func TestRepoToolsBin_WorktreeAndReclaimedInstall(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, installScript(counter), "")
	root := t.TempDir()

	// The shape the clone tool produces: a worktree of a bare clone, whose
	// `.git` is a file pointing at its git directory.
	base := filepath.Join(t.TempDir(), "base")
	setupGit(t, t.TempDir(), "clone", "-q", "--bare", "--single-branch", "--branch", "feature", origin, base)
	worktree := filepath.Join(t.TempDir(), "wt")
	setupGit(t, base, "worktree", "add", "-q", "--detach", worktree, "feature")

	result := RunRepoSetup(context.Background(), root, worktree, "")
	require.NotNil(t, result)
	require.Equal(t, RepoSetupRan, result.Status, result.Output)
	binDir := repoToolsBin(worktree)
	require.NotEmpty(t, binDir)
	assert.Contains(t, runCLI(t, worktree, "hello-tool").Stdout, "hello-from-tool")

	// The shared caches can be reclaimed between runs; a checkout whose install
	// is gone simply has no tools until the next clone runs setup again.
	require.NoError(t, os.RemoveAll(filepath.Join(root, "shared-cache", "tools")))
	assert.Empty(t, repoToolsBin(worktree))
	again := RunRepoSetup(context.Background(), root, worktree, "")
	require.NotNil(t, again)
	assert.Equal(t, RepoSetupRan, again.Status)
	assert.Equal(t, 2, lineCount(t, counter))
}

func TestRepoSetupResultNote(t *testing.T) {
	assert.Equal(t,
		"Repository setup script (.nudgebee/setup.sh on the default branch) ran in 12s. The tools it installed are on PATH for commands run in this repository.",
		(&RepoSetupResult{Status: RepoSetupRan, Duration: 12300 * time.Millisecond}).Note())
	assert.Equal(t,
		"Repository setup script (.nudgebee/setup.sh on the default branch) has already been run. The tools it installed are on PATH for commands run in this repository.",
		(&RepoSetupResult{Status: RepoSetupCached}).Note())
	assert.Equal(t,
		"Repository setup script (.nudgebee/setup.sh on the default branch) failed (exit code 7), so the tools it installs are not available. It will be run again after 10:30 UTC. Last output: curl: (6) Could not resolve host",
		(&RepoSetupResult{
			Status:   RepoSetupFailed,
			ExitCode: 7,
			Output:   "downloading\ncurl: (6) Could not resolve host\n\n",
			RetryAt:  time.Date(2026, 1, 2, 10, 30, 0, 0, time.UTC),
		}).Note())
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return raw
}

// End to end through the tool the agent calls: clone a branch of the request's
// repository, get the default branch's setup script run, and be told about it
// in the observation.
func TestRepoCloneTool_RunsTheRequestRepositorysSetupScript(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, installScript(counter), "")
	root := t.TempDir()
	gitClient := git.NewGitClient(root, time.Minute, 0)
	newTool := func(run, requestRepo string) *RepoCloneTool {
		tool := NewRepoCloneTool(filepath.Join(root, "runs", run), gitClient)
		tool.SetRequestRepo(requestRepo)
		return tool
	}

	first := newTool("one", origin).Execute(context.Background(), map[string]any{"repo_url": origin, "branch": "feature"})
	require.Equal(t, "success", first.Status, first.Error)
	assert.Contains(t, first.Observation, "Repository setup script (.nudgebee/setup.sh on the default branch) ran in")
	data, ok := first.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"status": RepoSetupRan, "exit_code": 0}, data["setup_script"])
	localPath, _ := data["local_path"].(string)
	require.NotEmpty(t, localPath)
	assert.Contains(t, runCLI(t, localPath, "hello-tool").Stdout, "hello-from-tool")
	assert.NotContains(t, setupGit(t, localPath, "branch", "-a"), "main", "reading the default branch does not add it to the agent's branch view")

	second := newTool("two", origin).Execute(context.Background(), map[string]any{"repo_url": origin, "branch": "feature"})
	require.Equal(t, "success", second.Status, second.Error)
	assert.Contains(t, second.Observation, "has already been run")
	assert.Equal(t, 1, lineCount(t, counter))

	plain := newTool("three", origin).Execute(context.Background(), map[string]any{"repo_url": setupOrigin(t, "", ""), "branch": "main"})
	require.Equal(t, "success", plain.Status, plain.Error)
	assert.NotContains(t, plain.Observation, "setup script")
}

// The model chooses what to clone. Cloning a repository the request is not
// about must not run that repository's code.
func TestRepoCloneTool_OtherRepositoriesSetupScriptIsNotRun(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	other := setupOrigin(t, installScript(counter), "")
	root := t.TempDir()
	gitClient := git.NewGitClient(root, time.Minute, 0)

	for run, requestRepo := range map[string]string{"no-request-repo": "", "different-request-repo": setupOrigin(t, "", "")} {
		tool := NewRepoCloneTool(filepath.Join(root, "runs", run), gitClient)
		tool.SetRequestRepo(requestRepo)
		resp := tool.Execute(context.Background(), map[string]any{"repo_url": other, "branch": "main"})
		require.Equal(t, "success", resp.Status, resp.Error)
		assert.NotContains(t, resp.Observation, "setup script", run)
		data, _ := resp.Data.(map[string]any)
		assert.NotContains(t, data, "setup_script", run)
		assert.Empty(t, repoToolsBin(data["local_path"].(string)), run)
	}
	assert.Equal(t, 0, lineCount(t, counter))
}

// A blip reaching the remote must not take away tools a checkout already has,
// and must not be reported as "this repository has no setup script".
func TestRunRepoSetup_UnreadableDefaultBranchKeepsTheToolsAlreadyThere(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, installScript(counter), "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")
	require.Equal(t, RepoSetupRan, RunRepoSetup(context.Background(), root, repo, "").Status)
	binDir := repoToolsBin(repo)
	require.NotEmpty(t, binDir)

	assert.Nil(t, RunRepoSetup(context.Background(), root, repo, "file://"+filepath.Join(t.TempDir(), "unreachable")))
	assert.Equal(t, binDir, repoToolsBin(repo))
}

// A script that succeeds but leaves something running is still a success, the
// clone is not held up waiting for that process, and the process does not
// outlive setup.
func TestRunRepoSetup_BackgroundProcessLeftBehindIsKilled(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	origin := setupOrigin(t, `sleep 60 &
echo $! > "`+pidFile+`"
printf '#!/bin/sh\necho hello-from-tool\n' > "$NB_TOOLS_BIN/hello-tool"
chmod +x "$NB_TOOLS_BIN/hello-tool"
`, "")
	repo := setupClone(t, origin, "main")

	started := time.Now()
	result := RunRepoSetup(context.Background(), t.TempDir(), repo, "")
	require.NotNil(t, result)
	assert.Equal(t, RepoSetupRan, result.Status, result.Output)
	assert.Less(t, time.Since(started), 20*time.Second)
	assert.NotEmpty(t, repoToolsBin(repo))

	pid := strings.TrimSpace(string(mustRead(t, pidFile)))
	assert.Eventually(t, func() bool {
		return exec.Command("kill", "-0", pid).Run() != nil
	}, 5*time.Second, 50*time.Millisecond)
}

// A Go module cache, for one, is written read-only. A failed attempt that left
// one behind must not stop every later attempt at the cleanup step.
func TestRunRepoSetup_ReadOnlyLeftoversDoNotBlockTheRetry(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, `echo run >> "`+counter+`"
mkdir -p "$NB_TOOLS_DIR/cache/pkg"
touch "$NB_TOOLS_DIR/cache/pkg/file"
chmod -R a-w "$NB_TOOLS_DIR/cache"
exit 1
`, "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")
	prev := repoSetupRetryAfter
	repoSetupRetryAfter = 0
	t.Cleanup(func() {
		repoSetupRetryAfter = prev
		_ = removeAllWritable(root)
	})

	require.Equal(t, RepoSetupFailed, RunRepoSetup(context.Background(), root, repo, "").Status)
	second := RunRepoSetup(context.Background(), root, repo, "")
	require.NotNil(t, second)
	assert.Equal(t, 1, second.ExitCode, second.Output)
	assert.Equal(t, 2, lineCount(t, counter), "the script ran again")
}

func TestRunRepoSetup_ClonesAtTheSameMomentInstallOnce(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "runs")
	origin := setupOrigin(t, "sleep 1\n"+installScript(counter), "")
	root := t.TempDir()
	repos := []string{setupClone(t, origin, "main"), setupClone(t, origin, "feature"), setupClone(t, origin, "main")}

	statuses := make([]string, len(repos))
	var wg sync.WaitGroup
	for i, repo := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result := RunRepoSetup(context.Background(), root, repo, ""); result != nil {
				statuses[i] = result.Status
			}
		}()
	}
	wg.Wait()

	sort.Strings(statuses)
	assert.Equal(t, []string{RepoSetupCached, RepoSetupCached, RepoSetupRan}, statuses)
	assert.Equal(t, 1, lineCount(t, counter))
	for _, repo := range repos {
		assert.NotEmpty(t, repoToolsBin(repo))
	}
}

// The tools directory the runner installs into has to be one the workspace
// cleanup knows about, or a pod under disk pressure could never reclaim it.
func TestRepoSetupInstallsUnderAReclaimableCache(t *testing.T) {
	origin := setupOrigin(t, installScript(filepath.Join(t.TempDir(), "runs")), "")
	root := t.TempDir()
	repo := setupClone(t, origin, "main")
	require.Equal(t, RepoSetupRan, RunRepoSetup(context.Background(), root, repo, "").Status)

	binDir := repoToolsBin(repo)
	reclaimable := false
	for _, cachePath := range workspacegc.CachePaths(root) {
		if strings.HasPrefix(binDir, cachePath+string(filepath.Separator)) {
			reclaimable = true
		}
	}
	assert.True(t, reclaimable, "%s is not under any of %v", binDir, workspacegc.CachePaths(root))
}

// Long-running commands (a dev server, a watcher) go through the background
// exec tool, which builds its own command environment.
func TestBackgroundExecTool_SeesTheRepositorysTools(t *testing.T) {
	origin := setupOrigin(t, installScript(filepath.Join(t.TempDir(), "runs")), "")
	repo := setupClone(t, origin, "main")
	require.Equal(t, RepoSetupRan, RunRepoSetup(context.Background(), t.TempDir(), repo, "").Status)

	for _, command := range []string{"hello-tool", "hello-tool | cat"} {
		tool := NewBackgroundExecTool(repo)
		start := tool.Execute(context.Background(), map[string]any{"action": "start", "command": command})
		require.Equal(t, "success", start.Status, start.Error)
		sessionID, _ := start.Data.(map[string]any)["session_id"].(string)
		require.NotEmpty(t, sessionID)

		var seen string
		require.Eventuallyf(t, func() bool {
			read := tool.Execute(context.Background(), map[string]any{"action": "read", "session_id": sessionID})
			seen += read.Observation
			return strings.Contains(seen, "hello-from-tool")
		}, 5*time.Second, 50*time.Millisecond, "%q never printed the tool's output; saw %q", command, seen)
	}
}

func TestRepoCloneTool_FailingSetupScriptDoesNotFailTheClone(t *testing.T) {
	origin := setupOrigin(t, "echo \"cannot reach the mirror\"\nexit 9\n", "")
	root := t.TempDir()
	tool := NewRepoCloneTool(filepath.Join(root, "runs", "one"), git.NewGitClient(root, time.Minute, 0))
	tool.SetRequestRepo(origin)

	resp := tool.Execute(context.Background(), map[string]any{"repo_url": origin, "branch": "feature"})
	require.Equal(t, "success", resp.Status, resp.Error)
	assert.Contains(t, resp.Observation, "Successfully cloned repository")
	assert.Contains(t, resp.Observation, "failed (exit code 9), so the tools it installs are not available.")
	assert.Contains(t, resp.Observation, "Last output: cannot reach the mirror")
	data, _ := resp.Data.(map[string]any)
	assert.Equal(t, map[string]any{"status": RepoSetupFailed, "exit_code": 9}, data["setup_script"])
}

// The lookup runs in the service on every command. A command can leave behind
// a `.git` that is not a file, and reading it must not hang or read without end.
func TestRepoToolsBin_HostileDotGitIsRefusedWithoutReadingIt(t *testing.T) {
	check := func(t *testing.T, dir string) {
		t.Helper()
		done := make(chan string, 1)
		go func() { done <- repoToolsBin(dir) }()
		select {
		case got := <-done:
			assert.Empty(t, got)
		case <-time.After(3 * time.Second):
			t.Fatal("repoToolsBin did not return")
		}
	}
	t.Run("a named pipe", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, syscall.Mkfifo(filepath.Join(dir, ".git"), 0600))
		check(t, dir)
	})
	t.Run("a link to something else", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "gitdir-file")
		require.NoError(t, os.WriteFile(target, []byte("gitdir: /somewhere\n"), 0644))
		require.NoError(t, os.Symlink(target, filepath.Join(dir, ".git")))
		check(t, dir)
	})
	t.Run("an oversized file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+strings.Repeat("x", maxDotGitFileBytes)), 0644))
		check(t, dir)
	})
}

func TestReadTail(t *testing.T) {
	write := func(content string) string {
		path := filepath.Join(t.TempDir(), "setup.log")
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
		return path
	}
	assert.Equal(t, "one\ntwo", readTail(write("one\ntwo\n"), 100))
	assert.Equal(t, "fetching https://example.com/acme/widgets.git", readTail(write("fetching https://user:s3cr3t@example.com/acme/widgets.git\n"), 100))

	// The cut lands inside the first line's URL, after its scheme. That line is
	// dropped whole: what is left of it would keep the token.
	long := "fetching https://user:s3cr3t@example.com/acme/widgets.git\nlast line\n"
	got := readTail(write(long), int64(len(long)-12))
	assert.Equal(t, "last line", got)
	assert.NotContains(t, got, "s3cr3t")
	assert.Empty(t, readTail(write(strings.Repeat("x", 500)), 100), "one line longer than the tail")
}
