package agents

import (
	"context"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/planners"
)

// A request-pinned revision must never reach PR creation. This is the cheap,
// deterministic half of the guard — it needs no repo on disk and no remote ref,
// which is why it exists separately from the branch-tip comparison.
func TestRefusePRFromPinnedRevision_RequestPinned(t *testing.T) {
	a := &OrchestratorAgent{}
	sc := &session.SessionContext{
		RepoContext: &planners.RepositoryContext{
			Branch: "main",
			Commit: "d16bfe05a744909de4b27f5875fe0d4ed41ce607",
		},
	}

	err := a.refusePRFromPinnedRevision(context.Background(), sc, t.TempDir(), "main", "abc123")
	if err == nil {
		t.Fatal("expected a refusal when the request pinned a commit, got nil")
	}
	// The message has to name the commit and the way out, or the caller cannot
	// act on it.
	for _, want := range []string{"d16bfe05", "raise_pr=false", "main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}

// The ordinary path must stay open: no pinned commit, and a base ref that does
// not resolve locally (the normal case for these single-branch clones) is not
// evidence of staleness.
func TestRefusePRFromPinnedRevision_UnpinnedProceeds(t *testing.T) {
	a := &OrchestratorAgent{}
	sc := &session.SessionContext{
		RepoContext: &planners.RepositoryContext{Branch: "main"},
	}

	if err := a.refusePRFromPinnedRevision(context.Background(), sc, t.TempDir(), "main", "abc123"); err != nil {
		t.Fatalf("unpinned analysis should be allowed to open a PR, got: %v", err)
	}
}

// A nil session must not panic on the PR path.
func TestRefusePRFromPinnedRevision_NilSession(t *testing.T) {
	a := &OrchestratorAgent{}
	if err := a.refusePRFromPinnedRevision(context.Background(), nil, t.TempDir(), "main", ""); err != nil {
		t.Fatalf("nil session should not refuse, got: %v", err)
	}
}
