package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/credentials"

	gitlib "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type GitClient struct {
	workspaceDir string
	timeout      time.Duration
	maxRepoSize  int64
	logger       *common.Logger
}

func NewGitClient(workspaceDir string, timeout time.Duration, maxRepoSize int64) *GitClient {
	logger := common.NewLogger("git_client", "git", "system", nil)
	return &GitClient{
		workspaceDir: workspaceDir,
		timeout:      timeout,
		maxRepoSize:  maxRepoSize,
		logger:       logger,
	}
}

func (gc *GitClient) SetLogger(logger *common.Logger) {
	gc.logger = logger
}

type BlameResult struct {
	CommitHash  string
	Author      string
	AuthorEmail string
	Date        time.Time
	Message     string
	LineContent string
}

type CloneResult struct {
	LocalPath     string
	Branch        string
	CommitHash    string
	CommitMessage string
}

type RepositoryInfo struct {
	Name          string
	Description   string
	DefaultBranch string
	LastCommit    string
	Size          int64
	FileCount     int
	Language      string
}

type BlameInfo struct {
	StartLine int
	EndLine   int
	Entries   []BlameEntry
}

type BlameEntry struct {
	Line          int
	Content       string
	CommitHash    string
	Author        string
	AuthorEmail   string
	CommitDate    string
	CommitMessage string
}

func (gc *GitClient) CloneRepository(ctx context.Context, repoURL string, creds *credentials.ResolvedCredentials) (string, error) {
	gc.logger.Log(common.EventStepStart, "Starting repository clone", map[string]any{
		"repository_url": repoURL,
		"workspace_dir":  gc.workspaceDir,
	})

	// Create workspace directory if it doesn't exist
	if err := os.MkdirAll(gc.workspaceDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create workspace directory: %w", err)
	}

	// Create unique directory for this repository
	repoDir := filepath.Join(gc.workspaceDir, fmt.Sprintf("repo_%d", time.Now().Unix()))
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create repo directory: %w", err)
	}

	gc.logger.Log(common.EventStepStart, "Created repository directory", map[string]any{
		"repo_dir": repoDir,
	})

	// Setup authentication
	auth, err := gc.setupAuth(creds, repoURL)
	if err != nil {
		gc.logger.Log(common.EventStepFailure, "Failed to setup authentication", map[string]any{
			"error": err.Error(),
		})
		return "", fmt.Errorf("failed to setup authentication: %w", err)
	}

	gc.logger.Log(common.EventStepComplete, "Authentication setup completed", nil)

	// Clone with timeout
	cloneCtx, cancel := context.WithTimeout(ctx, gc.timeout)
	defer cancel()

	cloneOptions := gitlib.CloneOptions{
		URL:  repoURL,
		Auth: auth,
	}

	gc.logger.Log(common.EventStepStart, "Starting git clone operation", map[string]any{
		"repository_url": repoURL,
		"target_dir":     repoDir,
		"timeout":        gc.timeout.String(),
	})

	_, err = gitlib.PlainCloneContext(cloneCtx, repoDir, false, &cloneOptions)
	if err != nil {
		gc.logger.Log(common.EventStepFailure, "Git clone operation failed", map[string]any{
			"error":          err.Error(),
			"repository_url": repoURL,
			"target_dir":     repoDir,
		})

		if removeErr := os.RemoveAll(repoDir); removeErr != nil {
			return "", fmt.Errorf("failed to clone and failed to cleanup: %w, cleanup error: %v", err, removeErr)
		}
		return "", fmt.Errorf("failed to clone repository: %w", err)
	}

	gc.logger.Log(common.EventStepComplete, "Repository cloned successfully", map[string]any{
		"repository_url": repoURL,
		"target_dir":     repoDir,
	})

	if creds != nil && creds.Token != "" {
		// --- Token-based HTTPS ---
		authRepoURL := gc.transformRepoURLWithToken(repoURL, creds.Token)
		cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "set-url", "origin", authRepoURL)
		if output, err := cmd.CombinedOutput(); err != nil {
			gc.logger.Log(common.EventStepFailure, "Failed to set authenticated remote URL", map[string]any{
				"error":  err.Error(),
				"output": string(output),
			})
			return "", fmt.Errorf("failed to set authenticated remote URL: %w", err)
		}
		gc.logger.Log(common.EventStepComplete, "Configured HTTPS remote with token for push", map[string]any{"remote_url": authRepoURL})

	}
	return repoDir, nil
}

func (gc *GitClient) transformRepoURLWithToken(repoURL, token string) string {
	// Transform repo URL to include authentication token
	// GitHub: https://x-access-token:<token>@github.com/org/repo.git
	// GitLab: https://oauth2:<token>@gitlab.com/group/project.git
	if strings.HasPrefix(repoURL, "https://") {
		// Determine format based on URL - GitLab uses oauth2 format
		if strings.Contains(repoURL, "gitlab") {
			return strings.Replace(repoURL, "https://", fmt.Sprintf("https://oauth2:%s@", token), 1)
		}
		// GitHub and others use x-access-token format
		return strings.Replace(repoURL, "https://", fmt.Sprintf("https://x-access-token:%s@", token), 1)
	}
	return repoURL
}

func (gc *GitClient) BlameFile(repoDir, filePath string, lineNumber int) (*BlameResult, error) {
	// Open repository
	repo, err := gitlib.PlainOpen(repoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Get HEAD commit
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Get blame for the file
	blame, err := gitlib.Blame(commit, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get blame: %w", err)
	}

	// Find the line
	if lineNumber <= 0 || lineNumber > len(blame.Lines) {
		return nil, fmt.Errorf("line number %d out of range", lineNumber)
	}

	line := blame.Lines[lineNumber-1] // Convert to 0-based index
	blameCommit, err := repo.CommitObject(line.Hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get blame commit: %w", err)
	}

	return &BlameResult{
		CommitHash:  line.Hash.String(),
		Author:      blameCommit.Author.Name,
		AuthorEmail: blameCommit.Author.Email,
		Date:        blameCommit.Author.When,
		Message:     blameCommit.Message,
		LineContent: line.Text,
	}, nil
}

func (gc *GitClient) GetCommitHistory(repoDir string, maxCommits int) ([]*object.Commit, error) {
	// Open repository
	repo, err := gitlib.PlainOpen(repoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Get HEAD commit
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Get commit iterator
	commits, err := repo.Log(&gitlib.LogOptions{From: ref.Hash()})
	if err != nil {
		return nil, fmt.Errorf("failed to get commit log: %w", err)
	}

	var result []*object.Commit
	count := 0
	err = commits.ForEach(func(c *object.Commit) error {
		if count >= maxCommits {
			return fmt.Errorf("stop iteration")
		}
		result = append(result, c)
		count++
		return nil
	})

	if err != nil && err.Error() != "stop iteration" {
		return nil, fmt.Errorf("failed to iterate commits: %w", err)
	}

	return result, nil
}

func (gc *GitClient) setupAuth(creds *credentials.ResolvedCredentials, repoURL string) (transport.AuthMethod, error) {
	if creds == nil {
		gc.logger.Log(common.EventStepStart, "No credentials provided - attempting public repository clone", map[string]any{})
		return nil, nil // No authentication for public repositories
	}

	gc.logger.Log(common.EventStepStart, "Setting up Git authentication", map[string]any{
		"credential_type": creds.Type,
		"has_token":       creds.Token != "",
		"has_username":    creds.Username != "",
		"has_ssh_key":     creds.SSHKey != "",
	})

	switch creds.Type {
	case "token":
		tokenPrefix := ""
		if len(creds.Token) > 10 {
			tokenPrefix = creds.Token[:10] + "..."
		}
		gc.logger.Log(common.EventStepStart, "Using token authentication", map[string]any{
			"token_prefix": tokenPrefix,
		})

		// Determine token username based on provider
		// GitHub: x-access-token (works for both PATs and GitHub App installation tokens)
		// GitLab: oauth2 (for personal access tokens)
		tokenUsername := "x-access-token"
		if strings.Contains(repoURL, "gitlab") {
			tokenUsername = "oauth2"
		}
		return &http.BasicAuth{
			Username: tokenUsername,
			Password: creds.Token,
		}, nil

	case "basic":
		return &http.BasicAuth{
			Username: creds.Username,
			Password: creds.Password,
		}, nil

	case "ssh_key":
		// Write SSH key to temporary file
		keyFile, err := gc.writeTempSSHKey(creds.SSHKey)
		if err != nil {
			return nil, fmt.Errorf("failed to write SSH key: %w", err)
		}

		// Setup SSH authentication
		auth, err := ssh.NewPublicKeysFromFile("git", keyFile, creds.SSHPassphrase)
		if err != nil {
			if removeErr := os.Remove(keyFile); removeErr != nil {
				return nil, fmt.Errorf("failed to setup SSH auth and failed to cleanup key file: %w, cleanup error: %v", err, removeErr)
			}
			return nil, fmt.Errorf("failed to setup SSH auth: %w", err)
		}

		return auth, nil

	case "none":
		gc.logger.Log(common.EventStepStart, "Using no authentication for public repository", nil)
		return nil, nil

	default:
		gc.logger.Log(common.EventStepFailure, "Unsupported credential type", map[string]any{
			"credential_type": creds.Type,
		})
		return nil, fmt.Errorf("unsupported credential type: %s", creds.Type)
	}
}

func (gc *GitClient) writeTempSSHKey(keyContent string) (string, error) {
	tempDir := filepath.Join(gc.workspaceDir, "temp_keys")
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return "", err
	}

	keyFile := filepath.Join(tempDir, fmt.Sprintf("key_%d", time.Now().Unix()))
	if err := os.WriteFile(keyFile, []byte(keyContent), 0600); err != nil {
		return "", err
	}

	return keyFile, nil
}

func (gc *GitClient) Cleanup(repoPath string) error {
	return os.RemoveAll(repoPath)
}

func (gc *GitClient) CleanupTempKeys() error {
	tempDir := filepath.Join(gc.workspaceDir, "temp_keys")
	return os.RemoveAll(tempDir)
}

// CloneRepository method that returns CloneResult for tools
func (gc *GitClient) CloneRepositoryForTools(ctx context.Context, repoURL string, creds *credentials.ResolvedCredentials, targetDir string, shallow bool) (*CloneResult, error) {
	// Create target directory if specified
	if targetDir != "" {
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create target directory: %w", err)
		}
	} else {
		targetDir = filepath.Join(gc.workspaceDir, fmt.Sprintf("repo_%d", time.Now().Unix()))
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create repo directory: %w", err)
		}
	}

	// Setup authentication
	auth, err := gc.setupAuth(creds, repoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to setup authentication: %w", err)
	}

	// Clone with timeout
	cloneCtx, cancel := context.WithTimeout(ctx, gc.timeout)
	defer cancel()

	cloneOptions := &gitlib.CloneOptions{
		URL:  repoURL,
		Auth: auth,
	}

	if shallow {
		cloneOptions.Depth = 1
		cloneOptions.SingleBranch = true
	}

	repo, err := gitlib.PlainCloneContext(cloneCtx, targetDir, false, cloneOptions)
	if err != nil {
		if removeErr := os.RemoveAll(targetDir); removeErr != nil {
			return nil, fmt.Errorf("failed to clone and failed to cleanup: %w, cleanup error: %v", err, removeErr)
		}
		return nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	// Get HEAD commit info
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Configure git credentials for subsequent operations (push, fetch, etc.)
	// This ensures native git commands can authenticate
	if creds != nil && creds.Token != "" {
		if err := gc.configureGitCredentials(targetDir, repoURL, creds); err != nil {
			gc.logger.Log(common.EventStepFailure, "Failed to configure git credentials", map[string]any{
				"error": err.Error(),
			})
			// Don't fail the clone, but warn that push operations may not work
		}
	}

	return &CloneResult{
		LocalPath:     targetDir,
		Branch:        ref.Name().Short(),
		CommitHash:    ref.Hash().String(),
		CommitMessage: commit.Message,
	}, nil
}

// GetRepositoryInfo gets basic repository information
func (gc *GitClient) GetRepositoryInfo(repoDir string) (*RepositoryInfo, error) {
	// Use git CLI instead of go-git to support worktrees (worktree .git is a pointer file, not a directory)
	hashCmd := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD")
	hashOut, err := hashCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD hash: %w", err)
	}
	commitHash := strings.TrimSpace(string(hashOut))

	branchCmd := exec.Command("git", "-C", repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, _ := branchCmd.Output()
	branchName := strings.TrimSpace(string(branchOut))
	if branchName == "" || branchName == "HEAD" {
		branchName = "main"
	}

	// Count tracked files
	lsCmd := exec.Command("git", "-C", repoDir, "ls-files")
	lsOut, _ := lsCmd.Output()
	fileCount := 0
	if len(lsOut) > 0 {
		fileCount = len(strings.Split(strings.TrimSpace(string(lsOut)), "\n"))
	}

	// Get directory size
	var totalSize int64
	_ = filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	return &RepositoryInfo{
		Name:          filepath.Base(repoDir),
		Description:   "Repository cloned for analysis",
		DefaultBranch: branchName,
		LastCommit:    commitHash,
		Size:          totalSize,
		FileCount:     fileCount,
		Language:      "Unknown",
	}, nil
}

// GetBlame gets git blame information for a file
func (gc *GitClient) GetBlame(repoDir, filePath string, startLine, endLine int) (*BlameInfo, error) {
	// Open repository
	repo, err := gitlib.PlainOpen(repoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	// Get HEAD commit
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Get blame for the file
	blame, err := gitlib.Blame(commit, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get blame: %w", err)
	}

	// Determine line range
	if startLine == 0 {
		startLine = 1
	}
	if endLine == 0 || endLine > len(blame.Lines) {
		endLine = len(blame.Lines)
	}

	var entries []BlameEntry
	for i := startLine - 1; i < endLine; i++ {
		if i >= len(blame.Lines) {
			break
		}

		line := blame.Lines[i]
		blameCommit, err := repo.CommitObject(line.Hash)
		if err != nil {
			continue // Skip this line if commit not found
		}

		entries = append(entries, BlameEntry{
			Line:          i + 1,
			Content:       line.Text,
			CommitHash:    line.Hash.String(),
			Author:        blameCommit.Author.Name,
			AuthorEmail:   blameCommit.Author.Email,
			CommitDate:    blameCommit.Author.When.Format(time.RFC3339),
			CommitMessage: blameCommit.Message,
		})
	}

	return &BlameInfo{
		StartLine: startLine,
		EndLine:   endLine,
		Entries:   entries,
	}, nil
}

// repoKeyFromURL returns a stable filesystem-safe key for a git URL.
// e.g. "https://github.com/org/repo.git" → "github.com_org_repo"
func repoKeyFromURL(repoURL string) string {
	u := repoURL
	// Strip scheme
	for _, prefix := range []string{"https://", "http://", "ssh://", "git@"} {
		u = strings.TrimPrefix(u, prefix)
	}
	// Handle git@host:org/repo.git format
	u = strings.Replace(u, ":", "/", 1)
	// Strip .git suffix
	u = strings.TrimSuffix(u, ".git")
	// Replace path separators with underscores
	u = strings.ReplaceAll(u, "/", "_")
	return u
}

// CloneOrReuseRepository clones a repo to a persistent base directory or reuses an existing clone.
// It creates a git worktree for the requested branch in worktreeDir for session isolation.
// Returns a CloneResult pointing to the worktree path.
func (gc *GitClient) CloneOrReuseRepository(ctx context.Context, repoURL string, creds *credentials.ResolvedCredentials, branch string, worktreeDir string) (*CloneResult, error) {
	repoKey := repoKeyFromURL(repoURL)
	baseDir := filepath.Join(gc.workspaceDir, "repos", repoKey)

	gc.logger.Log(common.EventStepStart, "Clone or reuse repository", map[string]any{
		"repo_url":     repoURL,
		"repo_key":     repoKey,
		"base_dir":     baseDir,
		"worktree_dir": worktreeDir,
		"branch":       branch,
	})

	// Build authenticated URL for HTTPS repos
	authURL := repoURL
	if creds != nil && creds.Token != "" {
		authURL = gc.transformRepoURLWithToken(repoURL, creds.Token)
	}

	// The bare clone is single-branch by construction and grows on demand —
	// only branches that have actually been requested by an analysis are
	// fetched. This keeps the agent's `git branch -a` view limited to refs
	// the request authorized, instead of exposing the full remote (currently
	// hundreds of stale claude/* exploration branches whose names look
	// task-relevant and pull the LLM off-task).
	//
	// Reuse with a different branch widens the refspec via `remote
	// set-branches --add` and then fetches only that branch.
	if _, err := os.Stat(filepath.Join(baseDir, "HEAD")); err == nil {
		gc.logger.Log(common.EventStepStart, "Reusing existing clone, ensuring branch is fetched", map[string]any{"base_dir": baseDir, "branch": branch})
		setURL := exec.CommandContext(ctx, "git", "-C", baseDir, "remote", "set-url", "origin", authURL)
		if out, err := setURL.CombinedOutput(); err != nil {
			gc.logger.Log(common.EventStepFailure, "Failed to update remote URL", map[string]any{"error": err.Error(), "output": string(out)})
		}
		if branch != "" {
			// Add this branch to the refspec list (no-op if already present)
			addBr := exec.CommandContext(ctx, "git", "-C", baseDir, "remote", "set-branches", "--add", "origin", branch)
			if out, err := addBr.CombinedOutput(); err != nil {
				gc.logger.Log(common.EventStepFailure, "Failed to add branch to refspec", map[string]any{"error": err.Error(), "output": string(out), "branch": branch})
			}
			fetchCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "fetch", "origin", branch)
			if out, err := fetchCmd.CombinedOutput(); err != nil {
				gc.logger.Log(common.EventStepFailure, "git fetch failed, will re-clone", map[string]any{"error": err.Error(), "output": string(out), "branch": branch})
				_ = os.RemoveAll(baseDir)
			}
		} else {
			// No specific branch — fetch what's already in the refspec
			fetchCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "fetch", "origin")
			if out, err := fetchCmd.CombinedOutput(); err != nil {
				gc.logger.Log(common.EventStepFailure, "git fetch failed, will re-clone", map[string]any{"error": err.Error(), "output": string(out)})
				_ = os.RemoveAll(baseDir)
			}
		}
	}

	if _, err := os.Stat(filepath.Join(baseDir, "HEAD")); os.IsNotExist(err) {
		// Fresh bare clone — single-branch by default
		gc.logger.Log(common.EventStepStart, "Performing fresh bare clone (single-branch)", map[string]any{"base_dir": baseDir, "branch": branch})
		if err := os.MkdirAll(filepath.Dir(baseDir), 0755); err != nil {
			return nil, fmt.Errorf("failed to create repos directory: %w", err)
		}
		cloneCtx, cancel := context.WithTimeout(ctx, gc.timeout)
		defer cancel()
		cloneArgs := []string{"clone", "--bare", "--single-branch"}
		if branch != "" {
			cloneArgs = append(cloneArgs, "--branch", branch)
		}
		cloneArgs = append(cloneArgs, authURL, baseDir)
		cmd := exec.CommandContext(cloneCtx, "git", cloneArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(baseDir)
			return nil, fmt.Errorf("bare clone failed: %s: %w", string(out), err)
		}
		gc.logger.Log(common.EventStepComplete, "Bare clone completed", map[string]any{"base_dir": baseDir})
	}

	// Determine the ref to check out
	ref := "origin/HEAD"
	if branch != "" {
		ref = "origin/" + branch
	}

	// Create worktree
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}
	wtCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "add", "--detach", worktreeDir, ref)
	if out, err := wtCmd.CombinedOutput(); err != nil {
		// If detach with ref fails, try without ref (use HEAD)
		gc.logger.Log(common.EventStepFailure, "Worktree add with ref failed, trying HEAD", map[string]any{"error": err.Error(), "output": string(out), "ref": ref})
		_ = os.RemoveAll(worktreeDir)
		if err2 := os.MkdirAll(worktreeDir, 0755); err2 != nil {
			return nil, fmt.Errorf("failed to create worktree directory: %w", err2)
		}
		wtCmd2 := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "add", "--detach", worktreeDir)
		if out2, err2 := wtCmd2.CombinedOutput(); err2 != nil {
			return nil, fmt.Errorf("worktree add failed: %s: %w", string(out2), err2)
		}
	}

	gc.logger.Log(common.EventStepComplete, "Worktree created", map[string]any{"worktree_dir": worktreeDir})

	// Configure credentials in worktree for push operations
	if creds != nil && creds.Token != "" {
		setURL := exec.CommandContext(ctx, "git", "-C", worktreeDir, "remote", "set-url", "origin", authURL)
		_ = setURL.Run()
	}

	// Get HEAD info from the worktree
	hashCmd := exec.CommandContext(ctx, "git", "-C", worktreeDir, "rev-parse", "HEAD")
	hashOut, _ := hashCmd.Output()
	commitHash := strings.TrimSpace(string(hashOut))

	branchCmd := exec.CommandContext(ctx, "git", "-C", worktreeDir, "rev-parse", "--abbrev-ref", "HEAD")
	branchOut, _ := branchCmd.Output()
	branchName := strings.TrimSpace(string(branchOut))
	if branchName == "HEAD" && branch != "" {
		branchName = branch
	}

	msgCmd := exec.CommandContext(ctx, "git", "-C", worktreeDir, "log", "-1", "--format=%s")
	msgOut, _ := msgCmd.Output()
	commitMsg := strings.TrimSpace(string(msgOut))

	return &CloneResult{
		LocalPath:     worktreeDir,
		Branch:        branchName,
		CommitHash:    commitHash,
		CommitMessage: commitMsg,
	}, nil
}

// CleanupWorktree removes a git worktree cleanly.
func (gc *GitClient) CleanupWorktree(worktreeDir string) error {
	// Find the base repo by checking .git file in worktree
	gitFile := filepath.Join(worktreeDir, ".git")
	content, err := os.ReadFile(gitFile)
	if err != nil {
		// Not a worktree or already cleaned up, just remove directory
		return os.RemoveAll(worktreeDir)
	}

	// Parse "gitdir: /path/to/base/.git/worktrees/..." to find base repo
	gitdir := strings.TrimSpace(string(content))
	gitdir = strings.TrimPrefix(gitdir, "gitdir: ")
	// Walk up to find the base .git dir
	parts := strings.Split(gitdir, string(filepath.Separator))
	for i, p := range parts {
		if p == "worktrees" {
			baseGitDir := strings.Join(parts[:i], string(filepath.Separator))
			baseDir := filepath.Dir(baseGitDir)
			cmd := exec.Command("git", "-C", baseDir, "worktree", "remove", "--force", worktreeDir)
			if out, err := cmd.CombinedOutput(); err != nil {
				gc.logger.Log(common.EventStepFailure, "git worktree remove failed, falling back to rm", map[string]any{"error": err.Error(), "output": string(out)})
				return os.RemoveAll(worktreeDir)
			}
			return nil
		}
	}

	return os.RemoveAll(worktreeDir)
}

// configureGitCredentials configures git to authenticate for subsequent operations
// This allows native git commands (push, fetch) to work after cloning
func (gc *GitClient) configureGitCredentials(repoDir, repoURL string, creds *credentials.ResolvedCredentials) error {
	// Only configure for HTTPS URLs with token-based auth
	if !strings.HasPrefix(repoURL, "https://") {
		return nil // SSH or other protocols don't need this
	}

	// For HTTPS with token, configure the remote URL with embedded credentials
	// GitHub: https://x-access-token:<token>@github.com/owner/repo.git
	// GitLab: https://oauth2:<token>@gitlab.com/group/project.git
	var authenticatedURL string
	if strings.Contains(repoURL, "gitlab") {
		authenticatedURL = strings.Replace(repoURL, "https://", fmt.Sprintf("https://oauth2:%s@", creds.Token), 1)
	} else {
		authenticatedURL = strings.Replace(repoURL, "https://", fmt.Sprintf("https://x-access-token:%s@", creds.Token), 1)
	}

	// Update the remote URL using git command
	cmd := exec.Command("git", "remote", "set-url", "origin", authenticatedURL)
	cmd.Dir = repoDir

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to configure git remote with credentials: %w", err)
	}

	gc.logger.Log(common.EventStepComplete, "Configured git credentials for push operations", map[string]any{
		"repo_dir": repoDir,
	})

	return nil
}
