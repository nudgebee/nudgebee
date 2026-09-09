package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

type GitTool struct {
	workspaceDir string
}

func NewGitTool(workspaceDir string) *GitTool {
	return &GitTool{workspaceDir: workspaceDir}
}

func (t *GitTool) Name() string {
	return "git"
}

func (t *GitTool) Description() string {
	return `Executes git commands for repository operations. Usage: git <command> [options]

This tool also commits and pushes your work — it is not read-only. Git identity
(nudgebee-bot <bot@nudgebee.com>) is already configured for this repo, so commit
commands work without any setup.

COMMON COMMANDS:
  status:       Show working tree status
  log:          Show commit history
  diff:         Show changes between commits, working tree, etc.
  show:         Show commit details
  branch:       List, create, or delete branches
  remote:       Manage remote repositories
  add:          Stage changes for commit
  commit:       Record staged changes
  push:         Upload commits to the remote
  checkout:     Discard uncommitted changes (with -- <path>) or switch refs

COMMON EXAMPLES:
  Status:                ["status"]
  Short status:          ["status", "--short"]
  Recent commits:        ["log", "--oneline", "-n", "10"]
  Commit with hash:      ["show", "abc123"]
  View diff:             ["diff"]
  Diff specific file:    ["diff", "HEAD", "--", "path/to/file"]
  Branch list:           ["branch", "-a"]
  Remote info:           ["remote", "-v"]
  Stage everything:      ["add", "-A"]
  Commit:                ["commit", "-m", "fix: description"]
  Push:                  ["push"]
  Discard a file's edits: ["checkout", "--", "path/to/file"]
  Discard all edits:     ["checkout", "--", "."]

ADVANCED EXAMPLES:
  Log with graph:        ["log", "--oneline", "--graph", "--all", "-n", "20"]
  Diff between commits:  ["diff", "commit1", "commit2"]
  File history:          ["log", "--follow", "--", "path/to/file"]
  Blame (line authors):  ["blame", "path/to/file"]
  Amend last commit:     ["commit", "--amend", "--no-edit"]
  Force-push after rewriting history: ["push", "--force-with-lease"]

IMPORTANT:
- Each argument must be a separate array element
- Example: ["log", "-n", "5"] NOT ["log -n 5"]
- Use "--" before file paths: ["diff", "HEAD", "--", "file.py"]
- For options with values: ["log", "-n", "10"] NOT ["log", "-n=10"]
- Never use ["push", "--force"] — use ["push", "--force-with-lease"] instead.`
}

func (t *GitTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Parameters for executing Git commands",
		map[string]any{
			"args": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Command arguments as separate strings. Example: ['log', '--oneline', '-n', '5'] executes 'git log --oneline -n 5'",
			},
		},
		[]string{"args"},
	)
}

func (t *GitTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var args []string
	if argsInput, ok := input["args"].([]any); ok {
		for _, arg := range argsInput {
			if argStr, ok := arg.(string); ok {
				args = append(args, argStr)
			}
		}
	} else if argStr, ok := input["args"].(string); ok && argStr != "" {
		// Fallback: LLM sometimes sends args as a single string instead of array
		args = strings.Fields(argStr)
	} else {
		return core.CreateErrorResponse(
			"invalid input: 'args' must be an array of strings or a single string",
			"Input parameter validation failed",
		)
	}

	if len(args) == 0 {
		return core.CreateErrorResponse(
			"no arguments provided to git tool",
			"Missing required parameters",
		)
	}

	// Use working directory from orchestrator, fallback to tool workspace
	repoDir := t.workspaceDir
	if workingDir, ok := input["working_directory"].(string); ok && workingDir != "" {
		repoDir = workingDir
	}

	args, fetchRetargeted := rewriteFetchForTracking(args)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("git command failed: %v\nStderr: %s", err, stderr.String()),
			"Git command execution failed",
		)
	}

	result := map[string]any{
		"stdout":  stdout.String(),
		"stderr":  stderr.String(),
		"command": fmt.Sprintf("git %v", args),
	}

	observation := fmt.Sprintf("Successfully executed: git %v", args)
	if fetchRetargeted {
		observation += "\n(note: fetch was retargeted to create refs/remotes/origin/<branch> so `git merge origin/<branch>` resolves in this single-branch workspace)"
	}
	if stdout.Len() > 0 {
		observation += fmt.Sprintf("\nOutput: %s", stdout.String())
	}

	return core.CreateSuccessResponse(
		"Git command executed successfully",
		observation,
		result,
	)
}

// rewriteFetchForTracking makes a plain `git fetch origin <branch>` populate
// refs/remotes/origin/<branch> instead of only FETCH_HEAD.
//
// The analysis workspace is a single-branch bare clone (branch noise is kept
// deliberately low so the LLM isn't derailed by unrelated remote branches), so
// `git fetch origin <branch>` does not create origin/<branch> and a follow-up
// `git merge origin/<branch>` — the natural way to reproduce a PR merge
// conflict — fails with "not something we can merge". Rewriting only the exact
// `fetch origin <branch>` shape into an explicit refspec creates the tracking
// ref for that one branch (the same technique as the followup agent's
// ensureBaseRefFetched), without widening the clone to every remote branch.
//
// Only the precise three-token form is touched; anything with extra args,
// flags, an existing refspec, or an unsafe branch name is passed through
// unchanged. Returns the possibly-rewritten args and whether a rewrite occurred.
func rewriteFetchForTracking(args []string) ([]string, bool) {
	if len(args) != 3 || args[0] != "fetch" || args[1] != "origin" {
		return args, false
	}
	branch := args[2]
	if strings.Contains(branch, ":") || strings.HasPrefix(branch, "-") ||
		strings.ContainsAny(branch, " \t?*[\\^~") || strings.Contains(branch, "..") {
		return args, false
	}
	return []string{"fetch", "origin", branch + ":refs/remotes/origin/" + branch}, true
}

func (t *GitTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

// IsReadOnly is false even though most documented commands (status, log, diff,
// show, branch, remote) are read-only: Execute does not restrict which git
// subcommand runs, so this tool can also run commit/add/push/reset/etc. This
// flag is a static per-tool property with no visibility into a specific
// call's args (see react_planner.go's executeSteps and its run-scoped read
// cache), so claiming true here would let a write command — e.g. commit, add,
// push issued through this tool — be batched concurrently with other "read
// only" steps (a race on .git/index.lock and remote state) or served from a
// stale cached result instead of actually re-running on retry. Both were
// observed causing the PR-followup commit-enforcement pass to silently leave
// real edits uncommitted (issue #36634).
func (t *GitTool) IsReadOnly() bool { return false }
