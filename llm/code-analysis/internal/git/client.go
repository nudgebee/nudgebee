package git

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		authRepoURL, err := gc.transformRepoURLWithToken(repoURL, creds.Token)
		if err != nil {
			return "", fmt.Errorf("refusing to set remote to an invalid repository URL: %w", err)
		}
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

func (gc *GitClient) transformRepoURLWithToken(repoURL, token string) (string, error) {
	return InjectTokenIntoURL(repoURL, token)
}

// InjectTokenIntoURL embeds an auth token into an HTTPS git URL using the
// provider-appropriate username — x-access-token for GitHub (works for both PATs
// and GitHub App installation tokens), oauth2 for GitLab. Any userinfo already
// present is replaced, so the token is never double-embedded. Non-HTTPS URLs and
// empty tokens are returned unchanged.
//
// The token is set via url.UserPassword so url.String() percent-encodes any
// special characters (e.g. '@' or ':') per RFC 3986, which git requires.
//
// Provider is inferred from the host substring "gitlab", matching the rest of this
// package; self-hosted GitLab on a non-"gitlab" host falls back to the GitHub
// username form (a pre-existing repo-wide convention).
// It fails closed: a repoURL that is not a well-formed repository URL yields an error
// rather than being handed back unchanged. Returning the input was how a chain-of-thought
// blob survived all the way to a `git push` command line (#35703).
func InjectTokenIntoURL(repoURL, token string) (string, error) {
	if err := ValidateRepoURL(repoURL); err != nil {
		return "", err
	}
	// scp-form remotes authenticate over SSH, and with no token there is nothing to
	// embed — in both cases the validated URL is already the right target.
	//
	// http:// is deliberately excluded: embedding a token there would send the
	// credential in cleartext. Note the asymmetry with StripURLUserinfo and
	// RedactURLCredentials, which DO cover http:// — a helper that removes credentials
	// must match every scheme one could appear under, while a helper that adds one
	// should match as few as possible. A plaintext remote can still be cloned; it just
	// never gets a token attached.
	if token == "" || !strings.HasPrefix(repoURL, "https://") {
		return repoURL, nil
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", fmt.Errorf("parse repository URL: %w", err)
	}
	tokenUser := "x-access-token"
	if strings.Contains(repoURL, "gitlab") {
		tokenUser = "oauth2"
	}
	u.User = url.UserPassword(tokenUser, token)
	return u.String(), nil
}

// StripURLUserinfo removes any embedded "user:pass@" from an HTTP(S) URL. Used to keep
// tokens out of logs when echoing an authenticated push target.
//
// A value that carries a scheme but cannot be parsed is reported as a placeholder
// rather than echoed: returning the input unchanged would defeat the whole point of
// the helper for exactly the malformed, token-bearing strings it exists to redact.
// Values with no HTTP(S) scheme (scp-form remotes, plain remote names like "origin")
// cannot embed userinfo of this shape and are passed through.
func StripURLUserinfo(repoURL string) string {
	if !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "http://") {
		return repoURL
	}
	u, err := url.Parse(repoURL)
	if err != nil {
		return "<unparseable-url>"
	}
	u.User = nil
	return u.String()
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

// repoLocks serializes base-clone / fetch / worktree-add per repository. Without
// it, two concurrent analyses of the same repo both observe a missing base and
// each perform a full bare clone (the "re-cloned all 9,199 files" waste), and
// the reuse path's mutations of the shared bare-repo config (remote set-url /
// set-branches, plus worktree metadata) race. Keyed by repoKey so different
// repos still prepare in parallel. Locks are process-lived and never removed —
// the set is bounded by the number of distinct repos a workspace pod sees.
//
// Implemented as a per-repo buffered channel (cap 1) rather than a sync.Mutex so
// the wait is context-aware: since holding the lock spans a multi-minute clone /
// fetch, a queued dispatch whose context is cancelled must be able to bail out
// instead of blocking on an uninterruptible Mutex.Lock() for that whole window.
var (
	repoLocksMu sync.Mutex
	repoLocks   = map[string]chan struct{}{}
)

// lockForRepo returns the per-repo semaphore: a cap-1 channel pre-filled with a
// token. Receive from it to acquire the lock; send back to release.
func lockForRepo(repoKey string) chan struct{} {
	repoLocksMu.Lock()
	defer repoLocksMu.Unlock()
	ch, ok := repoLocks[repoKey]
	if !ok {
		ch = make(chan struct{}, 1)
		ch <- struct{}{}
		repoLocks[repoKey] = ch
	}
	return ch
}

// baseCanCheckout reports whether the bare base repo holds at least one commit on
// any ref — the minimum for `git worktree add` to produce a working tree. Checked
// by state rather than by matching git's error text, so an unusable base is caught
// however it got that way (cloned while the remote was still empty, objects pruned,
// refs corrupted).
func baseCanCheckout(ctx context.Context, baseDir string) bool {
	out, err := exec.CommandContext(ctx, "git", "-C", baseDir, "rev-list", "-n", "1", "--all").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// ensureRemoteTracking makes each named branch resolvable as origin/<branch> in a bare
// base repo, by adding it to the remote's fetch refspec and fetching it.
//
// It widens the refspec one branch at a time rather than to refs/heads/*, so the
// agent's `git branch -a` view stays limited to branches the request actually asked
// for. Failures are logged and tolerated: a missing base branch should degrade the
// merge-conflict view, not fail the whole analysis.
func (gc *GitClient) ensureRemoteTracking(ctx context.Context, baseDir string, branches ...string) {
	seen := map[string]struct{}{}
	for _, branch := range branches {
		if branch == "" {
			continue
		}
		if _, dup := seen[branch]; dup {
			continue
		}
		seen[branch] = struct{}{}

		if len(branch) > 0 && branch[0] == '-' {
			gc.logger.Log(common.EventStepFailure, "Invalid branch name starting with dash", map[string]any{"branch": branch})
			continue
		}

		addBr := exec.CommandContext(ctx, "git", "-C", baseDir, "remote", "set-branches", "--add", "origin", branch)
		if out, err := addBr.CombinedOutput(); err != nil {
			gc.logger.Log(common.EventStepFailure, "Failed to add branch to refspec", map[string]any{
				"error": err.Error(), "output": string(out), "branch": branch,
			})
			continue
		}
		fetchCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "fetch", "origin", branch)
		if out, err := fetchCmd.CombinedOutput(); err != nil {
			gc.logger.Log(common.EventStepFailure, "Failed to fetch remote-tracking branch", map[string]any{
				"error": err.Error(), "output": string(out), "branch": branch,
			})
		}
	}
}

// CloneOrReuseRepository clones a repo to a persistent base directory or reuses an existing clone.
// It creates a git worktree for the requested branch in worktreeDir for session isolation.
// Returns a CloneResult pointing to the worktree path.
//
// extraBranches names additional branches to make available as origin/<name> — pass the
// base branch when the caller needs to diff or merge against it (reproducing a PR merge
// conflict needs both sides). They are fetched explicitly rather than by widening the
// refspec to everything, which would undo the narrowing described below.
func (gc *GitClient) CloneOrReuseRepository(ctx context.Context, repoURL string, creds *credentials.ResolvedCredentials, branch string, worktreeDir string, extraBranches ...string) (*CloneResult, error) {
	return gc.CloneOrReuseRepositoryAtCommit(ctx, repoURL, creds, branch, "", worktreeDir, extraBranches...)
}

// CloneSealedAtCommit checks out exactly `commit` into worktreeDir with no way
// to reach anything newer.
//
// Pinning the *checkout* does not pin what is *reachable*. The shared path
// clones with --single-branch, which limits which branch is fetched but not how
// far: the branch tip ref lands in the repo, years ahead of the pinned commit,
// so `git log --all` and `git show <sha>` reach commits that do not exist yet
// from the caller's point of view. For incident analysis that is merely
// confusing; for anything being scored it is the answer sitting in the working
// directory. An agent did exactly this — found the fix commit by pickaxe and
// read the corrected file out of it.
//
// Two properties do the work here:
//
//   - a shallow fetch of a SHA yields that commit and its ANCESTORS only. Git
//     has no way to walk forward, so no depth setting can expose a descendant.
//     History that blame and `log -S` legitimately use is preserved.
//   - origin is removed afterwards, so nothing can be fetched later.
//
// Isolated rather than shared on purpose: refs live in the base repo, so pruning
// them in a shared bare clone would race with concurrent analyses of the same
// repository (see the note on worktree origin URLs above). A private directory
// has no such coupling.
//
// Only valid when no PR is to be raised — pushing needs the remote this removes.
func (gc *GitClient) CloneSealedAtCommit(ctx context.Context, repoURL string, creds *credentials.ResolvedCredentials, commit string, depth int, worktreeDir string) (*CloneResult, error) {
	if err := ValidateCommitSHA(commit); err != nil {
		return nil, fmt.Errorf("invalid commit: %w", err)
	}
	if depth <= 0 {
		depth = 50
	}
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}

	authURL := repoURL
	if creds != nil && creds.Token != "" {
		var err error
		if authURL, err = gc.transformRepoURLWithToken(repoURL, creds.Token); err != nil {
			return nil, fmt.Errorf("failed to build authenticated URL: %w", err)
		}
	}

	run := func(timeout time.Duration, args ...string) ([]byte, error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return exec.CommandContext(cctx, "git", append([]string{"-C", worktreeDir}, args...)...).CombinedOutput()
	}

	if out, err := run(gc.timeout, "init", "--quiet"); err != nil {
		return nil, fmt.Errorf("git init failed: %s: %w", string(out), err)
	}
	if out, err := run(gc.timeout, "remote", "add", "origin", authURL); err != nil {
		return nil, fmt.Errorf("git remote add failed: %s: %w", StripURLUserinfo(string(out)), err)
	}
	if out, err := run(gc.timeout, "fetch", "--no-tags", "--depth", strconv.Itoa(depth), "origin", commit); err != nil {
		return nil, fmt.Errorf("shallow fetch of %s failed: %s: %w", commit, StripURLUserinfo(string(out)), err)
	}
	// Detached, at the requested commit and nothing else. A failure here must not
	// fall back to any other revision: analysing the wrong tree confidently is
	// worse than failing.
	if out, err := run(gc.timeout, "checkout", "--detach", commit); err != nil {
		return nil, fmt.Errorf("checkout of %s failed: %s: %w", commit, string(out), err)
	}
	// The seal. After this there is no configured remote, so no later fetch can
	// widen what is visible — including one the agent issues itself.
	if out, err := run(gc.timeout, "remote", "remove", "origin"); err != nil {
		return nil, fmt.Errorf("failed to remove origin: %s: %w", string(out), err)
	}

	head, err := run(gc.timeout, "rev-parse", "HEAD")
	if err != nil {
		// Returning an empty CommitHash would let a caller believe the checkout
		// succeeded at an unknown revision — the precise failure this function
		// exists to make impossible.
		return nil, fmt.Errorf("failed to resolve HEAD after checkout of %s: %s: %w", commit, string(head), err)
	}
	// The subject is descriptive only; a repository with no log formatting is not
	// a reason to fail an otherwise good checkout.
	subject, _ := run(gc.timeout, "log", "-1", "--pretty=%s")
	gc.logger.Log(common.EventStepComplete, "Sealed clone created", map[string]any{
		"worktree_dir": worktreeDir, "commit": commit, "depth": depth,
	})
	return &CloneResult{
		LocalPath:     worktreeDir,
		Branch:        "",
		CommitHash:    strings.TrimSpace(string(head)),
		CommitMessage: strings.TrimSpace(string(subject)),
	}, nil
}

// CloneOrReuseRepositoryAtCommit is CloneOrReuseRepository with an optional pinned
// commit. When commit is non-empty the worktree is checked out at exactly that
// commit rather than at the tip of a branch.
//
// Why this exists separately from `branch`: `git clone --branch` and
// `git remote set-branches` only accept ref names, so a SHA passed as a branch
// fails the clone, or worse resolves nowhere and silently falls back to HEAD —
// which yields an analysis of the wrong code with no error. Pinning is needed
// whenever the question is "what did this code look like when the incident
// fired", not "what does it look like now".
func (gc *GitClient) CloneOrReuseRepositoryAtCommit(ctx context.Context, repoURL string, creds *credentials.ResolvedCredentials, branch string, commit string, worktreeDir string, extraBranches ...string) (*CloneResult, error) {
	// An empty branch means "clone the default branch" and is supported throughout this
	// function, so only non-empty names are checked. A name that would be read by git as
	// an option is refused before it reaches any argv.
	if branch != "" {
		if err := ValidateBranchName(branch); err != nil {
			return nil, fmt.Errorf("invalid branch name: %w", err)
		}
	}
	if commit != "" {
		if err := ValidateCommitSHA(commit); err != nil {
			return nil, fmt.Errorf("invalid commit: %w", err)
		}
	}
	for _, extra := range extraBranches {
		if extra == "" {
			continue
		}
		if err := ValidateBranchName(extra); err != nil {
			return nil, fmt.Errorf("invalid base branch name: %w", err)
		}
	}

	repoKey := repoKeyFromURL(repoURL)
	baseDir := filepath.Join(gc.workspaceDir, "repos", repoKey)

	// Serialize base preparation + worktree creation for this repo so concurrent
	// same-repo dispatches reuse one bare clone instead of racing into duplicate
	// full clones and colliding on the shared base config. Different repos use
	// different locks and still run in parallel. Context-aware so a cancelled
	// dispatch waiting behind another repo's in-flight clone returns promptly.
	repoLock := lockForRepo(repoKey)
	select {
	case <-repoLock:
		defer func() { repoLock <- struct{}{} }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

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
		authenticated, err := gc.transformRepoURLWithToken(repoURL, creds.Token)
		if err != nil {
			return nil, fmt.Errorf("refusing to clone an invalid repository URL: %w", err)
		}
		authURL = authenticated
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
			if len(branch) > 0 && branch[0] == '-' {
				gc.logger.Log(common.EventStepFailure, "Invalid branch name starting with dash", map[string]any{"branch": branch})
				return nil, fmt.Errorf("invalid branch name: %s", branch)
			}
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

	freshBareClone := func() error {
		// Fresh bare clone — single-branch by default
		gc.logger.Log(common.EventStepStart, "Performing fresh bare clone (single-branch)", map[string]any{"base_dir": baseDir, "branch": branch})
		if err := os.MkdirAll(filepath.Dir(baseDir), 0755); err != nil {
			return fmt.Errorf("failed to create repos directory: %w", err)
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
			return fmt.Errorf("bare clone failed: %s: %w", string(out), err)
		}
		gc.logger.Log(common.EventStepComplete, "Bare clone completed", map[string]any{"base_dir": baseDir})

		// `git clone --bare` writes branches to refs/heads/* and configures no fetch
		// refspec, so refs/remotes/origin/* is empty and `origin/<branch>` does not
		// resolve. The worktree checkout then silently fell back to HEAD and
		// `git merge origin/<base>` could not reproduce a PR merge conflict. Establish
		// the remote-tracking refspec for exactly the branches this request needs.
		gc.ensureRemoteTracking(ctx, baseDir, append([]string{branch}, extraBranches...)...)
		return nil
	}

	if _, err := os.Stat(filepath.Join(baseDir, "HEAD")); os.IsNotExist(err) {
		if err := freshBareClone(); err != nil {
			return nil, err
		}
	} else if !baseCanCheckout(ctx, baseDir) {
		// The cached base holds no commit, so no worktree can ever be created from
		// it. The reuse path above cannot repair that: `git clone --bare` configures
		// no fetch refspec, so a later `git fetch origin` only writes FETCH_HEAD and
		// leaves refs/heads/* empty. Seen in production when a repo was first cloned
		// while it still had zero commits — every later analysis in that pod kept
		// failing on `worktree add` with `invalid reference: HEAD`. Discard and
		// re-clone; a base that has been pruned or corrupted recovers the same way.
		gc.logger.Log(common.EventStepFailure, "Cached clone resolves no commit, re-cloning", map[string]any{"base_dir": baseDir})
		_ = os.RemoveAll(baseDir)
		if err := freshBareClone(); err != nil {
			return nil, err
		}
	}

	// A remote with no commits clones successfully (exit 0, unborn HEAD), so a
	// clean clone is not evidence there is anything to check out. Say so plainly
	// here — otherwise the failure surfaces from `worktree add` as git's
	// `--orphan` advice plus `invalid reference: HEAD`, which reads like a bug in
	// the agent rather than an empty repository.
	if !baseCanCheckout(ctx, baseDir) {
		return nil, fmt.Errorf("repository has no commits to check out: %s", repoURL)
	}

	// On the reuse path the requested branch is already handled above; make sure any
	// additional branches the caller asked for are present too.
	if len(extraBranches) > 0 {
		gc.ensureRemoteTracking(ctx, baseDir, extraBranches...)
	}

	// Determine the ref to check out
	ref := "origin/HEAD"
	if branch != "" {
		ref = "origin/" + branch
	}

	// Resolve the ref to a concrete commit so the checkout is deterministic even
	// if the branch tip moves mid-run, and so the CloneResult reports exactly the
	// commit that was analyzed. Falls back to the symbolic ref if rev-parse fails.
	checkoutRef := ref
	if out, err := exec.CommandContext(ctx, "git", "-C", baseDir, "rev-parse", ref).Output(); err == nil {
		if sha := strings.TrimSpace(string(out)); sha != "" {
			checkoutRef = sha
		}
	}

	// A pinned commit overrides the branch tip. The single-branch refspec above
	// will usually not have brought the object down, so fetch it explicitly.
	if commit != "" {
		if err := gc.ensureCommitPresent(ctx, baseDir, commit); err != nil {
			return nil, err
		}
		checkoutRef = commit
	}

	// Create worktree
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create worktree directory: %w", err)
	}
	wtCmd := exec.CommandContext(ctx, "git", "-C", baseDir, "worktree", "add", "--detach", worktreeDir, checkoutRef)
	if out, err := wtCmd.CombinedOutput(); err != nil {
		// A pinned commit must never degrade to HEAD. The caller asked about one
		// specific revision; checking out a different one produces a confident
		// analysis of the wrong code, which is worse than failing.
		if commit != "" {
			_ = os.RemoveAll(worktreeDir)
			return nil, fmt.Errorf("worktree add at commit %s failed: %s: %w", commit, string(out), err)
		}
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

	// Intentionally do NOT set the worktree's origin URL here. Worktrees created from
	// one bare clone share the base repo's .git/config, so a `git remote set-url` would
	// race across concurrent analyses of the same repo. The push path instead pushes
	// directly to a token-embedded URL (stateless, no config mutation); the bare clone's
	// origin already carries auth for fetch.

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

// ensureCommitPresent makes `commit` resolvable inside the bare repo at baseDir.
//
// The bare clone is single-branch by construction, so an arbitrary historical
// commit is usually absent even when the branch it lives on was fetched. Tries
// the cheap targeted fetch first (GitHub and GitLab both serve arbitrary SHAs
// via uploadpack.allowAnySHA1InWant); falls back to widening the refspec and
// fetching everything for servers that refuse SHA requests.
func (gc *GitClient) ensureCommitPresent(ctx context.Context, baseDir, commit string) error {
	has := func() bool {
		return exec.CommandContext(ctx, "git", "-C", baseDir, "cat-file", "-e", commit+"^{commit}").Run() == nil
	}
	if has() {
		return nil
	}

	gc.logger.Log(common.EventStepStart, "Fetching pinned commit", map[string]any{"base_dir": baseDir, "commit": commit})
	fetchCtx, cancel := context.WithTimeout(ctx, gc.timeout)
	defer cancel()
	if out, err := exec.CommandContext(fetchCtx, "git", "-C", baseDir, "fetch", "--no-tags", "origin", commit).CombinedOutput(); err != nil {
		gc.logger.Log(common.EventStepFailure, "Targeted commit fetch failed, widening refspec", map[string]any{
			"commit": commit, "error": err.Error(), "output": string(out),
		})
	} else if has() {
		return nil
	}

	// Server refused the SHA (or served it without making it reachable). Widen the
	// refspec to all branches and fetch again — slower, but it is the only option
	// left before failing.
	// Bounded even though this only rewrites .git/config and never touches the
	// network: a stale index.lock is enough to block it indefinitely, and this
	// path already runs after a failed fetch. Failure stays non-fatal — the
	// fetch below is the operation that decides the outcome.
	setBranchesCtx, cancelSetBranches := context.WithTimeout(ctx, gc.timeout)
	defer cancelSetBranches()
	if out, err := exec.CommandContext(setBranchesCtx, "git", "-C", baseDir, "remote", "set-branches", "origin", "*").CombinedOutput(); err != nil {
		gc.logger.Log(common.EventStepFailure, "Failed to widen refspec", map[string]any{"error": err.Error(), "output": string(out)})
	}
	wideCtx, wideCancel := context.WithTimeout(ctx, gc.timeout)
	defer wideCancel()
	if out, err := exec.CommandContext(wideCtx, "git", "-C", baseDir, "fetch", "--no-tags", "origin").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to fetch commit %s: %s: %w", commit, string(out), err)
	}
	if !has() {
		return fmt.Errorf("commit %s not found in repository after fetch", commit)
	}
	return nil
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
	authenticatedURL, err := InjectTokenIntoURL(repoURL, creds.Token)
	if err != nil {
		return fmt.Errorf("refusing to configure remote with an invalid repository URL: %w", err)
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
