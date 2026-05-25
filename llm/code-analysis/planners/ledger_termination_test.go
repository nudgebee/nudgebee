package planners

import "testing"

// canTerminateFromLedger gates the "synthesise submit_analysis from ledger"
// fast-path. These tests pin the precondition logic so a regression in
// ledger-shape can't silently re-introduce the iter-30 / Partial fallback
// behaviour that the metacognition layer is built to prevent.
func TestCanTerminateFromLedger_RequiresExploreMode(t *testing.T) {
	p := &ReActPlanner{
		goal: &Goal{Mode: "fix"},
		ledger: &Ledger{
			Answer:    "x",
			Citations: []LedgerCitation{{FilePath: "a.go", LineStart: 1, Snippet: "x"}},
		},
	}
	if p.canTerminateFromLedger() {
		t.Error("non-explore mode should not terminate from ledger (other modes have their own contracts)")
	}
}

func TestCanTerminateFromLedger_RequiresAnswer(t *testing.T) {
	p := &ReActPlanner{
		goal: &Goal{Mode: "explore"},
		ledger: &Ledger{
			Citations: []LedgerCitation{{FilePath: "a.go", LineStart: 1, Snippet: "x"}},
		},
	}
	if p.canTerminateFromLedger() {
		t.Error("explore mode without an answer cannot satisfy contract")
	}
}

func TestCanTerminateFromLedger_RequiresAtLeastOneCompleteCitation(t *testing.T) {
	p := &ReActPlanner{
		goal: &Goal{Mode: "explore"},
		ledger: &Ledger{
			Answer:    "yes",
			Citations: []LedgerCitation{{FilePath: "", LineStart: 0, Snippet: ""}},
		},
	}
	if p.canTerminateFromLedger() {
		t.Error("citations with missing required fields should fail termination check")
	}
}

func TestCanTerminateFromLedger_HappyPath(t *testing.T) {
	p := &ReActPlanner{
		goal: &Goal{Mode: "explore"},
		ledger: &Ledger{
			Answer:    "yes",
			Citations: []LedgerCitation{{FilePath: "a.go", LineStart: 1, Snippet: "code"}},
		},
	}
	if !p.canTerminateFromLedger() {
		t.Error("complete ledger should be terminable")
	}
}

func TestCanTerminateFromLedger_NilSafe(t *testing.T) {
	if (&ReActPlanner{}).canTerminateFromLedger() {
		t.Error("nil goal/ledger should not terminate")
	}
}

func TestRecentStepsForReflection_ClampsToHistory(t *testing.T) {
	p := &ReActPlanner{
		currentSteps: []Step{
			{Number: 1, Action: "a"},
			{Number: 2, Action: "b"},
			{Number: 3, Action: "c"},
		},
	}
	if got := p.recentStepsForReflection(0); got != nil {
		t.Errorf("n=0 should return nil, got %+v", got)
	}
	if got := p.recentStepsForReflection(100); len(got) != 3 {
		t.Errorf("n>len should return all steps, got len=%d", len(got))
	}
	got := p.recentStepsForReflection(2)
	if len(got) != 2 || got[0].Action != "b" || got[1].Action != "c" {
		t.Errorf("expected last 2 steps, got %+v", got)
	}
}
