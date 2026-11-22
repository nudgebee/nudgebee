package git

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const setupScriptPath = ".nudgebee/setup.sh"

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed:\n%s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func hasCommit(dir, commit string) bool {
	cmd := exec.Command("git", "cat-file", "-e", commit+"^{commit}")
	cmd.Dir = dir
	return cmd.Run() == nil
}

func commitFile(t *testing.T, repo, relPath, content, message string) {
	t.Helper()
	full := filepath.Join(repo, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", message)
}

// originWithFeatureBranch returns the file:// URL of a repository whose default
// branch is main and which also has a `feature` branch. main gets more commits
// after `feature` is cut, so a single-branch clone of `feature` does not hold
// main's tip. The URL form matters: a clone from a plain local path hard-links
// the whole object store and would hold every commit.
func originWithFeatureBranch(t *testing.T, onMain, onFeature map[string]string) (url, dir string) {
	t.Helper()
	dir = t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "test@example.com")
	gitIn(t, dir, "config", "user.name", "test")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	gitIn(t, dir, "config", "tag.gpgsign", "false")
	commitFile(t, dir, "README.md", "base\n", "base")

	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	for relPath, content := range onFeature {
		commitFile(t, dir, relPath, content, "feature: "+relPath)
	}

	gitIn(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "main.txt", "main moved on\n", "main moves ahead")
	for relPath, content := range onMain {
		commitFile(t, dir, relPath, content, "main: "+relPath)
	}
	return "file://" + dir, dir
}

func cloneBranch(t *testing.T, originURL, branch string, extraArgs ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	args := append([]string{"clone", "-q", "--single-branch", "--branch", branch}, extraArgs...)
	gitIn(t, t.TempDir(), append(args, originURL, dir)...)
	return dir
}

// readDefault is the lookup a caller does: ask the remote for its default
// branch, then read the file at that tip.
func readDefault(t *testing.T, repoDir, originURL, relPath string, maxBytes int64) (content string, found bool, tip string, err error) {
	t.Helper()
	branch, tip, err := RemoteDefaultBranch(context.Background(), originURL)
	require.NoError(t, err)
	require.Equal(t, "main", branch)
	raw, found, err := ReadBranchFileAtCommit(context.Background(), repoDir, originURL, branch, tip, relPath, maxBytes)
	return string(raw), found, tip, err
}

func TestReadBranchFileAtCommit_ReadsDefaultBranchNotTheCheckedOutOne(t *testing.T) {
	originURL, _ := originWithFeatureBranch(t,
		map[string]string{setupScriptPath: "echo from-main\n"},
		map[string]string{setupScriptPath: "echo from-feature\n"})
	clone := cloneBranch(t, originURL, "feature")

	content, found, tip, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo from-main\n", content)
	assert.Equal(t, "echo from-feature\n", readFile(t, filepath.Join(clone, setupScriptPath)), "the working tree is untouched")
	assert.False(t, hasCommit(clone, tip), "the default branch was not fetched into the checkout")
}

func TestReadBranchFileAtCommit_TipAlreadyInTheCheckout(t *testing.T) {
	originURL, _ := originWithFeatureBranch(t, map[string]string{setupScriptPath: "echo from-main\n"}, nil)
	clone := cloneBranch(t, originURL, "main")

	content, found, tip, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo from-main\n", content)
	assert.True(t, hasCommit(clone, tip))
}

func TestReadBranchFileAtCommit_FileOnlyOnAnotherBranchIsNotFound(t *testing.T) {
	originURL, _ := originWithFeatureBranch(t, nil, map[string]string{setupScriptPath: "echo from-feature\n"})
	clone := cloneBranch(t, originURL, "feature")

	content, found, _, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, content)
}

// A tag can be pushed by someone who cannot merge to a protected default
// branch. `git fetch origin main` would resolve to a tag of that name first.
func TestReadBranchFileAtCommit_TagNamedLikeTheDefaultBranchIsNotRead(t *testing.T) {
	originURL, originDir := originWithFeatureBranch(t, map[string]string{setupScriptPath: "echo from-main\n"}, nil)
	gitIn(t, originDir, "checkout", "-q", "feature")
	commitFile(t, originDir, setupScriptPath, "echo from-a-tag\n", "what the tag points at")
	gitIn(t, originDir, "tag", "main")
	gitIn(t, originDir, "checkout", "-q", "main")
	clone := cloneBranch(t, originURL, "feature")

	content, found, _, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo from-main\n", content)
}

// A fetch into a shallow clone cuts its history at the fetched commit, which
// breaks a later merge with that branch.
func TestReadBranchFileAtCommit_ShallowCloneIsLeftAsItWas(t *testing.T) {
	originURL, _ := originWithFeatureBranch(t, map[string]string{setupScriptPath: "echo from-main\n"}, nil)
	clone := cloneBranch(t, originURL, "feature", "--depth", "1")
	require.Equal(t, "true", gitIn(t, clone, "rev-parse", "--is-shallow-repository"))
	shallowBefore := readFile(t, filepath.Join(clone, ".git", "shallow"))
	refsBefore := gitIn(t, clone, "for-each-ref", "--format=%(refname) %(objectname)")

	content, found, tip, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo from-main\n", content)
	assert.Equal(t, shallowBefore, readFile(t, filepath.Join(clone, ".git", "shallow")))
	assert.Equal(t, refsBefore, gitIn(t, clone, "for-each-ref", "--format=%(refname) %(objectname)"))
	assert.False(t, hasCommit(clone, tip))
}

// The shared clone is bare and tracks only the branches requests asked for.
// With the default branch among them, a fetch from a run's worktree would move
// the shared remote-tracking ref while another clone may be fetching it.
func TestReadBranchFileAtCommit_SharedBareCloneIsLeftAsItWas(t *testing.T) {
	originURL, originDir := originWithFeatureBranch(t, map[string]string{setupScriptPath: "echo v1\n"}, nil)
	base := filepath.Join(t.TempDir(), "base")
	gitIn(t, t.TempDir(), "clone", "-q", "--bare", "--single-branch", "--branch", "feature", originURL, base)
	gitIn(t, base, "config", "remote.origin.fetch", "+refs/heads/feature:refs/remotes/origin/feature")
	gitIn(t, base, "remote", "set-branches", "--add", "origin", "main")
	gitIn(t, base, "fetch", "-q", "origin")
	worktree := filepath.Join(t.TempDir(), "wt")
	gitIn(t, base, "worktree", "add", "-q", "--detach", worktree, "feature")

	// The default branch moves on after the shared clone last fetched it.
	commitFile(t, originDir, setupScriptPath, "echo v2\n", "main: new setup script")
	refsBefore := gitIn(t, base, "for-each-ref", "--format=%(refname) %(objectname)")

	content, found, tip, err := readDefault(t, worktree, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo v2\n", content, "the current tip is read, not the stale tracking ref")
	assert.Equal(t, refsBefore, gitIn(t, base, "for-each-ref", "--format=%(refname) %(objectname)"), "no ref in the shared clone moved")
	assert.False(t, hasCommit(base, tip), "nothing was fetched into the shared clone")
	assert.Equal(t, "false", gitIn(t, base, "rev-parse", "--is-shallow-repository"))
}

func TestReadBranchFileAtCommit_RefusesWhatIsNotARegularSmallFile(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		originURL, originDir := originWithFeatureBranch(t, nil, nil)
		require.NoError(t, os.MkdirAll(filepath.Join(originDir, ".nudgebee"), 0755))
		require.NoError(t, os.Symlink("../README.md", filepath.Join(originDir, setupScriptPath)))
		gitIn(t, originDir, "add", ".")
		gitIn(t, originDir, "commit", "-q", "-m", "symlinked setup script")
		clone := cloneBranch(t, originURL, "feature")

		_, found, _, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
		assert.False(t, found)
		assert.ErrorContains(t, err, "not a regular file")
	})
	t.Run("a directory", func(t *testing.T) {
		originURL, _ := originWithFeatureBranch(t, map[string]string{setupScriptPath + "/inner": "x\n"}, nil)
		clone := cloneBranch(t, originURL, "feature")

		_, found, _, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
		assert.False(t, found)
		assert.ErrorContains(t, err, "not a regular file")
	})
	t.Run("too large", func(t *testing.T) {
		originURL, _ := originWithFeatureBranch(t, map[string]string{setupScriptPath: strings.Repeat("x", 2000)}, nil)
		clone := cloneBranch(t, originURL, "feature")

		_, found, _, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
		assert.False(t, found)
		assert.ErrorContains(t, err, "larger than 1024 bytes")
	})
	t.Run("path outside the repository", func(t *testing.T) {
		originURL, _ := originWithFeatureBranch(t, nil, nil)
		clone := cloneBranch(t, originURL, "main")
		for _, relPath := range []string{".", "..", "../setup.sh", "/etc/passwd", "a/../../b", "a//b"} {
			_, found, _, err := readDefault(t, clone, originURL, relPath, 1024)
			assert.False(t, found, relPath)
			assert.ErrorContains(t, err, "invalid path", relPath)
		}
	})
}

func TestRemoteDefaultBranch_UnreachableRemoteIsAnError(t *testing.T) {
	_, _, err := RemoteDefaultBranch(context.Background(), "file://"+filepath.Join(t.TempDir(), "gone"))
	assert.ErrorContains(t, err, "default branch lookup failed")
}

func TestOriginURL(t *testing.T) {
	originURL, _ := originWithFeatureBranch(t, nil, nil)
	clone := cloneBranch(t, originURL, "main")
	got, err := OriginURL(context.Background(), clone)
	require.NoError(t, err)
	assert.Equal(t, originURL, got)
}

// The comparison decides whether a repository's code may run, so it must never
// call two different repositories the same. Calling one repository different
// only turns the feature off.
func TestSameRepository(t *testing.T) {
	same := [][2]string{
		{"https://github.com/acme/widgets", "https://github.com/acme/widgets.git"},
		{"https://github.com/acme/widgets", "https://github.com/acme/widgets/"},
		{"https://github.com/acme/widgets", "https://x-access-token:secret@github.com/acme/widgets.git"},
		{"https://github.com/acme/widgets", "git@github.com:acme/widgets.git"},
		{"https://github.com/acme/widgets", "ssh://git@github.com/acme/widgets.git"},
		{"https://github.com/acme/widgets", "ssh://git@github.com:22/acme/widgets"},
		{"https://github.com/acme/widgets", "http://github.com/acme/widgets"},
		{"https://github.com/acme/widgets", "https://GitHub.com/Acme/Widgets"},
		{"https://github.com:443/acme/widgets", "https://github.com/acme/widgets"},
		{"https://gitlab.com/acme/platform/api", "git@gitlab.com:acme/platform/api.git"},
		{"file:///srv/git/widgets", "file:///srv/git/widgets"},
	}
	for _, pair := range same {
		assert.Truef(t, SameRepository(pair[0], pair[1]), "%s and %s are the same repository", pair[0], pair[1])
		assert.Truef(t, SameRepository(pair[1], pair[0]), "%s and %s are the same repository", pair[1], pair[0])
	}

	different := [][2]string{
		{"https://github.com/acme/widgets", "https://github.com/acme/other"},
		{"https://github.com/acme/widgets", "https://gitlab.com/acme/widgets"},
		// Separators must not be interchangeable.
		{"https://gitlab.com/acme/data_pipeline", "https://gitlab.com/acme_data/pipeline"},
		{"https://gitlab.com/acme/platform/api", "https://gitlab.com/acme/platform_api"},
		{"https://gitlab.com/acme/platform/api", "https://gitlab.com/acme_platform/api"},
		// A port is part of which server it is, not part of the path.
		{"https://git.example.com:8443/a/b", "https://git.example.com/8443/a/b"},
		{"https://git.example.com:8443/a/b", "https://git.example.com:9443/a/b"},
		{"https://git.example.com:8443/a/b", "https://git.example.com/a/b"},
		{"file:///srv/git/widgets", "file:///srv/git/other"},
		{"/srv/git/widgets", "/srv/git/other"},
		// Nothing compares equal to something that names no repository.
		{"", ""},
		{"https://", "https://"},
		{"https://github.com", "https://github.com/"},
		{"https://github.com/acme/widgets", ""},
	}
	for _, pair := range different {
		assert.Falsef(t, SameRepository(pair[0], pair[1]), "%q and %q are not the same repository", pair[0], pair[1])
		assert.Falsef(t, SameRepository(pair[1], pair[0]), "%q and %q are not the same repository", pair[1], pair[0])
	}
}

// Hosted git servers honour the blob filter, which makes the file read below a
// fetch of its own. A file:// origin ignores the filter unless told to allow it.
func TestReadBranchFileAtCommit_OriginThatHonoursTheBlobFilter(t *testing.T) {
	originURL, originDir := originWithFeatureBranch(t, map[string]string{setupScriptPath: "echo from-main\n"}, nil)
	gitIn(t, originDir, "config", "uploadpack.allowFilter", "true")
	gitIn(t, originDir, "config", "uploadpack.allowAnySHA1InWant", "true")
	clone := cloneBranch(t, originURL, "feature")

	content, found, tip, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo from-main\n", content)
	assert.False(t, hasCommit(clone, tip))
}

// Any earlier command in the workspace can add a replace ref to the shared
// clone. It must not change what is read for the default branch tip.
func TestReadBranchFileAtCommit_ReplaceRefIsIgnored(t *testing.T) {
	originURL, _ := originWithFeatureBranch(t,
		map[string]string{setupScriptPath: "echo from-main\n"},
		map[string]string{setupScriptPath: "echo from-feature\n"})
	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, t.TempDir(), "clone", "-q", originURL, clone)
	mainTip := gitIn(t, clone, "rev-parse", "origin/main")
	gitIn(t, clone, "replace", mainTip, gitIn(t, clone, "rev-parse", "origin/feature"))
	require.Equal(t, "echo from-feature", gitIn(t, clone, "show", mainTip+":"+setupScriptPath), "the replace ref is in effect for plain git")

	content, found, _, err := readDefault(t, clone, originURL, setupScriptPath, 1024)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "echo from-main\n", content)
}

func TestRemoteDefaultBranch_ErrorCarriesNoCredentials(t *testing.T) {
	// Port 1 refuses at once; git reports the URL it could not reach.
	_, _, err := RemoteDefaultBranch(context.Background(), "http://someone:s3cr3t-token@127.0.0.1:1/acme/widgets.git")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cr3t-token")
	assert.NotContains(t, err.Error(), "someone")
}

// A remote that accepts the connection and then says nothing must not hold the
// call past its deadline: git's transport helper keeps the output pipe open
// after git itself has been killed.
func TestRemoteDefaultBranch_StalledRemoteDoesNotOutliveTheDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }() // held open, never answered
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, _, err = RemoteDefaultBranch(ctx, "http://"+listener.Addr().String()+"/acme/widgets.git")
	require.Error(t, err)
	assert.Less(t, time.Since(started), 10*time.Second)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}
