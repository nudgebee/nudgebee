package planners

import "testing"

func TestCheckSubmitGrounding_RejectsAllUngrounded(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	p.recordFileRead("app/components/UserProfile.tsx")

	// Every citation points at a file the agent never opened (the "cite a grep hit
	// you never read" failure mode) → reject.
	input := map[string]any{"citations": []any{
		map[string]any{"file_path": "pkg/scanners/pullsecrets_test.go"},
		map[string]any{"file_path": "runner/pkg/discovery/transform.go"},
	}}
	reject, msg := p.checkSubmitGrounding(input)
	if !reject {
		t.Fatalf("expected ungrounded submission to be rejected")
	}
	if msg == "" {
		t.Fatalf("expected a guidance message on rejection")
	}
}

func TestCheckSubmitGrounding_PassesWhenAnyGrounded(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	p.recordFileRead("app/components/UserProfile.tsx")

	// Mixed: one grounded citation is enough to pass.
	input := map[string]any{"citations": []any{
		map[string]any{"file_path": "app/components/UserProfile.tsx"},
		map[string]any{"file_path": "pkg/x_test.go"},
	}}
	if reject, _ := p.checkSubmitGrounding(input); reject {
		t.Fatalf("expected a submission with a grounded citation to pass")
	}
}

func TestCheckSubmitGrounding_NoCitationsIsOutOfScope(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	if reject, _ := p.checkSubmitGrounding(map[string]any{}); reject {
		t.Fatalf("submission without structured citations should not be rejected here")
	}
}

func TestWasFileRead_SuffixMatch(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	p.recordFileRead("/tmp/clone-123/api/foo.go")
	if !p.wasFileRead("api/foo.go") {
		t.Fatalf("expected relative citation to match absolute read path by suffix")
	}
	if p.wasFileRead("api/bar.go") {
		t.Fatalf("did not expect a non-read file to match")
	}
}

func TestShouldAbstainForced_NoReads(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	if !p.shouldAbstainForced() {
		t.Fatalf("expected abstain when no files were read")
	}
	p.recordFileRead("api/foo.go")
	if p.shouldAbstainForced() {
		t.Fatalf("did not expect abstain once a file was read (non-explore mode)")
	}
}
