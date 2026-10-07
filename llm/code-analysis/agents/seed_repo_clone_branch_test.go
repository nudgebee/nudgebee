package agents

import (
	"testing"

	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/planners"
	"nudgebee/code-analysis-agent/tools"
)

// TestSeedRepoCloneBranch verifies the clone's default branch is seeded from the
// request's target/base branch, while empty/HEAD/SHA-shaped values are skipped so
// the clone falls back to the remote default branch (current behavior).
func TestSeedRepoCloneBranch(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{name: "real branch is seeded", branch: "test", want: "test"},
		{name: "release branch is seeded", branch: "release/1.x", want: "release/1.x"},
		{name: "empty branch skipped", branch: "", want: ""},
		{name: "detached HEAD skipped", branch: "HEAD", want: ""},
		{name: "full sha skipped", branch: "4233af136c6786943072cce32e2371cac4e50d15", want: ""},
		{name: "short sha skipped", branch: "4233af13", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tools.NewRepoCloneTool("/tmp/ws", nil)
			sc := &session.SessionContext{
				RepoContext: &planners.RepositoryContext{Branch: tt.branch},
			}
			seedRepoCloneBranch(tool, sc)
			if got := tool.DefaultBranch(); got != tt.want {
				t.Fatalf("DefaultBranch() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSeedRepoCloneBranch_NilSafe ensures the helper tolerates missing pieces
// without panicking (nil tool, nil session, nil RepoContext).
func TestSeedRepoCloneBranch_NilSafe(t *testing.T) {
	seedRepoCloneBranch(nil, nil)

	tool := tools.NewRepoCloneTool("/tmp/ws", nil)
	seedRepoCloneBranch(tool, nil)
	seedRepoCloneBranch(tool, &session.SessionContext{})
	if got := tool.DefaultBranch(); got != "" {
		t.Fatalf("DefaultBranch() = %q, want empty", got)
	}
}

// TestSeedRepoCloneBranch_NoLeakAcrossRequests verifies the default branch is not
// leaked between requests on a reused tool: after seeding "test", a subsequent
// request with an empty/invalid branch must reset the default back to "" so the
// clone falls back to the remote default branch.
func TestSeedRepoCloneBranch_NoLeakAcrossRequests(t *testing.T) {
	tool := tools.NewRepoCloneTool("/tmp/ws", nil)

	seedRepoCloneBranch(tool, &session.SessionContext{
		RepoContext: &planners.RepositoryContext{Branch: "test"},
	})
	if got := tool.DefaultBranch(); got != "test" {
		t.Fatalf("after first request: DefaultBranch() = %q, want %q", got, "test")
	}

	for _, branch := range []string{"", "HEAD", "4233af13"} {
		seedRepoCloneBranch(tool, &session.SessionContext{
			RepoContext: &planners.RepositoryContext{Branch: branch},
		})
		if got := tool.DefaultBranch(); got != "" {
			t.Fatalf("after request with branch %q: DefaultBranch() = %q, want reset to empty", branch, got)
		}
		// Re-seed a real branch so each iteration starts from a non-empty state.
		seedRepoCloneBranch(tool, &session.SessionContext{
			RepoContext: &planners.RepositoryContext{Branch: "test"},
		})
	}
}

// TestSeedRepoCloneBranch_CommitFromRequestOnly: a run that writes a code change
// takes its revision from the request, so the tool is told to ignore a commit
// the model passes. An analysis is not, and the setting must not survive from a
// change run into the analysis that reuses the tool after it.
func TestSeedRepoCloneBranch_CommitFromRequestOnly(t *testing.T) {
	tool := tools.NewRepoCloneTool("/tmp/ws", nil)

	seedRepoCloneBranch(tool, &session.SessionContext{Mode: ModeFix})
	if !tool.CommitFromRequestOnly() {
		t.Fatal("a fix run must take its commit from the request only")
	}

	seedRepoCloneBranch(tool, &session.SessionContext{Mode: ModeExplore})
	if tool.CommitFromRequestOnly() {
		t.Fatal("an explore run must keep honouring a commit the model passes")
	}

	seedRepoCloneBranch(tool, &session.SessionContext{Mode: ModeFix})
	seedRepoCloneBranch(tool, nil)
	if tool.CommitFromRequestOnly() {
		t.Fatal("the fix-run setting leaked into a request with no session")
	}
}
