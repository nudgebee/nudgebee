package common

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"strings"

	"github.com/google/shlex"
)

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
	Command string
	Env     []string
	Timeout time.Duration
}

func SecureExecute(ctx context.Context, opts SecureCommandOptions) (string, string, error) {
	// 1. Parse the command string using shlex to safely handle arguments
	args, err := shlex.Split(opts.Command)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse command string: %w", err)
	}
	if len(args) == 0 {
		return "", "", fmt.Errorf("command cannot be empty")
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
	pid, err := syscall.ForkExec(path, args, &syscall.ProcAttr{
		Env:   append(opts.Env, "PATH="+os.Getenv("PATH")),
		Files: []uintptr{os.Stdin.Fd(), stdoutWriter.Fd(), stderrWriter.Fd()},
	})

	if err != nil {
		return "", "", fmt.Errorf("failed to fork/exec process: %w", err)
	}

	// --- Parent Process ---
	// Close child's pipe ends in the parent
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	// Read output from pipes concurrently
	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutChan := make(chan struct{})
	stderrChan := make(chan struct{})

	go func() {
		_, _ = io.Copy(&stdoutBuf, stdoutReader)
		close(stdoutChan)
	}()
	go func() {
		_, _ = io.Copy(&stderrBuf, stderrReader)
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

	select {
	case <-done:
		timer.Stop()
		if werr != nil {
			return "", "", fmt.Errorf("failed to wait for child process: %w", werr)
		}
	case <-timer.C:
		// Kill the child process on timeout
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return "", "", fmt.Errorf("command timed out after %v", timeout)
	case <-ctx.Done():
		timer.Stop()
		// Kill the child process on context cancellation
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return "", "", fmt.Errorf("command cancelled: %w", ctx.Err())
	}

	// Wait for pipe reading to complete
	<-stdoutChan
	<-stderrChan

	if ws.Exited() && ws.ExitStatus() != 0 {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("command exited with status %d", ws.ExitStatus())
	}
	if ws.Signaled() {
		return stdoutBuf.String(), stderrBuf.String(), fmt.Errorf("command terminated by signal %d", ws.Signal())
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}

// SecureExecutePipeline handles the execution of piped commands.
func SecureExecutePipeline(ctx context.Context, opts SecureCommandOptions) (string, string, error) {
	// Use shlex to tokenize the entire command correctly (respecting quotes)
	allTokens, err := shlex.Split(opts.Command)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse pipeline command: %w", err)
	}

	// Split tokens into stages separated by "|"
	var stages [][]string
	var currentStage []string
	for _, token := range allTokens {
		if token == "|" {
			if len(currentStage) > 0 {
				stages = append(stages, currentStage)
				currentStage = nil
			}
		} else {
			currentStage = append(currentStage, token)
		}
	}
	if len(currentStage) > 0 {
		stages = append(stages, currentStage)
	}

	if len(stages) == 0 {
		return "", "", fmt.Errorf("empty command")
	}

	// Set a default timeout if none is provided
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// Create commands for each stage of the pipeline
	cmds := make([]*exec.Cmd, len(stages))
	allowedTools := map[string]bool{"grep": true, "jq": true, "awk": true}

	for i, args := range stages {
		if len(args) == 0 {
			return "", "", fmt.Errorf("pipeline stage %d is empty", i)
		}

		// Security Check: only allow specific tools after the first command
		if i > 0 {
			if !allowedTools[args[0]] {
				return "", "", fmt.Errorf("tool '%s' is not allowed in a pipeline", args[0])
			}
		}

		cmds[i] = exec.CommandContext(cmdCtx, args[0], args[1:]...)
		// Set the environment for each command in the pipeline
		cmds[i].Env = append(opts.Env, "PATH="+os.Getenv("PATH"))
	}

	// Connect the pipes between commands
	for i := 0; i < len(cmds)-1; i++ {
		stdoutPipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return "", "", fmt.Errorf("failed to create stdout pipe for stage %d: %w", i, err)
		}
		cmds[i+1].Stdin = stdoutPipe
	}

	// Capture the final stdout and all stderr streams
	var finalStdout bytes.Buffer
	cmds[len(cmds)-1].Stdout = &finalStdout

	stderrBufs := make([]*bytes.Buffer, len(cmds))
	for i := range cmds {
		stderrBufs[i] = new(bytes.Buffer)
		cmds[i].Stderr = stderrBufs[i]
	}

	// Start all commands
	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			return "", "", fmt.Errorf("failed to start command %q: %w", cmd.String(), err)
		}
	}

	// Wait for all commands to finish
	var finalErr error
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			if finalErr == nil {
				finalErr = fmt.Errorf("pipeline stage %q failed: %w", cmd.String(), err)
			}
		}
	}

	// Combine all stderr outputs
	var combinedStderr bytes.Buffer
	for _, buf := range stderrBufs {
		combinedStderr.Write(buf.Bytes())
	}

	return finalStdout.String(), combinedStderr.String(), finalErr
}
