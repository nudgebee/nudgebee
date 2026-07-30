package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"nudgebee/code-analysis-agent/tools/core"
)

// blockedSearchPaths are root filesystem paths that should not be searched.
// Scanning these wastes time and produces irrelevant results.
var blockedSearchPaths = map[string]struct{}{
	"/":     {},
	"/usr":  {},
	"/etc":  {},
	"/var":  {},
	"/opt":  {},
	"/home": {},
	"/root": {},
	"/sys":  {},
}

type CLITool struct {
	repoScopeGuard
	workspaceDir         string
	timeout              time.Duration
	githubToken          string
	restrictPROperations bool
}

type CLIInput struct {
	Command     string `json:"command"`                // The command to execute
	WorkingDir  string `json:"working_dir,omitempty"`  // Directory to run command in
	Timeout     int    `json:"timeout,omitempty"`      // Timeout in seconds
	Description string `json:"description,omitempty"`  // Description of what this command does
	GitHubToken string `json:"github_token,omitempty"` // GitHub token for gh commands (injected by secure context)
	// Structured Git Operations
	GitOperation string            `json:"git_operation,omitempty"` // Git operation: status, diff, log, add, commit, branch, checkout, reset, show, config
	GitOptions   map[string]string `json:"git_options,omitempty"`   // Operation-specific options
	GitFiles     []string          `json:"git_files,omitempty"`     // Files to operate on
}

type CLIOutput struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir"`
	ExitCode   int    `json:"exit_code"`
	// StageExitCodes holds the per-stage exit codes (bash PIPESTATUS) of the final
	// pipeline in the command, when it had more than one stage. The overall ExitCode
	// only reflects the last stage, which hides earlier failures (`go build | grep -v
	// noise` reports grep's code, not the build's) — these are the honest codes.
	StageExitCodes []int  `json:"stage_exit_codes,omitempty"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	Error          string `json:"error,omitempty"`
	Duration       string `json:"duration"`
	// SpillPath is set when output exceeded the context cap; the full untruncated
	// output is preserved there so no signal is silently dropped.
	SpillPath string `json:"spill_path,omitempty"`
}

func NewCLITool(workspaceDir string) *CLITool {
	return &CLITool{
		workspaceDir: workspaceDir,
		timeout:      10 * time.Minute, // Default timeout — build commands on cold cache can take >5m
		githubToken:  "",               // Will be injected via secure context
	}
}

// SetRestrictPROperations blocks PR/MR creation commands (gh pr create, glab mr create).
// Used for agent-facing instances so only the orchestrator creates PRs with proper formatting.
func (t *CLITool) SetRestrictPROperations(restrict bool) {
	t.restrictPROperations = restrict
}

func (t *CLITool) Name() string {
	return "cli"
}

func (t *CLITool) Description() string {
	return `Execute shell commands or structured git operations. Supports both raw commands and structured git operations.

MODE 1: RAW COMMAND (flexible)
  {"command": "git log --oneline -n 10"}
  {"command": "find . -name '*.py'"}
  {"command": "ls -la src/"}

MODE 2: STRUCTURED GIT (recommended for git)
  {"git_operation": "status", "git_options": {"porcelain": "true"}}
  {"git_operation": "log", "git_options": {"max_count": "10"}}
  {"git_operation": "diff", "git_files": ["path/to/file.py"]}

SUPPORTED COMMANDS:
- Git operations: status, diff, log, add, commit, branch, checkout, etc.
- File operations: ls, cat, head, tail, find
- Search: grep (use grep tool or rg tool instead for better results)
- Other: Any standard Unix/shell command

EXAMPLES:
  List files:           {"command": "ls -la"}
  Recent commits:       {"git_operation": "log", "git_options": {"max_count": "5"}}
  Git status:           {"git_operation": "status"}
  Custom command:       {"command": "find . -type f -name '*.go'"}

WHEN TO USE:
- For commands not covered by specialized tools
- For quick shell operations
- For git operations (though git tool is better for complex cases)

PREFER SPECIALIZED TOOLS:
- file_view: Reading files (better than cat)
- grep/rg: Searching code (better than grep command)
- git tool: Complex git operations
- gh tool: GitHub operations`
}

func (t *CLITool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Execute CLI commands or structured git operations for code analysis and repository management",
		map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The command to execute (e.g., 'git log --oneline -10', 'grep -r \"pattern\" .', 'find . -name \"filename\"', 'gh pr list --search=\"commit_hash\"'). Use this for custom commands.",
			},
			"git_operation": map[string]any{
				"type":        "string",
				"description": "Structured git operation: status, diff, log, add, commit, branch, checkout, reset, show, config. Alternative to raw command.",
				"enum":        []string{"status", "diff", "log", "add", "commit", "branch", "checkout", "reset", "show", "config"},
			},
			"git_options": map[string]any{
				"type":        "object",
				"description": "Options for git operations (e.g., {\"porcelain\": \"true\"} for status, {\"message\": \"commit msg\"} for commit)",
			},
			"git_files": map[string]any{
				"type":        "array",
				"description": "Files to operate on for git commands (for add, diff, etc.)",
				"items": map[string]any{
					"type": "string",
				},
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "Directory to run the command in (defaults to cloned repository)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 600)",
				"default":     600,
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Brief description of what this command does",
			},
		},
		[]string{}, // Either command OR git_operation is required
	)
}

func (t *CLITool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var params CLIInput
	if err := core.ParseInput(input, &params); err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Invalid input parameters: %v", err),
			"Failed to parse tool input parameters",
		)
	}

	// Handle structured git operations or raw command
	var commandToExecute string
	if params.GitOperation != "" {
		// Build git command from structured input
		gitCmd, err := t.buildGitCommand(params.GitOperation, params.GitOptions, params.GitFiles)
		if err != nil {
			return core.CreateErrorResponse(
				fmt.Sprintf("Invalid git operation: %v", err),
				"Failed to build git command",
			)
		}
		commandToExecute = gitCmd
	} else if params.Command != "" {
		commandToExecute = params.Command
	} else {
		return core.CreateErrorResponse(
			"either 'command' or 'git_operation' is required",
			"Missing required parameter",
		)
	}

	// Block PR/MR creation for agent-facing instances — orchestrator handles this
	if blocked, reason := t.isRestrictedPROperation(commandToExecute); blocked {
		return core.CreateSuccessResponse(reason, reason, map[string]any{"blocked": true, "reason": reason})
	}

	// Security: Block dangerous commands
	if t.isDangerousCommand(commandToExecute) {
		return core.CreateErrorResponse(
			"Command blocked for security reasons",
			"Potentially dangerous command detected."+envPivotHint,
		)
	}

	// Use working directory from orchestrator, fallback to tool workspace
	baseRepoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		baseRepoDir = workingDir
	}

	// Set working directory from parameters or use base repository directory
	workingDir := params.WorkingDir
	if workingDir == "" {
		workingDir = baseRepoDir
	} else if !filepath.IsAbs(workingDir) {
		// Resolve relative paths within the repository directory
		workingDir = filepath.Join(baseRepoDir, workingDir)
	}

	if _, err := os.Stat(workingDir); os.IsNotExist(err) {
		err = os.MkdirAll(workingDir, 0755)
		if err != nil {
			return core.CreateErrorResponse(
				fmt.Sprintf("Failed to create working directory: %v", err),
				"Failed to create working directory",
			)
		}
	}

	// Refuse to report on another repository's pull requests. Resolved here, not
	// at construction, because workingDir is only final at this point.
	if blocked, reason := t.checkCommand(commandToExecute, workingDir); blocked {
		return core.CreateErrorResponse(reason, reason)
	}

	// Set timeout
	timeout := t.timeout
	if params.Timeout > 0 {
		timeout = time.Duration(params.Timeout) * time.Second
	}

	// Set GitHub token if provided
	githubToken := t.githubToken
	if params.GitHubToken != "" {
		githubToken = params.GitHubToken
	}

	// Execute command
	start := time.Now()
	result := t.executeCommand(ctx, commandToExecute, workingDir, timeout, githubToken)
	result.Duration = time.Since(start).String()

	response := map[string]any{
		"result": result,
	}

	observation := fmt.Sprintf("Executed: %s\nExit Code: %d\nDuration: %s",
		result.Command, result.ExitCode, result.Duration)

	// Debug logging (can be enabled for troubleshooting)
	// fmt.Printf("DEBUG CLI: command='%s' in dir='%s'\n", result.Command, result.WorkingDir)

	// Per-stage pipeline verdict: the pipeline's exit code only reflects its last
	// stage, so `go build | grep -v noise` can report success on a broken build and
	// failure on a clean one. When per-stage codes are available, judge every stage
	// honestly; a grep-family stage exiting 1 just means "no matches", not failure.
	if stages := lastPipelineStages(result.Command); len(result.StageExitCodes) > 1 && len(result.StageExitCodes) == len(stages) {
		failed := false
		parts := make([]string, 0, len(stages))
		for i, code := range result.StageExitCodes {
			bin := stageBinary(stages[i])
			note := ""
			if code != 0 {
				if isGrepFamily(bin) && code == 1 {
					note = " (no matches)"
				} else {
					failed = true
				}
			}
			parts = append(parts, fmt.Sprintf("%s=%d%s", bin, code, note))
		}
		stageSummary := strings.Join(parts, ", ")
		observation += "\nPipeline stage exits: " + stageSummary
		if result.SpillPath != "" {
			observation += fmt.Sprintf("\nFull untruncated output saved to: %s (read with file_view if needed)", result.SpillPath)
		}
		// Combined output lands in Stdout on pipeline exit 0 and Stderr otherwise,
		// which no longer tracks the honest verdict — show whichever is populated.
		output := result.Stdout
		if output == "" {
			output = result.Stderr
		}
		if !failed {
			observation += "\nCommand succeeded"
			if output != "" {
				observation += fmt.Sprintf("\nOutput:\n%s", output)
			} else {
				observation += "\nNo output captured"
			}
			return core.CreateSuccessResponse(
				fmt.Sprintf("Command executed: %s", commandToExecute),
				observation,
				response,
			)
		}
		observation += "\nCommand failed"
		if output != "" {
			observation += fmt.Sprintf("\nError:\n%s", output)
		}
		return core.CreateErrorResponseWithData(
			fmt.Sprintf("Pipeline stage failed (stage exits: %s)", stageSummary),
			observation,
			response,
		)
	}

	if result.ExitCode == 0 {
		observation += "\nCommand succeeded"
		// Include stdout in observation for LLM to see
		if result.Stdout != "" {
			observation += fmt.Sprintf("\nOutput:\n%s", result.Stdout)
		} else {
			observation += "\nNo output captured"
		}

		return core.CreateSuccessResponse(
			fmt.Sprintf("Command executed: %s", commandToExecute),
			observation,
			response,
		)
	} else if result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" &&
		strings.TrimSpace(result.Stderr) == "" && isSearchNoMatch(result.Command) {
		// grep/rg and friends exit with code 1 to mean "no lines matched" — a normal
		// empty result, not a failure (a real error is exit >= 2). Reporting it as an
		// error inflates failure metrics and nudges the agent into needless retries.
		observation += "\nCommand succeeded (no matches found)"
		return core.CreateSuccessResponse(
			fmt.Sprintf("Command executed: %s", commandToExecute),
			observation,
			response,
		)
	} else {
		observation += "\nCommand failed"
		if result.Stderr != "" {
			observation += fmt.Sprintf("\nError:\n%s", result.Stderr)
		}

		// Return error response when command fails
		errorMsg := fmt.Sprintf("Command exited with code %d", result.ExitCode)
		if result.Error != "" {
			errorMsg = result.Error
		} else if result.Stderr != "" {
			errorMsg = result.Stderr
		}

		// Environment-capability failures deserve a pivot, not a retry: the tool
		// doesn't exist here and installs are blocked. Without this hint the model
		// burns its failure budget on variants of the same unavailable tooling
		// (observed live: atlas → docker → install, all absent from the pod).
		if isEnvUnavailable(errorMsg, result.Stderr, result.Stdout) {
			observation += envPivotHint
		}

		return core.CreateErrorResponseWithData(errorMsg, observation, response)
	}
}

// isSearchNoMatch reports whether the command's final pipeline stage is a
// grep-family search tool, for which an exit code of 1 means "no matches" rather
// than an error. The exit code propagates from the last command in the pipeline,
// so only that stage is checked.
func isSearchNoMatch(cmd string) bool {
	// Split into pipeline stages on the operators that chain commands and hand the
	// pipeline's exit code to the *last* command run: pipes and sequence operators
	// (| || ; & &&). We deliberately do NOT split on redirection (< >), command
	// substitution (`), or grouping ((), because those characters routinely appear
	// inside quoted grep patterns (git conflict markers "<<<<<<<", arrow fns "=>").
	// Splitting on them would shatter a `grep "<<<<<<<" .` command so its final stage
	// no longer looks like a search, and its normal no-match exit 1 would be
	// misreported as a failure. Per-word env-prefix and path handling happens below
	// within the final stage.
	segs := strings.FieldsFunc(cmd, func(r rune) bool {
		switch r {
		case ';', '|', '&':
			return true
		}
		return false
	})
	for i := len(segs) - 1; i >= 0; i-- {
		fields := strings.Fields(segs[i])
		if len(fields) == 0 {
			continue
		}
		// Skip leading env-var assignments (e.g. `FOO=bar grep ...`).
		cmdIdx := 0
		for cmdIdx < len(fields) && strings.Contains(fields[cmdIdx], "=") {
			cmdIdx++
		}
		if cmdIdx >= len(fields) {
			continue
		}
		// Match on the binary's base name so `/usr/bin/grep` / `./bin/rg` also count.
		return isGrepFamily(filepath.Base(fields[cmdIdx])) // last real stage decides
	}
	return false
}

// envPivotHint teaches the recovery class for environment-capability failures:
// the answer is the repository's own tooling, not another external tool.
const envPivotHint = "\n[environment] That tool is not available in this environment and installing software is blocked. " +
	"Pivot to the repository's own tooling: check CLAUDE.md / AGENTS.md / README, Makefiles, and scripts/ directories " +
	"for the documented workflow instead of retrying or installing external tools."

// shellNotFoundRe matches the shell's own missing-binary report ("sh: docker:
// not found") without catching application-level messages that merely contain
// ": not found" (e.g. "database: not found"), which are real command errors.
var shellNotFoundRe = regexp.MustCompile(`(?i)\b(?:sh|bash|dash|zsh|env|exec)\b[^:\n]*: *[^:\n]+: +not found`)

// isEnvUnavailable reports whether a command failure is an environment-capability
// problem (missing binary, blocked install) rather than a real command error.
func isEnvUnavailable(parts ...string) bool {
	s := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(s, "executable file not found") ||
		strings.Contains(s, "command not found") ||
		strings.Contains(s, "blocked for security reasons") ||
		shellNotFoundRe.MatchString(s)
}

// isGrepFamily reports whether the binary is a search tool for which exit code 1
// means "no matches found" rather than an error.
func isGrepFamily(bin string) bool {
	switch bin {
	case "rg", "ripgrep", "grep", "egrep", "fgrep", "ag":
		return true
	}
	return false
}

// lastPipelineStages splits the command's final shell statement into its pipe
// stages. Statements are separated by ';', newline, '&&' and '||'; single-quoted
// and double-quoted regions are respected so `grep "a|b"` stays one stage. Only
// the final statement matters because bash PIPESTATUS reflects the last pipeline
// executed. The split is intentionally shallow — pipes inside `$(...)` or
// backticks may miscount stages, in which case callers fall back to the plain
// pipeline exit code (stage-count mismatch), never to a wrong verdict.
func lastPipelineStages(cmd string) []string {
	var stages []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	flushStage := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stages = append(stages, s)
		}
		cur.Reset()
	}
	newStatement := func() {
		stages = stages[:0]
		cur.Reset()
	}
	escaped := false
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		// Backslash escapes the next character (outside single quotes, where the
		// shell treats it literally) — so `\"` doesn't toggle quote state and `\|`
		// isn't a pipe.
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			cur.WriteRune(r)
			continue
		}
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			cur.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			cur.WriteRune(r)
		case inSingle || inDouble:
			cur.WriteRune(r)
		case r == ';' || r == '\n':
			newStatement()
		case r == '&' && i+1 < len(runes) && runes[i+1] == '&':
			newStatement()
			i++
		case r == '|' && i+1 < len(runes) && runes[i+1] == '|':
			newStatement()
			i++
		case r == '|':
			flushStage()
		default:
			cur.WriteRune(r)
		}
	}
	flushStage()
	return stages
}

// stageBinary extracts the base name of the binary a pipeline stage invokes,
// skipping leading env-var assignments (`FOO=bar grep ...` → "grep").
func stageBinary(stage string) string {
	fields := strings.Fields(stage)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") {
		i++
	}
	if i >= len(fields) {
		return ""
	}
	return filepath.Base(fields[i])
}

func (t *CLITool) executeCommand(ctx context.Context, command, workingDir string, timeout time.Duration, githubToken string) *CLIOutput {
	result := &CLIOutput{
		Command:    command,
		WorkingDir: workingDir,
	}

	// Validate working directory exists
	if _, err := os.Stat(workingDir); os.IsNotExist(err) {
		result.Error = fmt.Sprintf("Working directory does not exist: %s", workingDir)
		result.ExitCode = 1
		return result
	}

	// Create context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Check if command contains shell features (pipes, redirections, etc.)
	var cmd *exec.Cmd
	wantStageExits := false
	if t.containsShellFeatures(command) {
		// Multi-stage pipelines get bash with PIPESTATUS capture: the pipeline exit
		// code alone is the LAST stage's, which hides earlier failures (a broken
		// `go build | grep -v noise` looks like success). Kill switch:
		// NB_CLI_STAGE_EXITS=false restores plain sh semantics.
		if len(lastPipelineStages(command)) > 1 && os.Getenv("NB_CLI_STAGE_EXITS") != "false" {
			if bashPath, err := exec.LookPath("bash"); err == nil {
				wrapped := command + "\n" +
					`__nb_ps=("${PIPESTATUS[@]}"); printf '\n` + pipeStatusMarker + ` %s\n' "${__nb_ps[*]}"; exit "${__nb_ps[${#__nb_ps[@]}-1]}"`
				cmd = exec.CommandContext(cmdCtx, bashPath, "-c", wrapped)
				wantStageExits = true
			}
		}
		if cmd == nil {
			// Use shell to execute complex commands
			cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
		}
	} else {
		// Parse simple commands into parts
		args := strings.Fields(command)
		if len(args) == 0 {
			result.Error = "Empty command"
			result.ExitCode = 1
			return result
		}
		cmd = exec.CommandContext(cmdCtx, args[0], args[1:]...)
	}

	cmd.Dir = workingDir

	// Build any extra environment the command needs. Both `gh` and raw `git`
	// operations authenticate off the same injected token, but historically only
	// `gh` commands received it: a bare `git clone https://github.com/...` ran
	// unauthenticated and failed with "could not read Username", which the planner
	// then classified as an unrecoverable repo-access error and abstained on. Wire
	// the token into git HTTPS operations too so any clone/fetch the agent issues
	// directly (not just via the repo_clone tool) authenticates the same way.
	var extraEnv []string
	if t.isGitHubCommand(command) && githubToken != "" {
		extraEnv = append(extraEnv, fmt.Sprintf("GITHUB_TOKEN=%s", githubToken))
	}
	if commandInvokesGit(command) {
		extraEnv = append(extraEnv, gitHTTPSAuthEnv(githubToken)...)
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	// Use CombinedOutput for simpler, more reliable output capture
	output, err := cmd.CombinedOutput()

	if wantStageExits {
		var codes []int
		output, codes = extractPipeStatus(output)
		result.StageExitCodes = codes
	}

	// Store the output and provide more detailed error information
	if err != nil {
		result.Stderr = string(output)

		// Add context about the error
		if strings.Contains(string(output), "No such file or directory") {
			result.Error = fmt.Sprintf("File or directory not found. Working directory: %s", workingDir)
		} else if strings.Contains(string(output), "command not found") {
			result.Error = "Command not found. Make sure the command is installed and available in PATH"
		}

		if exitError, ok := err.(*exec.ExitError); ok {
			if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
				result.ExitCode = status.ExitStatus()
			} else {
				result.ExitCode = 1
			}
		} else {
			result.Error = fmt.Sprintf("Command execution failed: %v", err)
			result.ExitCode = 1
		}
	} else {
		result.Stdout = string(output)
		result.ExitCode = 0
	}

	// Normalize file paths in git command outputs
	if t.isGitCommand(command) {
		result.Stdout = t.normalizeGitPaths(result.Stdout, workingDir)
		if result.Stderr != "" {
			result.Stderr = t.normalizeGitPaths(result.Stderr, workingDir)
		}
	}

	// Limit output size to prevent context bloat. Never drop signal silently:
	// the full output is spilled to a file (path surfaced to the model), and the
	// truncated view keeps head AND tail — build/test failures print the error at
	// the end, which head-only truncation used to cut off.
	const stdoutCap, stderrCap = 4 * 1024, 2 * 1024
	if len(result.Stdout) > stdoutCap || len(result.Stderr) > stderrCap {
		result.SpillPath = spillFullOutput(command, result.Stdout, result.Stderr)
	}
	if len(result.Stdout) > stdoutCap {
		// Provide helpful suggestion for large git outputs
		hint := ""
		if strings.HasPrefix(command, "git show") && !strings.Contains(command, "--name-only") && !strings.Contains(command, "--") {
			hint = " - consider using 'git show --name-only " + strings.TrimPrefix(command, "git show ") + "' to see changed files, or 'git show <commit> -- <specific-file>' for targeted diff"
		}
		result.Stdout = headTailTruncate(result.Stdout, 3*1024, 1024, result.SpillPath, hint)
	}
	if len(result.Stderr) > stderrCap {
		result.Stderr = headTailTruncate(result.Stderr, 1024, 1024, result.SpillPath, "")
	}

	return result
}

// pipeStatusMarker prefixes the synthetic line the bash wrapper prints so the
// per-stage PIPESTATUS codes can be recovered from combined output.
const pipeStatusMarker = "__NB_PIPESTATUS__"

// extractPipeStatus strips the wrapper's marker line from combined output and
// parses the per-stage exit codes. Missing marker (command exited early, timeout)
// returns the output unchanged with nil codes — callers fall back to the plain
// pipeline exit code.
func extractPipeStatus(output []byte) ([]byte, []int) {
	s := string(output)
	idx := strings.LastIndex(s, pipeStatusMarker)
	if idx == -1 {
		return output, nil
	}
	line := s[idx:]
	if nl := strings.IndexByte(line, '\n'); nl != -1 {
		line = line[:nl]
	}
	var codes []int
	for _, f := range strings.Fields(strings.TrimPrefix(line, pipeStatusMarker)) {
		var c int
		if _, err := fmt.Sscanf(f, "%d", &c); err != nil {
			return output, nil
		}
		codes = append(codes, c)
	}
	if len(codes) == 0 {
		return output, nil
	}
	// Strip the marker line (and the blank line the wrapper printed before it).
	cleaned := strings.TrimRight(s[:idx], "\n")
	return []byte(cleaned), codes
}

// spillFullOutput writes the complete command output to a pod-local temp file
// (never inside the repo workspace — that would pollute git status and diffs)
// and returns its path, or "" if writing failed.
func spillFullOutput(command, stdout, stderr string) string {
	dir := filepath.Join(os.TempDir(), "nb-tool-output")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	f, err := os.CreateTemp(dir, "cli-*.log")
	if err != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# command: %s\n", command)
	if stdout != "" {
		b.WriteString("# --- stdout ---\n")
		b.WriteString(stdout)
		b.WriteString("\n")
	}
	if stderr != "" {
		b.WriteString("# --- stderr ---\n")
		b.WriteString(stderr)
		b.WriteString("\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return ""
	}
	if err := f.Close(); err != nil {
		return ""
	}
	return f.Name()
}

// headTailTruncate keeps the first head and last tail bytes of s with an honest
// marker in between stating how much was cut and where the full output lives.
func headTailTruncate(s string, head, tail int, spillPath, hint string) string {
	if len(s) <= head+tail {
		return s
	}
	// Snap both cut points to rune boundaries so a multi-byte character is never
	// split (head/tail stay byte budgets; the adjustment is at most 3 bytes).
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tailStart := len(s) - tail
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	spillNote := ""
	if spillPath != "" {
		spillNote = "; full output: " + spillPath
	}
	return s[:head] +
		fmt.Sprintf("\n... [output truncated: %d bytes total, showing first %d and last %d%s%s] ...\n", len(s), head, len(s)-tailStart, spillNote, hint) +
		s[tailStart:]
}

func (t *CLITool) isDangerousCommand(command string) bool {
	commandLower := strings.ToLower(command)

	// Phase 1: Detect bypass patterns that attempt to evade validation.
	if containsBypassPattern(commandLower) {
		return true
	}

	// Get command parts to check the actual command being run
	commandParts := strings.Fields(strings.TrimSpace(command))
	if len(commandParts) == 0 {
		return false
	}
	actualCommand := strings.ToLower(commandParts[0])

	// Strip leading "env" prefix, its flags, and VAR=VAL assignments so that
	// "env -i FOO=bar rm -rf /" still resolves to "rm" for blocklist checks.
	if actualCommand == "env" {
		commandParts = commandParts[1:]
		for len(commandParts) > 0 {
			p := commandParts[0]
			if strings.HasPrefix(p, "-") || strings.Contains(p, "=") {
				commandParts = commandParts[1:]
				continue
			}
			break
		}
		if len(commandParts) == 0 {
			return false
		}
		actualCommand = strings.ToLower(commandParts[0])
	}

	// Block root filesystem searches (waste time scanning entire OS)
	if actualCommand == "find" && len(commandParts) > 1 {
		if _, blocked := blockedSearchPaths[commandParts[1]]; blocked {
			return true
		}
	}

	// Security: Explicitly block recursive remove operations
	if actualCommand == "rm" {
		for _, part := range commandParts[1:] {
			if strings.HasPrefix(part, "--") {
				if part == "--recursive" {
					return true
				}
			} else if strings.HasPrefix(part, "-") {
				if strings.Contains(strings.ToLower(part), "r") {
					return true
				}
			}
		}
	}

	// Blocklist of system-admin commands that have no legitimate use in a
	// code-analysis workspace. Everything else is allowed — the pod sandbox
	// and workspace directory restrictions are the primary security boundary.
	blockedCommands := map[string]struct{}{
		// System administration
		"shutdown": {}, "reboot": {}, "halt": {}, "poweroff": {},
		"init": {}, "telinit": {},
		// User/group management
		"useradd": {}, "userdel": {}, "usermod": {},
		"groupadd": {}, "groupdel": {}, "groupmod": {},
		"passwd": {}, "chpasswd": {},
		// Disk/filesystem
		"mkfs": {}, "fdisk": {}, "parted": {}, "lvm": {},
		"dd": {},
		// Firewall/networking admin
		"iptables": {}, "ip6tables": {}, "nftables": {}, "ufw": {},
		// Service management
		"systemctl": {}, "service": {},
		// Package managers (should use pre-built images, not install at runtime)
		"apt-get": {}, "apt": {}, "dpkg": {},
		"yum": {}, "dnf": {}, "rpm": {},
		"apk": {}, "pacman": {},
		// Privilege escalation
		"sudo": {}, "su": {},
		// Mount operations
		"mount": {}, "umount": {},
	}

	if _, blocked := blockedCommands[actualCommand]; blocked {
		return true
	}

	// Also check base command for dotted variants (e.g. mkfs.ext4 → mkfs)
	if dotIdx := strings.IndexByte(actualCommand, '.'); dotIdx > 0 {
		if _, blocked := blockedCommands[actualCommand[:dotIdx]]; blocked {
			return true
		}
	}

	return false
}

// isRestrictedPROperation blocks PR/MR creation commands when restrictPROperations is set.
// Only the orchestrator should create PRs — it handles template formatting, branding, and metadata.
// Git push, commit, and other operations remain allowed for legitimate agent workflows.
func (t *CLITool) isRestrictedPROperation(command string) (bool, string) {
	if !t.restrictPROperations {
		return false, ""
	}

	cmdLower := strings.ToLower(command)

	const redirectMsg = "PR/MR creation is handled by the orchestrator. " +
		"Use submit_analysis to report your findings — the orchestrator will create the PR " +
		"with proper formatting, template compliance, and repository branding."

	if strings.Contains(cmdLower, "gh pr create") || strings.Contains(cmdLower, "gh mr create") ||
		strings.Contains(cmdLower, "glab mr create") {
		return true, redirectMsg
	}

	return false, ""
}

// containsBypassPattern detects shell tricks that attempt to evade command validation.
func containsBypassPattern(cmdLower string) bool {
	// 1. Piping through shell interpreters (base64 decode | sh, etc.)
	shellInterpreters := []string{"| sh", "| bash", "| zsh", "| dash", "|sh", "|bash", "|zsh", "|dash"}
	for _, s := range shellInterpreters {
		if strings.Contains(cmdLower, s) {
			return true
		}
	}

	// 2. Hex/octal escape sequences in $'...' (e.g. $'\x72\x6d')
	if strings.Contains(cmdLower, "$'\\x") || strings.Contains(cmdLower, "$'\\0") {
		return true
	}

	// 4. eval / source — direct shell code execution
	fields := strings.Fields(cmdLower)
	if len(fields) > 0 {
		firstCmd := fields[0]
		if firstCmd == "eval" || firstCmd == "source" || firstCmd == "." {
			return true
		}
	}

	// 5. Process substitution / command substitution used to hide commands
	// $(...) and `...` in a way that wraps the full command (already checked as shell features,
	// but explicitly block when the whole command is a substitution)
	if strings.HasPrefix(strings.TrimSpace(cmdLower), "$(") || strings.HasPrefix(strings.TrimSpace(cmdLower), "`") {
		return true
	}

	return false
}

func (t *CLITool) containsShellFeatures(command string) bool {
	shellFeatures := []string{"|", ">", "<", ">>", "<<", "&&", "||", ";", "`", "$(", "&", "*", "?", "[", "]", "{", "}", "'", "\""}

	for _, feature := range shellFeatures {
		if strings.Contains(command, feature) {
			return true
		}
	}
	return false
}

func (t *CLITool) isGitHubCommand(command string) bool {
	// Check if command starts with 'gh ' or is just 'gh'
	trimmed := strings.TrimSpace(command)
	return strings.HasPrefix(trimmed, "gh ") || trimmed == "gh"
}

func (t *CLITool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

// SetGitHubToken sets the GitHub token for gh commands
func (t *CLITool) SetGitHubToken(token string) {
	t.githubToken = token
}

// isGitCommand checks if the command is a git command that might output file paths
func (t *CLITool) isGitCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	return strings.HasPrefix(trimmed, "git ")
}

// gitInvocationRe matches a `git` invocation at a command position: the start of
// the command or immediately after a shell separator (`;`, `|`, `&`, `&&`, `||`,
// `(`), followed by whitespace. This catches chained clones like
// `cd repo && git clone …` while ignoring prose or other tokens that merely
// contain the letters "git" (e.g. `gitfoo`, `echo git`, "a note about git").
var gitInvocationRe = regexp.MustCompile(`(?:^|[;|&(])\s*git\s`)

// commandInvokesGit reports whether the command actually runs `git` (as opposed
// to a `gh` command or unrelated text). Used to decide when to inject git HTTPS
// authentication into the command's environment. It is intentionally broader
// than isGitCommand (which is prefix-only and scoped to output path
// normalization) so that chained invocations still authenticate.
func commandInvokesGit(command string) bool {
	return gitInvocationRe.MatchString(strings.TrimSpace(command))
}

// gitHTTPSAuthEnv returns environment entries that let raw `git` commands
// authenticate to GitHub over HTTPS using the injected token, mirroring how the
// repo_clone tool embeds `x-access-token:<token>@github.com` (works for both PATs
// and GitHub App installation tokens). Auth is applied via ephemeral GIT_CONFIG_*
// variables (an insteadOf rewrite) so the token is scoped to this process and is
// never persisted into the cloned repo's config. GIT_TERMINAL_PROMPT=0 is always
// set so a missing or invalid token fails fast instead of blocking on an
// interactive username prompt. github.com only — GitHub Enterprise and GitLab
// clones continue to route through the provider-aware repo_clone tool.
func gitHTTPSAuthEnv(token string) []string {
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if token == "" {
		return env
	}
	return append(env,
		"GIT_CONFIG_COUNT=1",
		fmt.Sprintf("GIT_CONFIG_KEY_0=url.https://x-access-token:%s@github.com/.insteadOf", token),
		"GIT_CONFIG_VALUE_0=https://github.com/",
	)
}

// normalizeGitPaths normalizes file paths in git command output to be relative to repository root
func (t *CLITool) normalizeGitPaths(output string, workingDir string) string {
	// If the working directory contains a temporary clone path, we need to normalize it
	// Look for patterns like "repo_XXXXX/" in the working directory

	// Get the actual repository name from the working directory
	// For example: /tmp/code-analysis/repo_1758009578/notifications-server
	// We want to extract the part after the last occurrence of repo_XXXXX/

	workingDirAbs, err := filepath.Abs(workingDir)
	if err != nil {
		return output // Return unchanged if we can't process
	}

	// Find the repository root by looking for the deepest directory that contains .git
	gitRoot := t.findGitRoot(workingDirAbs)
	if gitRoot == "" {
		return output // Return unchanged if we can't find git root
	}

	// Calculate the relative path from git root to working directory
	relPath, err := filepath.Rel(gitRoot, workingDirAbs)
	if err != nil {
		return output
	}

	// Normalize the output by replacing any absolute paths that start with gitRoot
	normalizedOutput := output

	// Replace absolute paths in git diff output
	if strings.Contains(normalizedOutput, gitRoot) {
		normalizedOutput = strings.ReplaceAll(normalizedOutput, gitRoot+"/", "")
		normalizedOutput = strings.ReplaceAll(normalizedOutput, gitRoot, "")
	}

	// Also handle paths relative to current working directory if needed
	// This is for cases where git outputs paths relative to the current dir
	if relPath != "." && relPath != "" {
		// For git diffs, normalize paths like "a/subdir/file.py" to just "subdir/file.py"
		lines := strings.Split(normalizedOutput, "\n")
		for i, line := range lines {
			// Handle git diff header lines
			if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
				// Extract the path part after a/ or b/
				parts := strings.SplitN(line, "/", 2)
				if len(parts) == 2 {
					path := parts[1]
					// If the path starts with the temp repo prefix, clean it
					if strings.Contains(path, "/") {
						pathParts := strings.Split(path, "/")
						// Look for patterns like repo_XXXXX and remove everything up to that
						for j, part := range pathParts {
							if strings.HasPrefix(part, "repo_") && len(part) > 5 {
								// Found temp repo dir, take everything after it
								if j+1 < len(pathParts) {
									cleanPath := strings.Join(pathParts[j+1:], "/")
									if strings.HasPrefix(line, "---") {
										lines[i] = "--- " + cleanPath
									} else {
										lines[i] = "+++ " + cleanPath
									}
								}
								break
							}

						}
					}
				}
			}
			normalizedOutput = strings.Join(lines, "\n")
		}
	}
	return normalizedOutput
}

// findGitRoot finds the git repository root directory
func (t *CLITool) findGitRoot(startDir string) string {
	dir := startDir
	for {
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			break
		}
		dir = parent
	}
	return ""
}

// buildGitCommand builds a git command from structured parameters
func (t *CLITool) buildGitCommand(operation string, options map[string]string, files []string) (string, error) {
	var args []string
	args = append(args, "git", operation)

	switch operation {
	case "status":
		if porcelain, ok := options["porcelain"]; ok && porcelain == "true" {
			args = append(args, "--porcelain")
		}
		if short, ok := options["short"]; ok && short == "true" {
			args = append(args, "--short")
		}

	case "diff":
		if cached, ok := options["cached"]; ok && cached == "true" {
			args = append(args, "--cached")
		}
		if nameOnly, ok := options["name-only"]; ok && nameOnly == "true" {
			args = append(args, "--name-only")
		}
		if revision, ok := options["revision"]; ok {
			args = append(args, revision)
		}
		args = append(args, files...)

	case "log":
		if oneline, ok := options["oneline"]; ok && oneline == "true" {
			args = append(args, "--oneline")
		}
		if maxCount, ok := options["max-count"]; ok {
			args = append(args, "--max-count", maxCount)
		}
		if since, ok := options["since"]; ok {
			args = append(args, "--since", since)
		}
		if format, ok := options["format"]; ok {
			args = append(args, "--format", format)
		}

	case "add":
		if len(files) == 0 {
			args = append(args, ".")
		} else {
			args = append(args, files...)
		}

	case "commit":
		if message, ok := options["message"]; ok {
			args = append(args, "-m", message)
		}
		if amend, ok := options["amend"]; ok && amend == "true" {
			args = append(args, "--amend")
		}

	case "branch":
		if list, ok := options["list"]; ok && list == "true" {
			args = append(args, "--list")
		}
		if name, ok := options["name"]; ok {
			args = append(args, name)
		}
		if delete, ok := options["delete"]; ok && delete == "true" {
			args = append(args, "-d")
			if name, ok := options["name"]; ok {
				args = append(args, name)
			}
		}

	case "checkout":
		if branch, ok := options["branch"]; ok {
			args = append(args, branch)
		}
		if newBranch, ok := options["new-branch"]; ok {
			args = append(args, "-b", newBranch)
		}
		args = append(args, files...)

	case "reset":
		if hard, ok := options["hard"]; ok && hard == "true" {
			args = append(args, "--hard")
		}
		if soft, ok := options["soft"]; ok && soft == "true" {
			args = append(args, "--soft")
		}
		if revision, ok := options["revision"]; ok {
			args = append(args, revision)
		}
		args = append(args, files...)

	case "show":
		if revision, ok := options["revision"]; ok {
			args = append(args, revision)
		}
		if nameOnly, ok := options["name-only"]; ok && nameOnly == "true" {
			args = append(args, "--name-only")
		}

	case "config":
		if global, ok := options["global"]; ok && global == "true" {
			args = append(args, "--global")
		}
		if key, ok := options["key"]; ok {
			args = append(args, key)
			if value, ok := options["value"]; ok {
				args = append(args, value)
			}
		}

	default:
		return "", fmt.Errorf("unsupported git operation: %s", operation)
	}

	return strings.Join(args, " "), nil
}
