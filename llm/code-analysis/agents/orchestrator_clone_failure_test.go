package agents

import (
	"errors"
	"strings"
	"testing"

	"nudgebee/code-analysis-agent/common"
)

// A fix-mode run whose clone never succeeded must not be reported as a no-op.
//
// The failure this guards, observed on the test env: the tenant had two enabled
// GitHub integrations and the clone ran with the one that does not cover the
// repo, so GitHub answered "Repository not found". The specialist could read
// nothing and abstained with requires_fix=false, which the orchestrator stamped
// as no_op — surfaced to the user as "the requested change is already present in
// the repository" with the resolution marked Success. Three consecutive
// rightsizing runs reported success while nothing had ever been read.
func TestRepoCloneFailureDetection(t *testing.T) {
	cases := []struct {
		name        string
		invocations []struct {
			tool   string
			status string
			err    error
		}
		wantFailed bool
		wantReason string
	}{
		{
			name: "clone attempted and never succeeded",
			invocations: []struct {
				tool   string
				status string
				err    error
			}{
				{"repo_clone", "error", errors.New("remote: Repository not found")},
			},
			wantFailed: true,
			wantReason: "Repository not found",
		},
		{
			name: "a later clone succeeded, so the run is fine",
			invocations: []struct {
				tool   string
				status string
				err    error
			}{
				{"repo_clone", "error", errors.New("transient network error")},
				{"repo_clone", "success", nil},
			},
			wantFailed: false,
		},
		{
			// A local-path analysis never clones. Treating "no clone" as a clone
			// failure would fail every one of those runs.
			name: "clone never attempted",
			invocations: []struct {
				tool   string
				status string
				err    error
			}{
				{"file_view", "success", nil},
			},
			wantFailed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := common.NewToolInvocationTracker("test-analysis")
			for _, inv := range tc.invocations {
				id := tracker.StartInvocation(inv.tool, map[string]any{})
				tracker.CompleteInvocation(id, nil, inv.status, inv.err)
			}

			a := &OrchestratorAgent{toolTracker: tracker}
			reason, failed := a.repoCloneFailure()

			if failed != tc.wantFailed {
				t.Fatalf("repoCloneFailure() failed = %v, want %v (reason %q)", failed, tc.wantFailed, reason)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason should carry the git error %q; got %q", tc.wantReason, reason)
			}
		})
	}
}

// Without a tracker there is no evidence either way — never claim a failure.
func TestRepoCloneFailureWithoutTracker(t *testing.T) {
	a := &OrchestratorAgent{}
	if _, failed := a.repoCloneFailure(); failed {
		t.Error("a missing tool tracker must not be reported as a clone failure")
	}
}
