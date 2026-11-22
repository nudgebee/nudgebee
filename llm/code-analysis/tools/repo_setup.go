package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"nudgebee/code-analysis-agent/internal/git"
)

// A repository can commit a setup script that installs the tools the agent
// needs to work on it (a migration CLI, a linter the image does not ship). The
// workspace cannot install OS packages, so the script installs user-space tools
// into a directory that is then put at the end of PATH for commands run inside
// that repository's checkout.
//
// Trust: the script is read from the tip of the repository's default branch,
// and only for the repository the request names, in a run that writes code
// (see RepoCloneTool). A branch under analysis does not get its own copy run,
// and neither does some other repository the model clones. It is given a
// minimal environment, but it runs as the same user as the agent's own
// commands and can read whatever they can read in the workspace: it is as
// trusted as that repository's own build and test commands, which such a run
// already executes. Nothing here protects the install directory, its state or the
// pointer to it from a later command in the workspace that overwrites them.

// RepoSetupScriptPath is where a repository declares its setup script,
// relative to the repository root.
const RepoSetupScriptPath = ".nudgebee/setup.sh"

const (
	// repoSetupMaxScriptBytes caps the script that will be read and run.
	repoSetupMaxScriptBytes = 256 << 10
	// repoSetupOutputTailBytes is how much of the script's output is kept.
	repoSetupOutputTailBytes = 4 << 10
	// repoToolsPointerFile, inside a checkout's git directory, holds the tools
	// bin directory that checkout's commands get on PATH. It lives in the git
	// directory so it never shows up as a change in the working tree.
	repoToolsPointerFile = "nb-tools-bin"
	repoSetupStateFile   = ".setup-state.json"
	// A worktree's `.git` is a one-line file naming its git directory.
	maxDotGitFileBytes = 4 << 10
)

// repoSetupEnvPassthrough is everything a setup script inherits from the
// service's environment. The script needs to reach the network the way the
// workspace is set up to and to put downloads where the image keeps its caches;
// it has no use for anything else the service was started with.
var repoSetupEnvPassthrough = []string{
	"HOME", "LANG", "LC_ALL", "TMPDIR",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
	"GOCACHE", "GOMODCACHE", "PIP_CACHE_DIR", "PYTHONPYCACHEPREFIX", "npm_config_cache", "PNPM_HOME",
}

// repoSetupEnv builds the script's environment. The install directory is last
// on its PATH, as it is for the agent's commands, so a script can use a tool it
// has just installed.
func repoSetupEnv(installDir, binDir string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH") + string(os.PathListSeparator) + binDir,
		"NB_TOOLS_DIR=" + installDir,
		"NB_TOOLS_BIN=" + binDir,
		"GOBIN=" + binDir,
	}
	for _, name := range repoSetupEnvPassthrough {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// Variables so tests can shorten them.
var (
	// repoSetupTimeout bounds one run of a setup script.
	repoSetupTimeout = 5 * time.Minute
	// repoSetupRetryAfter is how long a failed script is left alone before it
	// is run again, so a broken script does not cost every clone its timeout.
	repoSetupRetryAfter = 15 * time.Minute
)

const (
	RepoSetupRan    = "ran"
	RepoSetupCached = "cached"
	RepoSetupFailed = "failed"
)

// RepoSetupResult describes what happened to a repository's setup script.
type RepoSetupResult struct {
	Status   string        // RepoSetupRan, RepoSetupCached or RepoSetupFailed
	ExitCode int           // script exit code; -1 when it was killed
	Duration time.Duration // zero unless the script ran in this call
	Output   string        // tail of the script's combined output
	// RetryAt is set when a failure is being remembered rather than re-run.
	RetryAt time.Time
}

type repoSetupState struct {
	OK         bool      `json:"ok"`
	ExitCode   int       `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMS int64     `json:"duration_ms"`
	Output     string    `json:"output,omitempty"`
}

// Note is the line added to the clone tool's observation.
func (r *RepoSetupResult) Note() string {
	const subject = "Repository setup script (" + RepoSetupScriptPath + " on the default branch)"
	switch r.Status {
	case RepoSetupRan:
		return fmt.Sprintf("%s ran in %s. The tools it installed are on PATH for commands run in this repository.",
			subject, r.Duration.Round(time.Second))
	case RepoSetupCached:
		return subject + " has already been run. The tools it installed are on PATH for commands run in this repository."
	}
	note := fmt.Sprintf("%s failed (exit code %d), so the tools it installs are not available.", subject, r.ExitCode)
	if !r.RetryAt.IsZero() {
		note += fmt.Sprintf(" It will be run again after %s.", r.RetryAt.UTC().Format("15:04 UTC"))
	}
	if last := lastNonEmptyLine(r.Output); last != "" {
		note += " Last output: " + last
	}
	return note
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// repoSetupScripts remembers, per repository, the setup script found at the
// default branch tip it was last read from. Reading the script can cost a fetch
// when the checkout does not hold that tip; while the tip has not moved there
// is nothing new to read. One entry per repository, replaced when the tip moves.
var repoSetupScripts sync.Map // git.RepoIdentity -> repoSetupScript

type repoSetupScript struct {
	tip     string
	content []byte
	found   bool
}

// repoSetupLocks serializes runs of the same script so two analyses that clone
// at the same moment install once. Keyed by install directory; process-lived,
// bounded by the number of distinct setup scripts a workspace pod sees.
var (
	repoSetupLocksMu sync.Mutex
	repoSetupLocks   = map[string]chan struct{}{}
)

func lockRepoSetup(ctx context.Context, key string) (unlock func(), err error) {
	repoSetupLocksMu.Lock()
	ch, ok := repoSetupLocks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		ch <- struct{}{}
		repoSetupLocks[key] = ch
	}
	repoSetupLocksMu.Unlock()
	select {
	case <-ch:
		return func() { ch <- struct{}{} }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RunRepoSetup makes the tools a repository's setup script installs available
// to commands run in repoDir, running the script if this workspace has not run
// that exact script yet. repoDir is a checkout of the repository (a worktree of
// the shared clone or a plain clone): its working tree, refs and objects are
// not changed, and one pointer file is written into its git directory.
// remoteURL is the URL to reach the repository with, credentials included;
// empty means repoDir's origin.
// workspaceRoot is the workspace root the shared caches live under.
//
// It returns nil when the repository has no setup script, and also when the
// default branch could not be read: in that case the checkout keeps whatever
// tools an earlier call gave it. It never returns an error. Setup is best
// effort and must not fail the clone it follows.
//
// Installs are keyed by the script's contents, so a changed script installs
// into a fresh directory and identical scripts share one install.
func RunRepoSetup(ctx context.Context, workspaceRoot, repoDir, remoteURL string) *RepoSetupResult {
	if workspaceRoot == "" || repoDir == "" {
		return nil
	}
	script, found, err := readRepoSetupScript(ctx, repoDir, remoteURL)
	if err != nil {
		slog.Warn("repo setup: could not read the setup script from the default branch", "repo_dir", repoDir, "error", err)
		return nil
	}
	if !found {
		// Whatever an earlier clone of this checkout pointed at no longer applies.
		clearRepoToolsPointer(repoDir)
		return nil
	}

	sum := sha256.Sum256(script)
	installDir := filepath.Join(workspaceRoot, "shared-cache", "tools", hex.EncodeToString(sum[:]))
	binDir := filepath.Join(installDir, "bin")

	unlock, err := lockRepoSetup(ctx, installDir)
	if err != nil {
		return nil
	}
	defer unlock()

	result := repoSetupFromState(installDir)
	if result == nil {
		result = runRepoSetupScript(ctx, script, installDir, binDir)
		slog.Info("repo setup: script finished", "repo_dir", repoDir, "install_dir", installDir,
			"status", result.Status, "exit_code", result.ExitCode, "duration_ms", result.Duration.Milliseconds())
	}
	if result.Status == RepoSetupFailed {
		clearRepoToolsPointer(repoDir)
		return result
	}
	if err := writeRepoToolsPointer(repoDir, binDir); err != nil {
		slog.Warn("repo setup: could not record the tools directory for this checkout", "repo_dir", repoDir, "error", err)
	}
	return result
}

// readRepoSetupScript returns the setup script at the tip of the repository's
// default branch.
func readRepoSetupScript(ctx context.Context, repoDir, remoteURL string) (script []byte, found bool, err error) {
	if remoteURL == "" {
		if remoteURL, err = git.OriginURL(ctx, repoDir); err != nil {
			return nil, false, err
		}
	}
	branch, tip, err := git.RemoteDefaultBranch(ctx, remoteURL)
	if err != nil {
		return nil, false, err
	}
	key := git.RepoIdentity(remoteURL)
	if cached, ok := repoSetupScripts.Load(key); ok && cached.(repoSetupScript).tip == tip {
		return cached.(repoSetupScript).content, cached.(repoSetupScript).found, nil
	}
	script, found, err = git.ReadBranchFileAtCommit(ctx, repoDir, remoteURL, branch, tip, RepoSetupScriptPath, repoSetupMaxScriptBytes)
	if err != nil {
		return nil, false, err
	}
	repoSetupScripts.Store(key, repoSetupScript{tip: tip, content: script, found: found})
	return script, found, nil
}

// repoSetupFromState answers from an earlier run of the same script: a success
// is reused, a recent failure is remembered. nil means the script must run.
func repoSetupFromState(installDir string) *RepoSetupResult {
	raw, err := os.ReadFile(filepath.Join(installDir, repoSetupStateFile))
	if err != nil {
		return nil
	}
	var state repoSetupState
	if json.Unmarshal(raw, &state) != nil {
		return nil
	}
	if state.OK {
		return &RepoSetupResult{Status: RepoSetupCached}
	}
	if retryAt := state.FinishedAt.Add(repoSetupRetryAfter); time.Now().Before(retryAt) {
		return &RepoSetupResult{Status: RepoSetupFailed, ExitCode: state.ExitCode, Output: state.Output, RetryAt: retryAt}
	}
	return nil
}

func runRepoSetupScript(ctx context.Context, script []byte, installDir, binDir string) *RepoSetupResult {
	fail := func(err error) *RepoSetupResult {
		return &RepoSetupResult{Status: RepoSetupFailed, ExitCode: -1, Output: err.Error()}
	}
	// Start from nothing: a failed or timed-out earlier attempt may have left
	// half-installed files behind.
	if err := removeAllWritable(installDir); err != nil {
		return fail(fmt.Errorf("clearing install directory: %w", err))
	}
	workDir := filepath.Join(installDir, ".work")
	for _, dir := range []string{binDir, workDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fail(fmt.Errorf("creating install directory: %w", err))
		}
	}
	scriptPath := filepath.Join(installDir, "setup.sh")
	if err := os.WriteFile(scriptPath, script, 0600); err != nil {
		return fail(fmt.Errorf("writing setup script: %w", err))
	}
	// A file, not a pipe: with a pipe, a background process the script leaves
	// behind keeps the write end open and Run waits on it after the script itself
	// has exited.
	logPath := filepath.Join(installDir, "setup.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fail(fmt.Errorf("creating setup log: %w", err))
	}
	defer func() { _ = logFile.Close() }()

	shell := "sh"
	if bash, err := exec.LookPath("bash"); err == nil {
		shell = bash
	}
	runCtx, cancel := context.WithTimeout(ctx, repoSetupTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, shell, scriptPath)
	// An empty directory, not the checkout: the script comes from the default
	// branch and must not pick up files from whatever branch is checked out.
	cmd.Dir = workDir
	cmd.Env = repoSetupEnv(installDir, binDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// The script gets its own process group so that what it started can be
	// killed with it: an installer commonly forks (curl, tar, a package
	// manager), and killing only the shell would leave those running. A process
	// that moves itself to another group or session (a build daemon) is out of
	// reach.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	started := time.Now()
	runErr := cmd.Run()
	duration := time.Since(started)
	// Whatever the script left running in its group goes too, on success as much
	// as on failure: nothing from setup is meant to outlive it.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = removeAllWritable(workDir)

	// Only the redacted tail is kept; the raw log is not left on disk.
	_ = logFile.Close()
	output := readTail(logPath, repoSetupOutputTailBytes)
	_ = os.Remove(logPath)
	result := &RepoSetupResult{Status: RepoSetupRan, Duration: duration, Output: output}
	if runErr != nil {
		result.Status = RepoSetupFailed
		result.ExitCode = -1
		var exitErr *exec.ExitError
		switch {
		case errors.As(runErr, &exitErr):
			if exitErr.ExitCode() >= 0 {
				result.ExitCode = exitErr.ExitCode()
			}
		default:
			// The script never ran (no shell, bad working directory).
			result.Output = strings.TrimSpace(result.Output + "\n" + runErr.Error())
		}
		// Only the script's own limit is reported as a timeout; a run cut short
		// by the caller is handled below.
		if ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			result.Output = strings.TrimSpace(result.Output + "\nsetup script timed out after " + repoSetupTimeout.String())
		}
	}
	// A run cut short because the analysis itself was cancelled says nothing
	// about the script, so it is not remembered as a failure.
	if ctx.Err() != nil {
		_ = removeAllWritable(installDir)
		return result
	}
	state := repoSetupState{
		OK:         result.Status == RepoSetupRan,
		ExitCode:   result.ExitCode,
		FinishedAt: time.Now(),
		DurationMS: duration.Milliseconds(),
		Output:     result.Output,
	}
	if raw, err := json.Marshal(state); err == nil {
		if err := os.WriteFile(filepath.Join(installDir, repoSetupStateFile), raw, 0644); err != nil {
			slog.Warn("repo setup: could not record the result; the script will run again on the next clone", "install_dir", installDir, "error", err)
		}
	}
	if result.Status == RepoSetupFailed {
		result.RetryAt = state.FinishedAt.Add(repoSetupRetryAfter)
	}
	return result
}

// readTail returns the last whole lines of the file at path, at most max bytes,
// with any credentials embedded in URLs removed: the text is shown to the model
// and kept in the state file.
func readTail(path string, max int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	cut := false
	if info, err := file.Stat(); err == nil && info.Size() > max {
		_, _ = file.Seek(-max, io.SeekEnd)
		cut = true
	}
	raw, _ := io.ReadAll(io.LimitReader(file, max))
	if cut {
		// Start at a line boundary. A URL cut in half would lose the scheme the
		// redaction looks for and keep its token.
		_, rest, found := bytes.Cut(raw, []byte("\n"))
		if !found {
			return ""
		}
		raw = rest
	}
	return git.RedactURLCredentials(strings.TrimSpace(string(bytes.ToValidUTF8(raw, nil))))
}

// removeAllWritable is os.RemoveAll for a tree that may hold read-only
// directories, which a script leaves behind when it puts something like a Go
// module cache under its install directory.
func removeAllWritable(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr == nil && d.IsDir() {
			_ = os.Chmod(p, 0700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// checkoutGitDir returns the git directory of the innermost checkout that
// contains dir. A worktree has a `.git` file naming its git directory; a plain
// clone has a `.git` directory.
func checkoutGitDir(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Lstat(dotGit); err == nil {
			if info.IsDir() {
				return dotGit, true
			}
			// Checked before reading: a command can leave a `.git` that is a
			// pipe or a link to something endless, and this runs in the
			// service on every later command, outside any command timeout.
			if !info.Mode().IsRegular() || info.Size() > maxDotGitFileBytes {
				return "", false
			}
			raw, err := os.ReadFile(dotGit)
			if err != nil {
				return "", false
			}
			target, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
			if !ok {
				return "", false
			}
			target = strings.TrimSpace(target)
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			return filepath.Clean(target), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func writeRepoToolsPointer(repoDir, binDir string) error {
	gitDir, ok := checkoutGitDir(repoDir)
	if !ok {
		return fmt.Errorf("%s is not inside a git checkout", repoDir)
	}
	return os.WriteFile(filepath.Join(gitDir, repoToolsPointerFile), []byte(binDir+"\n"), 0644)
}

func clearRepoToolsPointer(repoDir string) {
	if gitDir, ok := checkoutGitDir(repoDir); ok {
		_ = os.Remove(filepath.Join(gitDir, repoToolsPointerFile))
	}
}

// repoToolsBin returns the tools directory for commands run in dir: the one
// RunRepoSetup recorded for the checkout dir is in. Empty when there is none,
// or when the directory it names is gone (the shared caches can be reclaimed
// between runs).
func repoToolsBin(dir string) string {
	gitDir, ok := checkoutGitDir(dir)
	if !ok {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(gitDir, repoToolsPointerFile))
	if err != nil {
		return ""
	}
	binDir := strings.TrimSpace(string(raw))
	if !filepath.IsAbs(binDir) {
		return ""
	}
	if info, err := os.Stat(binDir); err != nil || !info.IsDir() {
		return ""
	}
	return binDir
}

// repoToolsPathEnv returns the PATH assignment for a command run in dir, or ""
// when that checkout has no tools directory. The directory goes last, so a
// setup script can add tools but never replace one the image provides.
func repoToolsPathEnv(dir string) string {
	binDir := repoToolsBin(dir)
	if binDir == "" {
		return ""
	}
	path := os.Getenv("PATH")
	if path == "" {
		return "PATH=" + binDir
	}
	return "PATH=" + path + string(os.PathListSeparator) + binDir
}

// resolveRepoTool returns the path to run for a command started without a
// shell. os/exec resolves a bare name against this process's PATH, which never
// includes a repository's tools directory, so a name the image does not have
// is looked up there.
func resolveRepoTool(name, dir string) string {
	if name == "" || strings.ContainsRune(name, filepath.Separator) {
		return name
	}
	if _, err := exec.LookPath(name); err == nil {
		return name
	}
	binDir := repoToolsBin(dir)
	if binDir == "" {
		return name
	}
	candidate := filepath.Join(binDir, name)
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
		return candidate
	}
	return name
}
