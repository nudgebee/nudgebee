package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"nudgebee/code-analysis-agent/tools/core"
)

type GHTool struct {
	workspaceDir         string
	githubToken          string
	restrictPROperations bool
}

func NewGHTool(workspaceDir string) *GHTool {
	return &GHTool{workspaceDir: workspaceDir, githubToken: ""}
}

// NewGHToolWithToken creates a GHTool with a GitHub token for authenticated API calls
func NewGHToolWithToken(workspaceDir string, githubToken string) *GHTool {
	return &GHTool{workspaceDir: workspaceDir, githubToken: githubToken}
}

// SetGitHubToken sets the GitHub token for authenticated API calls
func (t *GHTool) SetGitHubToken(token string) {
	t.githubToken = token
}

// SetRestrictPROperations blocks PR creation commands.
// Used for agent-facing instances so only the orchestrator creates PRs with proper formatting.
func (t *GHTool) SetRestrictPROperations(restrict bool) {
	t.restrictPROperations = restrict
}

func (t *GHTool) Name() string {
	return "gh"
}

func (t *GHTool) Description() string {
	return `Executes GitHub CLI (gh) commands. Usage: gh <command> <subcommand> [flags]

CORE COMMANDS:
  pr:           Manage pull requests
  issue:        Manage issues
  repo:         Manage repositories
  search:       Search for repositories, issues, and pull requests
  run:          View workflow runs
  release:      Manage releases

COMMON EXAMPLES:
  List PRs:        ["pr", "list"]
  List PRs (limit): ["pr", "list", "--limit", "5"]
  List all PRs:    ["pr", "list", "--state", "all", "--limit", "10"]
  View PR:         ["pr", "view", "123"]
  Create PR:       ["pr", "create", "--title", "My PR", "--body", "Description"]

  List issues:     ["issue", "list"]
  View issue:      ["issue", "view", "456"]

  Search repos:    ["search", "repos", "nudgebee"]
  Search PRs:      ["search", "prs", "bug", "--repo", "owner/repo"]

  View repo:       ["repo", "view"]
  List releases:   ["release", "list"]

IMPORTANT:
- Each argument/flag/value must be a separate array element
- Example: ["pr", "list", "--limit", "10"] NOT ["pr list --limit 10"]
- For flags with values: ["--limit", "5"] NOT ["--limit=5"]
- gh pr list does NOT support --sort flag (GitHub limitation)
- Use --state for filtering: "open" | "closed" | "merged" | "all"
- By default, gh pr list shows recent PRs (no sorting needed)`
}

func (t *GHTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Parameters for executing GitHub CLI commands",
		map[string]any{
			"args": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Command arguments as separate strings. Example: ['pr', 'list', '--limit', '5', '--state', 'all'] executes 'gh pr list --limit 5 --state all'",
			},
		},
		[]string{"args"},
	)
}

func (t *GHTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var args []string
	if argsInput, ok := input["args"].([]any); ok {
		for _, arg := range argsInput {
			if argStr, ok := arg.(string); ok {
				args = append(args, argStr)
			}
		}
	} else {
		return core.CreateErrorResponse(
			"invalid input: 'args' is not a list of strings",
			"Input parameter validation failed",
		)
	}

	if len(args) == 0 {
		return core.CreateErrorResponse(
			"no arguments provided to gh tool",
			"Missing required parameters",
		)
	}

	// Block PR creation for agent-facing instances — orchestrator handles PR creation
	if t.restrictPROperations && len(args) >= 2 && args[0] == "pr" && args[1] == "create" {
		msg := "PR creation is handled by the orchestrator. Use submit_analysis to report your findings — " +
			"the orchestrator will create the PR with proper formatting, template compliance, and repository branding."
		return core.CreateSuccessResponse(msg, msg, map[string]any{"blocked": true, "reason": msg})
	}

	// Use repository helper for working directory injection and discovery
	repoHelper := NewRepositoryHelper()
	repoDir := repoHelper.GetWorkingDirectoryWithInjection(input, t.workspaceDir)

	cmd := exec.Command("gh", args...)
	cmd.Dir = repoDir

	// Set GITHUB_TOKEN environment variable for authenticated API calls
	if t.githubToken != "" {
		env := os.Environ()
		env = append(env, fmt.Sprintf("GITHUB_TOKEN=%s", t.githubToken))
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("gh command failed: %v\nStderr: %s", err, stderr.String()),
			"GitHub CLI command execution failed",
		)
	}

	result := map[string]any{
		"stdout":  stdout.String(),
		"stderr":  stderr.String(),
		"command": fmt.Sprintf("gh %v", args),
	}

	observation := fmt.Sprintf("Successfully executed: gh %v", args)
	if stdout.Len() > 0 {
		observation += fmt.Sprintf("\nOutput: %s", stdout.String())
	}

	return core.CreateSuccessResponse(
		"GitHub CLI command executed successfully",
		observation,
		result,
	)
}

func (t *GHTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}
