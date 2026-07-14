package planners

import (
	"strings"
	"testing"
)

// A retry attempt or next phase must inherit knowledge (findings, citations,
// open questions) but never conclusions (answer/confidence/ready_to_submit) —
// inheriting "done" would reproduce the premature-termination failure.
func TestSeedFrom_CarriesKnowledgeNotConclusions(t *testing.T) {
	prior := &Ledger{
		Findings:         []LedgerFinding{{Claim: "pool limit set in rdbms.go"}},
		Citations:        []LedgerCitation{{FilePath: "rdbms.go", LineStart: 42, Snippet: "SetMaxOpenConns(cfg.ServiceDBMaxConnection)"}},
		OpenSubQuestions: []string{"confirm idle timeout"},
		Answer:           "done answer",
		Confidence:       "High",
		ReadyToSubmit:    true,
	}
	fresh := NewLedger(nil)
	fresh.SeedFrom(prior)

	if len(fresh.Findings) != 1 || len(fresh.Citations) != 1 || len(fresh.OpenSubQuestions) != 1 {
		t.Fatalf("knowledge not carried: %+v", fresh)
	}
	if fresh.Answer != "" || fresh.Confidence != "" || fresh.ReadyToSubmit {
		t.Errorf("conclusions must NOT be carried: answer=%q ready=%v", fresh.Answer, fresh.ReadyToSubmit)
	}
}

func TestSeedFrom_Dedupes(t *testing.T) {
	l := NewLedger(nil)
	prior := &Ledger{
		Findings:         []LedgerFinding{{Claim: "A"}, {Claim: "B"}},
		Citations:        []LedgerCitation{{FilePath: "f.go", LineStart: 1, Snippet: "x"}},
		OpenSubQuestions: []string{"q1"},
	}
	l.SeedFrom(prior)
	l.SeedFrom(prior) // seeding twice (e.g. attempt carry + orchestrator handoff overlap)
	if len(l.Findings) != 2 || len(l.Citations) != 1 || len(l.OpenSubQuestions) != 1 {
		t.Errorf("seeding must dedupe: findings=%d citations=%d open=%d",
			len(l.Findings), len(l.Citations), len(l.OpenSubQuestions))
	}
}

func TestSetSeedLedger_MergesAndPlanConsumes(t *testing.T) {
	p := &ReActPlanner{}
	p.SetSeedLedger(&Ledger{Findings: []LedgerFinding{{Claim: "from specialist"}}})
	fb := NewLedger(nil)
	fb.AddOpenQuestion("Reviewer feedback to address in this attempt: fix the nil check")
	p.SetSeedLedger(fb)

	if p.seedLedger == nil || len(p.seedLedger.Findings) != 1 || len(p.seedLedger.OpenSubQuestions) != 1 {
		t.Fatalf("SetSeedLedger must merge: %+v", p.seedLedger)
	}
	// nil / empty seeds are no-ops
	p.SetSeedLedger(nil)
	p.SetSeedLedger(NewLedger(nil))
	if len(p.seedLedger.Findings) != 1 {
		t.Errorf("empty seeds must not clobber staged knowledge")
	}

	// Simulate what Plan() does at start: fresh ledger + consume seed.
	fresh := NewLedger(nil)
	fresh.SeedFrom(p.seedLedger)
	p.seedLedger = nil
	block := fresh.ToPromptBlock()
	if !strings.Contains(block, "from specialist") || !strings.Contains(block, "fix the nil check") {
		t.Errorf("seeded ledger must render staged knowledge, got: %s", block)
	}
}
