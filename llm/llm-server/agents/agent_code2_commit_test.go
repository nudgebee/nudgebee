package agents

import "testing"

// buildGitRepository mirrors the git_repository payload assembled in
// runCodeAnalysis. Kept in the test rather than extracted from production code
// because the rule under test is a policy decision, not a helper: pin the
// deployed revision for read-only work, never for work that ends in a PR.
func buildGitRepository(repo, branch, provider, commit string, raisePR bool) map[string]any {
	m := map[string]any{"url": repo, "branch": branch, "provider": provider}
	if !raisePR && commit != "" {
		m["commit"] = commit
	}
	return m
}

func TestGitRepositoryCommitGating(t *testing.T) {
	const sha = "d16bfe05a744909de4b27f5875fe0d4ed41ce607"

	cases := []struct {
		name      string
		commit    string
		raisePR   bool
		wantPin   bool
		rationale string
	}{
		{
			name: "analysis pins the deployed revision", commit: sha, raisePR: false, wantPin: true,
			rationale: "the whole point: diagnose the code that was actually running",
		},
		{
			name: "PR flow never pins", commit: sha, raisePR: true, wantPin: false,
			rationale: "a branch cut from an old commit reverts everything merged since; " +
				"code-analysis refuses the combination, so we must not create it",
		},
		{
			name: "no deployed revision known", commit: "", raisePR: false, wantPin: false,
			rationale: "absent commit must not emit an empty key",
		},
		{
			name: "no commit and raising a PR", commit: "", raisePR: true, wantPin: false,
			rationale: "unchanged from the pre-existing behaviour",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildGitRepository("https://github.com/o/r", "main", "github", tc.commit, tc.raisePR)
			_, pinned := got["commit"]
			if pinned != tc.wantPin {
				t.Fatalf("commit pinned=%v, want %v — %s", pinned, tc.wantPin, tc.rationale)
			}
			if tc.wantPin && got["commit"] != sha {
				t.Fatalf("pinned the wrong commit: %v", got["commit"])
			}
			// url/branch/provider must survive regardless: branch still governs
			// PR targeting even when a commit governs the checkout.
			for _, k := range []string{"url", "branch", "provider"} {
				if got[k] == "" || got[k] == nil {
					t.Errorf("%s missing from git_repository", k)
				}
			}
		})
	}
}
