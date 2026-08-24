package agents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/gitprovider"
)

// baseSyncGit runs a git command against dir with a hermetic config, failing
// the test on error. Mirrors pushProbeGitCmd in the push-verification test.
func baseSyncGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// baseSyncGitAllowFail is baseSyncGit for commands expected to fail.
func baseSyncGitAllowFail(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func baseSyncWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// baseSyncFixture builds a bare origin holding a `main` base branch and a
// `feature` PR branch forked from it, plus a working clone of `feature`.
// Returns the origin path and the clone path.
func baseSyncFixture(t *testing.T) (origin, clone string) {
	t.Helper()
	root := t.TempDir()

	// origin must be bare — pushing to a checked-out branch of a non-bare repo
	// is refused by git. Build it from a throwaway seed working tree.
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0755); err != nil {
		t.Fatal(err)
	}
	baseSyncGit(t, seed, "init", "-q", "-b", "main")
	baseSyncWrite(t, seed, "shared.txt", "line one\n")
	baseSyncWrite(t, seed, "untouched.txt", "stable\n")
	baseSyncGit(t, seed, "add", ".")
	baseSyncGit(t, seed, "commit", "-qm", "base commit")

	// Fork the PR branch here, so both sides share this commit as merge base.
	baseSyncGit(t, seed, "checkout", "-q", "-b", "feature")
	baseSyncWrite(t, seed, "feature.txt", "pr work\n")
	baseSyncGit(t, seed, "add", ".")
	baseSyncGit(t, seed, "commit", "-qm", "pr commit")
	baseSyncGit(t, seed, "checkout", "-q", "main")

	origin = filepath.Join(root, "origin")
	baseSyncGit(t, root, "clone", "-q", "--bare", seed, origin)

	clone = filepath.Join(root, "clone")
	baseSyncGit(t, root, "clone", "-q", "--branch", "feature", origin, clone)
	return origin, clone
}

// advanceBase pushes a new commit onto origin's main, so the PR branch falls
// behind. Content is written to path so callers can choose whether it collides
// with the PR branch's own edits.
func advanceBase(t *testing.T, origin, path, content, message string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "advance")
	baseSyncGit(t, root, "clone", "-q", "--branch", "main", origin, work)
	baseSyncWrite(t, work, path, content)
	baseSyncGit(t, work, "add", ".")
	baseSyncGit(t, work, "commit", "-qm", message)
	baseSyncGit(t, work, "push", "-q", "origin", "main")
}

func newBaseSyncAgent(clone string) *PRFollowupAgent {
	return &PRFollowupAgent{
		workspaceDir: clone,
		logger:       common.NewLogger("test", "test/repo", "", nil),
		provider:     gitprovider.ParseProvider("github"),
	}
}

// A PR branch that is already current must not produce a merge commit. Without
// this the agent would push an empty merge on every cron tick.
func TestSyncWithBaseUpToDateIsNoOp(t *testing.T) {
	_, clone := baseSyncFixture(t)
	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")

	before := strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD"))
	sync := a.syncWithBase("main", "feature")

	if sync.merged || sync.conflicted {
		t.Fatalf("expected no merge on an up-to-date branch, got %+v", sync)
	}
	if sync.behindBy != 0 {
		t.Fatalf("expected behindBy=0, got %d", sync.behindBy)
	}
	if after := strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD")); after != before {
		t.Fatalf("HEAD moved on an up-to-date branch: %s -> %s", before, after)
	}
}

// The core case: base moved, the merge is clean, and the merge commit must
// reach the remote. A local-only merge is the #36634 failure mode and must not
// be reported as merged.
func TestSyncWithBaseCleanMergePushes(t *testing.T) {
	origin, clone := baseSyncFixture(t)
	advanceBase(t, origin, "untouched.txt", "stable\nplus base work\n", "base moves on")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")

	sync := a.syncWithBase("main", "feature")

	if !sync.merged {
		t.Fatalf("expected a clean merge, got %+v", sync)
	}
	if sync.conflicted {
		t.Fatal("clean merge must not report conflicts")
	}
	if sync.behindBy != 1 {
		t.Fatalf("expected behindBy=1, got %d", sync.behindBy)
	}
	if sync.commitHash == "" {
		t.Fatal("expected a commit hash for the merge")
	}

	// The merge must actually be on the remote, not just local.
	if !a.headPushedToRemote(sync.commitHash, "feature") {
		t.Fatal("merge commit did not reach the remote")
	}
	// And it must be a real merge commit (two parents), not a fast-forward or
	// a squash — the PR's own commit has to survive.
	parents := strings.Fields(baseSyncGit(t, clone, "rev-list", "--parents", "-n", "1", "HEAD"))
	if len(parents) != 3 {
		t.Fatalf("expected a merge commit with two parents, got %d parent(s): %v", len(parents)-1, parents)
	}
	// Base content must now be present on the PR branch.
	got := baseSyncGit(t, clone, "show", "HEAD:untouched.txt")
	if !strings.Contains(got, "plus base work") {
		t.Fatalf("base content missing after merge: %q", got)
	}
	// No merge left dangling.
	if a.mergeInProgress() {
		t.Fatal("merge should be complete, not in progress")
	}
}

// A conflicting base change must leave the merge OPEN with the conflicted paths
// reported, so Execute can hand them to the planner. Aborting here would strand
// the PR exactly as before the feature existed.
func TestSyncWithBaseConflictLeavesMergeOpen(t *testing.T) {
	origin, clone := baseSyncFixture(t)

	// Both sides edit shared.txt, so the merge cannot auto-resolve.
	baseSyncGit(t, clone, "config", "user.email", "t@t")
	baseSyncGit(t, clone, "config", "user.name", "t")
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\n")
	baseSyncGit(t, clone, "commit", "-qam", "pr edits shared file")
	baseSyncGit(t, clone, "push", "-q", "origin", "feature")

	advanceBase(t, origin, "shared.txt", "line one\nbase edit\n", "base edits shared file")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")

	sync := a.syncWithBase("main", "feature")

	if !sync.conflicted {
		t.Fatalf("expected a conflicted merge, got %+v", sync)
	}
	if sync.merged {
		t.Fatal("a conflicted merge must not report merged")
	}
	if len(sync.conflictFiles) != 1 || sync.conflictFiles[0] != "shared.txt" {
		t.Fatalf("expected shared.txt to be the conflict, got %v", sync.conflictFiles)
	}
	if !a.mergeInProgress() {
		t.Fatal("merge must be left in progress for the agent to resolve")
	}
}

// finalizeMerge commits and pushes once the conflicts are resolved.
func TestFinalizeMergeCommitsResolvedConflicts(t *testing.T) {
	origin, clone := baseSyncFixture(t)
	baseSyncGit(t, clone, "config", "user.email", "t@t")
	baseSyncGit(t, clone, "config", "user.name", "t")
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\n")
	baseSyncGit(t, clone, "commit", "-qam", "pr edits shared file")
	baseSyncGit(t, clone, "push", "-q", "origin", "feature")
	advanceBase(t, origin, "shared.txt", "line one\nbase edit\n", "base edits shared file")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")
	if sync := a.syncWithBase("main", "feature"); !sync.conflicted {
		t.Fatalf("fixture did not conflict: %+v", sync)
	}

	// Stand in for the planner: resolve the conflict honestly and stage it.
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\nbase edit\n")
	baseSyncGit(t, clone, "add", "shared.txt")

	committed, head := a.finalizeMerge("feature")
	if !committed {
		t.Fatal("expected the resolved merge to be committed")
	}
	if !a.headPushedToRemote(head, "feature") {
		t.Fatal("resolved merge did not reach the remote")
	}
	if a.mergeInProgress() {
		t.Fatal("merge should be finished")
	}
	resolved := baseSyncGit(t, clone, "show", "HEAD:shared.txt")
	if !strings.Contains(resolved, "pr edit") || !strings.Contains(resolved, "base edit") {
		t.Fatalf("resolution did not survive the merge commit: %q", resolved)
	}
}

// git reports a path as unmerged from the INDEX, not file content. The agent
// resolves conflicts by editing files directly (replace/write tools) and does
// not reliably run `git add` afterward — a resolution that is genuinely
// correct must not be thrown away just because the index was never updated.
func TestFinalizeMergeAutoStagesResolvedFileMissingGitAdd(t *testing.T) {
	origin, clone := baseSyncFixture(t)
	baseSyncGit(t, clone, "config", "user.email", "t@t")
	baseSyncGit(t, clone, "config", "user.name", "t")
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\n")
	baseSyncGit(t, clone, "commit", "-qam", "pr edits shared file")
	baseSyncGit(t, clone, "push", "-q", "origin", "feature")
	advanceBase(t, origin, "shared.txt", "line one\nbase edit\n", "base edits shared file")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")
	if sync := a.syncWithBase("main", "feature"); !sync.conflicted {
		t.Fatalf("fixture did not conflict: %+v", sync)
	}

	// Resolve the file on disk exactly as before, but deliberately skip `git
	// add` — the failure mode this test guards against.
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\nbase edit\n")

	committed, head := a.finalizeMerge("feature")
	if !committed {
		t.Fatal("a correct resolution missing only `git add` must still be committed")
	}
	if !a.headPushedToRemote(head, "feature") {
		t.Fatal("resolved merge did not reach the remote")
	}
}

// The auto-stage must stay conservative: a file that still carries a conflict
// marker is never staged, so the merge still aborts exactly as before.
func TestFinalizeMergeStillAbortsWhenMarkersRemain(t *testing.T) {
	origin, clone := baseSyncFixture(t)
	baseSyncGit(t, clone, "config", "user.email", "t@t")
	baseSyncGit(t, clone, "config", "user.name", "t")
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\n")
	baseSyncGit(t, clone, "commit", "-qam", "pr edits shared file")
	baseSyncGit(t, clone, "push", "-q", "origin", "feature")
	advanceBase(t, origin, "shared.txt", "line one\nbase edit\n", "base edits shared file")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")
	if sync := a.syncWithBase("main", "feature"); !sync.conflicted {
		t.Fatalf("fixture did not conflict: %+v", sync)
	}
	beforeHead := strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD"))

	// The agent gave up mid-resolution: markers are still in the file, and
	// `git add` was never run either.
	committed, _ := a.finalizeMerge("feature")

	if committed {
		t.Fatal("must not auto-stage or commit a file that still has conflict markers")
	}
	if a.mergeInProgress() {
		t.Fatal("merge should have been aborted")
	}
	if after := strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD")); after != beforeHead {
		t.Fatalf("abort should restore HEAD: %s -> %s", beforeHead, after)
	}
}

// If the agent leaves conflict markers behind, the merge must be aborted rather
// than committed — a half-resolved tree must never reach the PR.
func TestFinalizeMergeAbortsWhenConflictsRemain(t *testing.T) {
	origin, clone := baseSyncFixture(t)
	baseSyncGit(t, clone, "config", "user.email", "t@t")
	baseSyncGit(t, clone, "config", "user.name", "t")
	baseSyncWrite(t, clone, "shared.txt", "line one\npr edit\n")
	baseSyncGit(t, clone, "commit", "-qam", "pr edits shared file")
	baseSyncGit(t, clone, "push", "-q", "origin", "feature")
	advanceBase(t, origin, "shared.txt", "line one\nbase edit\n", "base edits shared file")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")
	if sync := a.syncWithBase("main", "feature"); !sync.conflicted {
		t.Fatalf("fixture did not conflict: %+v", sync)
	}
	beforeHead := strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD"))

	// The planner gave up: nothing staged, markers still in the file.
	committed, _ := a.finalizeMerge("feature")

	if committed {
		t.Fatal("must not commit a tree that still has unmerged paths")
	}
	if a.mergeInProgress() {
		t.Fatal("merge should have been aborted")
	}
	if after := strings.TrimSpace(baseSyncGit(t, clone, "rev-parse", "HEAD")); after != beforeHead {
		t.Fatalf("abort should restore HEAD: %s -> %s", beforeHead, after)
	}
	if dirty := strings.TrimSpace(baseSyncGit(t, clone, "status", "--porcelain")); dirty != "" {
		t.Fatalf("abort should leave a clean tree, got:\n%s", dirty)
	}
}

// Regression guard for the sharpest hazard in this feature: autoCommitOrDiscard
// hard-resets any tree touching more than autoCommitMaxFiles (15) files. A base
// merge routinely exceeds that with content git itself produced, so a merge must
// never be routed through it — doing so would silently discard AND abort the
// merge, leaving the PR as stuck as before.
func TestFinalizeMergeCommitsMoreFilesThanAutoCommitCap(t *testing.T) {
	origin, clone := baseSyncFixture(t)

	// Move base by far more files than the auto-commit cap allows.
	fileCount := autoCommitMaxFiles * 3
	root := t.TempDir()
	work := filepath.Join(root, "advance")
	baseSyncGit(t, root, "clone", "-q", "--branch", "main", origin, work)
	for i := range fileCount {
		baseSyncWrite(t, work, fmt.Sprintf("bulk/file_%02d.txt", i), fmt.Sprintf("base content %d\n", i))
	}
	baseSyncGit(t, work, "add", ".")
	baseSyncGit(t, work, "commit", "-qm", "large base change")
	baseSyncGit(t, work, "push", "-q", "origin", "main")

	a := newBaseSyncAgent(clone)
	a.ensureBaseRefFetched("main")

	sync := a.syncWithBase("main", "feature")
	if !sync.merged {
		t.Fatalf("a large but clean base merge must still land, got %+v", sync)
	}
	if !a.headPushedToRemote(sync.commitHash, "feature") {
		t.Fatal("large merge did not reach the remote")
	}
	// Every base file must be present — proof nothing was discarded by a cap.
	for i := range fileCount {
		name := fmt.Sprintf("bulk/file_%02d.txt", i)
		if _, err := os.Stat(filepath.Join(clone, name)); err != nil {
			t.Fatalf("%s missing after merge — the file cap discarded it: %v", name, err)
		}
	}
}

// A shallow clone whose window excludes the fork point cannot merge at all
// ("refusing to merge unrelated histories"). ensureMergeBase must widen it.
func TestEnsureMergeBaseDeepensShallowClone(t *testing.T) {
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0755); err != nil {
		t.Fatal(err)
	}
	baseSyncGit(t, seed, "init", "-q", "-b", "main")
	baseSyncWrite(t, seed, "f.txt", "start\n")
	baseSyncGit(t, seed, "add", ".")
	baseSyncGit(t, seed, "commit", "-qm", "fork point")

	// Fork the PR branch at the very first commit...
	baseSyncGit(t, seed, "checkout", "-q", "-b", "feature")
	baseSyncWrite(t, seed, "feature.txt", "pr work\n")
	baseSyncGit(t, seed, "add", ".")
	baseSyncGit(t, seed, "commit", "-qm", "pr commit")

	// ...then push main far past it, so a depth-1 fetch of main cannot see the
	// fork point. This is the production shape: clone --depth 50 of the head
	// branch plus a depth-50 fetch of a base that has moved on since.
	baseSyncGit(t, seed, "checkout", "-q", "main")
	for i := range 12 {
		baseSyncWrite(t, seed, "f.txt", fmt.Sprintf("commit %d\n", i))
		baseSyncGit(t, seed, "commit", "-qam", fmt.Sprintf("base %d", i))
	}

	origin := filepath.Join(root, "origin")
	baseSyncGit(t, root, "clone", "-q", "--bare", seed, origin)

	clone := filepath.Join(root, "clone")
	baseSyncGit(t, root, "clone", "-q", "--depth", "1", "--branch", "feature", origin, clone)
	// Fetch base shallowly, exactly as ensureBaseRefFetched does.
	baseSyncGit(t, clone, "fetch", "--no-tags", "--depth", "1", "origin", "main:refs/remotes/origin/main")

	a := newBaseSyncAgent(clone)

	// Precondition: the shallow window genuinely hides the merge base, so this
	// test would be vacuous without the deepening.
	if a.hasMergeBase("origin/main") {
		t.Skip("shallow window unexpectedly contains the merge base; nothing to deepen")
	}
	// And git really does refuse the merge in this state.
	if _, err := baseSyncGitAllowFail(t, clone, "merge", "--no-edit", "origin/main"); err == nil {
		t.Skip("merge unexpectedly succeeded on the shallow clone")
	}
	_, _ = baseSyncGitAllowFail(t, clone, "merge", "--abort")

	if !a.ensureMergeBase("main", "feature") {
		t.Fatal("ensureMergeBase failed to expose a merge base on a shallow clone")
	}
	if !a.hasMergeBase("origin/main") {
		t.Fatal("merge base still not visible after deepening")
	}
}

// An unknown base or head must be a clean skip, never a merge attempt against
// a bogus ref. resolveBaseBranch returns "" whenever the provider lookup fails.
func TestSyncWithBaseSkipsUnknownRefs(t *testing.T) {
	_, clone := baseSyncFixture(t)
	a := newBaseSyncAgent(clone)

	for _, tc := range []struct{ base, branch string }{
		{"", "feature"},
		{"main", ""},
	} {
		sync := a.syncWithBase(tc.base, tc.branch)
		if sync.skipReason != "unknown_ref" {
			t.Fatalf("base=%q branch=%q: expected unknown_ref, got %+v", tc.base, tc.branch, sync)
		}
		if sync.merged || sync.conflicted {
			t.Fatalf("base=%q branch=%q: must not act on unknown refs", tc.base, tc.branch)
		}
	}
}

// A branch name that looks like a git flag must never reach a git argument.
// Both refs come from the provider API, but ensureBaseRefFetched already holds
// this line for the base ref and syncWithBase puts both into fetch refspecs and
// a push target.
func TestSyncWithBaseRejectsUnsafeRefs(t *testing.T) {
	_, clone := baseSyncFixture(t)
	a := newBaseSyncAgent(clone)

	for _, tc := range []struct{ name, base, branch string }{
		{"base looks like a flag", "--upload-pack=touch /tmp/pwn", "feature"},
		{"head looks like a flag", "main", "--exec=whoami"},
		{"base carries a refspec separator", "main:refs/heads/evil", "feature"},
		{"base carries a range", "main..evil", "feature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sync := a.syncWithBase(tc.base, tc.branch)
			if sync.skipReason != "unsafe_ref" {
				t.Fatalf("expected unsafe_ref, got %+v", sync)
			}
			if sync.merged || sync.conflicted {
				t.Fatal("must not act on an unsafe ref")
			}
		})
	}
}

// isPlainBranchName is an allowlist, not a denylist, so it must reject
// anything outside the real git-ref character set — not just the specific
// shell/flag characters a denylist happens to enumerate. Table covers valid
// real-world branch names alongside inputs a denylist could plausibly miss.
func TestIsPlainBranchNameAllowlist(t *testing.T) {
	valid := []string{
		"main", "feature/nb-36864-pr-followup-base-sync", "release-1.2.3",
		"hotfix/v1.0.0", "a", "dependabot/go_modules/foo-1.2.3",
	}
	for _, name := range valid {
		if !isPlainBranchName(name) {
			t.Errorf("expected %q to be accepted as a plain branch name", name)
		}
	}

	invalid := []string{
		"", "-flag", "main..evil", "refs/heads/main:x",
		"branch;rm -rf /", "branch|whoami", "branch`whoami`", "branch$(whoami)",
		"branch\nrm -rf /", "branch\twith\ttabs", "branch with spaces",
		".starts-with-dot", "ends-with-dot.", "branch~1", "branch^{}", "branch?*",
	}
	for _, name := range invalid {
		if isPlainBranchName(name) {
			t.Errorf("expected %q to be rejected as an unsafe branch name", name)
		}
	}
}

// The conflict prompt has to name every conflicted file and forbid the git
// commands that would strand or destroy the in-progress merge.
func TestBuildSystemPromptConflictSection(t *testing.T) {
	a := newBaseSyncAgent(t.TempDir())
	sync := baseSyncResult{
		behindBy:      4,
		conflicted:    true,
		conflictFiles: []string{"api/server.go", "pkg/util/helper.go"},
	}

	prompt := a.buildSystemPrompt("o/r", "12", "", "", "", "", nil, "main", sync)

	for _, want := range []string{
		"Merge conflicts with main",
		"4 commits behind",
		"api/server.go",
		"pkg/util/helper.go",
		"git merge --abort",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("conflict prompt missing %q", want)
		}
	}

	// And no conflict section at all when the merge was clean.
	clean := a.buildSystemPrompt("o/r", "12", "", "", "", "", nil, "main", baseSyncResult{merged: true})
	if strings.Contains(clean, "Merge conflicts with") {
		t.Error("clean merge must not emit a conflict section")
	}
}
