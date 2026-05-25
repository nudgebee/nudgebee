package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"nudgebee/code-analysis-agent/tools/core"
)

type GLabTool struct {
	workspaceDir         string
	gitlabToken          string
	restrictPROperations bool
}

func NewGLabTool(workspaceDir string) *GLabTool {
	return &GLabTool{workspaceDir: workspaceDir, gitlabToken: ""}
}

// NewGLabToolWithToken creates a GLabTool with a GitLab token for authenticated API calls
func NewGLabToolWithToken(workspaceDir string, gitlabToken string) *GLabTool {
	return &GLabTool{workspaceDir: workspaceDir, gitlabToken: gitlabToken}
}

// SetGitLabToken sets the GitLab token for authenticated API calls
func (t *GLabTool) SetGitLabToken(token string) {
	t.gitlabToken = token
}

// SetRestrictPROperations blocks MR creation commands.
// Used for agent-facing instances so only the orchestrator creates MRs with proper formatting.
func (t *GLabTool) SetRestrictPROperations(restrict bool) {
	t.restrictPROperations = restrict
}

func (t *GLabTool) Name() string {
	return "glab"
}

func (t *GLabTool) Description() string {
	return `Executes GitLab CLI (glab) commands. Usage: glab <command> <subcommand> [flags]

CORE COMMANDS:
  mr:           Manage merge requests
  issue:        Manage issues
  repo:         Manage repositories
  ci:           Work with GitLab CI/CD pipelines
  release:      Manage releases

COMMON EXAMPLES:
  List MRs:         ["mr", "list"]
  List MRs (limit): ["mr", "list", "-P", "5"]
  List all MRs:     ["mr", "list", "--state", "all", "-P", "10"]
  View MR:          ["mr", "view", "123"]
  Create MR:        ["mr", "create", "--title", "My MR", "--description", "Description"]
  Create MR (draft): ["mr", "create", "--draft", "--title", "WIP: My MR"]

  List issues:      ["issue", "list"]
  View issue:       ["issue", "view", "456"]

  View repo:        ["repo", "view"]
  List releases:    ["release", "list"]

  CI status:        ["ci", "status"]
  CI view:          ["ci", "view"]

IMPORTANT:
- Each argument/flag/value must be a separate array element
- Example: ["mr", "list", "-P", "10"] NOT ["mr list -P 10"]
- For flags with values: ["-P", "5"] NOT ["-P=5"]
- Use --description/-d for MR body (NOT --body like gh)
- Use -P for pagination limit (NOT --limit like gh)
- GitLab uses "merge request" (MR) terminology, not "pull request" (PR)
- Use --source-branch to specify the source branch for MR creation`
}

func (t *GLabTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Parameters for executing GitLab CLI commands",
		map[string]any{
			"args": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Command arguments as separate strings. Example: ['mr', 'create', '--title', 'Fix bug', '--description', 'Description'] executes 'glab mr create --title \"Fix bug\" --description \"Description\"'",
			},
		},
		[]string{"args"},
	)
}

func (t *GLabTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
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
			"no arguments provided to glab tool",
			"Missing required parameters",
		)
	}

	// Use repository helper for working directory injection and discovery
	repoHelper := NewRepositoryHelper()
	repoDir := repoHelper.GetWorkingDirectoryWithInjection(input, t.workspaceDir)

	if len(args) >= 2 && args[0] == "mr" && args[1] == "create" {
		// Block MR creation for agent-facing instances — orchestrator handles this
		if t.restrictPROperations {
			msg := "MR creation is handled by the orchestrator. Use submit_analysis to report your findings — " +
				"the orchestrator will create the MR with proper formatting, template compliance, and repository branding."
			return core.CreateSuccessResponse(msg, msg, map[string]any{"blocked": true, "reason": msg})
		}

		// Validate MR creation to prevent common mistakes
		if err := t.validateMRCreation(repoDir, args); err != nil {
			return core.CreateErrorResponse(
				fmt.Sprintf("MR creation validation failed: %v", err),
				"Invalid MR creation attempt",
			)
		}
	}

	cmd := exec.Command("glab", args...)
	cmd.Dir = repoDir

	// Set GITLAB_TOKEN environment variable for authenticated API calls
	if t.gitlabToken != "" {
		env := os.Environ()
		env = append(env, fmt.Sprintf("GITLAB_TOKEN=%s", t.gitlabToken))
		cmd.Env = env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("glab command failed: %v\nStderr: %s", err, stderr.String()),
			"GitLab CLI command execution failed",
		)
	}

	result := map[string]any{
		"stdout":  stdout.String(),
		"stderr":  stderr.String(),
		"command": fmt.Sprintf("glab %v", args),
	}

	observation := fmt.Sprintf("Successfully executed: glab %v", args)
	if stdout.Len() > 0 {
		observation += fmt.Sprintf("\nOutput: %s", stdout.String())
	}

	return core.CreateSuccessResponse(
		"GitLab CLI command executed successfully",
		observation,
		result,
	)
}

func (t *GLabTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}

// validateMRCreation checks if MR creation is valid (not trying to create MR from main to main)
func (t *GLabTool) validateMRCreation(repoDir string, args []string) error {
	// Get current branch
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoDir

	output, err := cmd.Output()
	if err != nil {
		// If we can't determine branch, allow the operation (glab will handle the error)
		return nil
	}

	currentBranch := string(bytes.TrimSpace(output))

	// Check if --source-branch is explicitly provided in args
	hasSourceBranch := false
	for i, arg := range args {
		if arg == "--source-branch" && i+1 < len(args) {
			hasSourceBranch = true
			break
		}
	}

	// If current branch is main/master and no --source-branch is specified, return helpful error
	if (currentBranch == "main" || currentBranch == "master") && !hasSourceBranch {
		return fmt.Errorf("cannot create MR from '%s' branch. Please:\n"+
			"1. Create a feature branch: git checkout -b feature/your-branch-name\n"+
			"2. Make and commit your changes\n"+
			"3. Then create the MR\n"+
			"Or use --source-branch flag to specify a different branch", currentBranch)
	}

	return nil
}
