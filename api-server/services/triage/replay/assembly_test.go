package replay

import (
	"testing"
)

// TestAssemblyHarness is the four-tier assembly gate (#34658). It scores the
// reference assemblers against the seed-relative corpus and asserts that today's
// behaviour (raw same-name grouping) cannot recover causes or impact across
// services, while the intended tiering does — without pulling in-window distractors
// into the incident. When the production triage.AssembleTiers lands it implements
// the IntendedAssembler's rules and is held to the same gate.
func TestAssemblyHarness(t *testing.T) {
	set, err := LoadGoldenSet("testdata/golden_assembly.json")
	if err != nil {
		t.Fatalf("load assembly golden set: %v", err)
	}
	if len(set.Events) == 0 {
		t.Fatal("assembly golden set is empty")
	}

	baseline := ScoreAssembly(set, BaselineAssembler{})
	intended := ScoreAssembly(set, IntendedAssembler{})

	for _, s := range []AssemblyScore{baseline, intended} {
		t.Logf("%-9s core=%.2f cause=%.2f impact=%.2f chronic_fold=%.2f false_includes=%d",
			s.Assembler, s.CoreRecall, s.CauseRecall, s.ImpactRecall, s.ChronicFoldRate, s.FalseIncludes)
	}

	// Baseline reproduces the audit: raw same-name grouping recovers only some core
	// members (it fragments on ReplicaSet-hash names) and is blind to cause/impact.
	if baseline.CoreRecall >= intended.CoreRecall {
		t.Errorf("expected baseline to fragment core (hash-named members), got baseline=%.2f intended=%.2f",
			baseline.CoreRecall, intended.CoreRecall)
	}
	if baseline.CauseRecall != 0 || baseline.ImpactRecall != 0 {
		t.Errorf("expected baseline blind to cross-service tiers (cause=impact=0), got cause=%.2f impact=%.2f",
			baseline.CauseRecall, baseline.ImpactRecall)
	}

	// Intended recovers every tier on the labelled incidents, folds the chronic
	// members, and never pulls a distractor into the incident.
	if intended.CoreRecall < 0.99 {
		t.Errorf("intended core recall < 0.99: %.2f", intended.CoreRecall)
	}
	if intended.CauseRecall < 0.99 {
		t.Errorf("intended cause recall < 0.99: %.2f", intended.CauseRecall)
	}
	if intended.ImpactRecall < 0.99 {
		t.Errorf("intended impact recall < 0.99: %.2f", intended.ImpactRecall)
	}
	if intended.ChronicFoldRate < 0.99 {
		t.Errorf("intended chronic fold rate < 0.99: %.2f", intended.ChronicFoldRate)
	}
	if intended.FalseIncludes != 0 {
		t.Errorf("intended pulled %d distractor(s) into an incident; want 0", intended.FalseIncludes)
	}

	// The point of the phase: tiering must recover cross-service cause/impact that
	// same-name grouping cannot.
	if intended.CauseRecall <= baseline.CauseRecall || intended.ImpactRecall <= baseline.ImpactRecall {
		t.Errorf("intended did not beat baseline on cross-service tiers: cause %.2f vs %.2f, impact %.2f vs %.2f",
			intended.CauseRecall, baseline.CauseRecall, intended.ImpactRecall, baseline.ImpactRecall)
	}
}
