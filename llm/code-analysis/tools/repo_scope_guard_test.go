package tools

import (
	"os/exec"
	"testing"
)

// newRepoDir creates a throwaway git repo whose origin points at remoteURL.
func newRepoDir(t *testing.T, remoteURL string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", remoteURL},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestIsOutOfScopePRLookup(t *testing.T) {
	const scoped = "https://github.com/nudgebee/nudgebee-enterprise.git"

	tests := []struct {
		name        string
		command     string
		wantBlocked bool
	}{
		{
			// The exact command that produced the metabase answer.
			name:        "foreign repo pulls api",
			command:     "curl -s https://api.github.com/repos/metabase/metabase/pulls/35094",
			wantBlocked: true,
		},
		{
			name:        "foreign repo pull web url",
			command:     "curl -sL https://github.com/metabase/metabase/pull/35094",
			wantBlocked: true,
		},
		{
			name:        "foreign repo issue",
			command:     "curl -s https://api.github.com/repos/other/proj/issues/12",
			wantBlocked: true,
		},
		{
			name:        "in-scope repo is allowed",
			command:     "curl -s https://api.github.com/repos/nudgebee/nudgebee-enterprise/pulls/35094",
			wantBlocked: false,
		},
		{
			name:        "in-scope repo case insensitive",
			command:     "curl -s https://api.github.com/repos/NudgeBee/NudgeBee-Enterprise/pulls/1",
			wantBlocked: false,
		},
		{
			// Reading another project's source is legitimate research.
			name:        "foreign raw file read is allowed",
			command:     "curl -s https://raw.githubusercontent.com/metabase/metabase/master/README.md",
			wantBlocked: false,
		},
		{
			name:        "foreign repo contents api is allowed",
			command:     "curl -s https://api.github.com/repos/metabase/metabase/contents/README.md",
			wantBlocked: false,
		},
		{
			// `gh api` omits the host, so a host-anchored matcher misses it
			// entirely — and it is the idiomatic way to reach the API.
			name:        "gh api foreign repo",
			command:     "gh api repos/metabase/metabase/pulls/35094",
			wantBlocked: true,
		},
		{
			name:        "gh api foreign repo with leading slash",
			command:     "gh api /repos/metabase/metabase/pulls/35094",
			wantBlocked: true,
		},
		{
			name:        "gh api in-scope repo",
			command:     "gh api repos/nudgebee/nudgebee-enterprise/pulls/35094",
			wantBlocked: false,
		},
		{
			name:        "gh pr view with foreign --repo",
			command:     "gh pr view 35094 --repo metabase/metabase --json title,state",
			wantBlocked: true,
		},
		{
			name:        "gh issue view with foreign -R",
			command:     "gh issue view 12 -R other/proj",
			wantBlocked: true,
		},
		{
			name:        "gh pr view with in-scope --repo",
			command:     "gh pr view 35094 --repo nudgebee/nudgebee-enterprise",
			wantBlocked: false,
		},
		{
			// No PR/issue involved — cloning or inspecting another repo is fine.
			name:        "gh repo view foreign is allowed",
			command:     "gh repo view --repo metabase/metabase",
			wantBlocked: false,
		},
		{
			name:        "gh pr view without --repo resolves from cwd",
			command:     "gh pr view 35094 --json comments,reviews",
			wantBlocked: false,
		},
		{
			name:        "unrelated command",
			command:     "go build ./...",
			wantBlocked: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepoDir(t, scoped)
			var guard repoScopeGuard
			blocked, reason := guard.checkCommand(tc.command, dir)
			if blocked != tc.wantBlocked {
				t.Fatalf("blocked = %v (reason %q), want %v", blocked, reason, tc.wantBlocked)
			}
			if blocked && reason == "" {
				t.Fatal("blocked with an empty reason; the agent needs to be told why")
			}
		})
	}
}

// A checkout whose repo we cannot determine must not block anything — the guard
// is a correctness aid, not a sandbox, and failing closed would break analyses
// that legitimately run outside a git checkout.
func TestIsOutOfScopePRLookupFailsOpen(t *testing.T) {
	dir := t.TempDir() // no git repo, no origin
	var guard repoScopeGuard
	blocked, _ := guard.checkCommand(
		"curl -s https://api.github.com/repos/metabase/metabase/pulls/35094", dir)
	if blocked {
		t.Fatal("blocked despite the scope repo being undeterminable; guard must fail open")
	}
}

func TestResolveScopeRepoRemoteForms(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"https://github.com/nudgebee/nudgebee-enterprise.git", "nudgebee/nudgebee-enterprise"},
		{"https://github.com/nudgebee/nudgebee-enterprise", "nudgebee/nudgebee-enterprise"},
		{"git@github.com:nudgebee/nudgebee-enterprise.git", "nudgebee/nudgebee-enterprise"},
		{"https://gitlab.com/nudgebee/other.git", ""},
	}
	for _, tc := range tests {
		t.Run(tc.remote, func(t *testing.T) {
			dir := newRepoDir(t, tc.remote)
			var guard repoScopeGuard
			if got := guard.resolveScopeRepo(dir); got != tc.want {
				t.Fatalf("resolveScopeRepo() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A PR body quoting another repository is prose, not a lookup. Our own generated
// descriptions cite the PRs they came from, so without this the orchestrator
// could not open a PR at all.
func TestGhCommandForScopeCheckDropsContentFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "body value dropped",
			args: []string{"pr", "create", "--title", "fix: thing", "--body", "supersedes https://github.com/metabase/metabase/pull/1"},
			want: "gh pr create",
		},
		{
			name: "equals form dropped",
			args: []string{"pr", "create", "--body=see repos/other/proj/pulls/9"},
			want: "gh pr create",
		},
		{
			name: "rest field values dropped",
			args: []string{"api", "--method", "PATCH", "repos/nudgebee/app/pulls/7", "-f", "body=cf repos/x/y/issues/3"},
			want: "gh api --method PATCH repos/nudgebee/app/pulls/7",
		},
		{
			name: "targets are kept",
			args: []string{"pr", "view", "35094", "--repo", "metabase/metabase"},
			want: "gh pr view 35094 --repo metabase/metabase",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ghCommandForScopeCheck(tc.args); got != tc.want {
				t.Fatalf("ghCommandForScopeCheck() = %q, want %q", got, tc.want)
			}
		})
	}
}

// End-to-end on the two shapes that matter: creating a PR whose body cites a
// foreign PR is allowed; viewing a foreign PR is not.
func TestGuardAllowsPRCreateWithForeignLinkInBody(t *testing.T) {
	dir := newRepoDir(t, "https://github.com/nudgebee/nudgebee-enterprise.git")

	var guard repoScopeGuard
	body := []string{"pr", "create", "--body", "context: https://github.com/metabase/metabase/pull/35094"}
	if blocked, reason := guard.checkCommand(ghCommandForScopeCheck(body), dir); blocked {
		t.Fatalf("blocked PR creation over a link in the body: %s", reason)
	}

	var guard2 repoScopeGuard
	view := []string{"pr", "view", "35094", "--repo", "metabase/metabase"}
	if blocked, _ := guard2.checkCommand(ghCommandForScopeCheck(view), dir); !blocked {
		t.Fatal("did not block a foreign PR view")
	}
}

func TestGuardBlocksAngleBracketWrappedForeignPRURL(t *testing.T) {
	dir := newRepoDir(t, "https://github.com/nudgebee/nudgebee-enterprise.git")

	var guard repoScopeGuard
	command := "curl <https://github.com/metabase/metabase/pull/35094>"
	if blocked, _ := guard.checkCommand(command, dir); !blocked {
		t.Fatal("did not block an angle-bracket-wrapped foreign PR URL")
	}
}
