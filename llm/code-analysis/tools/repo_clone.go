package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"nudgebee/code-analysis-agent/internal/credentials"
	"nudgebee/code-analysis-agent/internal/git"
	"nudgebee/code-analysis-agent/models"
	"nudgebee/code-analysis-agent/tools/core"
)

// maxCloneAttempts is the maximum number of clone attempts per URL before giving up.
const maxCloneAttempts = 2

type RepoCloneTool struct {
	workspaceDir  string
	gitClient     *git.GitClient
	cloneAttempts map[string]int // URL -> attempt count, prevents infinite clone retries
}

type RepoCloneInput struct {
	RepoURL      string              `json:"repo_url"`
	LocalPath    string              `json:"local_path,omitempty"` // Path to existing local repository
	Credentials  *models.Credentials `json:"credentials,omitempty"`
	Branch       string              `json:"branch,omitempty"`
	Shallow      bool                `json:"shallow,omitempty"`
	WorkspaceDir string              `json:"workspace_dir,omitempty"` // Shared workspace directory from planner
}

func NewRepoCloneTool(workspaceDir string, gitClient *git.GitClient) *RepoCloneTool {
	return &RepoCloneTool{
		workspaceDir:  workspaceDir,
		gitClient:     gitClient,
		cloneAttempts: make(map[string]int),
	}
}

func (t *RepoCloneTool) Name() string {
	return "repo_clone"
}

func (t *RepoCloneTool) Description() string {
	return "Clone a Git repository to the workspace for analysis. REQUIRES repo_url parameter. Use repo_url to specify the Git repository URL (e.g. https://github.com/user/repo.git)."
}

func (t *RepoCloneTool) InputSchema() core.ToolSchema {
	return core.CreateToolSchema(
		"object",
		"Parameters for cloning a repository",
		map[string]any{
			"repo_url": map[string]any{
				"type":        "string",
				"description": "The URL of the Git repository to clone",
			},
			"credentials": map[string]any{
				"type":        "object",
				"description": "Authentication credentials for the repository",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"description": "Type of credentials (token, ssh_key, basic, encrypted, env_ref)",
						"enum":        []string{"token", "ssh_key", "basic", "encrypted", "env_ref"},
					},
					"value": map[string]any{
						"type":        "string",
						"description": "The credential value",
					},
					"username": map[string]any{
						"type":        "string",
						"description": "Username for basic auth",
					},
					"password": map[string]any{
						"type":        "string",
						"description": "Password for basic auth",
					},
				},
			},
			"branch": map[string]any{
				"type":        "string",
				"description": "Specific branch to clone (optional, defaults to default branch)",
			},
			"shallow": map[string]any{
				"type":        "boolean",
				"description": "Whether to perform a shallow clone (faster, less history)",
				"default":     false,
			},
			"workspace_dir": map[string]any{
				"type":        "string",
				"description": "Shared workspace directory (automatically provided by planner)",
			},
		},
		[]string{"repo_url"},
	)
}

func (t *RepoCloneTool) Execute(ctx context.Context, input map[string]any) core.NBToolResponse {
	var params RepoCloneInput
	if err := core.ParseInput(input, &params); err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Invalid input parameters: %v", err),
			"Failed to parse tool input parameters",
		)
	}

	// Handle local repository path
	if params.LocalPath != "" {
		return t.handleLocalRepository(params)
	}

	if params.RepoURL == "" {
		return core.CreateErrorResponse(
			"repo_url or local_path is required",
			"Missing required parameter",
		)
	}

	// Retry limit: prevent LLM from calling repo_clone repeatedly with the same failing URL
	attempts := t.cloneAttempts[params.RepoURL]
	if attempts >= maxCloneAttempts {
		return core.CreateErrorResponse(
			fmt.Sprintf("Repository clone already failed %d times for URL: %s. Do not retry — proceed with analysis using available information or call submit_analysis.", attempts, params.RepoURL),
			"Clone retry limit exceeded",
		)
	}
	t.cloneAttempts[params.RepoURL] = attempts + 1

	// Determine workspace directory - use shared workspace if provided, otherwise fall back to default
	workspaceDir := t.workspaceDir
	if params.WorkspaceDir != "" {
		workspaceDir = params.WorkspaceDir
	}

	// Create a simple directory for this repository (no timestamp since workspace is temporary)
	repoName := filepath.Base(params.RepoURL)
	if repoName == "." || repoName == "/" {
		repoName = "repository"
	}
	// Remove .git suffix if present
	if filepath.Ext(repoName) == ".git" {
		repoName = repoName[:len(repoName)-4]
	}

	repoDir := filepath.Join(workspaceDir, repoName)

	// Ensure workspace directory exists
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Failed to create workspace directory: %v", err),
			"Workspace preparation failed",
		)
	}

	// Convert models.Credentials to internal credentials format
	var internalCreds *credentials.ResolvedCredentials
	if params.Credentials != nil {
		internalCreds = &credentials.ResolvedCredentials{
			Type:     params.Credentials.Type,
			Token:    params.Credentials.Value,
			Username: params.Credentials.Username,
			Password: params.Credentials.Password,
		}
	}

	// Clone or reuse repository via worktree for performance
	cloneResult, err := t.gitClient.CloneOrReuseRepository(ctx, params.RepoURL, internalCreds, params.Branch, repoDir)
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Failed to clone repository: %v", err),
			"Repository cloning failed",
		)
	}

	// Get repository information
	info, err := t.gitClient.GetRepositoryInfo(cloneResult.LocalPath)
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Failed to get repository info: %v", err),
			"Repository info retrieval failed",
		)
	}

	response := map[string]any{
		"local_path":     cloneResult.LocalPath,
		"repo_url":       params.RepoURL,
		"branch":         cloneResult.Branch,
		"commit_hash":    cloneResult.CommitHash,
		"commit_message": cloneResult.CommitMessage,
		"repository_info": map[string]any{
			"name":           info.Name,
			"description":    info.Description,
			"default_branch": info.DefaultBranch,
			"last_commit":    info.LastCommit,
			"size":           info.Size,
			"file_count":     info.FileCount,
			"language":       info.Language,
		},
	}

	observation := fmt.Sprintf("Successfully cloned repository '%s' to '%s'. Repository has %d files and default branch '%s'.",
		params.RepoURL, cloneResult.LocalPath, info.FileCount, info.DefaultBranch)

	// Append repository index if available
	if idx, err := IndexRepository(cloneResult.LocalPath); err == nil {
		observation += "\n\n" + idx.FormatAsContext()
	}

	return core.CreateSuccessResponse(
		fmt.Sprintf("Repository cloned successfully to: %s", cloneResult.LocalPath),
		observation,
		response,
	)
}

// handleLocalRepository processes a request to use an existing local repository
func (t *RepoCloneTool) handleLocalRepository(params RepoCloneInput) core.NBToolResponse {
	// Verify the local path exists and is a directory
	if _, err := os.Stat(params.LocalPath); os.IsNotExist(err) {
		return core.CreateErrorResponse(
			fmt.Sprintf("Local repository path does not exist: %s", params.LocalPath),
			"Local repository not found",
		)
	}

	// Verify it's a git repository
	gitDir := filepath.Join(params.LocalPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return core.CreateErrorResponse(
			fmt.Sprintf("Path is not a git repository: %s", params.LocalPath),
			"Invalid git repository",
		)
	}

	// Get repository information
	info, err := t.gitClient.GetRepositoryInfo(params.LocalPath)
	if err != nil {
		return core.CreateErrorResponse(
			fmt.Sprintf("Failed to get repository info: %v", err),
			"Repository info retrieval failed",
		)
	}

	response := map[string]any{
		"local_path":     params.LocalPath,
		"repo_url":       params.RepoURL,     // May be empty for local-only repos
		"branch":         info.DefaultBranch, // Use default branch from repository info
		"commit_hash":    "local-repository",
		"commit_message": "Using existing local repository",
		"repository_info": map[string]any{
			"name":           info.Name,
			"default_branch": info.DefaultBranch,
			"file_count":     info.FileCount,
			"language":       info.Language,
		},
	}

	observation := fmt.Sprintf("Using existing local repository at '%s'. Repository has %d files and default branch '%s'.",
		params.LocalPath, info.FileCount, info.DefaultBranch)

	// Append repository index if available
	if idx, err := IndexRepository(params.LocalPath); err == nil {
		observation += "\n\n" + idx.FormatAsContext()
	}

	return core.CreateSuccessResponse(
		fmt.Sprintf("Using local repository at: %s", params.LocalPath),
		observation,
		response,
	)
}

func (t *RepoCloneTool) GetType() core.NBToolType {
	return core.NBToolTypeCodeAnalysis
}
