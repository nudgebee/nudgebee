package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	// defaultBranch is the branch the clone should check out when the caller (the
	// LLM) omits an explicit `branch`. It is seeded by the agent from the request's
	// target/base branch (RepoContext.Branch) so the working tree — and therefore
	// any fix branch cut from it — starts on the same branch the PR targets. Without
	// this the clone falls back to the remote default branch (e.g. main), and a PR
	// opened against `test` would diff the entire main↔test delta.
	defaultBranch string
}

type RepoCloneInput struct {
	RepoURL     string              `json:"repo_url"`
	LocalPath   string              `json:"local_path,omitempty"` // Path to existing local repository
	Credentials *models.Credentials `json:"credentials,omitempty"`
	Branch      string              `json:"branch,omitempty"`
	// BaseBranch names the branch this work would merge into. It is fetched alongside
	// Branch so `git merge origin/<base>` can reproduce a PR merge conflict — the bare
	// clone is otherwise narrowed to the requested branch only.
	BaseBranch   string `json:"base_branch,omitempty"`
	Shallow      bool   `json:"shallow,omitempty"`
	WorkspaceDir string `json:"workspace_dir,omitempty"` // Shared workspace directory from planner
}

func NewRepoCloneTool(workspaceDir string, gitClient *git.GitClient) *RepoCloneTool {
	return &RepoCloneTool{
		workspaceDir:  workspaceDir,
		gitClient:     gitClient,
		cloneAttempts: make(map[string]int),
	}
}

// SetDefaultBranch seeds the branch used when an invocation omits `branch`.
// Callers should pass the request's target/base branch (and skip SHA-shaped
// values, which are not valid checkout targets here).
func (t *RepoCloneTool) SetDefaultBranch(branch string) {
	t.defaultBranch = branch
}

// DefaultBranch returns the branch used when an invocation omits `branch`.
func (t *RepoCloneTool) DefaultBranch() string {
	return t.defaultBranch
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
						"description": "Type of credentials (token, ssh_key, basic, env_ref, none)",
						"enum":        []string{"token", "ssh_key", "basic", "env_ref", "none"},
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
			"base_branch": map[string]any{
				"type":        "string",
				"description": "Branch this work merges into (optional). Fetch it too when you need to diff or merge against the base, e.g. to reproduce a PR merge conflict.",
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

	// When the caller omits an explicit branch, fall back to the request's
	// target/base branch so the clone checks out the same branch the PR will
	// target — not the remote default branch.
	if params.Branch == "" && t.defaultBranch != "" {
		params.Branch = t.defaultBranch
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
	cloneResult, err := t.gitClient.CloneOrReuseRepository(ctx, params.RepoURL, internalCreds, params.Branch, repoDir, params.BaseBranch)
	if err != nil {
		// Sanitize the raw git error before surfacing it in either Error or
		// Observation — git usually redacts credentials in URLs but not
		// always (older versions, custom auth handlers, unusual URL shapes).
		// Belt-and-suspenders: strip embedded creds from `https://TOKEN@host/...`
		// / `https://user:pass@host/...` before this string lands in the
		// LLM prompt or the analytics column. See sanitizeGitError.
		safeErr := sanitizeGitError(err.Error())
		errMsg := "Failed to clone repository: " + safeErr
		// Surface the underlying (sanitized) git error in Observation so
		// downstream analytics + humans reading tool traces see WHY it failed —
		// previously Observation was the generic "Repository cloning failed"
		// with no diagnostic. tracked_wrapper.go persists Observation to
		// llm_conversation_tool_calls.response, so all monitoring on this
		// tool was flying blind (2026-07-02 sweep: 20 of 20 repo_clone errors
		// stored the same generic string with zero signal).
		observation := "Repository cloning failed: " + safeErr
		if hint := repoCloneErrorHint(safeErr); hint != "" {
			observation = observation + "\n\nHint: " + hint
		}
		return core.CreateErrorResponse(errMsg, observation)
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

// urlCredsRegex matches embedded credentials in a git URL — the
// `TOKEN@` or `user:pass@` prefix before a host. Non-greedy on the user
// portion (`[^@/\s]+`) so trailing paths and command-line surroundings
// don't get pulled into the match. Only http/https URLs — git URLs of the
// form `git@github.com:foo/bar.git` use `git@` as a fixed username with no
// secret material, so they are intentionally not touched.
var urlCredsRegex = regexp.MustCompile(`(https?://)[^@/\s]+@`)

// sanitizeGitError strips embedded credentials from URLs in a git error
// string before it is persisted to the LLM prompt / analytics column.
// Idempotent, safe on inputs that contain no URLs at all.
func sanitizeGitError(errStr string) string {
	return urlCredsRegex.ReplaceAllString(errStr, "${1}<redacted>@")
}

// repoCloneErrorHint returns an actionable recovery hint for common git clone
// failure classes. Prior to this, every clone failure surfaced as the generic
// "Repository cloning failed" — the git error was captured in the Error field
// but never made it into the LLM-visible observation or the analytics column,
// so the model had no signal to react to and every failure looked identical.
// Returns "" for unrecognized errors so the raw git output continues to
// dominate. Mirrors the tool-hint contract established in llm-server PR #33404.
func repoCloneErrorHint(rawError string) string {
	lower := strings.ToLower(rawError)
	switch {
	case strings.Contains(lower, "authentication failed"),
		strings.Contains(lower, "invalid username or password"),
		strings.Contains(lower, "could not read username"):
		return "Authentication to the remote failed. The provided credentials are invalid, missing, or lack access to this repository. Report the specific repo URL back to the user so they can supply a valid token — do NOT retry with the same credentials."
	case strings.Contains(lower, "repository not found"),
		strings.Contains(lower, "not found") && strings.Contains(lower, "404"):
		return "The remote returned 'repository not found'. Either the URL is wrong (check spelling of owner/repo), the repo is private and the token lacks access, or the repo was deleted/renamed. Report the exact URL back to the user before retrying with a different URL."
	case strings.Contains(lower, "permission denied") && strings.Contains(lower, "publickey"):
		return "SSH key authentication failed. The clone URL is SSH form (git@...) but no SSH key is configured or the key lacks access. If the caller provided an HTTPS token, retry with the HTTPS URL (https://github.com/owner/repo.git) instead of the SSH form."
	case strings.Contains(lower, "unable to resolve host"),
		strings.Contains(lower, "could not resolve host"),
		strings.Contains(lower, "connection timed out"),
		strings.Contains(lower, "connection refused"):
		return "Network error reaching the git remote (DNS or connectivity). Not something you can fix from this tool — report the specific host to the user and stop retrying."
	case strings.Contains(lower, "couldn't find remote ref"),
		strings.Contains(lower, "remote branch") && strings.Contains(lower, "not found"),
		strings.Contains(lower, "reference is not a tree"):
		return "The specified branch does not exist on the remote. Retry with `branch: \"main\"` (or the repo's actual default branch) — or omit the branch parameter to clone the default."
	case strings.Contains(lower, "disk full"),
		strings.Contains(lower, "no space left on device"):
		return "Workspace is out of disk. Not recoverable from this tool — report to the user."
	}
	return ""
}
