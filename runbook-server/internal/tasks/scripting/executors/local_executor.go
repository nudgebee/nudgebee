package executors

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"nudgebee/runbook/internal/tasks/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const maxOutputSize = 1024 * 1024 // 1MB

// LocalExecutor implements ScriptExecutor using os/exec.
// Note: This executor runs scripts on the host machine and is not recommended for untrusted input.
type LocalExecutor struct{}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

// limitWriter implements io.Writer and limits the amount of data written.
// It is thread-safe.
type limitWriter struct {
	w         io.Writer
	limit     int
	written   int
	truncated bool
	mu        sync.Mutex
}

func (l *limitWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.truncated {
		return len(p), nil
	}
	if l.written+len(p) > l.limit {
		remaining := l.limit - l.written
		if remaining > 0 {
			// We ignore error here as we want to continue pretending we wrote everything
			_, _ = l.w.Write(p[:remaining])
			l.written += remaining
		}
		l.truncated = true
		return len(p), nil
	}
	n, err := l.w.Write(p)
	l.written += n
	return n, err
}

// getSafeHostEnv returns a list of environment variables that are safe to pass to the script.
// This prevents sensitive host environment variables from being leaked to the script.
func getSafeHostEnv() []string {
	safeKeys := []string{
		"PATH", "HOME", "LANG", "USER", "SHELL", "TERM", "TMPDIR", "TZ",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
	}
	// Initialize as empty slice (not nil) to prevent os/exec from defaulting to full host environment
	env := make([]string, 0)
	for _, key := range safeKeys {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, fmt.Sprintf("%s=%s", key, val))
		}
	}
	// Also allow LC_* variables
	for _, envStr := range os.Environ() {
		if strings.HasPrefix(envStr, "LC_") {
			env = append(env, envStr)
		}
	}
	return env
}

func (e *LocalExecutor) Execute(taskCtx types.TaskContext, config ExecutionConfig) (string, error) {
	var cmd *exec.Cmd

	if config.Cwd == "" {
		tempDir, err := os.MkdirTemp("", "runbook-script-*")
		if err != nil {
			return "", fmt.Errorf("failed to create temp directory: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				// Log the error if cleanup fails
				slog.Default().Error("failed to remove temporary directory", "path", tempDir, "error", err)
			}
		}()

		var scriptName string
		var interpreter string

		switch config.Language {
		case "bash":
			scriptName = "script.sh"
			interpreter = "/bin/sh"
		case "javascript":
			scriptName = "script.js"
			interpreter = "node"
		case "python":
			scriptName = "script.py"
			interpreter = "python3"
		case "powershell":
			scriptName = "script.ps1"
			interpreter = "pwsh"
		default:
			return "", fmt.Errorf("unsupported language: %s", config.Language)
		}

		scriptPath := filepath.Join(tempDir, scriptName)
		scriptContent := config.Script
		if config.Language == "powershell" {
			scriptContent = BuildPowerShellConfigPrefix() + "\n" + config.Script
		}
		// Use 0700 permissions to ensure only the owner can read/write/execute the script
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0700); err != nil {
			return "", fmt.Errorf("failed to write script file: %w", err)
		}

		cmd = exec.CommandContext(taskCtx.GetContext(), interpreter, scriptPath)
		if len(config.Args) > 0 {
			cmd.Args = append(cmd.Args, config.Args...)
		}
		cmd.Dir = tempDir
	} else {
		switch config.Language {
		case "bash":
			// sh -c script -- arg1 arg2
			// $0 becomes "--", $1 becomes arg1
			args := []string{"-c", config.Script, "--"}
			if len(config.Args) > 0 {
				args = append(args, config.Args...)
			}
			cmd = exec.CommandContext(taskCtx.GetContext(), "/bin/sh", args...)
		case "javascript":
			args := []string{"-e", config.Script}
			if len(config.Args) > 0 {
				args = append(args, config.Args...)
			}
			cmd = exec.CommandContext(taskCtx.GetContext(), "node", args...)
		case "python":
			args := []string{"-c", config.Script}
			if len(config.Args) > 0 {
				args = append(args, config.Args...)
			}
			cmd = exec.CommandContext(taskCtx.GetContext(), "python3", args...)
		case "powershell":
			psConfigPrefix := BuildPowerShellConfigPrefix()
			args := []string{"-NoProfile", "-Command", psConfigPrefix + config.Script}
			if len(config.Args) > 0 {
				args = append(args, config.Args...)
			}
			cmd = exec.CommandContext(taskCtx.GetContext(), "pwsh", args...)
		default:
			return "", fmt.Errorf("unsupported language: %s", config.Language)
		}
		cmd.Dir = config.Cwd
	}

	// Set environment variables
	// Start with a safe subset of host environment variables
	cmd.Env = getSafeHostEnv()

	// Append user-provided environment variables
	for key, value := range config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	var outBuf bytes.Buffer
	lw := &limitWriter{w: &outBuf, limit: maxOutputSize}
	cmd.Stdout = lw
	cmd.Stderr = lw

	err := cmd.Run()
	outputStr := strings.TrimSpace(outBuf.String())

	if lw.truncated {
		outputStr += fmt.Sprintf("\n... output truncated to %d bytes ...", maxOutputSize)
		slog.Warn("LocalExecutor: output truncated", "limit", maxOutputSize)
	}

	if err != nil {
		if execErr, ok := err.(*exec.Error); ok && execErr.Err == exec.ErrNotFound {
			return outputStr, fmt.Errorf("interpreter %q not found — %s is not installed in this environment. "+
				"Consider using the Kubernetes executor with an image that includes %s",
				cmd.Path, config.Language, config.Language)
		}
		return outputStr, fmt.Errorf("script execution failed: %w", err)
	}

	return outputStr, nil
}
