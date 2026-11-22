package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// defaultBranchLookupTimeout bounds each local or ls-remote git call made to
	// read a file from the default branch. The read is optional, so it must not
	// hold a clone up for long.
	defaultBranchLookupTimeout = 30 * time.Second
	// defaultBranchFetchTimeout bounds the one fetch that read may need.
	defaultBranchFetchTimeout = 2 * time.Minute
)

func runGit(ctx context.Context, timeout time.Duration, dir string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		// A replace ref in the shared clone must not change what is read for
		// a commit: any earlier command in the workspace could have added one.
		"GIT_NO_REPLACE_OBJECTS=1",
	)
	// The timeout has to end the call, not just git itself. git runs its
	// transport as a child (git-remote-https) that inherits the output pipes,
	// so with git alone killed Output keeps waiting on a stalled connection.
	// Kill the whole group, and stop waiting on the pipes shortly after.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		// git quotes the remote URL back in its errors; never let a token through.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if line, _, _ := bytes.Cut(bytes.TrimSpace(exitErr.Stderr), []byte("\n")); len(line) > 0 {
				if len(line) > 300 {
					line = line[:300]
				}
				err = fmt.Errorf("%w: %s", err, RedactURLCredentials(string(line)))
			}
		}
	}
	return out, err
}

var defaultGitPorts = map[string]string{"https": "443", "http": "80", "ssh": "22"}

// RepoIdentity reduces a repository URL to a form in which two URLs for the
// same repository are equal: host and path, lower-cased, without credentials,
// a default port, surrounding slashes or a trailing ".git". Empty when the URL
// names no repository.
//
// It is for deciding whether two URLs are the same repository, so it must
// never make two different ones equal. A URL that is not http(s), ssh or
// scp-like (a local path, file://) is returned as it is: only the identical
// string is the same repository.
func RepoIdentity(repoURL string) string {
	raw := strings.TrimSpace(repoURL)
	var host, repoPath string
	if scheme, _, hasScheme := strings.Cut(raw, "://"); hasScheme {
		scheme = strings.ToLower(scheme)
		if _, known := defaultGitPorts[scheme]; !known {
			return raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return ""
		}
		host, repoPath = strings.ToLower(u.Hostname()), u.Path
		if port := u.Port(); port != "" && port != defaultGitPorts[scheme] {
			host += ":" + port
		}
	} else if h, p := SplitRepoURL(raw); h != "" {
		host, repoPath = strings.ToLower(h), p
	} else {
		return raw
	}
	repoPath = strings.Trim(strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git"), "/")
	if repoPath == "" {
		return ""
	}
	return host + "/" + strings.ToLower(repoPath)
}

// SameRepository reports whether two repository URLs name the same repository.
func SameRepository(a, b string) bool {
	identity := RepoIdentity(a)
	return identity != "" && identity == RepoIdentity(b)
}

// OriginURL returns the URL of repoDir's `origin` remote.
func OriginURL(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, defaultBranchLookupTimeout, repoDir, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("reading the origin URL failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteDefaultBranch asks the remote which branch its HEAD points at and what
// commit that branch is on now.
func RemoteDefaultBranch(ctx context.Context, remoteURL string) (branch, tip string, err error) {
	out, err := runGit(ctx, defaultBranchLookupTimeout, "", "ls-remote", "--symref", "--", remoteURL, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("default branch lookup failed: %w", err)
	}
	branch, tip = parseLsRemoteHead(string(out))
	if branch == "" {
		return "", "", errors.New("remote did not report a default branch")
	}
	return branch, tip, nil
}

// ReadBranchFileAtCommit returns the contents of relPath at commit, the tip of
// branch on the remote. found is false with a nil error when the commit has no
// such file. Only a regular file of at most maxBytes is returned.
//
// repoDir is a checkout of the same repository and is only ever read: when it
// already holds the commit the file comes from there. Otherwise the commit is
// fetched into a throwaway repository, not into repoDir or the shared clone
// behind it. A fetch into either would change what a run sees: it moves a
// shared remote-tracking ref while another clone may be fetching the same ref,
// and it cuts a shallow clone's history at the fetched commit.
//
// The branch is fetched by its full ref name, so a tag with the same name as
// the branch is never what gets read.
func ReadBranchFileAtCommit(ctx context.Context, repoDir, remoteURL, branch, commit, relPath string, maxBytes int64) (content []byte, found bool, err error) {
	if clean := path.Clean(relPath); clean != relPath || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(relPath) {
		return nil, false, fmt.Errorf("invalid path %q", relPath)
	}
	if err := ValidateBranchName(branch); err != nil {
		return nil, false, fmt.Errorf("invalid branch name: %w", err)
	}
	if err := ValidateCommitSHA(commit); err != nil {
		return nil, false, fmt.Errorf("invalid commit: %w", err)
	}

	dir := repoDir
	if _, err := runGit(ctx, defaultBranchLookupTimeout, repoDir, "cat-file", "-e", commit+"^{commit}"); err != nil {
		// Named like a run's checkout so that the workspace cleanup, which sweeps
		// abandoned "code-analysis-" directories in the temp directory, also
		// removes one that a crash left behind.
		scratch, err := os.MkdirTemp("", "code-analysis-default-branch-")
		if err != nil {
			return nil, false, fmt.Errorf("creating a scratch repository failed: %w", err)
		}
		defer func() { _ = os.RemoveAll(scratch) }()
		if _, err := runGit(ctx, defaultBranchLookupTimeout, "", "init", "--quiet", "--bare", scratch); err != nil {
			return nil, false, fmt.Errorf("creating a scratch repository failed: %w", err)
		}
		if _, err := runGit(ctx, defaultBranchLookupTimeout, scratch, "remote", "add", "--", "origin", remoteURL); err != nil {
			return nil, false, fmt.Errorf("creating a scratch repository failed: %w", err)
		}
		// One commit, and no file contents until one is asked for: the single
		// file read below is then all that is downloaded. A server without
		// filter support ignores the filter and sends the whole commit.
		if _, err := runGit(ctx, defaultBranchFetchTimeout, scratch, "fetch", "--quiet", "--no-tags", "--depth=1",
			"--filter=blob:none", "origin", "refs/heads/"+branch); err != nil {
			return nil, false, fmt.Errorf("fetching default branch %q failed: %w", branch, err)
		}
		// The branch may have moved since the caller looked; read what was fetched.
		fetched, err := runGit(ctx, defaultBranchLookupTimeout, scratch, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return nil, false, fmt.Errorf("resolving fetched default branch %q failed: %w", branch, err)
		}
		dir, commit = scratch, strings.TrimSpace(string(fetched))
	}

	// "<mode> <type> <object>\t<path>", or nothing when the path is absent.
	entry, err := runGit(ctx, defaultBranchLookupTimeout, dir, "ls-tree", commit, "--", relPath)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s on default branch %q failed: %w", relPath, branch, err)
	}
	fields := strings.Fields(string(entry))
	if len(fields) < 3 {
		return nil, false, nil
	}
	mode, objType, object := fields[0], fields[1], fields[2]
	// A symlink (120000) is refused: its target is a path, not the file's contents.
	if objType != "blob" || (mode != "100644" && mode != "100755") {
		return nil, false, fmt.Errorf("%s on default branch %q is not a regular file", relPath, branch)
	}
	sizeOut, err := runGit(ctx, defaultBranchFetchTimeout, dir, "cat-file", "-s", object)
	if err != nil {
		return nil, false, fmt.Errorf("sizing %s on default branch %q failed: %w", relPath, branch, err)
	}
	if size, convErr := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64); convErr != nil || size > maxBytes {
		return nil, false, fmt.Errorf("%s on default branch %q is larger than %d bytes", relPath, branch, maxBytes)
	}
	content, err = runGit(ctx, defaultBranchFetchTimeout, dir, "cat-file", "blob", object)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s on default branch %q failed: %w", relPath, branch, err)
	}
	return content, true, nil
}
