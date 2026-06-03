package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"nudgebee/code-analysis-agent/config"

	"github.com/gin-gonic/gin"
)

// ExecutionRequest defines the expected input format for command execution.
type ExecutionRequest struct {
	Command        string            `json:"command"`
	ConversationID string            `json:"conversation_id"`
	Env            map[string]string `json:"env"`
}

// ExecutionResponse defines the output format for command execution.
type ExecutionResponse struct {
	CommandStatus  string `json:"command_status"`
	Response       string `json:"response"`
	Error          string `json:"error,omitempty"`
	ConversationID string `json:"conversation_id"`
}

// LimitedWriter is a writer that caps the amount of data written to an underlying writer.
type LimitedWriter struct {
	W     io.Writer
	Limit int64
	total int64
	mu    sync.Mutex
}

func (lw *LimitedWriter) Write(p []byte) (n int, err error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()

	if lw.total >= lw.Limit {
		lw.total += int64(len(p))
		return len(p), nil
	}
	remaining := lw.Limit - lw.total
	if int64(len(p)) > remaining {
		_, err = lw.W.Write(p[:remaining])
		lw.total += int64(len(p))
		return len(p), err
	}
	n, err = lw.W.Write(p)
	lw.total += int64(n)
	return n, err
}

var (
	// Ensure conversation IDs are safe to use as directory names.
	safePathRe = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
)

type ExecutionHandler struct {
	Config *config.Config
}

func NewExecutionHandler(cfg *config.Config) *ExecutionHandler {
	return &ExecutionHandler{
		Config: cfg,
	}
}

// HandleExecute processes the remote command execution request.
func (h *ExecutionHandler) HandleExecute(c *gin.Context) {
	var req ExecutionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ExecutionResponse{
			CommandStatus: "failed",
			Error:         "Invalid request body",
		})
		return
	}

	resp := ExecutionResponse{
		ConversationID: req.ConversationID,
	}

	// 1. Validation
	if req.Command == "" {
		resp.CommandStatus = "failed"
		resp.Error = "Command is empty"
		c.JSON(http.StatusOK, resp)
		return
	}
	if req.ConversationID == "" {
		resp.CommandStatus = "failed"
		resp.Error = "Conversation ID is empty"
		c.JSON(http.StatusOK, resp)
		return
	}
	if !safePathRe.MatchString(req.ConversationID) {
		resp.CommandStatus = "failed"
		resp.Error = "Invalid conversation ID format"
		c.JSON(http.StatusOK, resp)
		return
	}

	// 2. Directory Setup
	// Use the configured execution workspace directory + conversation_id
	baseDir := h.Config.Execution.WorkspaceDir
	if baseDir == "" {
		// Fallback to analysis workspace if execution workspace is not configured
		if h.Config.Analysis.WorkspaceDir != "" {
			baseDir = filepath.Join(h.Config.Analysis.WorkspaceDir, "exec_workspaces")
		} else {
			baseDir = "/tmp/code-analysis/exec_workspaces" // Hard default
		}
	}

	// Ensure baseDir is absolute for safety
	absBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		resp.CommandStatus = "failed"
		resp.Error = fmt.Sprintf("Failed to resolve base directory: %v", err)
		c.JSON(http.StatusOK, resp)
		return
	}

	workDir := filepath.Join(absBaseDir, req.ConversationID)

	// Final safety check: ensure workDir is within absBaseDir
	rel, err := filepath.Rel(absBaseDir, workDir)
	if err != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, "/") {
		resp.CommandStatus = "failed"
		resp.Error = "Invalid workspace path"
		c.JSON(http.StatusOK, resp)
		return
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		resp.CommandStatus = "failed"
		resp.Error = fmt.Sprintf("Failed to create workspace: %v", err)
		c.JSON(http.StatusOK, resp)
		return
	}

	// 3. Execution
	// Use context with timeout to prevent hangs
	timeout := 30 * time.Second
	if h.Config.Server.WriteTimeout > 0 {
		timeout = h.Config.Server.WriteTimeout - (2 * time.Second) // Slightly less than server timeout
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	// Security: check for path traversal in command
	if err := h.validateCommand(req.Command, workDir); err != nil {
		resp.CommandStatus = "failed"
		resp.Error = fmt.Sprintf("Security validation failed: %v", err)
		c.JSON(http.StatusOK, resp)
		return
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", req.Command)
	cmd.Dir = workDir

	// Apply environment variables - strictly from request, do NOT inherit host env
	// Ensure basic PATH is present if not provided, otherwise standard tools fail
	cmd.Env = []string{
		"PATH=/home/appuser/.local/bin:/home/appuser/bin:/app/google-cloud-sdk/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		fmt.Sprintf("HOME=%s", workDir),
		fmt.Sprintf("PWD=%s", workDir),
	}
	for k, v := range req.Env {
		if k == "PATH" || k == "HOME" || k == "PWD" {
			// Skip or handle specially if needed
			continue
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Pass through specific environment variables if available in the host environment
	// These are required for the shim to function.
	passThroughEnvs := []string{"NB_LLM_SERVER_URL", "NB_ACCOUNT_ID", "NB_WORKSPACE_TOKEN"}
	for _, envKey := range passThroughEnvs {
		if val, exists := os.LookupEnv(envKey); exists {
			// Only set if not already provided in request
			if _, provided := req.Env[envKey]; !provided {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", envKey, val))
			}
		}
	}

	// Cap output to prevent OOM (1MB limit)
	var outBuf bytes.Buffer
	limitedW := &LimitedWriter{W: &outBuf, Limit: 1024 * 1024}
	cmd.Stdout = limitedW
	cmd.Stderr = limitedW

	err = cmd.Run()
	resp.Response = outBuf.String()
	if limitedW.total > limitedW.Limit {
		resp.Response += "\n...(truncated)"
	}

	if err != nil {
		resp.CommandStatus = "failed"
		resp.Error = err.Error()
	} else {
		resp.CommandStatus = "success"
	}

	c.JSON(http.StatusOK, resp)
}

// validateCommand performs security checks on the command to prevent escaping the workspace.
func (h *ExecutionHandler) validateCommand(command, workDir string) error {
	commandLower := strings.ToLower(command)

	// 0. Block bypass patterns (encoding tricks, shell indirection, inline interpreters)
	if err := detectBypassPatterns(commandLower); err != nil {
		return err
	}

	// 1. Block explicit attempts to access sensitive system directories
	sensitivePaths := []string{"/etc", "/var", "/root", "/home", "/proc", "/sys", "/dev", "/usr/local/etc", "/usr/local/var"}
	for _, path := range sensitivePaths {
		patterns := []string{" " + path, "=" + path, "\"" + path, "'" + path}
		if strings.HasPrefix(command, path) {
			return fmt.Errorf("access to absolute path %s is blocked", path)
		}
		for _, p := range patterns {
			if strings.Contains(commandLower, p) {
				return fmt.Errorf("access to absolute path %s is blocked", path)
			}
		}
	}

	// 2. Block potential path traversal
	if strings.Contains(command, "..") {
		if strings.Contains(command, " ../") || strings.Contains(command, "/../") || strings.HasPrefix(command, "../") {
			return fmt.Errorf("path traversal with '..' is restricted")
		}
	}

	// 3. Block dangerous system commands
	dangerousCmds := []string{"sudo ", "su ", "chown ", "chmod 777", "rm -rf /", "mkfs", "fdisk", "apt-get", "apk ", "yum ", "dnf "}
	for _, cmd := range dangerousCmds {
		if strings.Contains(commandLower, cmd) {
			return fmt.Errorf("dangerous command %s is blocked", cmd)
		}
	}

	// 4. Block symlink creation to sensitive paths (ln -s /etc/passwd link && cat link)
	if strings.Contains(commandLower, "ln ") && strings.Contains(commandLower, "-s") {
		for _, path := range sensitivePaths {
			if strings.Contains(command, path) {
				return fmt.Errorf("symlink to sensitive path %s is blocked", path)
			}
		}
	}

	return nil
}

// detectBypassPatterns checks for shell tricks used to evade command validation.
func detectBypassPatterns(cmdLower string) error {
	// 1. Piping through shell interpreters (e.g. echo "cm0g..." | base64 -d | sh)
	shellInterpreters := []string{"| sh", "| bash", "| zsh", "| dash", "|sh", "|bash", "|zsh", "|dash"}
	for _, s := range shellInterpreters {
		if strings.Contains(cmdLower, s) {
			return fmt.Errorf("piping to shell interpreter is blocked")
		}
	}

	// 2. Hex/octal escape sequences ($'\x72\x6d' → rm)
	if strings.Contains(cmdLower, "$'\\x") || strings.Contains(cmdLower, "$'\\0") {
		return fmt.Errorf("hex/octal escape sequences are blocked")
	}

	// 4. eval / source
	fields := strings.Fields(cmdLower)
	if len(fields) > 0 {
		first := fields[0]
		if first == "eval" || first == "source" || first == "." {
			return fmt.Errorf("eval/source commands are blocked")
		}
	}

	// 5. env prefix bypass (env rm -rf /) — strip env, its flags, and VAR=val
	// assignments, then re-check the actual command.
	if len(fields) > 1 && fields[0] == "env" {
		stripped := fields[1:]
		for len(stripped) > 0 {
			p := stripped[0]
			if strings.HasPrefix(p, "-") || strings.Contains(p, "=") {
				stripped = stripped[1:]
				continue
			}
			break
		}
		if len(stripped) > 0 {
			rebuiltCmd := strings.Join(stripped, " ")
			if err := detectBypassPatterns(rebuiltCmd); err != nil {
				return fmt.Errorf("env prefix bypass blocked: %w", err)
			}
		}
	}

	// 6. Command starts with command substitution
	trimmed := strings.TrimSpace(cmdLower)
	if strings.HasPrefix(trimmed, "$(") || strings.HasPrefix(trimmed, "`") {
		return fmt.Errorf("command substitution as primary command is blocked")
	}

	return nil
}
