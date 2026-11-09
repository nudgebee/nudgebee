package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"nudgebee/code-analysis-agent/tools/core"
)

// maxConcurrentBackgroundSessions bounds how many background processes one
// specialist's tool instance can have running at once. Deliberately small:
// this is for a handful of long-running jobs per run (a dev server, a build
// the agent wants to keep working alongside), not a general job queue.
const maxConcurrentBackgroundSessions = 4

// maxTrackedSessions bounds how many sessions (running or already exited) one
// tool instance keeps in memory at all. Running sessions are already capped
// at maxConcurrentBackgroundSessions; this instead bounds the *exited* ones
// an agent can accumulate by sequentially starting and killing sessions over
// a long run — each holds up to 2*backgroundOutputCap of buffered output.
// Generous relative to the concurrency cap so a recently-exited session an
// agent still wants to read stays available.
const maxTrackedSessions = 5 * maxConcurrentBackgroundSessions

// backgroundOutputCap bounds how many bytes of stdout/stderr a session keeps
// in memory. A live process can log indefinitely, so — unlike cli_tool's
// head+tail keep on a bounded one-shot result — a running session only ever
// has a meaningful tail; older bytes are dropped from the front.
const backgroundOutputCap = 32 * 1024

// BackgroundExecTool starts a long-running command (a dev server, a build)
// without blocking the calling step, and lets the agent poll its output or
// kill it later. It is deliberately a separate tool from cli_tool rather than
// a "background: true" flag on it: a still-running process has no exit code
// yet, and reusing cli_tool's CLIOutput shape (exit_code, stdout, stderr) for
// that state risks the agent reading "no error yet" as "command succeeded".
//
// Scope: this is detached background execution, not a terminal. stdout/stderr
// are plain os/exec pipes and stdin is never attached — there is no pseudo-
// terminal (PTY). That means (a) a process that only line-buffers when
// attached to a TTY may print nothing to `read` until it flushes or exits,
// even though it is running fine, and (b) a command that blocks on a prompt
// (password, Y/n) can never be answered through this tool — kill it instead.
// Interactive/PTY-backed sessions are a deliberately separate, unbuilt
// feature (see docs/architecture-decisions.md); this tool does not attempt to
// approximate one, and callers should not expect TTY-dependent output timing.
//
// Security posture matches cli_tool exactly: isDangerousCommand and
// isRestrictedPROperation are the same free functions cli_tool.go calls, not
// a second implementation — this tool does not and must not relax that
// blocklist. It only changes *when* a permitted command's output is
// observed, never *what* is permitted to run.
//
// Lifecycle: a session's process is started with exec.CommandContext(ctx, …)
// against the same ctx the whole analysis run shares (ReActPlanner passes one
// context through every tool call for the run's lifetime — see
// planners.ReActPlanner.Plan). That context is canceled when the run ends
// (success, failure, or the analysis.max_processing_time timeout), which
// kills every outstanding background process automatically — no separate
// teardown wiring is needed, and no session can outlive the HTTP request that
// started it. The agent should still call kill once a session's output is no
// longer needed, since maxConcurrentBackgroundSessions is shared for the
// whole run.
//
// Session state does not survive past this *tool instance* — a fresh
// specialist agent (e.g. CodeFixerAgent, constructed separately after a
// CodeAgent run) cannot read or kill a session another agent started. This is
// intentional scope: background execution is for one specialist's own
// investigation, not a cross-agent handoff.
type BackgroundExecTool struct {
	repoScopeGuard
	workspaceDir         string
	githubToken          string
	restrictPROperations bool

	mu       sync.Mutex
	sessions map[string]*backgroundSession
	nextID   int
}

type backgroundSession struct {
	id         string
	command    string
	workingDir string
	startedAt  time.Time
	finishedAt time.Time
	cmd        *exec.Cmd
	stdout     *boundedWriter
	stderr     *boundedWriter

	mu       sync.Mutex
	exited   bool
	exitCode int
	exitErr  string
}

// boundedWriter is an io.Writer that keeps only the most recent maxBytes
// written to it, discarding from the front once full.
type boundedWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	maxBytes int
	dropped  int
}

func newBoundedWriter(maxBytes int) *boundedWriter {
	return &boundedWriter{maxBytes: maxBytes}
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if extra := w.buf.Len() - w.maxBytes; extra > 0 {
		w.buf.Next(extra)
		w.dropped += extra
	}
	return len(p), nil
}

// snapshot returns everything currently buffered and how many bytes have
// been dropped from the front over the session's lifetime so far.
func (w *boundedWriter) snapshot() (data string, dropped int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String(), w.dropped
}

func NewBackgroundExecTool(workspaceDir string) *BackgroundExecTool {
	return &BackgroundExecTool{
		workspaceDir: workspaceDir,
		sessions:     make(map[string]*backgroundSession),
	}
}

// SetRestrictPROperations mirrors CLITool's — only the orchestrator creates PRs.
func (t *BackgroundExecTool) SetRestrictPROperations(restrict bool) {
	t.restrictPROperations = restrict
}

// SetGitHubToken mirrors CLITool's so a background command that itself
// invokes git/gh authenticates the same way a foreground one would.
func (t *BackgroundExecTool) SetGitHubToken(token string) {
	t.githubToken = token
}

func (t *BackgroundExecTool) Name() string {
	return "cli_background"
}

func (t *BackgroundExecTool) Description() string {
	return `Start, poll, or stop a LONG-RUNNING shell command (a dev server, a build, a watch process) without blocking your next step. Use 'cli' instead for anything that finishes in well under its timeout — this tool exists only for commands you need to keep running while you do other work.

ACTIONS (set "action" to one of):
  start — {"action": "start", "command": "npm run dev", "working_dir": "optional"}
    Returns a session_id immediately; the command keeps running in the background.
  read  — {"action": "read", "session_id": "bg-1"}
    Returns everything the process has printed so far (tail-capped) and whether it is still "running" or has "exited" (with its exit code). Call this again later to see new output — it is not consumed, so re-reading is safe.
  kill  — {"action": "kill", "session_id": "bg-1"}
    Stops the process. Call this once you no longer need it — only a few sessions can run at once per analysis.

No terminal is attached to the process (no PTY, no stdin): if it's the kind of program that only prints once you interact with it, or it's blocked waiting for a password/Y-n prompt, it will look silent or stuck to 'read' — kill it and try a non-interactive form of the command instead (e.g. a --yes/--force flag) rather than waiting for output that will never come.

Same command-safety rules as 'cli' apply here — this does not unlock anything 'cli' would refuse. Every background session is force-stopped automatically when this analysis run ends, even if you never call kill.`
}

func (t *BackgroundExecTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Start, poll, or stop a long-running background shell command",
		map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "One of: start, read, kill",
				"enum":        []string{"start", "read", "kill"},
			},
			"command": map[string]any{
				"type":        "string",
				"description": "The command to run in the background (required for action=start)",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Directory to run the command in (defaults to the cloned repository); only used for action=start",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "The session_id returned by action=start (required for action=read and action=kill)",
			},
		},
		[]string{"action"},
	)
}

func (t *BackgroundExecTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

type backgroundExecInput struct {
	Action     string `json:"action"`
	Command    string `json:"command,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
}

// backgroundExecOutput is deliberately not shaped like cli_tool's CLIOutput:
// a still-running process has no exit code, and reusing that struct's
// always-populated ExitCode int risked the agent reading a running session's
// zero-value exit code as a successful exit.
type backgroundExecOutput struct {
	SessionID          string `json:"session_id"`
	Command            string `json:"command"`
	WorkingDir         string `json:"working_dir"`
	Status             string `json:"status"` // "running" or "exited"
	ExitCode           *int   `json:"exit_code,omitempty"`
	Stdout             string `json:"stdout"`
	Stderr             string `json:"stderr"`
	StdoutDroppedBytes int    `json:"stdout_dropped_bytes,omitempty"`
	StderrDroppedBytes int    `json:"stderr_dropped_bytes,omitempty"`
	StartedAt          string `json:"started_at"`
	RunningFor         string `json:"running_for"`
}

func (t *BackgroundExecTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var params backgroundExecInput
	if err := core.ParseInput(input, &params); err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Invalid input parameters: %v", err),
			"Failed to parse tool input parameters",
		)
	}

	switch strings.ToLower(strings.TrimSpace(params.Action)) {
	case "start":
		return t.start(ctx, params, input)
	case "read":
		return t.read(params)
	case "kill":
		return t.kill(params)
	default:
		return core.CreateErrorResponse(
			fmt.Sprintf("Unknown action %q — must be one of: start, read, kill", params.Action),
			"Invalid action",
		)
	}
}

func (t *BackgroundExecTool) start(ctx context.Context, params backgroundExecInput, rawInput map[string]any) core.NBToolResponse {
	command := strings.TrimSpace(params.Command)
	if command == "" {
		return core.CreateErrorResponse("'command' is required for action=start", "Missing required parameter")
	}

	if blocked, reason := isRestrictedPROperation(command, t.restrictPROperations); blocked {
		return core.CreateSuccessResponse(reason, reason, map[string]any{"blocked": true, "reason": reason})
	}
	if isDangerousCommand(command) {
		return core.CreateErrorResponse(
			"Command blocked for security reasons",
			"Potentially dangerous command detected."+envPivotHint,
		)
	}

	baseRepoDir := t.workspaceDir
	if workingDir, ok := rawInput["working_directory"].(string); ok && workingDir != "" {
		baseRepoDir = workingDir
	}
	workingDir := params.WorkingDir
	if workingDir == "" {
		workingDir = baseRepoDir
	} else if !filepath.IsAbs(workingDir) {
		workingDir = filepath.Join(baseRepoDir, workingDir)
	}
	if _, err := os.Stat(workingDir); os.IsNotExist(err) {
		if err := os.MkdirAll(workingDir, 0755); err != nil {
			return core.CreateErrorResponse(
				fmt.Sprintf("Failed to create working directory: %v", err),
				"Failed to create working directory",
			)
		}
	}

	// Refuse to report on another repository's pull requests/issues — same
	// guard cli_tool and gh_tool apply, and for the same reason: a background
	// command (e.g. `gh pr view --repo other/repo --watch`) can reach the
	// GitHub API just as easily as a one-shot one.
	if blocked, reason := t.checkCommand(command, workingDir); blocked {
		return core.CreateErrorResponse(reason, reason)
	}

	t.mu.Lock()
	running := 0
	var runningList []string
	for id, s := range t.sessions {
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if !exited {
			running++
			runningList = append(runningList, fmt.Sprintf("%s (%s)", id, s.command))
		}
	}
	if running >= maxConcurrentBackgroundSessions {
		t.mu.Unlock()
		return core.CreateErrorResponse(
			fmt.Sprintf("Too many background sessions already running (max %d): %s", maxConcurrentBackgroundSessions, strings.Join(runningList, ", ")),
			"kill an existing session before starting another",
		)
	}
	t.evictOldestExitedLocked()
	t.nextID++
	sessionID := fmt.Sprintf("bg-%d", t.nextID)
	t.mu.Unlock()

	var cmd *exec.Cmd
	if containsShellFeatures(command) {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	} else {
		args := strings.Fields(command)
		if len(args) == 0 {
			return core.CreateErrorResponse("Empty command", "Empty command")
		}
		cmd = exec.CommandContext(ctx, resolveRepoTool(args[0], workingDir), args[1:]...)
	}
	cmd.Dir = workingDir

	// A dev server started via `sh -c` (or directly) commonly forks its own
	// child process (npm run dev -> node, webpack-dev-server -> workers).
	// Killing only cmd.Process — the default behavior of both Process.Kill()
	// and exec.CommandContext's ctx-cancellation — leaves that child orphaned,
	// still holding the port. Setpgid puts the whole tree in its own process
	// group so a single negative-PID signal reaches all of it, and Cancel
	// overrides ctx-cancellation's default (kill cmd.Process only) to do the
	// same. WaitDelay bounds cmd.Wait(): stdout/stderr are plain io.Writers,
	// so os/exec backs them with a real pipe — if a grandchild somehow still
	// holds that pipe's write end open after the group is killed, Wait()
	// would otherwise block forever waiting for EOF, permanently pinning one
	// of the few concurrent-session slots.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	var extraEnv []string
	if isGitHubCommand(command) && t.githubToken != "" {
		extraEnv = append(extraEnv, fmt.Sprintf("GITHUB_TOKEN=%s", t.githubToken))
	}
	if commandInvokesGit(command) {
		extraEnv = append(extraEnv, gitHTTPSAuthEnv(t.githubToken)...)
	}
	// Tools the repository's setup script installed (see repo_setup.go).
	if toolsPath := repoToolsPathEnv(workingDir); toolsPath != "" {
		extraEnv = append(extraEnv, toolsPath)
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	sess := &backgroundSession{
		id:         sessionID,
		command:    command,
		workingDir: workingDir,
		startedAt:  time.Now(),
		cmd:        cmd,
		stdout:     newBoundedWriter(backgroundOutputCap),
		stderr:     newBoundedWriter(backgroundOutputCap),
	}
	cmd.Stdout = sess.stdout
	cmd.Stderr = sess.stderr

	if err := cmd.Start(); err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Failed to start command: %v", err),
			"Failed to start background command",
		)
	}

	t.mu.Lock()
	t.sessions[sessionID] = sess
	t.mu.Unlock()

	// Wait in the background so the exit status/code is captured whenever the
	// process actually finishes; ctx cancellation (run end) kills it via
	// exec.CommandContext, and this goroutine then records that as the exit.
	go func() {
		waitErr := cmd.Wait()
		sess.mu.Lock()
		defer sess.mu.Unlock()
		sess.finishedAt = time.Now()
		sess.exited = true
		if waitErr != nil {
			sess.exitErr = waitErr.Error()
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				sess.exitCode = exitErr.ExitCode()
			} else {
				sess.exitCode = -1
			}
		}
	}()

	observation := fmt.Sprintf("Started background session %s: %s (in %s)", sessionID, command, workingDir)
	return core.CreateSuccessResponse(
		fmt.Sprintf("Background command started: %s", command),
		observation,
		map[string]any{"session_id": sessionID, "command": command, "working_dir": workingDir},
	)
}

// evictOldestExitedLocked drops the oldest exited sessions once the tracked
// total exceeds maxTrackedSessions, so an agent that sequentially starts and
// kills many sessions over a long run doesn't accumulate their output
// buffers forever. Never evicts a still-running session. Caller must hold t.mu.
func (t *BackgroundExecTool) evictOldestExitedLocked() {
	if len(t.sessions) < maxTrackedSessions {
		return
	}
	type candidate struct {
		id        string
		startedAt time.Time
	}
	var exited []candidate
	for id, s := range t.sessions {
		s.mu.Lock()
		isExited := s.exited
		s.mu.Unlock()
		if isExited {
			exited = append(exited, candidate{id: id, startedAt: s.startedAt})
		}
	}
	sort.Slice(exited, func(i, j int) bool { return exited[i].startedAt.Before(exited[j].startedAt) })
	toEvict := len(t.sessions) - maxTrackedSessions + 1
	for i := 0; i < toEvict && i < len(exited); i++ {
		delete(t.sessions, exited[i].id)
	}
}

func (t *BackgroundExecTool) lookup(sessionID string) (*backgroundSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sess, ok := t.sessions[sessionID]
	return sess, ok
}

func runningDuration(startedAt, finishedAt time.Time, exited bool) time.Duration {
	if exited && !finishedAt.IsZero() {
		return finishedAt.Sub(startedAt)
	}
	return time.Since(startedAt)
}

func (t *BackgroundExecTool) read(params backgroundExecInput) core.NBToolResponse {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return core.CreateErrorResponse("'session_id' is required for action=read", "Missing required parameter")
	}
	sess, ok := t.lookup(sessionID)
	if !ok {
		return core.CreateErrorResponse(fmt.Sprintf("Unknown session_id: %s", sessionID), "No such background session")
	}

	stdout, stdoutDropped := sess.stdout.snapshot()
	stderr, stderrDropped := sess.stderr.snapshot()

	sess.mu.Lock()
	exited := sess.exited
	finishedAt := sess.finishedAt
	exitCode := sess.exitCode
	exitErr := sess.exitErr
	sess.mu.Unlock()

	out := backgroundExecOutput{
		SessionID:          sess.id,
		Command:            sess.command,
		WorkingDir:         sess.workingDir,
		Stdout:             stdout,
		Stderr:             stderr,
		StdoutDroppedBytes: stdoutDropped,
		StderrDroppedBytes: stderrDropped,
		StartedAt:          sess.startedAt.Format(time.RFC3339),
		RunningFor:         runningDuration(sess.startedAt, finishedAt, exited).Round(time.Second).String(),
	}

	observation := fmt.Sprintf("Session %s: %s", sess.id, sess.command)
	if exited {
		out.Status = "exited"
		code := exitCode
		out.ExitCode = &code
		observation += fmt.Sprintf("\nStatus: exited (code %d)", code)
		if exitErr != "" && code == -1 {
			observation += fmt.Sprintf("\nError: %s", exitErr)
		}
	} else {
		out.Status = "running"
		observation += fmt.Sprintf("\nStatus: still running (%s)", out.RunningFor)
	}
	if stdout != "" {
		observation += fmt.Sprintf("\nStdout:\n%s", stdout)
	}
	if stderr != "" {
		observation += fmt.Sprintf("\nStderr:\n%s", stderr)
	}
	if stdoutDropped > 0 || stderrDropped > 0 {
		observation += fmt.Sprintf("\n[note] earliest %d stdout / %d stderr bytes were dropped to stay under the buffer cap — only the most recent output is shown", stdoutDropped, stderrDropped)
	}

	return core.CreateSuccessResponse(
		fmt.Sprintf("Read background session %s", sess.id),
		observation,
		out,
	)
}

func (t *BackgroundExecTool) kill(params backgroundExecInput) core.NBToolResponse {
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return core.CreateErrorResponse("'session_id' is required for action=kill", "Missing required parameter")
	}
	sess, ok := t.lookup(sessionID)
	if !ok {
		return core.CreateErrorResponse(fmt.Sprintf("Unknown session_id: %s", sessionID), "No such background session")
	}

	sess.mu.Lock()
	alreadyExited := sess.exited
	sess.mu.Unlock()
	if alreadyExited {
		return core.CreateSuccessResponse(
			fmt.Sprintf("Session %s had already exited", sess.id),
			fmt.Sprintf("Session %s was not running (already exited)", sess.id),
			map[string]any{"session_id": sess.id, "already_exited": true},
		)
	}

	if sess.cmd.Process != nil {
		// Kill the whole process group (negative PID), not just cmd.Process —
		// see the Setpgid/Cancel comment in start(). Process.Kill() alone
		// would orphan any child the command forked.
		_ = syscall.Kill(-sess.cmd.Process.Pid, syscall.SIGKILL)
	}

	observation := fmt.Sprintf("Killed background session %s: %s", sess.id, sess.command)
	return core.CreateSuccessResponse(observation, observation, map[string]any{"session_id": sess.id, "killed": true})
}
