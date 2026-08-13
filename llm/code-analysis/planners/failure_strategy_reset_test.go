package planners

import "testing"

// The one-time strategy reset restarts the consecutive-failure budget: steps
// before failureForgivenessIndex must not count, so the model gets a full fresh
// budget after the pivot instead of dying on the next single failure.
func TestCountConsecutiveFailuresHonorsForgiveness(t *testing.T) {
	p := &ReActPlanner{}
	fail := func(n int) Step { return Step{Number: n, Action: "cli", Status: "failed"} }
	p.currentSteps = []Step{fail(1), fail(2), fail(3)}

	if got := p.countConsecutiveFailures(); got != 3 {
		t.Fatalf("baseline count = %d, want 3", got)
	}

	// Grant the reset: everything so far is forgiven.
	p.failureForgivenessIndex = len(p.currentSteps)
	if got := p.countConsecutiveFailures(); got != 0 {
		t.Fatalf("post-reset count = %d, want 0", got)
	}

	// New failures after the reset count normally.
	p.currentSteps = append(p.currentSteps, fail(4), fail(5))
	if got := p.countConsecutiveFailures(); got != 2 {
		t.Fatalf("post-reset new failures = %d, want 2", got)
	}

	// A success breaks the chain as before.
	p.currentSteps = append(p.currentSteps, Step{Number: 6, Action: "cli", Status: "completed"}, fail(7))
	if got := p.countConsecutiveFailures(); got != 1 {
		t.Fatalf("after success = %d, want 1", got)
	}
}
