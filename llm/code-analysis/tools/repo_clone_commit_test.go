package tools

import "testing"

// The commit default is seeded per request by the orchestrator, on a tool that
// is long-lived and reused across requests. These cover the two ways that goes
// wrong: an explicit call losing to the seed, and a stale seed leaking into the
// next request.
func TestRepoCloneTool_DefaultCommit(t *testing.T) {
	tool := &RepoCloneTool{}

	if got := tool.DefaultCommit(); got != "" {
		t.Fatalf("a fresh tool should have no default commit, got %q", got)
	}

	const sha = "d16bfe05a744909de4b27f5875fe0d4ed41ce607"
	tool.SetDefaultCommit(sha)
	if got := tool.DefaultCommit(); got != sha {
		t.Fatalf("DefaultCommit() = %q, want %q", got, sha)
	}

	// Reset must clear, not no-op. The orchestrator seeds "" when a request has
	// no pinned commit; if that were ignored the tool would silently reuse the
	// previous request's revision and analyse the wrong code.
	tool.SetDefaultCommit("")
	if got := tool.DefaultCommit(); got != "" {
		t.Fatalf("DefaultCommit() = %q after reset, want empty", got)
	}
}

// The seed is a fallback, not an override: a caller that names a commit must win.
func TestRepoCloneInput_ExplicitCommitBeatsDefault(t *testing.T) {
	tool := &RepoCloneTool{}
	tool.SetDefaultCommit("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	params := RepoCloneInput{Commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if params.Commit == "" && tool.DefaultCommit() != "" {
		params.Commit = tool.DefaultCommit()
	}
	if params.Commit != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("explicit commit was overwritten by the default: %q", params.Commit)
	}

	empty := RepoCloneInput{}
	if empty.Commit == "" && tool.DefaultCommit() != "" {
		empty.Commit = tool.DefaultCommit()
	}
	if empty.Commit != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("omitted commit did not fall back to the default: %q", empty.Commit)
	}
}
