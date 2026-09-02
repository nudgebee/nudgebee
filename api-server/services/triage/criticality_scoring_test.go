package triage

import "testing"

// An ordinary alert on an ordinary workload — the baseline every case below moves away from.
func baselineVerdict() SignalVerdict {
	return SignalVerdict{
		Intrinsic: "medium", Blast: "single_workload",
		RecurrenceSemantics: "neutral", EnvSensitivity: "partial",
		BandFloor: "P3", BandCeiling: "P1",
	}
}

// The overwhelming majority of workloads have no criticality row at all. Their scores must not move
// by a single point as a result of this change.
func TestCriticalityUntieredWorkloadDoesNotChangeScore(t *testing.T) {
	v := baselineVerdict()
	want, wantPrio, _ := computeVerdictScore(&v, "prod", 1, workloadTier{})

	for _, tier := range []workloadTier{
		{}, // no row
		{level: CriticalityMedium, source: CriticalitySourceFact}, // explicitly medium
		{level: "", source: CriticalitySourceUser},                // source with no level
		{level: "nonsense", source: CriticalitySourceLLM},         // unparseable level
	} {
		got, prio, _ := computeVerdictScore(&v, "prod", 1, tier)
		if got != want || prio != wantPrio {
			t.Errorf("tier %+v moved the score to %d/%s, want unchanged %d/%s", tier, got, prio, want, wantPrio)
		}
	}
}

func TestCriticalityAdjustsScoreByTier(t *testing.T) {
	v := baselineVerdict()
	base, _, _ := computeVerdictScore(&v, "prod", 1, workloadTier{})

	cases := []struct {
		level string
		delta int
	}{
		{CriticalityCritical, 12},
		{CriticalityHigh, 6},
		{CriticalityMedium, 0},
		{CriticalityLow, -10},
	}
	for _, c := range cases {
		t.Run(c.level, func(t *testing.T) {
			// fact_signal, so only the additive term applies — no band movement.
			got, _, factors := computeVerdictScore(&v, "prod", 1, workloadTier{level: c.level, source: CriticalitySourceFact})
			if got != base+c.delta {
				t.Errorf("score = %d, want %d (base %d %+d)", got, base+c.delta, base, c.delta)
			}
			if factors["criticality"] != c.level {
				t.Errorf("factors missing criticality: %v", factors["criticality"])
			}
			if factors["criticality_adjustment"] != c.delta {
				t.Errorf("criticality_adjustment = %v, want %d", factors["criticality_adjustment"], c.delta)
			}
			if _, overridden := factors["criticality_band_override"]; overridden {
				t.Error("a derived tier must never move the band")
			}
		})
	}
}

// An operator declaring a workload critical must be able to lift it out of a band the class verdict
// capped lower. This is the whole point of the review screen, and it is what an additive term alone
// cannot do.
func TestUserDeclaredCriticalRaisesTheBand(t *testing.T) {
	// A class the model decided is low-severity and capped at P2.
	v := SignalVerdict{
		Intrinsic: "low", Blast: "single_workload",
		RecurrenceSemantics: "neutral", EnvSensitivity: "partial",
		BandFloor: "P3", BandCeiling: "P2",
	}

	derived, derivedPrio, _ := computeVerdictScore(&v, "prod", 1, workloadTier{level: CriticalityCritical, source: CriticalitySourceLLM})
	if derivedPrio != "P3" && derivedPrio != "P2" {
		t.Fatalf("a derived critical should stay inside the verdict band, got %d/%s", derived, derivedPrio)
	}

	score, prio, factors := computeVerdictScore(&v, "prod", 1, workloadTier{level: CriticalityCritical, source: CriticalitySourceUser})
	if prio != "P1" && prio != "P0" {
		t.Fatalf("an operator-declared critical must reach at least P1, got %d/%s", score, prio)
	}
	if factors["criticality_band_override"] == nil {
		t.Error("the band override must be recorded in the score breakdown")
	}
}

func TestUserDeclaredLowCapsTheBand(t *testing.T) {
	// A class the model decided is serious, on a workload the operator says does not matter.
	v := SignalVerdict{
		Intrinsic: "critical", Blast: "control_plane",
		RecurrenceSemantics: "neutral", EnvSensitivity: "partial",
		BandFloor: "P2", BandCeiling: "P0",
	}

	if _, prio, _ := computeVerdictScore(&v, "prod", 1, workloadTier{level: CriticalityLow, source: CriticalitySourceLLM}); prio == "P3" {
		t.Error("a derived low must not be able to force P3")
	}
	score, prio, _ := computeVerdictScore(&v, "prod", 1, workloadTier{level: CriticalityLow, source: CriticalitySourceUser})
	if prio != "P3" {
		t.Fatalf("an operator-declared low must cap at P3, got %d/%s", score, prio)
	}
}

// The band written into score_factors must be the ADJUSTED one. processor.go re-applies clampToBand
// after additive scoring rules, so writing the verdict's original band there would silently undo an
// operator's override the moment any rule touched the event.
func TestUserBandOverrideSurvivesTheLaterReclamp(t *testing.T) {
	v := SignalVerdict{
		Intrinsic: "low", Blast: "single_workload",
		RecurrenceSemantics: "neutral", EnvSensitivity: "partial",
		BandFloor: "P3", BandCeiling: "P2",
	}
	score, _, factors := computeVerdictScore(&v, "prod", 1, workloadTier{level: CriticalityCritical, source: CriticalitySourceUser})

	if got := clampToBand(score, factors); got != score {
		t.Fatalf("re-clamping dropped the override: %d -> %d (band=%v)", score, got, factors["band"])
	}
	if factors["band"] == v.BandFloor+".."+v.BandCeiling {
		t.Error("factors carry the verdict's original band, so the override will be undone downstream")
	}
}

// A user `low` ceiling under a verdict floor of P1 would invert the band. The operator's end wins and
// the other end gives way, rather than producing a nonsensical range that clampToBand rejects.
func TestUserBandOverrideNeverInvertsTheBand(t *testing.T) {
	cases := []struct {
		name  string
		v     SignalVerdict
		tier  workloadTier
		wantP string
	}{
		{
			name:  "user low under a P1 floor",
			v:     SignalVerdict{Intrinsic: "critical", Blast: "control_plane", EnvSensitivity: "none", BandFloor: "P1", BandCeiling: "P0"},
			tier:  workloadTier{level: CriticalityLow, source: CriticalitySourceUser},
			wantP: "P3",
		},
		{
			name:  "user critical over a P3 ceiling",
			v:     SignalVerdict{Intrinsic: "info", Blast: "expected_change", EnvSensitivity: "none", BandFloor: "P3", BandCeiling: "P3"},
			tier:  workloadTier{level: CriticalityCritical, source: CriticalitySourceUser},
			wantP: "P1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			score, prio, factors := computeVerdictScore(&c.v, "prod", 1, c.tier)
			if prio != c.wantP {
				t.Errorf("priority = %s (score %d), want %s", prio, score, c.wantP)
			}
			// Whatever band came out, it must be one clampToBand accepts — an inverted band is
			// silently ignored downstream, which would drop the override.
			if got := clampToBand(score, factors); got != score {
				t.Errorf("band %v was rejected on re-clamp: %d -> %d", factors["band"], score, got)
			}
		})
	}
}

// A verdict can arrive with an empty or unrecognised band end — computeVerdictScore has always
// handled a missing ceiling as "no ceiling". Resolving those through the score maps directly returns
// the zero value, which reads as "ceiling at 0": it silently discarded an operator's cap and made
// every band look inverted. Every case here has at least one band end missing.
func TestUserBandOverrideWithMissingVerdictBandEnds(t *testing.T) {
	cases := []struct {
		name  string
		v     SignalVerdict
		tier  workloadTier
		wantP string
	}{
		{
			name:  "user low still caps a verdict with no ceiling",
			v:     SignalVerdict{Intrinsic: "critical", Blast: "control_plane", EnvSensitivity: "none", BandFloor: "P3"},
			tier:  workloadTier{level: CriticalityLow, source: CriticalitySourceUser},
			wantP: "P3",
		},
		{
			name:  "user critical still lifts a verdict with no floor",
			v:     SignalVerdict{Intrinsic: "low", Blast: "single_workload", EnvSensitivity: "none", BandCeiling: "P0"},
			tier:  workloadTier{level: CriticalityCritical, source: CriticalitySourceUser},
			wantP: "P1",
		},
		{
			name:  "user high on a verdict with neither end set",
			v:     SignalVerdict{Intrinsic: "low", Blast: "single_workload", EnvSensitivity: "none"},
			tier:  workloadTier{level: CriticalityHigh, source: CriticalitySourceUser},
			wantP: "P2",
		},
		{
			name:  "an unrecognised band end is treated as absent, not as zero",
			v:     SignalVerdict{Intrinsic: "low", Blast: "single_workload", EnvSensitivity: "none", BandFloor: "P9", BandCeiling: "banana"},
			tier:  workloadTier{level: CriticalityCritical, source: CriticalitySourceUser},
			wantP: "P1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			score, prio, factors := computeVerdictScore(&c.v, "prod", 1, c.tier)
			if prio != c.wantP {
				t.Errorf("priority = %s (score %d, band %v), want %s", prio, score, factors["band"], c.wantP)
			}
			if got := clampToBand(score, factors); got != score {
				t.Errorf("band %v was rejected on re-clamp: %d -> %d", factors["band"], score, got)
			}
		})
	}
}

// A missing band end must not change how an UNTIERED workload scores either — the resolver defaults
// have to match the behaviour computeVerdictScore had before they existed.
func TestMissingBandEndsUnchangedForUntieredWorkloads(t *testing.T) {
	for _, v := range []SignalVerdict{
		{Intrinsic: "high", Blast: "customer_facing", EnvSensitivity: "partial"},                    // neither end
		{Intrinsic: "high", Blast: "customer_facing", EnvSensitivity: "partial", BandFloor: "P2"},   // no ceiling
		{Intrinsic: "high", Blast: "customer_facing", EnvSensitivity: "partial", BandCeiling: "P1"}, // no floor
	} {
		score, _, factors := computeVerdictScore(&v, "prod", 1, workloadTier{})
		if score < 0 || score > 100 {
			t.Errorf("verdict %+v produced an out-of-range score %d", v, score)
		}
		if _, overridden := factors["criticality_band_override"]; overridden {
			t.Errorf("verdict %+v: an untiered workload must never record a band override", v)
		}
	}
}

// The regression that motivated moving criticality out of the prompt: two workloads with the SAME
// name in different namespaces share one cached class verdict, and used to therefore share one
// criticality. They must now score differently from that same verdict.
func TestSameNameWorkloadsScoreDifferentlyFromOneCachedVerdict(t *testing.T) {
	shared := baselineVerdict() // as if minted once for class "...|services-server"

	prod, _, _ := computeVerdictScore(&shared, "prod", 1, workloadTier{level: CriticalityCritical, source: CriticalitySourceLLM})
	test, _, _ := computeVerdictScore(&shared, "prod", 1, workloadTier{level: CriticalityLow, source: CriticalitySourceLLM})

	if prod <= test {
		t.Fatalf("prod services-server (%d) must outrank the test one (%d) despite sharing a verdict", prod, test)
	}
}
