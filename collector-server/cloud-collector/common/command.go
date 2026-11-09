package common

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"strings"

	"github.com/google/shlex"
)

// QuoteCommandArg encodes one argv value for the shlex round trip used by
// SecureExecute. SecureExecute never invokes a shell, but it accepts a command
// string and tokenizes it, so dynamically sourced values must remain one
// literal argument.
func QuoteCommandArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// ValidateCliCommand checks if the command's positional arguments (skipping flags)
// match any blocked subcommand sequence. blockedPrefixes are space-separated token
// sequences after the CLI tool name, e.g. ["configure", "sso", "config set"].
// Flags like --region are skipped so "aws --region us-east-1 sso login" is still blocked.
func ValidateCliCommand(command string, blockedPrefixes []string) error {
	args, err := shlex.Split(command)
	if err != nil {
		return fmt.Errorf("failed to parse command: %w", err)
	}
	if len(args) < 2 {
		return nil
	}

	// Extract positional args after the CLI tool name, skipping flags and their values.
	var positional []string
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			// Skip flag value if it's a --key value pair (not --key=value)
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++ // skip the flag's value
			}
			continue
		}
		positional = append(positional, args[i])
	}

	positionalStr := strings.Join(positional, " ")
	for _, prefix := range blockedPrefixes {
		prefixTokens := strings.Fields(prefix)
		// Check that blocked tokens match the start of positional args as whole words
		if len(prefixTokens) <= len(positional) {
			match := true
			for j, tok := range prefixTokens {
				if positional[j] != tok {
					match = false
					break
				}
			}
			if match {
				return fmt.Errorf("command %q is not allowed", prefix)
			}
		} else if strings.HasPrefix(positionalStr, prefix) {
			// Fallback for single-token prefixes
			return fmt.Errorf("command %q is not allowed", prefix)
		}
	}
	return nil
}

// SecureCommandOptions defines the parameters for running a secure command.
type SecureCommandOptions struct {
	Command        string
	Env            []string
	Timeout        time.Duration
	MaxOutputBytes int
	// OutputTruncated, when non-nil, is set after both process streams are
	// drained. The process is never allowed to block merely because capture hit
	// its limit.
	OutputTruncated *bool
	// Untrusted marks a command written by a caller (a user, an automation or
	// the LLM) rather than by the collector itself. It must pass
	// ValidateUntrustedArgs and runs from an empty working directory of its own,
	// so it cannot point the CLI at local files.
	Untrusted bool
}

// ValidateUntrustedArgs refuses a caller-written command that would make the
// CLI read a file on the collector. Commands run as one process with no shell,
// so a "|" token is refused rather than handed to the CLI. A file reference
// (file://, fileb://, an Azure @path, or the value of a flag such as
// --flags-file or --src-path) is refused when it names something that exists
// here, resolving relative paths against dir, the command's own empty working
// directory, so only paths outside it can match. Plain arguments are not
// checked: CloudWatch log group names such as "/" or "/var/log" look like paths
// but are not read from disk.
func ValidateUntrustedArgs(args []string, dir string) error {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "|" {
			return errors.New("shell pipes are not supported: run one command and filter its output with --query, --filter or --format")
		}
		values := []string{arg}
		if strings.HasPrefix(arg, "-") {
			if name, value, ok := strings.Cut(arg, "="); ok {
				values = []string{value}
				if isFileFlag(name) {
					values = append(values, fileFlagPaths(value)...)
				}
			} else if isFileFlag(arg) && i+1 < len(args) {
				values = append(values, fileFlagPaths(args[i+1])...)
			}
		}
		for _, value := range values {
			if err := validateFileReference(value, dir); err != nil {
				return err
			}
		}
	}
	return nil
}

// isFileFlag reports whether a flag's value is a local path by convention, e.g.
// gcloud --flags-file / --metadata-from-file, az --template-file / --src-path,
// or the short forms az uses for them (-f).
func isFileFlag(flag string) bool {
	flag = strings.ToLower(flag)
	if flag == "-f" || flag == "-d" || flag == "-p" {
		return true
	}
	for _, suffix := range []string{"file", "files", "path", "dir", "key", "cert", "config", "configuration", "token", "credential", "credentials", "license"} {
		if strings.HasSuffix(flag, suffix) {
			return true
		}
	}
	return false
}

// fileFlagPaths returns the paths in a file flag's value as @ references. The
// value is a path or a comma-separated list of paths or key=path pairs, e.g.
// gcloud --metadata-from-file startup-script=run.sh,ssh-keys=keys.txt.
func fileFlagPaths(value string) []string {
	paths := []string{"@" + value}
	for _, part := range strings.Split(value, ",") {
		if _, path, ok := strings.Cut(part, "="); ok {
			part = path
		}
		paths = append(paths, "@"+part)
	}
	return paths
}

func validateFileReference(value, dir string) error {
	lower := strings.ToLower(value)
	var path string
	switch {
	case strings.HasPrefix(lower, "file://"):
		path = value[len("file://"):]
	case strings.HasPrefix(lower, "fileb://"):
		path = value[len("fileb://"):]
	case strings.HasPrefix(value, "@"):
		path = value[1:]
	default:
		return nil
	}
	// The CLIs expand "~" and environment variables in file paths themselves.
	if strings.HasPrefix(path, "~") || strings.Contains(path, "$") {
		return fmt.Errorf("argument %q refers to a file on cloud-collector, which is not allowed", value)
	}
	if path == "" || path == "-" {
		return nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("argument %q refers to a file on cloud-collector, which is not allowed", value)
	}
	return nil
}

// CommandExitError reports a process exit while preserving its concrete status
// for callers such as workspace CLI shims. Its text intentionally matches the
// legacy SecureExecute error.
type CommandExitError struct{ ExitCode int }

func (e *CommandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.ExitCode)
}

// CommandTimeoutError distinguishes a process deadline from transport and
// executor failures without requiring callers to inspect error strings.
type CommandTimeoutError struct{ Timeout time.Duration }

func (e *CommandTimeoutError) Error() string {
	return fmt.Sprintf("command timed out after %v", e.Timeout)
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{remaining: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	if b.remaining <= 0 {
		if originalLen > 0 {
			b.truncated = true
		}
		return originalLen, nil
	}
	toWrite := len(p)
	if toWrite > b.remaining {
		toWrite = b.remaining
		b.truncated = true
	}
	_, _ = b.buffer.Write(p[:toWrite])
	b.remaining -= toWrite
	return originalLen, nil
}

func (b *cappedBuffer) String() string { return b.buffer.String() }

const structuredOutputDrainTimeout = 5 * time.Second

// waitForCommandOutput waits for both pipes to reach EOF. In structured mode,
// bound that wait because a descendant which outlives the direct child can
// retain an inherited pipe descriptor indefinitely. Closing the readers is
// equivalent to exec.Cmd's WaitDelay behavior for this lower-level ForkExec
// implementation and lets the copy goroutines terminate.
func waitForCommandOutput(structured bool, stdoutReader, stderrReader *os.File, stdoutDone, stderrDone <-chan struct{}, drainTimeout time.Duration) bool {
	if !structured {
		<-stdoutDone
		<-stderrDone
		return false
	}

	timer := time.NewTimer(drainTimeout)
	defer timer.Stop()
	for stdoutDone != nil || stderrDone != nil {
		select {
		case <-stdoutDone:
			stdoutDone = nil
		case <-stderrDone:
			stderrDone = nil
		case <-timer.C:
			_ = stdoutReader.Close()
			_ = stderrReader.Close()
			if stdoutDone != nil {
				<-stdoutDone
			}
			if stderrDone != nil {
				<-stderrDone
			}
			return true
		}
	}
	return false
}

// killProcessGroup kills the process group led by pid (set up via Setpgid in
// SecureExecute's ForkExec call). ESRCH means the group is already gone —
// the child can legitimately finish and be reaped by the concurrent Wait4
// right as the timeout/cancel fires, which is a benign race, not a failure —
// so only unexpected errors are logged.
func killProcessGroup(pid int, reason string) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		slog.Error("command: failed to kill process group", "error", err, "pid", pid, "reason", reason)
	}
}

func SecureExecute(ctx context.Context, opts SecureCommandOptions) (string, string, error) {
	structuredResult := opts.MaxOutputBytes > 0 || opts.OutputTruncated != nil
	// 1. Parse the command string using shlex to safely handle arguments
	args, err := shlex.Split(opts.Command)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse command string: %w", err)
	}
	if len(args) == 0 {
		return "", "", fmt.Errorf("command cannot be empty")
	}

	var workDir string
	if opts.Untrusted {
		workDir, err = os.MkdirTemp("", "cli-workdir-*")
		if err != nil {
			return "", "", fmt.Errorf("failed to create working directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(workDir) }()
		if err := ValidateUntrustedArgs(args, workDir); err != nil {
			return "", "", err
		}
	}

	// Find the absolute path of the executable
	path, err := exec.LookPath(args[0])
	if err != nil {
		return "", "", fmt.Errorf("executable not found: %w", err)
	}

	// 2. Create pipes for stdout and stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	defer func() { _ = stdoutReader.Close() }()
	defer func() { _ = stdoutWriter.Close() }()

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return "", "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	defer func() { _ = stderrReader.Close() }()
	defer func() { _ = stderrWriter.Close() }()

	// 3. Fork the process
	// Setpgid puts the child in its own process group (pgid == pid) so the
	// timeout/cancel paths below can kill the whole group it spawned via
	// Kill(-pid, ...) instead of targeting a group that doesn't exist.
	pid, err := syscall.ForkExec(path, args, &syscall.ProcAttr{
		Dir:   workDir,
		Env:   append(opts.Env, "PATH="+os.Getenv("PATH")),
		Files: []uintptr{os.Stdin.Fd(), stdoutWriter.Fd(), stderrWriter.Fd()},
		Sys:   &syscall.SysProcAttr{Setpgid: true},
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to fork/exec process: %w", err)
	}

	// --- Parent Process ---
	// Close child's pipe ends in the parent
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	// Read output from pipes concurrently
	maxOutputBytes := opts.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = int(^uint(0) >> 1)
	}
	stdoutBuf := newCappedBuffer(maxOutputBytes)
	stderrBuf := newCappedBuffer(maxOutputBytes)
	stdoutChan := make(chan struct{})
	stderrChan := make(chan struct{})

	go func() {
		_, _ = io.Copy(stdoutBuf, stdoutReader)
		close(stdoutChan)
	}()
	go func() {
		_, _ = io.Copy(stderrBuf, stderrReader)
		close(stderrChan)
	}()

	// Set a default timeout if none is provided
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 180 * time.Second
	}
	timer := time.NewTimer(timeout)

	// Wait for the child process to finish or timeout
	var ws syscall.WaitStatus
	var werr error
	done := make(chan struct{})
	go func() {
		_, werr = syscall.Wait4(pid, &ws, 0, nil)
		close(done)
	}()

	var terminalErr error
	select {
	case <-done:
		timer.Stop()
		if werr != nil {
			return "", "", fmt.Errorf("failed to wait for child process: %w", werr)
		}
	case <-timer.C:
		// The child may have finished and been reaped (closing done) in the
		// instant before this case was chosen — recheck before killing so we
		// don't send SIGKILL to a pid the OS may have already recycled.
		select {
		case <-done:
			if werr != nil {
				return "", "", fmt.Errorf("failed to wait for child process: %w", werr)
			}
		default:
			killProcessGroup(pid, "timed-out")
			if !structuredResult {
				return "", "", fmt.Errorf("command timed out after %v", timeout)
			}
			terminalErr = &CommandTimeoutError{Timeout: timeout}
			<-done
		}
	case <-ctx.Done():
		timer.Stop()
		select {
		case <-done:
			if werr != nil {
				return "", "", fmt.Errorf("failed to wait for child process: %w", werr)
			}
		default:
			killProcessGroup(pid, "cancelled")
			if !structuredResult {
				return "", "", fmt.Errorf("command cancelled: %w", ctx.Err())
			}
			terminalErr = fmt.Errorf("command cancelled: %w", ctx.Err())
			<-done
		}
	}

	// Wait for pipe reading to complete. The structured path bounds this wait
	// in case a grandchild retains an inherited stdout/stderr descriptor.
	drainTimedOut := waitForCommandOutput(structuredResult, stdoutReader, stderrReader, stdoutChan, stderrChan, structuredOutputDrainTimeout)
	if opts.OutputTruncated != nil {
		*opts.OutputTruncated = stdoutBuf.truncated || stderrBuf.truncated || drainTimedOut
	}

	if terminalErr != nil {
		return stdoutBuf.String(), stderrBuf.String(), terminalErr
	}
	if ws.Exited() && ws.ExitStatus() != 0 {
		if !structuredResult {
			return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("command exited with status %d", ws.ExitStatus())
		}
		return stdoutBuf.String(), stderrBuf.String(), &CommandExitError{ExitCode: ws.ExitStatus()}
	}
	if ws.Signaled() {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("command terminated by signal %d", ws.Signal())
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}
